package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// SettledTradeRepository defines the persistence contract for trade settlement idempotency.
type SettledTradeRepository interface {
	// WithTx binds the repository to an active PostgreSQL transaction.
	WithTx(tx pgx.Tx) SettledTradeRepository

	// RegisterSettlement attempts to atomically insert a settled trade row.
	// Returns (true, nil) if inserted, or (false, nil) if the trade was already settled.
	RegisterSettlement(ctx context.Context, tradeID, marketID string, sequence uint64) (bool, error)

	// IsSettled checks whether a trade has already been settled.
	IsSettled(ctx context.Context, tradeID string) (bool, error)
}
