package market

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"tradedrift/services/matching-engine/internal/matcher"
	"tradedrift/services/matching-engine/internal/orderbook"
)

// Mode represents whether the market is replaying history, processing live events, or blocked.
type Mode int32

const (
	ModeRecovery    Mode = 0 // Replaying Kafka events — all output suppressed
	ModeLive        Mode = 1 // Normal operation — output emitted to Publisher
	ModeBlocked     Mode = 2 // Unrecoverable state / retention gap — all trading commands rejected
	ModeReconciling Mode = 3 // Empty-book reset applied — trading disabled until reconciliation approved
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
	OrderID       uuid.UUID
	UserID        uuid.UUID
	MarketID      string
	Side          orderbook.SideType
	OrderType     orderbook.OrderType
	Price         decimal.Decimal // zero for MARKET orders
	Quantity      decimal.Decimal
	ClientOrderID string
	OccurredAt    time.Time
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
	MarketID          string
	InputQueue        chan InputEvent              // buffered — Kafka Consumer sends here
	OutputQueue       chan orderbook.MatchResult   // buffered — Publisher reads from here
	book              *orderbook.OrderBook        // exclusively owned by Run() goroutine
	config            MarketConfig
	mode              atomic.Int32                // ModeRecovery, ModeLive, or ModeBlocked
	blockedReason     atomic.Pointer[error]       // error explaining why market entered ModeBlocked
	processedEvents   map[uuid.UUID]bool          // in-memory fast deduplication cache
	eventRing         eventRingBuffer             // O-1 eviction ring
	lastAppliedOffset int64                       // tracks in-memory application position (Issue #1)
	isFatalHalt       atomic.Bool                 // set on fatal failure; prevents drain on ctx.Done()
	isRunning         atomic.Bool                 // guards against duplicate active Run() loops
	HaltCallback      func()                      // callback to fail-stop process on corruption/failure
	snapshot          atomic.Pointer[MarketSnapshot] // lock-free coherent point-in-time state
}

// TriggerFatalHalt marks this engine as fatally halted and fires HaltCallback.
// The event loop will skip draining on ctx.Done() when this is set.
func (m *MarketEngine) TriggerFatalHalt() {
	if m.isFatalHalt.Swap(true) {
		return // already fatally halted
	}
	if m.HaltCallback != nil {
		m.HaltCallback()
	}
}

// IsFatalHalt returns whether this engine has undergone a fatal halt.
func (m *MarketEngine) IsFatalHalt() bool {
	return m.isFatalHalt.Load()
}

// IsRunning returns whether an active Run() loop is currently executing.
func (m *MarketEngine) IsRunning() bool {
	return m.isRunning.Load()
}

// NewMarketEngine creates a ready-to-run MarketEngine in RECOVERY mode.
func NewMarketEngine(config MarketConfig) *MarketEngine {
	m := &MarketEngine{
		MarketID:          config.MarketID,
		InputQueue:        make(chan InputEvent, 1000),
		OutputQueue:       make(chan orderbook.MatchResult, 1000),
		book:              orderbook.NewOrderBook(config.MarketID),
		config:            config,
		processedEvents:   make(map[uuid.UUID]bool, ringBufferCapacity),
		lastAppliedOffset: -1, // default to no offset applied
	}
	m.mode.Store(int32(ModeRecovery)) // always starts in RECOVERY
	m.snapshot.Store(&MarketSnapshot{
		MarketID:   config.MarketID,
		State:      "RECOVERING",
		Sequence:   0,
		SnapshotAt: time.Now().UTC(),
		Orders:     nil,
	})
	return m
}

// SetLive transitions the engine from ModeRecovery to ModeLive.
// Allowed ONLY when current mode is ModeRecovery (for normal replay completion).
// Fails if the engine is fatally halted, blocked, or in ModeReconciling.
func (m *MarketEngine) SetLive() error {
	if m.isFatalHalt.Load() {
		return fmt.Errorf("cannot transition market %s to ModeLive: engine is fatally halted", m.MarketID)
	}
	if !m.mode.CompareAndSwap(int32(ModeRecovery), int32(ModeLive)) {
		curr := Mode(m.mode.Load())
		return fmt.Errorf("cannot transition market %s to ModeLive: current mode is %v", m.MarketID, curr)
	}
	m.PublishAtomicSnapshot()
	return nil
}

// SetReconciling transitions the engine from ModeRecovery to ModeReconciling after an authorized reset.
// Fails if the engine is fatally halted or blocked.
func (m *MarketEngine) SetReconciling(reason string) error {
	if m.isFatalHalt.Load() {
		return fmt.Errorf("cannot transition market %s to ModeReconciling: engine is fatally halted", m.MarketID)
	}
	if reason == "" {
		reason = "empty-book reset completed; awaiting reconciliation approval"
	}
	err := errors.New(reason)
	m.blockedReason.Store(&err)
	if Mode(m.mode.Load()) == ModeReconciling {
		return nil
	}
	if !m.mode.CompareAndSwap(int32(ModeRecovery), int32(ModeReconciling)) {
		curr := Mode(m.mode.Load())
		return fmt.Errorf("cannot transition market %s to ModeReconciling: current mode is %v", m.MarketID, curr)
	}
	m.PublishAtomicSnapshot()
	return nil
}

// ApproveReconciliation transitions the engine from ModeReconciling to ModeLive.
// Allowed ONLY from ModeReconciling, and fails if the engine is fatally halted.
func (m *MarketEngine) ApproveReconciliation() error {
	if m.isFatalHalt.Load() {
		return fmt.Errorf("cannot approve reconciliation for market %s: engine is fatally halted", m.MarketID)
	}
	if !m.mode.CompareAndSwap(int32(ModeReconciling), int32(ModeLive)) {
		curr := Mode(m.mode.Load())
		return fmt.Errorf("cannot approve reconciliation for market %s: current mode is %v (expected ModeReconciling)", m.MarketID, curr)
	}
	m.blockedReason.Store(nil)
	m.PublishAtomicSnapshot()
	return nil
}

// SetBlocked transitions the engine to ModeBlocked and persists the failure reason.
// A blocked market rejects all trading commands and cannot transition to ModeLive.
// This transition is irreversible and safe against concurrent transitions.
func (m *MarketEngine) SetBlocked(err error) {
	if err == nil {
		err = errors.New("market blocked due to unrecoverable state")
	}
	m.blockedReason.Store(&err)
	for {
		curr := m.mode.Load()
		if Mode(curr) == ModeBlocked {
			break
		}
		if m.mode.CompareAndSwap(curr, int32(ModeBlocked)) {
			break
		}
	}
	m.PublishAtomicSnapshot()
}

// IsBlocked returns true if the engine is in ModeBlocked.
func (m *MarketEngine) IsBlocked() bool {
	return Mode(m.mode.Load()) == ModeBlocked
}

// IsReconciling returns true if the engine is in ModeReconciling.
func (m *MarketEngine) IsReconciling() bool {
	return Mode(m.mode.Load()) == ModeReconciling
}

// BlockedReason returns the recorded error that caused this market to become blocked.
func (m *MarketEngine) BlockedReason() error {
	ptr := m.blockedReason.Load()
	if ptr == nil {
		return nil
	}
	return *ptr
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
	if err := orderbook.Restore(m.book, snap, m.MarketID, m.config.Partition, checkpoint, expectedChecksum, m.config.TickSize, m.config.LotSize); err != nil {
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

// SetLastAppliedOffset restores the in-memory application position from a baseline checkpoint.
func (m *MarketEngine) SetLastAppliedOffset(offset int64) {
	m.lastAppliedOffset = offset
}

// Partition returns the Kafka partition ID assigned to this market.
func (m *MarketEngine) Partition() int {
	return m.config.Partition
}

// Mode returns the current engine mode.
func (m *MarketEngine) Mode() Mode {
	return Mode(m.mode.Load())
}