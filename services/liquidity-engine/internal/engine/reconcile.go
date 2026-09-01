package engine

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/inventory"
)

func (e *Engine) runReconcileAll(ctx context.Context) {
	if e.inv.IsStale(e.cfg.MaxBalanceStaleness) {
		e.logger.Warn("skipping reconcile — inventory is stale")
		e.publishSnapshot()
		return
	}

	allMarkets := make([]string, len(e.cfg.Markets))
	for i, m := range e.cfg.Markets {
		allMarkets[i] = m.MarketID
	}

	effectiveQuote := e.inv.EffectiveAvailableQuote(allMarkets)
	isDegraded := false

	for _, mc := range e.cfg.Markets {
		// Safety check: if market is paused due to ME timeouts, skip new order creation
		if e.marketPaused[mc.MarketID] {
			e.logger.Warn("skipping reconcile — market is paused due to ME timeouts (existing resting orders remain)",
				zap.String("market_id", mc.MarketID))
			isDegraded = true
			continue
		}

		// Safety check: if order state from Order Service is unsynchronized or stale (>90s), do not create new orders
		lastSync := e.tracker.LastSuccessfulSync(mc.MarketID)
		if lastSync.IsZero() || time.Since(lastSync) > e.cfg.MaxOrderStateStaleness {
			e.logger.Warn("order state is unsynchronized or stale — skipping new order creation on market (existing resting orders remain)",
				zap.String("market_id", mc.MarketID),
				zap.Duration("staleness", time.Since(lastSync)),
				zap.Duration("max_staleness", e.cfg.MaxOrderStateStaleness))
			isDegraded = true
			continue
		}

		start := time.Now()

		effectiveBase := e.inv.EffectiveAvailableBase(mc.MarketID)
		skew := inventory.ComputeSkew(&mc, effectiveBase, effectiveQuote, e.logger)

		if skew.BaseTier != inventory.TierNormal || skew.QuoteTier != inventory.TierNormal {
			isDegraded = true
		}

		e.metrics.SetLevelCount(mc.MarketID, "BUY", skew.BidCount)
		e.metrics.SetLevelCount(mc.MarketID, "SELL", skew.AskCount)

		cmds, err := e.reconciler.ReconcileMarket(ctx, mc.MarketID, skew.BidCount, skew.AskCount)
		if err != nil {
			e.logger.Error("reconcile failed",
				zap.String("market_id", mc.MarketID),
				zap.Error(err))
		}

		elapsed := time.Since(start).Milliseconds()
		e.metrics.ObserveReconcileDuration(mc.MarketID, float64(elapsed))

		if cmds > 0 {
			e.logger.Info("reconcile complete",
				zap.String("market_id", mc.MarketID),
				zap.Int("commands_published", cmds),
				zap.Int64("duration_ms", elapsed))
		}
	}

	if isDegraded && e.state == StateRunning {
		e.setState(StateDegraded)
	} else if !isDegraded && e.state == StateDegraded {
		e.setState(StateRunning)
	} else {
		e.publishSnapshot()
	}
}

func (e *Engine) runReconcileMarket(ctx context.Context, marketID string) {
	if e.inv.IsStale(e.cfg.MaxBalanceStaleness) {
		e.logger.Warn("skipping targeted reconcile — inventory is stale", zap.String("market_id", marketID))
		e.publishSnapshot()
		return
	}

	mc := e.cfg.ForMarket(marketID)
	if mc == nil {
		return
	}

	// Safety check: if market is paused due to ME timeouts, skip targeted reconcile
	if e.marketPaused[marketID] {
		e.logger.Warn("skipping targeted reconcile — market is paused due to ME timeouts (existing resting orders remain)",
			zap.String("market_id", marketID))
		e.publishSnapshot()
		return
	}

	// Safety check: if order state from Order Service is unsynchronized or stale (>90s), do not create new orders
	lastSync := e.tracker.LastSuccessfulSync(mc.MarketID)
	if lastSync.IsZero() || time.Since(lastSync) > e.cfg.MaxOrderStateStaleness {
		e.logger.Warn("order state is unsynchronized or stale — skipping targeted reconcile (existing resting orders remain)",
			zap.String("market_id", mc.MarketID),
			zap.Duration("staleness", time.Since(lastSync)),
			zap.Duration("max_staleness", e.cfg.MaxOrderStateStaleness))
		e.publishSnapshot()
		return
	}

	allMarkets := make([]string, len(e.cfg.Markets))
	for i, m := range e.cfg.Markets {
		allMarkets[i] = m.MarketID
	}

	effectiveQuote := e.inv.EffectiveAvailableQuote(allMarkets)
	effectiveBase := e.inv.EffectiveAvailableBase(mc.MarketID)
	skew := inventory.ComputeSkew(mc, effectiveBase, effectiveQuote, e.logger)

	start := time.Now()

	e.metrics.SetLevelCount(mc.MarketID, "BUY", skew.BidCount)
	e.metrics.SetLevelCount(mc.MarketID, "SELL", skew.AskCount)

	cmds, err := e.reconciler.ReconcileMarket(ctx, mc.MarketID, skew.BidCount, skew.AskCount)
	if err != nil {
		e.logger.Error("targeted reconcile failed",
			zap.String("market_id", mc.MarketID),
			zap.Error(err))
	}

	elapsed := time.Since(start).Milliseconds()
	e.metrics.ObserveReconcileDuration(mc.MarketID, float64(elapsed))

	if cmds > 0 {
		e.logger.Info("targeted reconcile complete",
			zap.String("market_id", mc.MarketID),
			zap.Int("commands_published", cmds),
			zap.Int64("duration_ms", elapsed))
	}
	e.publishSnapshot()
}

func (e *Engine) syncAllMarkets(ctx context.Context) error {
	for _, mc := range e.cfg.Markets {
		if err := e.reconciler.SyncFromOrderService(ctx, mc.MarketID); err != nil {
			return fmt.Errorf("sync %s: %w", mc.MarketID, err)
		}
	}
	return nil
}
