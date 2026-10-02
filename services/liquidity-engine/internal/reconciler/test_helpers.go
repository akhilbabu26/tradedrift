package reconciler

import (
	"context"

	"github.com/shopspring/decimal"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/meclient"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/pricing"
)

// SetMEClient allows updating the ME client (used in testing and dynamic wiring).
func (r *Reconciler) SetMEClient(c *meclient.Client) {
	r.meClient = c
}

// MEClient returns the current ME client.
func (r *Reconciler) MEClient() *meclient.Client {
	return r.meClient
}

// SetProducer allows updating the command producer (used in testing).
func (r *Reconciler) SetProducer(p CommandProducer) {
	r.producer = p
}

// Producer returns the command producer.
func (r *Reconciler) Producer() CommandProducer {
	return r.producer
}

// SyncWithMESnapshot exposes snapshot reconciliation for test verification.
func (r *Reconciler) SyncWithMESnapshot(ctx context.Context, marketID string, snap *meclient.MarketSnapshot, mc *config.MarketConfig) {
	r.syncWithMESnapshot(ctx, marketID, snap, mc)
}

// HandleMissingRestingOrder exposes missing order handling for test verification.
func (r *Reconciler) HandleMissingRestingOrder(ctx context.Context, o *order.LiveOrder, mc *config.MarketConfig) {
	r.handleMissingRestingOrder(ctx, o, mc)
}

// HandleCancellingTimeout exposes cancelling timeout handling for test verification.
func (r *Reconciler) HandleCancellingTimeout(ctx context.Context, o *order.LiveOrder, mc *config.MarketConfig) {
	r.handleCancellingTimeout(ctx, o, mc)
}

// RetryCancelOrStale exposes retry-or-stale transition for test verification.
func (r *Reconciler) RetryCancelOrStale(ctx context.Context, o *order.LiveOrder, mc *config.MarketConfig) {
	r.retryCancelOrStale(ctx, o, mc)
}

// ApplyCreate exposes order create dispatch for test verification.
func (r *Reconciler) ApplyCreate(ctx context.Context, e order.DiffEntry, mc *config.MarketConfig) error {
	_, err := r.applyCreate(ctx, e, mc)
	return err
}

// CanCreateLevel exposes capital cap validation for test verification.
func (r *Reconciler) CanCreateLevel(mc *config.MarketConfig, level pricing.PriceLevel) (bool, string) {
	return r.canCreateLevel(mc, level)
}

// CurrentReference exposes reference price resolution for test verification.
func (r *Reconciler) CurrentReference(mc *config.MarketConfig) (decimal.Decimal, int64, Freshness) {
	snap := r.currentReference(mc)
	return snap.Price, snap.Version, snap.Freshness
}

// CurrentReferenceSnapshot exposes the full reference snapshot for test verification.
func (r *Reconciler) CurrentReferenceSnapshot(mc *config.MarketConfig) ReferenceSnapshot {
	return r.currentReference(mc)
}

// ParseMERemainingQuantity exposes parseMERemainingQuantity for direct testing.
func ParseMERemainingQuantity(meOrder meclient.MMOrderSummary, fallback decimal.Decimal) decimal.Decimal {
	return parseMERemainingQuantity(meOrder, fallback, nil)
}

// ApplyRefMovementReprice exposes ref movement repricing for test verification.
func (r *Reconciler) ApplyRefMovementReprice(ctx context.Context, mc *config.MarketConfig, ref decimal.Decimal, targetVer int64, action RepricingAction) int {
	return r.applyRefMovementReprice(ctx, mc, ref, targetVer, action)
}

// GetMissingCycles returns the missing cycle count for a level.
func (r *Reconciler) GetMissingCycles(levelID string) int {
	return r.missingCycles[levelID]
}

// SetMissingCycles sets the missing cycle count for a level.
func (r *Reconciler) SetMissingCycles(levelID string, count int) {
	r.missingCycles[levelID] = count
}

// GetNotFoundCount returns the OS NOT_FOUND count for a level.
func (r *Reconciler) GetNotFoundCount(levelID string) int {
	return r.notFoundCount[levelID]
}

// SetNotFoundCount sets the OS NOT_FOUND count for a level.
func (r *Reconciler) SetNotFoundCount(levelID string, count int) {
	r.notFoundCount[levelID] = count
}

// HasOrphanCancelInFlight reports whether an orphan cancel is in-flight.
func (r *Reconciler) HasOrphanCancelInFlight(orderID string) bool {
	r.orphanCancelMu.Lock()
	defer r.orphanCancelMu.Unlock()
	_, inFlight := r.orphanCancelInFlight[orderID]
	return inFlight
}

// GetRefState returns the tracked RefState for a market.
func (r *Reconciler) GetRefState(marketID string) *RefState {
	return r.refStates[marketID]
}

// SetRefState sets the tracked RefState for a market.
func (r *Reconciler) SetRefState(marketID string, s *RefState) {
	r.refStates[marketID] = s
}

// IsMarketPaused exposes the paused check for test verification.
func (r *Reconciler) IsMarketPaused(marketID string) bool {
	return r.isMarketPaused(marketID)
}

// RefProvider returns the current reference price provider.
func (r *Reconciler) RefProvider() ReferencePriceProvider {
	return r.refProvider
}

