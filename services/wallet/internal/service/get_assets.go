package service

import (
	"context"
	"fmt"

	"tradedrift/services/wallet/internal/repository"
)

// GetSupportedAssets returns all enabled assets on the platform.
// Used by the Market Service to validate base/quote asset codes.
func (s *Service) GetSupportedAssets(ctx context.Context) ([]*repository.SupportedAsset, error) {
	assets, err := s.assetRepo.GetEnabled(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch supported assets: %w", err)
	}
	return assets, nil
}
