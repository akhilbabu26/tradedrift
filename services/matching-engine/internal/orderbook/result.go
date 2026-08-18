package orderbook

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Fill represents one individual trade produced during matching.
// A single incoming order can produce multiple Fills in one sweep (multi-level).
// Each Fill gets its own TradeID (UUIDv7 generated in-memory at match time).
type Fill struct {
	TradeID      uuid.UUID
	MarketID     string          // market this trade occurred in (e.g. "BTC-USDT")
	MakerOrderID uuid.UUID       // the resting order that was consumed
	TakerOrderID uuid.UUID       // the incoming order that triggered the match
	BuyOrderID   uuid.UUID       // whichever of maker/taker had side == BUY
	SellOrderID  uuid.UUID       // whichever of maker/taker had side == SELL
	BuyerUserID  uuid.UUID
	SellerUserID uuid.UUID
	Price        decimal.Decimal // ALWAYS the maker's price — never taker's
	Quantity     decimal.Decimal // min(incoming.RemainingQty, best.RemainingQty)
}

// CancelledOrder is produced when an order is removed from the book.
// reason values:
//   "user_requested"          — explicit user cancel
//   "ioc_expired"             — MARKET order unfilled remainder
//   "invalid_order_parameters" — tick/lot size violation (defensive)
type CancelledOrder struct {
	OrderID           uuid.UUID
	UserID            uuid.UUID
	MarketID          string
	RemainingQuantity decimal.Decimal
	Reason            string
	CancelledAt       time.Time
}

// DepthLevel is one price level in a depth snapshot.
type DepthLevel struct {
	Price    decimal.Decimal
	Quantity decimal.Decimal // PriceLevel.TotalQty — pre-aggregated
}

// DepthSnapshot is the top-N levels of the book, pushed to Redis after every match.
type DepthSnapshot struct {
	MarketID   string
	Bids       []DepthLevel
	Asks       []DepthLevel
	SnapshotAt time.Time
}

// KafkaPosition uniquely identifies one message's position in Kafka.
// Topic + Partition + Offset together form the global checkpoint key.
// Offset alone is NOT globally unique — it is only unique within one partition.
type KafkaPosition struct {
	Topic     string
	Partition int
	Offset    int64
}

// MatchResult is produced for every single Kafka input event (one-in one-out).
type MatchResult struct {
	Fills          []Fill
	CancelResult   *CancelledOrder
	DepthSnapshot  DepthSnapshot
	SourcePosition KafkaPosition   // ← replaces SourceOffset int64
}
