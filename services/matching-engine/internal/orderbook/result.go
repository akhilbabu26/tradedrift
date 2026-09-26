package orderbook

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Fill represents one individual trade produced during matching.
// A single incoming order can produce multiple Fills in one sweep (multi-level).
// Each Fill gets its own TradeID — a deterministic UUIDv5 derived from
// eventID + makerID + takerID + fillIndex. The same inputs always produce
// the same TradeID, making fills safe to replay without duplicate detection.
type Fill struct {
	TradeID      uuid.UUID
	MarketID     string          // market this trade occurred in (e.g. "BTC-USDT")
	Sequence     uint64          // Authoritative matching engine sequence (> 0)
	MakerOrderID uuid.UUID       // the resting order that was consumed
	TakerOrderID uuid.UUID       // the incoming order that triggered the match
	BuyOrderID   uuid.UUID       // whichever of maker/taker had side == BUY
	SellOrderID  uuid.UUID       // whichever of maker/taker had side == SELL
	BuyerUserID  uuid.UUID
	SellerUserID uuid.UUID
	Price        decimal.Decimal // ALWAYS the maker's price — never taker's
	Quantity     decimal.Decimal // min(incoming.RemainingQty, best.RemainingQty)
}

// CancelStatus is the explicit outcome of a cancel attempt in the Matching Engine.
// Every user-requested cancel produces exactly one CancelStatus — never an implicit nil.
// Zero value is intentionally invalid; always set via a named constant.
type CancelStatus string

const (
	// CancelStatusRemovedFromBook — order was found in the book and removed.
	// Authoritative. Order Service MUST transition CANCELLING → CANCELLED and release funds.
	// This is the ONLY value that authorizes a wallet fund release downstream.
	CancelStatusRemovedFromBook CancelStatus = "REMOVED_FROM_BOOK"

	// CancelStatusAlreadyAbsent — order was not found in the book.
	// The ME cannot determine why: filled, previously cancelled, recovery
	// inconsistency, or ordering gap. The Order Service MUST consult its own DB
	// to decide the correct action. Funds MUST NOT be released on this value alone.
	CancelStatusAlreadyAbsent CancelStatus = "ALREADY_ABSENT"
)

// CancelOutcome is the typed return value of matcher.Cancel — never nil.
// Zero value is invalid — always construct with a named CancelStatus.
type CancelOutcome struct {
	Status            CancelStatus
	OrderID           uuid.UUID
	UserID            uuid.UUID       // populated when Status == CancelStatusRemovedFromBook
	MarketID          string          // populated when Status == CancelStatusRemovedFromBook
	RemainingQuantity decimal.Decimal // populated when Status == CancelStatusRemovedFromBook
}

// CancelledOrder is produced when an order is removed from the book.
// reason values:
//
//	"user_requested"           — explicit user cancel via cancel pipeline
//	"ioc_expired"              — MARKET order unfilled remainder
//	"invalid_order_parameters" — tick/lot size violation (defensive)
type CancelledOrder struct {
	OrderID           uuid.UUID
	UserID            uuid.UUID
	MarketID          string
	RemainingQuantity decimal.Decimal
	Reason            string
	// CancelStatus carries the explicit ME outcome for user-requested cancels.
	// Always set when Reason == "user_requested".
	// Zero value for "ioc_expired" and "invalid_order_parameters" — those use
	// the non-conditional MarkOrderCancelled DB path, not the cancel pipeline.
	CancelStatus CancelStatus
	CancelledAt  time.Time
	SourceOffset int64 // Kafka offset that initiated this cancellation
}

// DepthLevel is one price level in a depth snapshot.
type DepthLevel struct {
	Price    decimal.Decimal
	Quantity decimal.Decimal // PriceLevel.TotalQty — pre-aggregated
}

// DepthSnapshot is the top-N levels of the book, pushed to Redis after every match.
type DepthSnapshot struct {
	MarketID   string
	Sequence   uint64 // Authoritative matching engine sequence (> 0)
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
	SourcePosition KafkaPosition
	Snapshot       *BookSnapshot // optional serialized order book state
	BarrierReached bool          // true when EventRecoveryBarrier is processed
	BarrierOffset  int64         // offset of the checkpoint watermark triggering the barrier
}
