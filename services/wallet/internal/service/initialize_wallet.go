package service

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/wallet/internal/repository"
	platformuuid "tradedrift/platform/uuid"
)

// InitializeWallet creates wallet rows for every enabled asset for a new user.
// Idempotent per (user_id, asset) — safe to call multiple times.
func (s *Service) InitializeWallet(ctx context.Context, userID string) error {
	// 1. Load all enabled assets
	assets, err := s.assetRepo.GetEnabled(ctx)
	if err != nil {
		return fmt.Errorf("failed to load supported assets: %w", err)
	}

	// 2. Execute all wallet creations inside a single atomic transaction
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin wallet initialization transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	walletRepo := s.walletRepo.WithTx(tx)
	txnRepo := s.txnRepo.WithTx(tx)

	now := time.Now().UTC()

	for _, asset := range assets {
		// 3. Check if wallet already exists for this (user, asset) pair
		existing, err := walletRepo.GetByUserAndAsset(ctx, userID, asset.AssetCode)
		if err != nil {
			return fmt.Errorf("failed to check existing wallet for asset %s: %w", asset.AssetCode, err)
		}
		if existing != nil {
			// Already initialized for this asset — skip (idempotency)
			s.log.Debug("wallet already exists, skipping",
				zap.String("userID", userID),
				zap.String("asset", asset.AssetCode),
			)
			continue
		}

		// 4. Create the wallet row
		walletID, err := platformuuid.New()
		if err != nil {
			return fmt.Errorf("failed to generate wallet ID: %w", err)
		}
		wallet := &repository.Wallet{
			ID:               walletID,
			UserID:           userID,
			Asset:            asset.AssetCode,
			AvailableBalance: asset.SeedAmount,
			ReservedBalance:  "0",
			IsFrozen:         false,
			InitialBalance:   asset.SeedAmount,
			TotalBalance:     asset.SeedAmount,
		}

		if err := walletRepo.Create(ctx, wallet); err != nil {
			return fmt.Errorf("failed to create wallet for asset %s: %w", asset.AssetCode, err)
		}

		// 5. If seed amount > 0, write an INITIAL_ALLOCATION transaction (ledger entry)
		if asset.SeedAmount != "0" && asset.SeedAmount != "0.0000000000" {
			txnID, err := platformuuid.New()
			if err != nil {
				return fmt.Errorf("failed to generate transaction ID: %w", err)
			}
			txn := &repository.WalletTransaction{
				ID:              txnID,
				WalletID:        walletID,
				ReferenceID:     userID,
				ReferenceType:   repository.RefInitialAllocation,
				TransactionType: repository.TxnTypeCredit,
				Asset:           asset.AssetCode,
				Amount:          asset.SeedAmount,
				CreatedAt:       now,
			}
			if err := txnRepo.Create(ctx, txn); err != nil {
				return fmt.Errorf("failed to insert INITIAL_ALLOCATION transaction for asset %s: %w", asset.AssetCode, err)
			}
		}

		s.log.Info("wallet initialized",
			zap.String("userID", userID),
			zap.String("asset", asset.AssetCode),
			zap.String("seedAmount", asset.SeedAmount),
		)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit wallet initialization transaction: %w", err)
	}

	return nil
}