package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"tradedrift/services/wallet/internal/repository"
)

type AssetRepository struct {
	db repository.DBTX
}

func NewAssetRepository(db repository.DBTX) *AssetRepository {
	return &AssetRepository{db: db}
}

func (r *AssetRepository) GetAll(ctx context.Context) ([]*repository.SupportedAsset, error) {
	return r.queryList(ctx, `
		SELECT asset_code, asset_name, decimals, is_enabled, seed_amount, display_order
		FROM supported_assets
		ORDER BY display_order
	`)
}

func (r *AssetRepository) GetEnabled(ctx context.Context) ([]*repository.SupportedAsset, error) {
	return r.queryList(ctx, `
		SELECT asset_code, asset_name, decimals, is_enabled, seed_amount, display_order
		FROM supported_assets
		WHERE is_enabled = true
		ORDER BY display_order
	`)
}

func (r *AssetRepository) GetByCode(ctx context.Context, assetCode string) (*repository.SupportedAsset, error) {
	query := `
		SELECT asset_code, asset_name, decimals, is_enabled, seed_amount, display_order
		FROM supported_assets
		WHERE asset_code = $1
	`
	var a repository.SupportedAsset
	err := r.db.QueryRow(ctx, query, assetCode).Scan(
		&a.AssetCode, &a.AssetName, &a.Decimals,
		&a.IsEnabled, &a.SeedAmount, &a.DisplayOrder,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query asset by code: %w", err)
	}
	return &a, nil
}

func (r *AssetRepository) queryList(ctx context.Context, sql string) ([]*repository.SupportedAsset, error) {
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

// Compile-time check.
var _ repository.AssetRepository = (*AssetRepository)(nil)
