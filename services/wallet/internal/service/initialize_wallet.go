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

	now := time.Now().UTC()

	for _, asset := range assets {
		// 2. Check if wallet already exists for this (user, asset) pair
		existing, err := s.walletRepo.GetByUserAndAsset(ctx, userID, asset.AssetCode)
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

		// 3. Create the wallet row
		walletID, err := platformuuid.New()
		if err != nil{
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

		if err := s.walletRepo.Create(ctx, wallet); err != nil {
			return fmt.Errorf("failed to create wallet for asset %s: %w", asset.AssetCode, err)
		}

		// 4. If seed amount > 0, write an INITIAL_ALLOCATION transaction (ledger entry)
		txnID, err := platformuuid.New()
		if err != nil {
    		return fmt.Errorf("failed to generate transaction ID: %w", err)
		}
		if asset.SeedAmount != "0" && asset.SeedAmount != "0.0000000000" {
			txn := &repository.WalletTransaction{
				ID:              txnID,
				WalletID:        walletID,
				ReferenceID:     userID,
				ReferenceType:   "INITIAL_ALLOCATION",
				TransactionType: "CREDIT",
				Asset:           asset.AssetCode,
				Amount:          asset.SeedAmount,
				CreatedAt:       now,
			}
			if err := s.txnRepo.Create(ctx, txn); err != nil {
				return fmt.Errorf("failed to insert INITIAL_ALLOCATION transaction for asset %s: %w", asset.AssetCode, err)
			}
		}

		s.log.Info("wallet initialized",
			zap.String("userID", userID),
			zap.String("asset", asset.AssetCode),
			zap.String("seedAmount", asset.SeedAmount),
		)
	}

	return nil
}