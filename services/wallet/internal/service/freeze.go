package service

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"tradedrift/services/wallet/internal/repository"
)

// FreezeWallet locks or unlocks a user's wallet for a specific asset.
func (s *Service) FreezeWallet(ctx context.Context, userID, asset string, freeze bool, reason string) (bool, error) {
	wallet, err := s.walletRepo.GetByUserAndAsset(ctx, userID, asset)
	if err != nil {
		return false, fmt.Errorf("failed to fetch wallet: %w", err)
	}
	if wallet == nil {
		return false, repository.ErrWalletNotFound
	}

	if freeze {
		if err := s.walletRepo.FreezeWallet(ctx, wallet.ID, "admin", reason); err != nil {
			return false, fmt.Errorf("failed to freeze wallet: %w", err)
		}
		s.log.Info("wallet frozen successfully",
			zap.String("walletID", wallet.ID),
			zap.String("userID", userID),
			zap.String("asset", asset),
			zap.String("reason", reason),
		)
		return true, nil
	}

	if err := s.walletRepo.UnfreezeWallet(ctx, wallet.ID); err != nil {
		return false, fmt.Errorf("failed to unfreeze wallet: %w", err)
	}
	s.log.Info("wallet unfrozen successfully",
		zap.String("walletID", wallet.ID),
		zap.String("userID", userID),
		zap.String("asset", asset),
	)
	return false, nil
}
