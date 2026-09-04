package repository

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

var (
	ErrInsufficientHoldings   = errors.New("seller has insufficient holdings for trade")
	ErrSelfTrade              = errors.New("self-trade detected: buyer_id equals seller_id")
	ErrTradeAlreadyProcessed  = errors.New("trade has already been processed")
)

// Holding represents a user's cumulative position in a crypto asset.
// Invariant PI-1: No USDT holding row is ever stored here.
type Holding struct {
	UserID            string          `json:"user_id"`
	AssetCode         string          `json:"asset_code"`
	Quantity          decimal.Decimal `json:"quantity"`
	TotalCost         decimal.Decimal `json:"total_cost"`
	RealizedPnL       decimal.Decimal `json:"realized_pnl"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// AverageEntryPrice calculates weighted average entry cost: TotalCost / Quantity.
func (h Holding) AverageEntryPrice() decimal.Decimal {
	if h.Quantity.IsZero() || h.Quantity.IsNegative() {
		return decimal.Zero
	}
	return h.TotalCost.Div(h.Quantity)
}

// ProcessedTrade records idempotency to prevent double-counting on Kafka replays.
type ProcessedTrade struct {
	TradeID     string    `json:"trade_id"`
	UserID      string    `json:"user_id"`
	ProcessedAt time.Time `json:"processed_at"`
}

// OutboxMessage represents a pending or published PortfolioUpdated event.
type OutboxMessage struct {
	ID           string     `json:"id"`
	AggregateID  string     `json:"aggregate_id"`  // user_id
	EventType    string     `json:"event_type"`    // "PortfolioUpdated"
	Payload      []byte     `json:"payload"`
	PartitionKey string     `json:"partition_key"` // user_id
	Status       string     `json:"status"`        // "PENDING" | "PUBLISHED"
	CreatedAt    time.Time  `json:"created_at"`
	PublishedAt  *time.Time `json:"published_at,omitempty"`
}

// TradeSettledInput is the normalized accounting input passed from the Kafka consumer.
type TradeSettledInput struct {
	TradeID      string
	BuyerID      string
	SellerID     string
	MarketID     string
	BaseAsset    string
	QuoteAsset   string
	Price        decimal.Decimal
	Quantity     decimal.Decimal
	ExecutedAt   time.Time
	SettledAt    time.Time
}

// Repository defines the storage contract for Portfolio accounting and outbox management.
type Repository interface {
	// GetHoldingsByUser returns all non-zero asset holdings for a given user.
	GetHoldingsByUser(ctx context.Context, userID string) ([]Holding, error)

	// ProcessTradeSettled executes the 1-atomic transaction:
	// Deduplication check -> Deterministic row locks -> Buyer calculation ->
	// Seller calculation -> Outbox insertion -> ProcessedTrade record.
	ProcessTradeSettled(ctx context.Context, input TradeSettledInput) ([]OutboxMessage, error)

	// FetchPendingOutbox returns up to limit unhandled outbox events using FOR UPDATE SKIP LOCKED.
	FetchPendingOutbox(ctx context.Context, limit int) ([]OutboxMessage, error)

	// MarkOutboxPublished updates outbox records to 'PUBLISHED' with published_at = NOW().
	MarkOutboxPublished(ctx context.Context, ids []string) error
}
