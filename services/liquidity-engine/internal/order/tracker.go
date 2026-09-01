// Package order provides the in-memory tracker for MM-001 resting orders.
//
// The tracker is the LE's working picture of what orders currently exist.
// It is NOT authoritative — the Order Service is authoritative.
// On startup, the tracker is populated from ListMMOrders().
// During operation it is updated by reconcile cycles and trade events.
//
// All tracker mutations happen in the engine's single event loop goroutine.
// No locking is required because of this constraint.
package order

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"tradedrift/services/liquidity-engine/internal/pricing"
)

// Status represents the lifecycle state of a tracked MM order.
type Status string

const (
	// StatusPending — OrderCreate published to Kafka, awaiting OS confirmation.
	// The LE has sent the command but has not yet verified the OS received it.
	// Diff: excluded from CREATE.
	StatusPending Status = "PENDING"

	// StatusOSRegistered — Order Service has confirmed the order exists (OPEN).
	// This means OS will return it on ListMMOrders for recovery.
	// It does NOT mean ME has the order in the live order book.
	// Diff: excluded from CREATE (already in flight).
	// Transitions to RESTING after MEConfirmationTimeout if ME is healthy.
	StatusOSRegistered Status = "OS_REGISTERED"

	// StatusResting — ME has accepted the order into the live order book.
	// Confirmed indirectly: OS is OPEN and ME liveness healthy for MEConfirmationTimeout.
	// In V2, an OrderRested Kafka event from ME will trigger this directly.
	// Diff: eligible for CANCEL or CORRECT.
	StatusResting Status = "RESTING"

	// StatusCancelling — OrderCancel published, awaiting Order Service confirmation.
	// Diff: excluded from all actions (let it resolve).
	StatusCancelling Status = "CANCELLING"

	// StatusStale — Cancel retry limit exceeded, reconciliation frozen for this level.
	// Diff: excluded from all actions. Authoritative resync required.
	StatusStale Status = "STALE"
)

// LiveOrder is one entry in the tracker.
// It represents the LE's current working knowledge of a specific MM order.
//
// Three-layer identity:
//
//	LevelID        = "MM-BTC-USDT-ASK-01"          (stable logical slot)
//	Generation     = 3                               (monotonic lifecycle counter)
//	ClientOrderID  = "MM-BTC-USDT-ASK-01-G003"     (idempotency key sent to Order Service)
//	OrderID        = "<UUID assigned by ME/OS>"     (authoritative ID for cancel commands)
type LiveOrder struct {
	// Identity
	LevelID       string // stable logical slot (never changes)
	Generation    int    // monotonic; increments on fill/correction/completion
	ClientOrderID string // LE-generated idempotency key = LevelID + "-G" + zero-padded generation
	OrderID       string // ME/OS-assigned UUID (learned from ListMMOrders, used in cancel payload)

	// Order details
	MarketID     string
	Side         string
	Price        decimal.Decimal
	OriginalQty  decimal.Decimal // from Order Service response
	RemainingQty decimal.Decimal // from Order Service response — used for committed calc
	FilledQty    decimal.Decimal // derived: OriginalQty - RemainingQty

	// State
	Status Status

	// Kafka dispatch tracking (Fix 4)
	KafkaPublished bool // true once successfully written to Kafka topic

	// Timing and retry tracking
	PendingSince      time.Time
	OSRegisteredSince time.Time // set when status transitions PENDING → OS_REGISTERED
	CancellingSince   time.Time
	CancelRetries     int

	// CORRECT flow: set when a CANCEL is issued to correct a wrong-price order.
	// After cancel confirms, the reconciler creates a replacement using this level.
	QueuedCorrection *pricing.PriceLevel
}

// IncrementCancelRetry increments the retry counter and resets the timer.
// Called from the CANCELLING timeout handler inside the single event loop.
func (o *LiveOrder) IncrementCancelRetry() {
	o.CancelRetries++
	o.CancellingSince = time.Now()
}

// Tracker holds the in-memory working state of all MM orders across all markets.
// The zero value is not usable — use NewTracker().
//
// CONCURRENCY: All public methods must be called from the engine's single event loop.
type Tracker struct {
	// orders maps LevelID → LiveOrder across all markets.
	orders map[string]*LiveOrder

	// generations maps LevelID → current generation number.
	// Survives Remove() so that generation is monotonically increasing.
	generations map[string]int

	// lastSync maps marketID → time of last successful ListMMOrders sync.
	lastSync map[string]time.Time
}

// NewTracker creates an empty tracker ready for use.
func NewTracker() *Tracker {
	return &Tracker{
		orders:      make(map[string]*LiveOrder),
		generations: make(map[string]int),
		lastSync:    make(map[string]time.Time),
	}
}

// SetPending adds or updates a level as PENDING after an OrderCreate is registered with Order Service.
func (t *Tracker) SetPending(levelID, orderID, clientOrderID string, gen int, level pricing.PriceLevel) {
	t.orders[levelID] = &LiveOrder{
		LevelID:        levelID,
		Generation:     gen,
		OrderID:        orderID,
		ClientOrderID:  clientOrderID,
		MarketID:       level.MarketID,
		Side:           level.Side,
		Price:          level.Price,
		OriginalQty:    level.Quantity,
		RemainingQty:   level.Quantity, // assumed full until Order Service confirms partial
		FilledQty:      decimal.Zero,
		Status:         StatusPending,
		KafkaPublished: false,
		PendingSince:   time.Now(),
	}
}

// SetKafkaPublished marks Kafka dispatch as confirmed for a pending order.
func (t *Tracker) SetKafkaPublished(levelID string, published bool) {
	if o, ok := t.orders[levelID]; ok {
		o.KafkaPublished = published
	}
}

// SetOSRegistered transitions a PENDING order to OS_REGISTERED.
// Called when CheckPendingTimeouts confirms OS has the order (status=OPEN).
//
// OS_REGISTERED means: the Order Service will return this order on ListMMOrders
// (enabling LE recovery), but it does NOT prove ME has the order in its book.
// That distinction is tracked explicitly to avoid incorrect inventory accounting.
func (t *Tracker) SetOSRegistered(levelID, orderID string, origQty, remainQty decimal.Decimal) {
	o, ok := t.orders[levelID]
	if !ok {
		return
	}
	o.OrderID = orderID
	o.OriginalQty = origQty
	o.RemainingQty = remainQty
	o.FilledQty = origQty.Sub(remainQty)
	o.Status = StatusOSRegistered
	o.OSRegisteredSince = time.Now()
}

// SetResting transitions an order to RESTING — ME has accepted it into the live book.
// In V1 this is called after MEConfirmationTimeout elapses with a healthy ME.
// In V2 this should be called directly on an OrderRested event from the ME.
func (t *Tracker) SetResting(levelID, orderID string, origQty, remainQty decimal.Decimal) {
	o, ok := t.orders[levelID]
	if !ok {
		return
	}
	o.OrderID = orderID
	o.OriginalQty = origQty
	o.RemainingQty = remainQty
	o.FilledQty = origQty.Sub(remainQty)
	o.Status = StatusResting
}

// SetCancelling transitions a RESTING order to CANCELLING.
func (t *Tracker) SetCancelling(levelID string) {
	o, ok := t.orders[levelID]
	if !ok {
		return
	}
	o.Status = StatusCancelling
	o.CancellingSince = time.Now()
	o.CancelRetries = 0
}

// QueueCorrection stores the desired level to create after the current CANCELLING order is confirmed cancelled.
func (t *Tracker) QueueCorrection(levelID string, desired pricing.PriceLevel) {
	o, ok := t.orders[levelID]
	if !ok {
		return
	}
	o.QueuedCorrection = &desired
}

// SetStale transitions a CANCELLING order to STALE after retry limit exceeded.
// Diff() will exclude STALE orders from all actions until resync resolves them.
func (t *Tracker) SetStale(levelID string) {
	o, ok := t.orders[levelID]
	if !ok {
		return
	}
	o.Status = StatusStale
}

// Remove deletes an order from the tracker. The generation counter is preserved.
// Called when Order Service confirms CANCELLED or FILLED.
func (t *Tracker) Remove(levelID string) {
	delete(t.orders, levelID)
}

// NextGeneration returns the next generation number for a level and increments the counter.
// Generation is monotonically increasing and survives Remove() calls.
// Once SetPending is called with a given generation, that generation is COMMITTED.
// There is no rollback — the PENDING tracker entry keeps the generation alive for retry.
func (t *Tracker) NextGeneration(levelID string) int {
	t.generations[levelID]++
	return t.generations[levelID]
}

// CurrentGeneration returns the current generation without incrementing.
func (t *Tracker) CurrentGeneration(levelID string) int {
	return t.generations[levelID]
}

// Get returns the LiveOrder for a level, or nil if not tracked.
func (t *Tracker) Get(levelID string) *LiveOrder {
	return t.orders[levelID]
}

// All returns all tracked orders for a given market.
func (t *Tracker) All(marketID string) []*LiveOrder {
	var result []*LiveOrder
	for _, o := range t.orders {
		if o.MarketID == marketID {
			result = append(result, o)
		}
	}
	return result
}

// AllMarkets returns all tracked orders across all markets.
func (t *Tracker) AllMarkets() []*LiveOrder {
	result := make([]*LiveOrder, 0, len(t.orders))
	for _, o := range t.orders {
		result = append(result, o)
	}
	return result
}

// ActiveCount returns the number of orders in RESTING or OS_REGISTERED status for a market and side.
// OS_REGISTERED orders are included because they represent committed slots that OS will recover,
// and we expect ME to have them (or receive them shortly).
func (t *Tracker) ActiveCount(marketID, side string) int {
	count := 0
	for _, o := range t.orders {
		if o.MarketID == marketID && o.Side == side &&
			(o.Status == StatusResting || o.Status == StatusOSRegistered) {
			count++
		}
	}
	return count
}

// RestingCount returns the number of orders in strictly StatusResting status for a market and side.
// Used exclusively for readiness checks (/readyz) to ensure liquidity actually rests in the ME book.
func (t *Tracker) RestingCount(marketID, side string) int {
	count := 0
	for _, o := range t.orders {
		if o.MarketID == marketID && o.Side == side && o.Status == StatusResting {
			count++
		}
	}
	return count
}

// PendingCount returns the count of PENDING and OS_REGISTERED orders for a market.
func (t *Tracker) PendingCount(marketID string) int {
	count := 0
	for _, o := range t.orders {
		if o.MarketID == marketID &&
			(o.Status == StatusPending || o.Status == StatusOSRegistered) {
			count++
		}
	}
	return count
}

// StaleCount returns STALE order count for a market.
func (t *Tracker) StaleCount(marketID string) int {
	count := 0
	for _, o := range t.orders {
		if o.MarketID == marketID && o.Status == StatusStale {
			count++
		}
	}
	return count
}

// CommittedBase returns the total base asset committed by SELL orders in RESTING, OS_REGISTERED, or PENDING.
// Used by inventory.EffectiveAvailable to compute effective_available_base.
// Uses RemainingQty (not OriginalQty) to account for partial fills.
func (t *Tracker) CommittedBase(marketID string) decimal.Decimal {
	total := decimal.Zero
	for _, o := range t.orders {
		if o.MarketID == marketID && o.Side == "SELL" &&
			(o.Status == StatusResting || o.Status == StatusOSRegistered || o.Status == StatusPending) {
			total = total.Add(o.RemainingQty)
		}
	}
	return total
}

// CommittedQuote returns the total quote asset committed by BUY orders in RESTING, OS_REGISTERED, or PENDING.
// committed_quote = Σ(RemainingQty × Price) for all active BUY orders.
func (t *Tracker) CommittedQuote(marketID string) decimal.Decimal {
	total := decimal.Zero
	for _, o := range t.orders {
		if o.MarketID == marketID && o.Side == "BUY" &&
			(o.Status == StatusResting || o.Status == StatusOSRegistered || o.Status == StatusPending) {
			total = total.Add(o.RemainingQty.Mul(o.Price))
		}
	}
	return total
}

// RecordSync marks a successful Order Service sync for a market.
func (t *Tracker) RecordSync(marketID string) {
	t.lastSync[marketID] = time.Now()
}

// LastSuccessfulSync returns the time of the last successful ListMMOrders for a market.
func (t *Tracker) LastSuccessfulSync(marketID string) time.Time {
	return t.lastSync[marketID]
}

// SyncFromOrders populates the tracker from a set of live orders returned by ListMMOrders.
// Generation numbers are recovered from client_order_id to preserve monotonic ordering across restarts.
// Duplicate LevelIDs in the OS response are deduplicated by selecting the highest Generation.
// Recovered orders enter StatusOSRegistered (awaiting ME confirmation window) rather than blindly assuming RESTING.
// Returns (addedCount, duplicatesCount).
func (t *Tracker) SyncFromOrders(marketID string, orders []OSOrder) (int, int) {
	added := 0
	duplicates := 0

	// Deduplicate incoming orders by LevelID, choosing the highest generation
	uniqueOrders := make(map[string]OSOrder, len(orders))
	for _, o := range orders {
		_, exists := uniqueOrders[o.LevelID]
		if exists {
			duplicates++
		}
		if !exists || o.Generation > uniqueOrders[o.LevelID].Generation {
			uniqueOrders[o.LevelID] = o
		}
	}

	for _, o := range uniqueOrders {
		// Update generation map with the highest observed generation
		if o.Generation > t.generations[o.LevelID] {
			t.generations[o.LevelID] = o.Generation
		}

		existing := t.orders[o.LevelID]
		if existing != nil {
			// Update authoritative fields from OS response
			existing.OrderID = o.OrderID
			existing.OriginalQty = o.OriginalQty
			existing.RemainingQty = o.RemainingQty
			existing.FilledQty = o.OriginalQty.Sub(o.RemainingQty)
			if existing.Status == StatusPending {
				existing.Status = StatusOSRegistered
				existing.OSRegisteredSince = time.Now()
			}
			continue
		}

		// New entry — recovered from Order Service
		gen := o.Generation
		if gen <= 0 {
			gen = 1
		}
		if gen > t.generations[o.LevelID] {
			t.generations[o.LevelID] = gen
		}

		t.orders[o.LevelID] = &LiveOrder{
			LevelID:           o.LevelID,
			Generation:        gen,
			ClientOrderID:     o.ClientOrderID,
			OrderID:           o.OrderID,
			MarketID:          marketID,
			Side:              o.Side,
			Price:             o.Price,
			OriginalQty:       o.OriginalQty,
			RemainingQty:      o.RemainingQty,
			FilledQty:         o.OriginalQty.Sub(o.RemainingQty),
			Status:            StatusOSRegistered, // recovered from OS, starts in OS_REGISTERED
			KafkaPublished:    true,
			OSRegisteredSince: time.Now(),
		}
		added++
	}
	t.RecordSync(marketID)
	return added, duplicates
}

// OSOrder is the minimal order representation received from the Order Service.
// The LevelID and Generation are extracted from the idempotency_key field (= client_order_id).
type OSOrder struct {
	LevelID       string          // extracted from ClientOrderID (prefix before last "-G")
	Generation    int             // extracted from ClientOrderID (suffix after last "-G")
	ClientOrderID string          // = idempotency_key from Order Service
	OrderID       string          // ME/OS-assigned UUID
	Side          string
	Price         decimal.Decimal
	OriginalQty   decimal.Decimal // = order.Quantity
	RemainingQty  decimal.Decimal // = order.RemainingQuantity
}

// ClientOrderID constructs the client_order_id for a given level and generation.
// Format: "MM-BTC-USDT-ASK-01-G003"
func ClientOrderID(levelID string, gen int) string {
	return fmt.Sprintf("%s-G%03d", levelID, gen)
}
