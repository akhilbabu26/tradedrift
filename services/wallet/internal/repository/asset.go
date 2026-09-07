package repository

import "context"

// SupportedAsset is a row from the supported_assets table.
type SupportedAsset struct {
	AssetCode    string
	AssetName    string
	Decimals     int
	IsEnabled    bool
	SeedAmount   string // Decimal string
	DisplayOrder int
}

// AssetRepository defines the persistence contract for supported assets.
type AssetRepository interface {
	// GetAll returns all assets (enabled and disabled).
	GetAll(ctx context.Context) ([]*SupportedAsset, error)

	// GetEnabled returns only assets where is_enabled = true.
	GetEnabled(ctx context.Context) ([]*SupportedAsset, error)

	// GetByCode returns a single asset by its asset_code.
	// Returns nil, nil if not found.
	GetByCode(ctx context.Context, assetCode string) (*SupportedAsset, error)
}
