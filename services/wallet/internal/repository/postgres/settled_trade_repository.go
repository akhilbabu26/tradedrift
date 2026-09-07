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
// Returns:
//   - (true,  nil) → inserted successfully; this transaction is the winner, proceed with settlement.
//   - (false, nil) → trade_id already exists with the same market_id and sequence; idempotent success.
//   - (false, ErrSettlementConflict) → trade_id already exists but with DIFFERENT market_id or sequence;
//     this indicates upstream replay corruption and must be surfaced as an error.
func (r *SettledTradeRepository) RegisterSettlement(ctx context.Context, tradeID, marketID string, sequence uint64) (bool, error) {
	insertQuery := `
		INSERT INTO settled_trades (trade_id, market_id, sequence, settled_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (trade_id) DO NOTHING
	`
	tag, err := r.db.Exec(ctx, insertQuery, tradeID, marketID, sequence)
	if err != nil {
		return false, fmt.Errorf("failed to register settled trade: %w", err)
	}
	if tag.RowsAffected() > 0 {
		// Row was inserted — this call is the first settlement for this TradeID.
		return true, nil
	}

	// Row already existed (ON CONFLICT DO NOTHING did nothing).
	// Read the existing record and compare to detect replay corruption.
	var existingMarketID string
	var existingSequence uint64
	selectQuery := `SELECT market_id, sequence FROM settled_trades WHERE trade_id = $1`
	err = r.db.QueryRow(ctx, selectQuery, tradeID).Scan(&existingMarketID, &existingSequence)
	if err != nil {
		return false, fmt.Errorf("failed to read existing settled trade record: %w", err)
	}

	if existingMarketID != marketID || existingSequence != sequence {
		return false, fmt.Errorf("%w: trade_id=%s stored=(market=%s seq=%d) incoming=(market=%s seq=%d)",
			repository.ErrSettlementConflict,
			tradeID, existingMarketID, existingSequence, marketID, sequence,
		)
	}

	// Same market_id and sequence — genuine idempotent duplicate.
	return false, nil
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
