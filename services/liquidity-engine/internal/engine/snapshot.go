package engine

import (
	"time"
)

// StatusSnapshot represents an immutable, thread-safe snapshot of engine and market statuses.
type StatusSnapshot struct {
	State                EngineState
	IsReady              bool
	MarketStates         map[string]string
	ActiveBids           map[string]int // strictly RESTING bids
	ActiveAsks           map[string]int // strictly RESTING asks
	InventoryLastRefresh time.Time
	InventoryStale       bool
}

// publishSnapshot builds an immutable StatusSnapshot and stores it atomically.
// MUST be called from the event loop goroutine only.
func (e *Engine) publishSnapshot() {
	invStale := e.inv.IsStale(e.cfg.MaxBalanceStaleness)
	snap := &StatusSnapshot{
		State:                e.state,
		MarketStates:         make(map[string]string, len(e.cfg.Markets)),
		ActiveBids:           make(map[string]int, len(e.cfg.Markets)),
		ActiveAsks:           make(map[string]int, len(e.cfg.Markets)),
		InventoryLastRefresh: e.inv.LastRefresh(),
		InventoryStale:       invStale,
	}

	anyMarketActive := false

	for _, mc := range e.cfg.Markets {
		// Strictly RESTING counts for readiness (/readyz) — OS_REGISTERED orders are not yet in ME book
		restingBids := e.tracker.RestingCount(mc.MarketID, "BUY")
		restingAsks := e.tracker.RestingCount(mc.MarketID, "SELL")
		snap.ActiveBids[mc.MarketID] = restingBids
		snap.ActiveAsks[mc.MarketID] = restingAsks

		if e.marketPaused[mc.MarketID] {
			snap.MarketStates[mc.MarketID] = "PAUSED_ME"
			continue
		}
		if invStale {
			snap.MarketStates[mc.MarketID] = "PAUSED_INVENTORY"
			continue
		}
		lastSync := e.tracker.LastSuccessfulSync(mc.MarketID)
		if lastSync.IsZero() {
			snap.MarketStates[mc.MarketID] = "UNSYNCHRONIZED"
			continue
		}
		if time.Since(lastSync) > e.cfg.MaxOrderStateStaleness {
			snap.MarketStates[mc.MarketID] = "STALE"
			continue
		}
		snap.MarketStates[mc.MarketID] = "RUNNING"
		if restingBids >= e.cfg.MinReadyBids && restingAsks >= e.cfg.MinReadyAsks {
			anyMarketActive = true
		}
	}

	if e.state == StateRunning || e.state == StateDegraded {
		snap.IsReady = anyMarketActive
	} else {
		snap.IsReady = false
	}

	e.snapshot.Store(snap)
}

// State returns the current engine state. Safe to call from any goroutine.
func (e *Engine) State() EngineState {
	if snap := e.snapshot.Load(); snap != nil {
		return snap.State
	}
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	return e.state
}

// ReadyBids returns the number of strictly RESTING bid orders for a market. Thread-safe lock-free read.
func (e *Engine) ReadyBids(marketID string) int {
	if snap := e.snapshot.Load(); snap != nil {
		return snap.ActiveBids[marketID]
	}
	return 0
}

// ReadyAsks returns the number of strictly RESTING ask orders for a market. Thread-safe lock-free read.
func (e *Engine) ReadyAsks(marketID string) int {
	if snap := e.snapshot.Load(); snap != nil {
		return snap.ActiveAsks[marketID]
	}
	return 0
}

// MarketStates returns a defensive copy of the operational status map of each configured market. Thread-safe lock-free read.
func (e *Engine) MarketStates() map[string]string {
	if snap := e.snapshot.Load(); snap != nil {
		res := make(map[string]string, len(snap.MarketStates))
		for k, v := range snap.MarketStates {
			res[k] = v
		}
		return res
	}
	return make(map[string]string)
}

// IsReady returns true if the engine is running and at least one market has active RESTING orders. Thread-safe lock-free read.
func (e *Engine) IsReady() bool {
	if snap := e.snapshot.Load(); snap != nil {
		return snap.IsReady
	}
	return false
}

// InventoryLastRefresh returns when the wallet balance was last fetched from the atomic snapshot.
func (e *Engine) InventoryLastRefresh() time.Time {
	if snap := e.snapshot.Load(); snap != nil {
		return snap.InventoryLastRefresh
	}
	return time.Time{}
}

// MaxBalanceStaleness returns the configured max balance staleness.
func (e *Engine) MaxBalanceStaleness() time.Duration {
	return e.cfg.MaxBalanceStaleness
}
