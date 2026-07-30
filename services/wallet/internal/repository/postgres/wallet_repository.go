package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/wallet/internal/repository"
)

type WalletRepository struct {
	db *pgxpool.Pool
}

func NewWalletRepository(db *pgxpool.Pool) *WalletRepository {
	return &WalletRepository{db: db}
}

func (r *WalletRepository) GetByUserAndAsset(ctx context.Context, userID, asset string) (*repository.Wallet, error) {
	query := `
		SELECT id, user_id, asset, available_balance, reserved_balance,
		       is_frozen, frozen_at, frozen_by, freeze_reason,
		       initial_balance, total_balance, created_at, updated_at
		FROM wallets
		WHERE user_id = $1 AND asset = $2
	`
	var w repository.Wallet
	err := r.db.QueryRow(ctx, query, userID, asset).Scan(
		&w.ID, &w.UserID, &w.Asset, &w.AvailableBalance, &w.ReservedBalance,
		&w.IsFrozen, &w.FrozenAt, &w.FrozenBy, &w.FreezeReason,
		&w.InitialBalance, &w.TotalBalance, &w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query wallet: %w", err)
	}
	return &w, nil
}

func (r *WalletRepository) GetAllByUser(ctx context.Context, userID string) ([]*repository.Wallet, error) {
	query := `
		SELECT id, user_id, asset, available_balance, reserved_balance,
		       is_frozen, frozen_at, frozen_by, freeze_reason,
		       initial_balance, total_balance, created_at, updated_at
		FROM wallets
		WHERE user_id = $1
		ORDER BY (SELECT display_order FROM supported_assets WHERE asset_code = wallets.asset)
	`
	rows, err := r.db.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to query wallets by user: %w", err)
	}
	defer rows.Close()

	var wallets []*repository.Wallet
	for rows.Next() {
		var w repository.Wallet
		if err := rows.Scan(
			&w.ID, &w.UserID, &w.Asset, &w.AvailableBalance, &w.ReservedBalance,
			&w.IsFrozen, &w.FrozenAt, &w.FrozenBy, &w.FreezeReason,
			&w.InitialBalance, &w.TotalBalance, &w.CreatedAt, &w.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan wallet row: %w", err)
		}
		wallets = append(wallets, &w)
	}
	return wallets, nil
}

func (r *WalletRepository) Create(ctx context.Context, w *repository.Wallet) error {
	query := `
		INSERT INTO wallets (
			id, user_id, asset, available_balance, reserved_balance,
			is_frozen, initial_balance, total_balance, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
	`
	_, err := r.db.Exec(ctx, query,
		w.ID, w.UserID, w.Asset, w.AvailableBalance, w.ReservedBalance,
		w.IsFrozen, w.InitialBalance, w.TotalBalance,
	)
	if err != nil {
		return fmt.Errorf("failed to insert wallet: %w", err)
	}
	return nil
}

func (r *WalletRepository) CreditAvailable(ctx context.Context, walletID, amount string) error {
	query := `
		UPDATE wallets
		SET available_balance = available_balance + $1::DECIMAL,
		    total_balance      = total_balance + $1::DECIMAL,
		    updated_at         = NOW()
		WHERE id = $2
	`
	res, err := r.db.Exec(ctx, query, amount, walletID)
	if err != nil {
		return fmt.Errorf("failed to credit available balance: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("wallet not found: %s", walletID)
	}
	return nil
}

func (r *WalletRepository) DebitAvailable(ctx context.Context, walletID, amount string) error {
	query := `
		UPDATE wallets
		SET available_balance = available_balance - $1::DECIMAL,
		    total_balance      = total_balance - $1::DECIMAL,
		    updated_at         = NOW()
		WHERE id = $2
		  AND available_balance >= $1::DECIMAL
	`
	res, err := r.db.Exec(ctx, query, amount, walletID)
	if err != nil {
		return fmt.Errorf("failed to debit available balance: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("insufficient balance or wallet not found")
	}
	return nil
}

func (r *WalletRepository) MoveToReserved(ctx context.Context, walletID, amount string) error {
	query := `
		UPDATE wallets
		SET available_balance = available_balance - $1::DECIMAL,
		    reserved_balance  = reserved_balance  + $1::DECIMAL,
		    updated_at        = NOW()
		WHERE id = $2
		  AND available_balance >= $1::DECIMAL
	`
	res, err := r.db.Exec(ctx, query, amount, walletID)
	if err != nil {
		return fmt.Errorf("failed to move funds to reserved: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("insufficient balance or wallet not found")
	}
	return nil
}

func (r *WalletRepository) MoveFromReserved(ctx context.Context, walletID, amount string) error {
	query := `
		UPDATE wallets
		SET reserved_balance  = reserved_balance  - $1::DECIMAL,
		    available_balance = available_balance + $1::DECIMAL,
		    updated_at        = NOW()
		WHERE id = $2
	`
	res, err := r.db.Exec(ctx, query, amount, walletID)
	if err != nil {
		return fmt.Errorf("failed to move funds from reserved: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("wallet not found: %s", walletID)
	}
	return nil
}

func (r *WalletRepository) DebitReserved(ctx context.Context, walletID, amount string) error {
	query := `
		UPDATE wallets
		SET reserved_balance = reserved_balance - $1::DECIMAL,
		    total_balance     = total_balance    - $1::DECIMAL,
		    updated_at        = NOW()
		WHERE id = $2
	`
	res, err := r.db.Exec(ctx, query, amount, walletID)
	if err != nil {
		return fmt.Errorf("failed to debit reserved balance: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("wallet not found: %s", walletID)
	}
	return nil
}

func (r *WalletRepository) FreezeWallet(ctx context.Context, walletID, frozenBy, reason string) error {
	query := `
		UPDATE wallets
		SET is_frozen     = true,
		    frozen_at     = NOW(),
		    frozen_by     = $2,
		    freeze_reason = $3,
		    updated_at    = NOW()
		WHERE id = $1
	`
	res, err := r.db.Exec(ctx, query, walletID, frozenBy, reason)
	if err != nil {
		return fmt.Errorf("failed to freeze wallet: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("wallet not found: %s", walletID)
	}
	return nil
}

func (r *WalletRepository) UnfreezeWallet(ctx context.Context, walletID string) error {
	query := `
		UPDATE wallets
		SET is_frozen     = false,
		    frozen_at     = NULL,
		    frozen_by     = NULL,
		    freeze_reason = NULL,
		    updated_at    = NOW()
		WHERE id = $1
	`
	res, err := r.db.Exec(ctx, query, walletID)
	if err != nil {
		return fmt.Errorf("failed to unfreeze wallet: %w", err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("wallet not found: %s", walletID)
	}
	return nil
}
