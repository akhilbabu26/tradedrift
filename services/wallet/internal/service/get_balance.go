package service

import (
	"context"
	"fmt"

	"tradedrift/services/wallet/internal/repository"
)

// GetBalance returns the balance for a single (user, asset) pair.
func (s *Service) GetBalance(ctx context.Context, userID, asset string) (*repository.Wallet, error) {
	wallet, err := s.walletRepo.GetByUserAndAsset(ctx, userID, asset)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch balance: %w", err)
	}
	if wallet == nil {
		return nil, fmt.Errorf("wallet not found for asset %s", asset)
	}
	return wallet, nil
}

// GetBalances returns all wallets for a user, ordered by display_order.
func (s *Service) GetBalances(ctx context.Context, userID string) ([]*repository.Wallet, error) {
	wallets, err := s.walletRepo.GetAllByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch balances: %w", err)
	}
	return wallets, nil
}
