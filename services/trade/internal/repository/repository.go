package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ErrSequenceConflict is returned by Create when a different trade_id already
// holds the same (market_id, me_sequence) in the database.
// This violates the UNIQUE(market_id, me_sequence) constraint and signals a
// Matching Engine producer integrity bug — the event is not retryable.
// The consumer maps this to a *PoisonError and routes it to the DLQ.
var ErrSequenceConflict = errors.New("me_sequence already exists for this market")

// Trade is the domain entity owned by Trade Service.
// It is immutable after creation — no UPDATE or DELETE ever executes on it.
type Trade struct {
	ID           uuid.UUID
	BuyerID      uuid.UUID
	SellerID     uuid.UUID
	BuyOrderID   uuid.UUID
	SellOrderID  uuid.UUID
	MarketID     string
	BaseAsset    string
	QuoteAsset   string
	Price        decimal.Decimal
	Quantity     decimal.Decimal
	Sequence     uint64    // ME per-market monotonic counter (> 0)
	ExecutedAt   time.Time // ME clock
	SettledAt    time.Time // Wallet clock
}

// Cursor encodes the keyset pagination position for trade list queries.
// Pagination is on (executed_at DESC, id DESC) — stable under concurrent inserts.
type Cursor struct {
	ExecutedAt time.Time
	ID         uuid.UUID
}

// Repository defines the data access contract for the Trade Service.
// All methods are safe for concurrent use from the Kafka consumer and gRPC handlers.
type Repository interface {
	// Create inserts a trade row. Idempotent on Trade.ID:
	//   - If id already exists → ON CONFLICT (id) DO NOTHING → returns nil.
	//   - If (market_id, me_sequence) already exists for a DIFFERENT id →
	//     returns ErrSequenceConflict (producer integrity violation).
	//   - Any other error → retryable DB/network failure.
	Create(ctx context.Context, t *Trade) error

	// GetByID returns the trade with the given id, or nil if not found.
	GetByID(ctx context.Context, id uuid.UUID) (*Trade, error)

	// ListByUser returns trades where userID is buyer OR seller,
	// cursor-paginated on (executed_at DESC, id DESC).
	// marketID filters by market when non-empty.
	ListByUser(ctx context.Context, userID uuid.UUID, marketID string, after *Cursor, limit int) ([]Trade, error)

	// ListByMarket returns public trades for a market, without user identity fields.
	// Cursor-paginated on (executed_at DESC, id DESC).
	ListByMarket(ctx context.Context, marketID string, after *Cursor, limit int) ([]Trade, error)
}
