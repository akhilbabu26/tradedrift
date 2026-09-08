package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/wallet/internal/repository"
	platformuuid "tradedrift/platform/uuid"
)

// ReserveFunds locks funds for an order. Called by Order Service before an order is placed.
//
// Idempotent:
//   - (ACTIVE | PARTIALLY_CONSUMED, same identity) → returns existing reservation.
//   - (RELEASED | CONSUMED)  → ErrReservationConflict — order lifecycle ended, cannot reuse.
//   - (any status, different user/asset/amount) → ErrReservationConflict — identity mismatch.
//
// All operations are executed inside an atomic PostgreSQL transaction with deterministic locking.
func (s *Service) ReserveFunds(ctx context.Context, userID, orderID, asset, amount string) (*repository.Reservation, error) {
	// ── Step 1: Domain input validation (runs before any DB I/O) ─────────────────────────────────
	if err := validateReservationRequest(asset, amount); err != nil {
		return nil, err
	}

	// ── Step 2: Begin atomic PostgreSQL transaction ───────────────────────────────────────────────
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin reservation transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	walletRepo := s.walletRepo.WithTx(tx)
	reservRepo := s.reservRepo.WithTx(tx)
	txnRepo := s.txnRepo.WithTx(tx)

	// ── Step 3: Lock and fetch existing reservation (SELECT ... FOR UPDATE) ───────────────────────
	// Locks the reservation row immediately to serialise concurrent retries.
	existing, err := reservRepo.GetByOrderIDForUpdate(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing reservation: %w", err)
	}
	if existing != nil {
		return validateExistingReservation(existing, userID, asset, amount)
	}

	// ── Step 4: Fetch the wallet to locate wallet.ID ───────────────────────────────────────────────
	wallet, err := walletRepo.GetByUserAndAsset(ctx, userID, asset)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch wallet: %w", err)
	}
	if wallet == nil {
		return nil, repository.ErrInsufficientBalance
	}

	// ── Step 5: Deterministically lock the wallet row (SELECT ... FOR UPDATE) ─────────────────────
	if _, err := walletRepo.LockByIDs(ctx, []string{wallet.ID}); err != nil {
		return nil, fmt.Errorf("failed to acquire wallet lock: %w", err)
	}

	// ── Step 6: Re-check reservation after acquiring wallet lock ──────────────────────────────────
	// A concurrent tx may have created a reservation while we waited for the wallet lock.
	existingAfterLock, err := reservRepo.GetByOrderIDForUpdate(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("failed to re-check reservation after lock: %w", err)
	}
	if existingAfterLock != nil {
		return validateExistingReservation(existingAfterLock, userID, asset, amount)
	}

	// ── Step 7: Validate asset enabled + per-asset decimal precision ──────────────────────────────
	assetInfo, err := s.assetRepo.GetByCode(ctx, asset)
	if err != nil {
		return nil, fmt.Errorf("failed to look up asset %s: %w", asset, err)
	}
	if assetInfo == nil {
		return nil, fmt.Errorf("%w: asset %q is not a supported asset", repository.ErrInvalidReservation, asset)
	}
	if !assetInfo.IsEnabled {
		return nil, fmt.Errorf("%w: asset %q is currently disabled", repository.ErrInvalidReservation, asset)
	}
	amountDec, _ := decimal.NewFromString(amount) // already validated in Step 1
	if scale := decimalScale(amountDec); scale > assetInfo.Decimals {
		return nil, fmt.Errorf("%w: amount %s has %d decimal places, maximum for %s is %d",
			repository.ErrInvalidReservation, amount, scale, asset, assetInfo.Decimals)
	}

	// ── Step 8: Reject if wallet is frozen ────────────────────────────────────────────────────────
	if wallet.IsFrozen {
		return nil, repository.ErrWalletFrozen
	}

	// ── Step 9: Move funds from available → reserved (atomic SQL UPDATE with balance guard) ───────
	if err := walletRepo.MoveToReserved(ctx, wallet.ID, amount); err != nil {
		return nil, err
	}

	// ── Step 10: Create reservation row ───────────────────────────────────────────────────────────
	reservationID, err := platformuuid.New()
	if err != nil {
		return nil, fmt.Errorf("failed to generate reservation ID: %w", err)
	}
	now := time.Now().UTC()
	reservation := &repository.Reservation{
		ID:              reservationID,
		OrderID:         orderID,
		UserID:          userID,
		Asset:           asset,
		ReservedAmount:  amount,
		ConsumedAmount:  "0",
		RemainingAmount: amount,
		Status:          repository.ReservationActive,
		CreatedAt:       now,
	}
	if err := reservRepo.Create(ctx, reservation); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			// Concurrent tx committed between Steps 6 and 10 — fetch and validate it.
			existingDup, getErr := reservRepo.GetByOrderIDForUpdate(ctx, orderID)
			if getErr == nil && existingDup != nil {
				return validateExistingReservation(existingDup, userID, asset, amount)
			}
		}
		return nil, fmt.Errorf("failed to create reservation: %w", err)
	}

	// ── Step 11: Write ledger entry (RESERVATION, DEBIT) ──────────────────────────────────────────
	txnID, err := platformuuid.New()
	if err != nil {
		return nil, fmt.Errorf("failed to generate transaction ID: %w", err)
	}
	txn := &repository.WalletTransaction{
		ID:              txnID,
		WalletID:        wallet.ID,
		ReferenceID:     orderID,
		ReferenceType:   repository.RefReservation,
		TransactionType: repository.TxnTypeDebit,
		Asset:           asset,
		Amount:          amount,
		CreatedAt:       now,
	}
	if err := txnRepo.Create(ctx, txn); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			s.log.Warn("duplicate reservation transaction ignored", zap.String("orderID", orderID))
		} else {
			return nil, fmt.Errorf("failed to write reservation ledger entry: %w", err)
		}
	}

	// ── Step 12: Commit atomic PostgreSQL transaction ──────────────────────────────────────────────
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit reservation transaction: %w", err)
	}

	s.log.Info("funds reserved",
		zap.String("userID", userID),
		zap.String("orderID", orderID),
		zap.String("asset", asset),
		zap.String("amount", amount),
	)

	return reservation, nil
}

// validateReservationRequest performs input validation before opening a transaction.
// Rejects obviously bad inputs before touching the database.
func validateReservationRequest(asset, amount string) error {
	if strings.TrimSpace(asset) == "" {
		return fmt.Errorf("%w: asset must not be empty", repository.ErrInvalidReservation)
	}

	d, err := decimal.NewFromString(amount)
	if err != nil {
		return fmt.Errorf("%w: amount %q is not a valid decimal: %v", repository.ErrInvalidReservation, amount, err)
	}
	if !d.IsPositive() {
		return fmt.Errorf("%w: amount must be > 0, got %q", repository.ErrInvalidReservation, amount)
	}
	// Reject scientific notation (e.g. "5e4") — all amounts must be plain decimal.
	if d.Exponent() > 0 {
		return fmt.Errorf("%w: amount %q must be plain decimal notation (scientific notation not accepted)", repository.ErrInvalidReservation, amount)
	}
	return nil
}

// validateExistingReservation checks that an existing reservation for the same orderID
// matches the caller's (userID, asset, amount) identity and has a non-terminal status.
// Returns the reservation on idempotent success, or ErrReservationConflict.
func validateExistingReservation(existing *repository.Reservation, userID, asset, amount string) (*repository.Reservation, error) {
	// Reject terminal statuses — orderID represents one immutable reservation lifecycle.
	switch existing.Status {
	case repository.ReservationReleased, repository.ReservationConsumed:
		return nil, fmt.Errorf("%w: order %s reservation is already %s and cannot be reused",
			repository.ErrReservationConflict, existing.OrderID, existing.Status)
	}

	// Verify identity fields match the incoming request.
	if existing.UserID != userID {
		return nil, fmt.Errorf("%w: order %s belongs to user %s, not %s",
			repository.ErrReservationConflict, existing.OrderID, existing.UserID, userID)
	}
	if existing.Asset != asset {
		return nil, fmt.Errorf("%w: order %s reserved asset %s, not %s",
			repository.ErrReservationConflict, existing.OrderID, existing.Asset, asset)
	}
	// Compare reserved amount as decimals to avoid string representation differences.
	existingAmt, err1 := decimal.NewFromString(existing.ReservedAmount)
	requestedAmt, err2 := decimal.NewFromString(amount)
	if err1 != nil || err2 != nil || !existingAmt.Equal(requestedAmt) {
		return nil, fmt.Errorf("%w: order %s was reserved for %s %s, got request for %s",
			repository.ErrReservationConflict, existing.OrderID, existing.ReservedAmount, existing.Asset, amount)
	}

	// Genuine idempotent retry with matching identity and live status.
	return existing, nil
}

// decimalScale returns the number of significant decimal places in d (ignoring trailing zeros).
func decimalScale(d decimal.Decimal) int {
	if d.Equal(d.Truncate(0)) {
		return 0
	}
	for places := int32(1); places <= 10; places++ {
		if d.Equal(d.Truncate(places)) {
			return int(places)
		}
	}
	return 11
}
