package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ─── Status Constants ──────────────────────────────────────────────────────────

const (
	StatusPending = "PENDING"
	StatusSettled = "SETTLED"
	StatusFailed  = "FAILED"
)

// ─── Domain Entity ─────────────────────────────────────────────────────────────

// SettledTrade represents one row in the settled_trades ledger.
// It tracks both phases of settlement:
//   - PENDING: trade registered, gRPC call to Wallet not yet confirmed
//   - SETTLED: wallet balances atomically updated, Kafka offset acknowledged
//   - FAILED: terminal failure after max retries or non-recoverable business error
type SettledTrade struct {
	TradeID     uuid.UUID
	BuyerID     uuid.UUID
	SellerID    uuid.UUID
	BuyOrderID  uuid.UUID
	SellOrderID uuid.UUID
	MarketID    string
	BaseAsset   string
	QuoteAsset  string
	Price       string
	Quantity    string
	Sequence    uint64 // ME per-market monotonic counter — stored for recovery
	Status      string
	ExecutedAt  time.Time
	SettledAt   *time.Time // nil until status = SETTLED
	RetryCount  int
	NextRetryAt *time.Time
	LastError   *string
}

// ─── Repository Interface ──────────────────────────────────────────────────────

// Repository defines the data access interface for the settled_trades ledger.
// All methods must be safe for concurrent use from the Kafka consumer
// and the background recovery goroutine.
type Repository interface {
	// Insert records a new trade with status=PENDING.
	// Uses ON CONFLICT (trade_id) DO NOTHING — safe if two goroutines
	// race to insert the same trade_id (e.g. during partition rebalance).
	Insert(ctx context.Context, t *SettledTrade) error

	// FindByTradeID returns the settlement record for the given trade.
	// Returns nil, nil if no row exists (not an error).
	FindByTradeID(ctx context.Context, id uuid.UUID) (*SettledTrade, error)

	// MarkSettled transitions a PENDING row to SETTLED and records settled_at.
	// This is Phase 3 of the 3-phase settlement pipeline.
	MarkSettled(ctx context.Context, id uuid.UUID) error

	// RecordSettlementFailure atomically updates retry_count, last_error, and next_retry_at.
	// If permanent is true, status is transitioned to FAILED.
	RecordSettlementFailure(ctx context.Context, id uuid.UUID, lastErr string, permanent bool, nextRetry time.Time) error

	// FindStalePending returns up to `limit` PENDING rows whose next_retry_at
	// is past, using FOR UPDATE SKIP LOCKED.
	// SKIP LOCKED ensures the recovery goroutine never races the main consumer.
	FindStalePending(ctx context.Context, olderThan time.Duration, limit int) ([]*SettledTrade, error)
}
