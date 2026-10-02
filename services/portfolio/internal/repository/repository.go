package repository

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

var (
	ErrInsufficientHoldings  = errors.New("seller has insufficient holdings for trade")
	ErrSelfTrade             = errors.New("self-trade detected: buyer_id equals seller_id")
	ErrTradeAlreadyProcessed = errors.New("trade has already been processed")
	ErrSequenceCollision     = errors.New("sequence collision detected: sequence already claimed by another trade")
	ErrTradeConflict         = errors.New("trade metadata conflict: duplicate trade with differing metadata")
	ErrOutboxLeaseExpired    = errors.New("outbox lease expired or row is no longer in PROCESSING state")
)

// Holding represents a user's cumulative position in a crypto asset.
// Invariant PI-1: No USDT holding row is ever stored here.
type Holding struct {
	UserID      string          `json:"user_id"`
	AssetCode   string          `json:"asset_code"`
	Quantity    decimal.Decimal `json:"quantity"`
	TotalCost   decimal.Decimal `json:"total_cost"`
	RealizedPnL decimal.Decimal `json:"realized_pnl"`
	Version     int64           `json:"version"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// AverageEntryPrice calculates weighted average entry cost: TotalCost / Quantity.
func (h Holding) AverageEntryPrice() decimal.Decimal {
	if h.Quantity.IsZero() || h.Quantity.IsNegative() {
		return decimal.Zero
	}
	return h.TotalCost.Div(h.Quantity)
}

// ProcessedTrade records idempotency and audit metadata to prevent double-counting on Kafka replays.
type ProcessedTrade struct {
	TradeID     string    `json:"trade_id"`
	UserID      string    `json:"user_id"`
	MarketID    string    `json:"market_id"`
	Sequence    uint64    `json:"sequence"`
	ProcessedAt time.Time `json:"processed_at"`
}

// OutboxMessage represents a pending, processing, or published PortfolioUpdated event.
type OutboxMessage struct {
	ID           string     `json:"id"`
	AggregateID  string     `json:"aggregate_id"` // user_id
	EventType    string     `json:"event_type"`   // "PortfolioUpdated"
	Payload      []byte     `json:"payload"`
	PartitionKey string     `json:"partition_key"` // user_id
	Status       string     `json:"status"`        // "PENDING" | "PROCESSING" | "PUBLISHED"
	ClaimedAt    *time.Time `json:"claimed_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	PublishedAt  *time.Time `json:"published_at,omitempty"`
}

// BootstrapInput identifies a bootstrap position record to apply.
// Quantities and cost basis are read from portfolio_bootstrap_positions,
// not passed here — the migration SQL is the single source of truth for seed values.
type BootstrapInput struct {
	UserID    string
	AssetCode string
	Source    string // e.g. "SYSTEM_BOOTSTRAP"
}

// UserTradeInput is the normalized accounting input for a single user leg from portfolio.user.trades.v1.
type UserTradeInput struct {
	TradeID    string
	UserID     string
	OrderID    string
	Role       string // "BUY" | "SELL"
	MarketID   string
	BaseAsset  string
	QuoteAsset string
	Price      decimal.Decimal
	Quantity   decimal.Decimal
	Sequence   uint64
	ExecutedAt time.Time
	SettledAt  time.Time
}

// TradeSettledInput is the normalized accounting input passed from the Kafka consumer.
type TradeSettledInput struct {
	TradeID    string
	BuyerID    string
	SellerID   string
	MarketID   string
	BaseAsset  string
	QuoteAsset string
	Price      decimal.Decimal
	Quantity   decimal.Decimal
	Sequence   uint64
	ExecutedAt time.Time
	SettledAt  time.Time
}

// Repository defines the storage contract for Portfolio accounting and outbox management.
type Repository interface {
	// GetHoldingsByUser returns all non-zero asset holdings for a given user.
	GetHoldingsByUser(ctx context.Context, userID string) ([]Holding, error)

	// ProcessUserTrade executes the atomic single-user position mutation transaction:
	// Sequence integrity assertion -> User leg idempotency check -> Physical row lock ->
	// Buy/Sell accounting -> Invariant assertions -> Version increment -> Outbox event.
	ProcessUserTrade(ctx context.Context, input UserTradeInput) (*OutboxMessage, error)

	// Deprecated: ProcessTradeSettled is retained exclusively for legacy/audit compatibility
	// and is NOT invoked by the active consumer. Production accounting uses ProcessUserTrade.
	ProcessTradeSettled(ctx context.Context, input TradeSettledInput) ([]OutboxMessage, error)

	// InitializePositionFromBootstrap applies the opening position defined in
	// portfolio_bootstrap_positions for (user_id, asset_code, source) to holdings exactly once.
	//
	// Behaviour:
	//   - Row not found in bootstrap table      → (false, nil)  — no-op
	//   - applied_at already set                → (false, nil)  — idempotent on restart
	//   - holdings row does not exist           → INSERT with bootstrap quantity
	//   - holdings row exists (prior trades)    → UPDATE quantity += bootstrap quantity
	//   - In all cases applied_at is set in the same commit as the holdings change
	//
	// Returns (true, nil) when bootstrap was applied this call.
	InitializePositionFromBootstrap(ctx context.Context, in BootstrapInput) (bool, error)

	// FetchPendingOutbox returns up to limit unhandled outbox events using FOR UPDATE SKIP LOCKED.
	FetchPendingOutbox(ctx context.Context, limit int) ([]OutboxMessage, error)

	// MarkOutboxPublished updates outbox records to 'PUBLISHED' with published_at = NOW().
	MarkOutboxPublished(ctx context.Context, ids []string) error
}
