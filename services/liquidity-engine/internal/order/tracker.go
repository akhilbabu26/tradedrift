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
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"tradedrift/services/liquidity-engine/internal/pricing"
)

// Status represents the lifecycle state of a tracked MM order.
type Status string

const (
	// StatusPending — OrderCreate published to Kafka, ME has not yet confirmed.
	// knownSet: YES. Diff: excluded from CREATE.
	StatusPending Status = "PENDING"

	// StatusResting — Confirmed by Order Service (OPEN or PARTIALLY_FILLED, remaining > 0).
	// knownSet: YES. Diff: eligible for CANCEL or CORRECT.
	StatusResting Status = "RESTING"

	// StatusCancelling — OrderCancel published, awaiting Order Service confirmation.
	// knownSet: YES. Diff: excluded from all actions (let it resolve).
	StatusCancelling Status = "CANCELLING"

	// StatusStale — Cancel retry limit exceeded, reconciliation frozen for this level.
	// knownSet: YES. Diff: excluded from all actions. Authoritative resync required.
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

	// Timing and retry tracking
	PendingSince    time.Time
	CancellingSince time.Time
	CancelRetries   int

	// CORRECT flow: set when a CANCEL is issued to correct a wrong-price order.
	// After cancel confirms, the reconciler creates a replacement using this level.
	QueuedCorrection *pricing.PriceLevel

	// Internal
	mu sync.Mutex // protects CancelRetries and CancellingSince only (timer goroutine may update)
}

// IncrementCancelRetry atomically increments the retry counter and resets the timer.
// Called from the CANCELLING timeout handler.
func (o *LiveOrder) IncrementCancelRetry() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.CancelRetries++
	o.CancellingSince = time.Now()
}

// Tracker holds the in-memory working state of all MM orders across all markets.
// The zero value is not usable — use NewTracker().
//
// CONCURRENCY: All public methods must be called from the engine's single event loop.
// The only exception is IncrementCancelRetry (locked internally).
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

// SetPending adds or updates a level as PENDING after an OrderCreate command is published.
func (t *Tracker) SetPending(levelID, clientOrderID string, gen int, level pricing.PriceLevel) {
	t.orders[levelID] = &LiveOrder{
		LevelID:       levelID,
		Generation:    gen,
		ClientOrderID: clientOrderID,
		MarketID:      level.MarketID,
		Side:          level.Side,
		Price:         level.Price,
		OriginalQty:   level.Quantity,
		RemainingQty:  level.Quantity, // assumed full until Order Service confirms partial
		FilledQty:     decimal.Zero,
		Status:        StatusPending,
		PendingSince:  time.Now(),
	}
}

// SetResting updates an existing entry to RESTING with authoritative quantities from Order Service.
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
func (t *Tracker) NextGeneration(levelID string) int {
	t.generations[levelID]++
	return t.generations[levelID]
}

// DecrementGeneration rolls back a generation increment.
// Called when an OrderCreate publish fails — allows the next retry to use the same
// client_order_id, making the retry idempotent at the Order Service level.
func (t *Tracker) DecrementGeneration(levelID string) {
	if t.generations[levelID] > 0 {
		t.generations[levelID]--
	}
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

// ActiveCount returns the number of orders in RESTING status for a market and side.
func (t *Tracker) ActiveCount(marketID, side string) int {
	count := 0
	for _, o := range t.orders {
		if o.MarketID == marketID && o.Side == side && o.Status == StatusResting {
			count++
		}
	}
	return count
}

// PendingCount returns PENDING order count for a market.
func (t *Tracker) PendingCount(marketID string) int {
	count := 0
	for _, o := range t.orders {
		if o.MarketID == marketID && o.Status == StatusPending {
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

// CommittedBase returns the total base asset committed by RESTING and PENDING SELL orders.
// Used by inventory.EffectiveAvailable to compute effective_available_base.
// Uses RemainingQty (not OriginalQty) to account for partial fills.
func (t *Tracker) CommittedBase(marketID string) decimal.Decimal {
	total := decimal.Zero
	for _, o := range t.orders {
		if o.MarketID == marketID && o.Side == "SELL" &&
			(o.Status == StatusResting || o.Status == StatusPending) {
			total = total.Add(o.RemainingQty)
		}
	}
	return total
}

// CommittedQuote returns the total quote asset committed by RESTING and PENDING BUY orders.
// committed_quote = Σ(RemainingQty × Price) for all active BUY orders.
func (t *Tracker) CommittedQuote(marketID string) decimal.Decimal {
	total := decimal.Zero
	for _, o := range t.orders {
		if o.MarketID == marketID && o.Side == "BUY" &&
			(o.Status == StatusResting || o.Status == StatusPending) {
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
// Existing PENDING or CANCELLING entries are preserved if they don't appear in the OS response.
// STALE entries are resolved if the OS response clarifies them.
func (t *Tracker) SyncFromOrders(marketID string, orders []OSOrder) int {
	added := 0
	for _, o := range orders {
		existing := t.orders[o.LevelID]
		if existing != nil {
			// Update authoritative fields from OS response
			existing.OrderID = o.OrderID
			existing.OriginalQty = o.OriginalQty
			existing.RemainingQty = o.RemainingQty
			existing.FilledQty = o.OriginalQty.Sub(o.RemainingQty)
			if existing.Status == StatusPending || existing.Status == StatusStale {
				existing.Status = StatusResting
			}
			continue
		}

		// New entry — not in tracker but found in Order Service
		gen, ok := t.generations[o.LevelID]
		if !ok {
			gen = 1
			t.generations[o.LevelID] = gen
		}

		t.orders[o.LevelID] = &LiveOrder{
			LevelID:       o.LevelID,
			Generation:    gen,
			ClientOrderID: o.ClientOrderID,
			OrderID:       o.OrderID,
			MarketID:      marketID,
			Side:          o.Side,
			Price:         o.Price,
			OriginalQty:   o.OriginalQty,
			RemainingQty:  o.RemainingQty,
			FilledQty:     o.OriginalQty.Sub(o.RemainingQty),
			Status:        StatusResting,
		}
		added++
	}
	t.RecordSync(marketID)
	return added
}

// OSOrder is the minimal order representation received from the Order Service.
// The LevelID is extracted from the idempotency_key field (which equals client_order_id).
type OSOrder struct {
	LevelID       string          // extracted from ClientOrderID (prefix before last "-G")
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
