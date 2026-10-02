package reconciler

import (
	"context"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/meclient"
	"tradedrift/services/liquidity-engine/internal/order"
)

// ConfirmRestingFromSnapshot promotes tracked orders to RESTING if and only if
// they are confirmed present in the Matching Engine's atomic snapshot.
// INVARIANT: CANCELLING and STALE are locked states. Presence in ME snapshot must NEVER promote them back to RESTING.
func (r *Reconciler) ConfirmRestingFromSnapshot(marketID string, snap *meclient.MarketSnapshot) {
	if snap == nil || snap.State != "LIVE" {
		return
	}

	snapOrders := make(map[string]meclient.MMOrderSummary, len(snap.Orders))
	for _, o := range snap.Orders {
		snapOrders[o.LevelID] = o
	}

	for _, o := range r.tracker.All(marketID) {
		// Only unconfirmed orders (OS_REGISTERED or PENDING) can be promoted to RESTING.
		// Crucially, CANCELLING and STALE orders must NEVER be promoted back to RESTING.
		if o.Status != order.StatusOSRegistered && o.Status != order.StatusPending {
			continue
		}
		if meOrder, found := snapOrders[o.LevelID]; found {
			if meOrder.ClientOrderID == o.ClientOrderID || meOrder.OrderID == o.OrderID {
				r.logger.Info("order confirmed RESTING via ME snapshot",
					zap.String("level_id", o.LevelID),
					zap.String("order_id", meOrder.OrderID),
					zap.String("client_order_id", o.ClientOrderID),
					zap.String("prev_status", string(o.Status)))
				remQty := parseMERemainingQuantity(meOrder, o.RemainingQty, r.logger)
				r.tracker.SetResting(o.LevelID, meOrder.OrderID, o.OriginalQty, remQty)
				delete(r.notFoundCount, o.LevelID)
			}
		}
	}
}

// ConfirmOSRegisteredOrders verifies OS_REGISTERED orders against the Matching Engine snapshot.
// Blind auto-promotion has been REMOVED — orders are promoted to RESTING
// only when confirmed present in the ME snapshot.
// When meClient == nil or ME is unhealthy, this check is FAIL-CLOSED: orders remain OS_REGISTERED.
func (r *Reconciler) ConfirmOSRegisteredOrders(marketID string, meHealthy bool) {
	if !meHealthy {
		r.logger.Warn("ME is unhealthy — holding OS_REGISTERED orders without promoting to RESTING",
			zap.String("market_id", marketID))
		return
	}

	if r.meClient == nil {
		r.logger.Warn("ME client is nil — holding OS_REGISTERED orders without promoting (fail-closed)",
			zap.String("market_id", marketID))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	snap, err := r.meClient.FetchSnapshot(ctx, marketID)
	if err != nil {
		r.logger.Warn("failed to fetch ME snapshot during unconfirmed orders check",
			zap.String("market_id", marketID),
			zap.Error(err))
		return
	}
	r.ConfirmRestingFromSnapshot(marketID, snap)
}
