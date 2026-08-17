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

// MatchResult is produced for every single Kafka input event (one-in one-out).
// It bundles all fills + any cancel + the depth snapshot + the source Kafka offset.
// SourceOffset is used by the Publisher to write exactly one checkpoint per event.
type MatchResult struct {
	Fills         []Fill
	CancelResult  *CancelledOrder // non-nil for cancels, rejects, or IOC expiry
	DepthSnapshot DepthSnapshot
	SourceOffset  int64           // Kafka offset of the input event
}
