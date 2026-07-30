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
// Idempotent: if the reservation is already RELEASED, returns success immediately.
func (s *Service) ReleaseFunds(ctx context.Context, orderID string) error {

	// Step 1: Fetch the reservation
	reservation, err := s.reservRepo.GetByOrderID(ctx, orderID)
	if err != nil {
		return fmt.Errorf("failed to fetch reservation: %w", err)
	}
	if reservation == nil {
		return fmt.Errorf("reservation not found for order %s", orderID)
	}

	// Step 2: Idempotency — already released?
	if reservation.Status == repository.ReservationReleased || reservation.Status == repository.ReservationConsumed {
		s.log.Debug("reservation already settled, skipping release",
			zap.String("orderID", orderID),
			zap.String("status", reservation.Status),
		)
		return nil
	}

	// Step 3: Only return what's still remaining (partial fills may have consumed some)
	amountToReturn := reservation.RemainingAmount

	// Step 4: Fetch the wallet to get wallet.ID
	wallet, err := s.walletRepo.GetByUserAndAsset(ctx, reservation.UserID, reservation.Asset)
	if err != nil {
		return fmt.Errorf("failed to fetch wallet: %w", err)
	}
	if wallet == nil {
		return fmt.Errorf("wallet not found for user %s and asset %s", reservation.UserID, reservation.Asset)
	}

	// Step 5: Check ledger idempotency — RELEASE already recorded?
	alreadyReleased, err := s.txnRepo.ExistsByKey(ctx, orderID, "RELEASE", reservation.Asset)
	if err != nil {
		return fmt.Errorf("failed to check release transaction: %w", err)
	}
	if alreadyReleased {
		// Ledger has RELEASE but reservation status wasn't updated — fix the status
		return s.reservRepo.UpdateStatus(ctx, reservation.ID, repository.ReservationReleased)
	}

	// Step 6: Move remaining funds from reserved → available
	if err := s.walletRepo.MoveFromReserved(ctx, wallet.ID, amountToReturn); err != nil {
		return fmt.Errorf("failed to return funds to available: %w", err)
	}

	// Step 7: Mark reservation as RELEASED
	if err := s.reservRepo.UpdateStatus(ctx, reservation.ID, repository.ReservationReleased); err != nil {
		return fmt.Errorf("failed to update reservation status: %w", err)
	}

	// Step 8: Write ledger entry (RELEASE, CREDIT)
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
	if err := s.txnRepo.Create(ctx, txn); err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			s.log.Warn("duplicate release transaction ignored", zap.String("orderID", orderID))
		} else {
			return fmt.Errorf("failed to write release ledger entry: %w", err)
		}
	}

	s.log.Info("funds released",
		zap.String("orderID", orderID),
		zap.String("asset", reservation.Asset),
		zap.String("amountReturned", amountToReturn),
	)

	return nil
}
