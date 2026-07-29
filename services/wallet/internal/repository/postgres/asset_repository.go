package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/wallet/internal/repository"
)

type AssetRepository struct {
	db *pgxpool.Pool
}

func NewAssetRepository(db *pgxpool.Pool) *AssetRepository {
	return &AssetRepository{db: db}
}

func (r *AssetRepository) GetAll(ctx context.Context) ([]*repository.SupportedAsset, error) {
	return r.query(ctx, `
		SELECT asset_code, asset_name, decimals, is_enabled, seed_amount, display_order
		FROM supported_assets
		ORDER BY display_order
	`)
}

func (r *AssetRepository) GetEnabled(ctx context.Context) ([]*repository.SupportedAsset, error) {
	return r.query(ctx, `
		SELECT asset_code, asset_name, decimals, is_enabled, seed_amount, display_order
		FROM supported_assets
		WHERE is_enabled = true
		ORDER BY display_order
	`)
}

func (r *AssetRepository) query(ctx context.Context, sql string) ([]*repository.SupportedAsset, error) {
	rows, err := r.db.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("failed to query supported assets: %w", err)
	}
	defer rows.Close()

	var assets []*repository.SupportedAsset
	for rows.Next() {
		var a repository.SupportedAsset
		if err := rows.Scan(
			&a.AssetCode, &a.AssetName, &a.Decimals,
			&a.IsEnabled, &a.SeedAmount, &a.DisplayOrder,
		); err != nil {
			return nil, fmt.Errorf("failed to scan asset row: %w", err)
		}
		assets = append(assets, &a)
	}
	return assets, nil
}
