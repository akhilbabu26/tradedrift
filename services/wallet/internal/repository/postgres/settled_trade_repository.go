package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"tradedrift/services/wallet/internal/repository"
)

type SettledTradeRepository struct {
	db repository.DBTX
}

func NewSettledTradeRepository(db repository.DBTX) *SettledTradeRepository {
	return &SettledTradeRepository{db: db}
}

func (r *SettledTradeRepository) WithTx(tx pgx.Tx) repository.SettledTradeRepository {
	return &SettledTradeRepository{db: tx}
}

// RegisterSettlement attempts to atomically insert a settled trade.
// Returns true if successfully inserted (winner of the settlement race),
// or false if the trade_id was already present (duplicate/already settled).
func (r *SettledTradeRepository) RegisterSettlement(ctx context.Context, tradeID, marketID string, sequence uint64) (bool, error) {
	query := `
		INSERT INTO settled_trades (trade_id, market_id, sequence, settled_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (trade_id) DO NOTHING;
	`
	tag, err := r.db.Exec(ctx, query, tradeID, marketID, sequence)
	if err != nil {
		return false, fmt.Errorf("failed to register settled trade: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// IsSettled checks whether trade_id is present in settled_trades.
func (r *SettledTradeRepository) IsSettled(ctx context.Context, tradeID string) (bool, error) {
	query := `SELECT EXISTS (SELECT 1 FROM settled_trades WHERE trade_id = $1)`
	var exists bool
	err := r.db.QueryRow(ctx, query, tradeID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check settled trade existence: %w", err)
	}
	return exists, nil
}

// Compile-time check.
var _ repository.SettledTradeRepository = (*SettledTradeRepository)(nil)
