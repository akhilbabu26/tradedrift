package market

import (
    "github.com/google/uuid"
    "github.com/shopspring/decimal"

    "tradedrift/services/matching-engine/internal/matcher"
    "tradedrift/services/matching-engine/internal/orderbook"
)

// Mode represents whether the market is replaying history or processing live events.
type Mode int

const (
	ModeRecovery Mode = iota // Replaying Kafka events — all output suppressed
	ModeLive                 // Normal operation — output emitted to Publisher
)

// MarketConfig holds market-specific rules fetched from Market Service at startup.
type MarketConfig struct {
	MarketID string
	TickSize decimal.Decimal // minimum price increment
	LotSize  decimal.Decimal // minimum quantity increment
}

// InputEvent wraps a raw Kafka message routed to this market's Event Loop.
type InputEvent struct {
	Type         EventType
	OrderCreated *OrderCreatedPayload     // non-nil when Type == EventOrderCreated
	OrderCancel  *OrderCancelPayload      // non-nil when Type == EventOrderCancel
	Topic        string
	Partition    int
	Offset       int64                    // Kafka offset — used for checkpoint
}

type EventType int

const (
	EventOrderCreated EventType = iota
	EventOrderCancel
)

// OrderCreatedPayload is the deserialized form of the Kafka OrderCreated event.
type OrderCreatedPayload struct {
	OrderID   uuid.UUID
	UserID    uuid.UUID
	MarketID  string
	Side      orderbook.SideType
	OrderType orderbook.OrderType
	Price     decimal.Decimal // zero for MARKET orders
	Quantity  decimal.Decimal
}

// OrderCancelPayload is the deserialized form of the Kafka OrderCancelRequested event.
type OrderCancelPayload struct {
	OrderID  uuid.UUID
	UserID   uuid.UUID
	MarketID string
}

// MarketEngine owns one OrderBook for one market.
// It is driven by its Event Loop goroutine exclusively.
//
// Goroutine ownership rule (enforced by construction, not by mutex):
//   - Kafka Consumer   → touches only InputQueue
//   - Event Loop       → exclusively owns Book
//   - Publisher        → touches only OutputQueue
type MarketEngine struct {
	MarketID    string
	InputQueue  chan InputEvent              // buffered — Kafka Consumer sends here
	OutputQueue chan orderbook.MatchResult   // buffered — Publisher reads from here
	book        *orderbook.OrderBook        // exclusively owned by Run() goroutine
	config      MarketConfig
	mode        Mode
}

// NewMarketEngine creates a ready-to-run MarketEngine in RECOVERY mode.
func NewMarketEngine(config MarketConfig) *MarketEngine {
	return &MarketEngine{
		MarketID:    config.MarketID,
		InputQueue:  make(chan InputEvent, 1000),
		OutputQueue: make(chan orderbook.MatchResult, 1000),
		book:        orderbook.NewOrderBook(config.MarketID),
		config:      config,
		mode:        ModeRecovery, // always starts in RECOVERY
	}
}

// SetLive transitions the engine from RECOVERY to LIVE mode.
// Called by the recovery package after replay reaches the checkpoint offset.
func (m *MarketEngine) SetLive() {
	m.mode = ModeLive
}

// GetDepth returns the current Top-N depth snapshot from the in-memory book.
// Used by the Replayer after replay completes to push a fresh snapshot to Redis
// before going live. Safe to call from the Replayer because it runs before
// engine.Run() handles any live events.
func (m *MarketEngine) GetDepth(n int) orderbook.DepthSnapshot {
	return matcher.GetDepth(m.book, n)
}

// GetSequence returns the current authoritative order book sequence.
func (m *MarketEngine) GetSequence() uint64 {
	return m.book.Sequence
}

// SetSequence restores the sequence from a persisted baseline checkpoint.
func (m *MarketEngine) SetSequence(seq uint64) {
	if seq > m.book.Sequence {
		m.book.Sequence = seq
	}
}