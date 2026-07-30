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

// ReserveFunds locks funds for an order. Called by Order Service before an order is placed.
// Idempotent: if funds are already reserved for orderID, returns the existing reservation.
func (s *Service) ReserveFunds(ctx context.Context, userID, orderID, asset, amount string) (*repository.Reservation, error) {

	// Step 1: Idempotency check — already reserved for this order?
	existing, err := s.reservRepo.GetByOrderID(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing reservation: %w", err)
	}
	if existing != nil {
		s.log.Debug("reservation already exists, returning existing",
			zap.String("orderID", orderID),
			zap.String("status", existing.Status),
		)
		return existing, nil
	}

	// Step 2: Fetch the wallet
	wallet, err := s.walletRepo.GetByUserAndAsset(ctx, userID, asset)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch wallet: %w", err)
	}
	if wallet == nil {
		return nil, fmt.Errorf("wallet not found for user %s and asset %s", userID, asset)
	}

	// Step 3: Reject if wallet is frozen
	if wallet.IsFrozen {
		return nil, fmt.Errorf("wallet is frozen for asset %s", asset)
	}

	// Step 4: Check idempotency on ledger (guard against rare race)
	alreadyDebited, err := s.txnRepo.ExistsByKey(ctx, orderID, "RESERVATION", asset)
	if err != nil {
		return nil, fmt.Errorf("failed to check transaction existence: %w", err)
	}
	if alreadyDebited {
		// Reservation row is missing but ledger entry exists — inconsistency guard
		return nil, fmt.Errorf("inconsistent state: ledger debited but reservation not found for order %s", orderID)
	}

	// Step 5: Move funds from available → reserved (atomic SQL UPDATE)
	if err := s.walletRepo.MoveToReserved(ctx, wallet.ID, amount); err != nil {
		return nil, fmt.Errorf("failed to reserve funds: %w", err)
		// Note: MoveToReserved returns error if available_balance < amount
	}

	// Step 6: Create reservation row
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
	if err := s.reservRepo.Create(ctx, reservation); err != nil {
		return nil, fmt.Errorf("failed to create reservation: %w", err)
	}

	// Step 7: Write ledger entry (RESERVATION, DEBIT)
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
	if err := s.txnRepo.Create(ctx, txn); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			// Ledger already has this entry — safe to ignore
			s.log.Warn("duplicate reservation transaction ignored", zap.String("orderID", orderID))
		} else {
			return nil, fmt.Errorf("failed to write reservation ledger entry: %w", err)
		}
	}

	s.log.Info("funds reserved",
		zap.String("userID", userID),
		zap.String("orderID", orderID),
		zap.String("asset", asset),
		zap.String("amount", amount),
	)

	return reservation, nil
}
