package market

import (
	"time"

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
	MarketID         string
	TickSize         decimal.Decimal // minimum price increment
	LotSize          decimal.Decimal // minimum quantity increment
	Partition        int             // Kafka partition assigned to this market
	SnapshotInterval int             // snapshot interval in event count
	SnapshotDuration time.Duration   // snapshot interval in time duration
}

// InputEvent wraps a raw Kafka message routed to this market's Event Loop.
type InputEvent struct {
	EventID      uuid.UUID
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
	EventRecoveryBarrier
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

// eventRingBuffer is a fixed-capacity FIFO ring buffer of UUIDs for O(1) deduplication eviction.
// When the buffer is full, the oldest entry is evicted in O(1) without any slice shifts.
const ringBufferCapacity = 50_000

type eventRingBuffer struct {
	slots [ringBufferCapacity]uuid.UUID
	head  int // next write position (oldest entry)
	count int // number of live entries
}

// add inserts an event ID, returning the evicted UUID (or uuid.Nil if not yet full).
func (r *eventRingBuffer) add(id uuid.UUID) (evicted uuid.UUID) {
	if r.count == ringBufferCapacity {
		// Buffer full — evict the oldest entry at head.
		evicted = r.slots[r.head]
		r.slots[r.head] = id
		r.head = (r.head + 1) % ringBufferCapacity
	} else {
		// Buffer not yet full — write at (head + count) % capacity.
		pos := (r.head + r.count) % ringBufferCapacity
		r.slots[pos] = id
		r.count++
	}
	return evicted
}

// MarketEngine owns one OrderBook for one market.
// It is driven by its Event Loop goroutine exclusively.
//
// Goroutine ownership rule (enforced by construction, not by mutex):
//   - Kafka Consumer   → touches only InputQueue
//   - Event Loop       → exclusively owns Book
//   - Publisher        → touches only OutputQueue
type MarketEngine struct {
	MarketID          string
	InputQueue        chan InputEvent              // buffered — Kafka Consumer sends here
	OutputQueue       chan orderbook.MatchResult   // buffered — Publisher reads from here
	book              *orderbook.OrderBook        // exclusively owned by Run() goroutine
	config            MarketConfig
	mode              Mode
	processedEvents   map[uuid.UUID]bool          // in-memory fast deduplication cache
	eventRing         eventRingBuffer             // O-1 eviction ring
	lastAppliedOffset int64                       // tracks in-memory application position (Issue #1)
	HaltCallback      func()                      // callback to fail-stop process on corruption/failure
}

// NewMarketEngine creates a ready-to-run MarketEngine in RECOVERY mode.
func NewMarketEngine(config MarketConfig) *MarketEngine {
	return &MarketEngine{
		MarketID:          config.MarketID,
		InputQueue:        make(chan InputEvent, 1000),
		OutputQueue:       make(chan orderbook.MatchResult, 1000),
		book:              orderbook.NewOrderBook(config.MarketID),
		config:            config,
		mode:              ModeRecovery, // always starts in RECOVERY
		processedEvents:   make(map[uuid.UUID]bool, ringBufferCapacity),
		lastAppliedOffset: -1, // default to no offset applied
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
	m.book.Sequence = seq
}

// RestoreFromSnapshot validates metadata, resets the book structures, and restores book order nodes.
func (m *MarketEngine) RestoreFromSnapshot(snap orderbook.BookSnapshot, expectedChecksum []byte, checkpoint int64) error {
	if err := orderbook.Restore(m.book, snap, m.MarketID, m.config.Partition, checkpoint, expectedChecksum); err != nil {
		return err
	}
	m.book.Sequence = snap.Sequence
	m.lastAppliedOffset = snap.Offset
	return nil
}

// GetLastAppliedOffset returns the in-memory last applied offset.
func (m *MarketEngine) GetLastAppliedOffset() int64 {
	return m.lastAppliedOffset
}

// Partition returns the Kafka partition ID assigned to this market.
func (m *MarketEngine) Partition() int {
	return m.config.Partition
}