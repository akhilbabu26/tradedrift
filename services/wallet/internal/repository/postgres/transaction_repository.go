package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/pgconn"

	"tradedrift/services/wallet/internal/repository"
)

type TransactionRepository struct {
	db *pgxpool.Pool
}

func NewTransactionRepository(db *pgxpool.Pool) *TransactionRepository {
	return &TransactionRepository{db: db}
}

func (r *TransactionRepository) Create(ctx context.Context, t *repository.WalletTransaction) error {
	query := `
		INSERT INTO wallet_transactions (
			id, wallet_id, reference_id, reference_type,
			transaction_type, asset, amount, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err := r.db.Exec(ctx, query,
		t.ID, t.WalletID, t.ReferenceID, t.ReferenceType,
		t.TransactionType, t.Asset, t.Amount, t.CreatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return repository.ErrDuplicate
		}
		return fmt.Errorf("failed to insert wallet transaction: %w", err)
	}
	return nil
}

func (r *TransactionRepository) ExistsByKey(ctx context.Context, referenceID, referenceType, asset string) (bool, error) {
	query := `
		SELECT EXISTS (
			SELECT 1 FROM wallet_transactions
			WHERE reference_id = $1
			  AND reference_type = $2
			  AND asset = $3
		)
	`
	var exists bool
	err := r.db.QueryRow(ctx, query, referenceID, referenceType, asset).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check transaction existence: %w", err)
	}
	return exists, nil
}

func (r *TransactionRepository) CreateBatch(ctx context.Context, txns []*repository.WalletTransaction) error {
	for _, t := range txns {
		if err := r.Create(ctx, t); err != nil {
			return err
		}
	}
	return nil
}
