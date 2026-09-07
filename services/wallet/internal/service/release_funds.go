package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/wallet/internal/repository"
	platformuuid "tradedrift/platform/uuid"
)

// ReleaseFunds returns reserved funds to available balance when an order is cancelled.
// Idempotent: if the reservation is already RELEASED or CONSUMED, returns success immediately.
// All operations are executed inside an atomic PostgreSQL transaction with deterministic locking.
func (s *Service) ReleaseFunds(ctx context.Context, orderID string) error {
	// ── Step 1: Begin atomic PostgreSQL transaction ───────────────────────────────────────────────
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin release transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	walletRepo := s.walletRepo.WithTx(tx)
	reservRepo := s.reservRepo.WithTx(tx)
	txnRepo := s.txnRepo.WithTx(tx)

	// ── Step 2: Lock and fetch reservation row (SELECT ... FOR UPDATE) ───────────────────────────
	reservation, err := reservRepo.GetByOrderIDForUpdate(ctx, orderID)
	if err != nil {
		return fmt.Errorf("failed to fetch reservation for order %s: %w", orderID, err)
	}
	if reservation == nil {
		return fmt.Errorf("%w: reservation not found for order %s", repository.ErrReservationNotFound, orderID)
	}

	// ── Step 3: Idempotency check inside transaction ─────────────────────────────────────────────
	if reservation.Status == repository.ReservationReleased || reservation.Status == repository.ReservationConsumed {
		s.log.Debug("reservation already settled/released, skipping release",
			zap.String("orderID", orderID),
			zap.String("status", reservation.Status),
		)
		return nil
	}

	// ── Step 4: Only return what's still remaining (partial fills may have consumed some) ─────────
	amountToReturn := reservation.RemainingAmount

	// ── Step 5: Fetch the wallet to locate wallet.ID ───────────────────────────────────────────────
	wallet, err := walletRepo.GetByUserAndAsset(ctx, reservation.UserID, reservation.Asset)
	if err != nil {
		return fmt.Errorf("failed to fetch wallet: %w", err)
	}
	if wallet == nil {
		return fmt.Errorf("wallet not found for user %s and asset %s", reservation.UserID, reservation.Asset)
	}

	// ── Step 6: Deterministically lock the wallet row (SELECT ... FOR UPDATE) ─────────────────────
	if _, err := walletRepo.LockByIDs(ctx, []string{wallet.ID}); err != nil {
		return fmt.Errorf("failed to acquire wallet lock: %w", err)
	}

	// ── Step 7: Move remaining funds from reserved → available ────────────────────────────────────
	if err := walletRepo.MoveFromReserved(ctx, wallet.ID, amountToReturn); err != nil {
		return fmt.Errorf("failed to return funds to available: %w", err)
	}

	// ── Step 8: Mark reservation as RELEASED ──────────────────────────────────────────────────────
	if err := reservRepo.UpdateStatus(ctx, reservation.ID, repository.ReservationReleased); err != nil {
		return fmt.Errorf("failed to update reservation status: %w", err)
	}

	// ── Step 9: Write ledger entry (RELEASE, CREDIT) ──────────────────────────────────────────────
	txnID, err := platformuuid.New()
	if err != nil {
		return fmt.Errorf("failed to generate transaction ID: %w", err)
	}
	txn := &repository.WalletTransaction{
		ID:              txnID,
		WalletID:        wallet.ID,
		ReferenceID:     orderID,
		ReferenceType:   repository.RefRelease,
		TransactionType: repository.TxnTypeCredit,
		Asset:           reservation.Asset,
		Amount:          amountToReturn,
		CreatedAt:       time.Now().UTC(),
	}
	if err := txnRepo.Create(ctx, txn); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			s.log.Warn("duplicate release transaction ignored", zap.String("orderID", orderID))
		} else {
			return fmt.Errorf("failed to write release ledger entry: %w", err)
		}
	}

	// ── Step 10: Commit atomic PostgreSQL transaction ─────────────────────────────────────────────
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit release transaction: %w", err)
	}

	s.log.Info("funds released",
		zap.String("orderID", orderID),
		zap.String("asset", reservation.Asset),
		zap.String("amountReturned", amountToReturn),
	)

	return nil
}
