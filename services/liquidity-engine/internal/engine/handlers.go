package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/kafka"
	"tradedrift/services/liquidity-engine/internal/order"
)

func (e *Engine) handleTrade(env kafka.TradeEnvelope) {
	// Acknowledge to Kafka consumer ONLY after all state changes for this trade are complete
	defer func() {
		if env.Ack != nil {
			close(env.Ack)
		}
	}()

	ev := env.Event

	// Fix 7: Bounded TradeID deduplication
	if ev.TradeID != "" {
		if _, seen := e.processedTrades[ev.TradeID]; seen {
			e.logger.Debug("ignoring duplicate trade event", zap.String("trade_id", ev.TradeID))
			return
		}
		if len(e.tradeHistory) >= e.maxTradeHistory {
			oldest := e.tradeHistory[0]
			e.tradeHistory = e.tradeHistory[1:]
			delete(e.processedTrades, oldest)
		}
		e.processedTrades[ev.TradeID] = struct{}{}
		e.tradeHistory = append(e.tradeHistory, ev.TradeID)
	}

	if ev.MMSide == "" {
		return
	}

	e.inv.ApplyTrade(ev)

	mmOrderMatched := false
	for _, o := range e.tracker.All(ev.MarketID) {
		if o.OrderID == ev.MakerOrderID || o.OrderID == ev.TakerOrderID {
			mmOrderMatched = true
			e.metrics.IncOrdersFilled(ev.MarketID, o.Side)
			e.logger.Info("MM order filled in ME",
				zap.String("order_id", o.OrderID),
				zap.String("level_id", o.LevelID),
				zap.String("market_id", ev.MarketID),
				zap.String("mm_side", ev.MMSide),
				zap.String("quantity", ev.Quantity.String()))

			if o.Status == order.StatusResting || o.Status == order.StatusOSRegistered {
				newRemaining := o.RemainingQty.Sub(ev.Quantity)
				if newRemaining.IsNegative() {
					newRemaining = decimal.Zero
				}
				o.RemainingQty = newRemaining
				o.FilledQty = o.OriginalQty.Sub(newRemaining)
			}
			break
		}
	}

	// Fix 5 (v4): Coalesced zero-drop targeted reconciliation after fill
	if mmOrderMatched {
		e.dirtyMarkets[ev.MarketID] = true
		e.scheduleTargetedReconcile()
		e.publishSnapshot()
	}
}

func (e *Engine) scheduleTargetedReconcile() {
	if e.targetedDebounceTimer != nil {
		e.targetedDebounceTimer.Stop()
	}
	debounce := e.cfg.TargetedReconcileDebounce
	if debounce <= 0 {
		debounce = 200 * time.Millisecond
	}
	e.targetedDebounceTimer = time.AfterFunc(debounce, func() {
		select {
		case e.events <- loopEvent{kind: evTargetedReconcile}:
		default:
			// Event loop already has queued events — dirtyMarkets map retains the flag
		}
	})
}

func (e *Engine) handleWalletRefresh(ctx context.Context) {
	if err := e.refreshWalletBalances(ctx); err != nil {
		e.logger.Warn("periodic wallet balance fetch failed", zap.Error(err))
	}

	if e.inv.IsStale(e.cfg.MaxBalanceStaleness) {
		e.logger.Error("wallet balance stale — marking degraded state",
			zap.Duration("max_staleness", e.cfg.MaxBalanceStaleness))
		if e.state == StateRunning {
			e.setState(StateDegraded)
		}
	} else if e.state == StateDegraded {
		anyPaused := false
		for _, paused := range e.marketPaused {
			if paused {
				anyPaused = true
				break
			}
		}
		if !anyPaused {
			e.setState(StateRunning)
		}
	}
	e.publishSnapshot()
}

func (e *Engine) refreshWalletBalances(ctx context.Context) error {
	if e.walletSvc == nil {
		return nil
	}
	ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	balances, err := e.walletSvc.GetMMBalances(ctxTimeout)
	if err != nil {
		return fmt.Errorf("fetch MM balances: %w", err)
	}

	e.inv.RefreshFromWallet(balances)
	return nil
}

func (e *Engine) handlePendingCheck(ctx context.Context) {
	// Single probe to Matching Engine /status endpoint for all configured markets
	var (
		marketHealth map[string]bool
		probeErr     error
	)
	if e.meClient != nil {
		ctxProbe, cancelProbe := context.WithTimeout(ctx, 2*time.Second)
		marketHealth, probeErr = e.meClient.CheckAllMarkets(ctxProbe)
		cancelProbe()

		if probeErr != nil {
			e.metrics.IncMEHealthProbe("failure")
		} else {
			e.metrics.IncMEHealthProbe("success")
		}
	}

	threshold := e.cfg.MELivenessThreshold
	if threshold <= 0 {
		threshold = 3
	}

	for _, mc := range e.cfg.Markets {
		// 1. Order-level pending timeout retry (retries Kafka publish or re-queries OS)
		// This NEVER alters ME health or pauses the market.
		e.reconciler.CheckPendingTimeouts(ctx, mc.MarketID)

		// 2. Matching Engine liveness evaluation (strictly governed by direct ME probe)
		meLive := (probeErr == nil && marketHealth != nil && marketHealth[mc.MarketID])
		if !meLive {
			e.consecutiveMETimeouts[mc.MarketID]++
			if e.consecutiveMETimeouts[mc.MarketID] >= threshold {
				if !e.marketPaused[mc.MarketID] {
					e.marketPaused[mc.MarketID] = true
					e.metrics.IncMELivenessTimeout(mc.MarketID)
					e.metrics.IncMarketPause(mc.MarketID, "pause")
					e.logger.Warn("ME liveness threshold reached — pausing market order creation",
						zap.String("market_id", mc.MarketID),
						zap.Int("consecutive_failures", e.consecutiveMETimeouts[mc.MarketID]),
						zap.Int("threshold", threshold),
						zap.Error(probeErr))
				}
			} else {
				e.logger.Warn("ME health probe failed — counting toward threshold",
					zap.String("market_id", mc.MarketID),
					zap.Int("consecutive_failures", e.consecutiveMETimeouts[mc.MarketID]),
					zap.Int("threshold", threshold),
					zap.Error(probeErr))
			}
		} else {
			if e.marketPaused[mc.MarketID] || e.consecutiveMETimeouts[mc.MarketID] > 0 {
				e.logger.Info("Matching Engine is live and healthy — resetting timeouts and unpausing market",
					zap.String("market_id", mc.MarketID))
				if e.marketPaused[mc.MarketID] {
					e.metrics.IncMarketPause(mc.MarketID, "resume")
				}
			}
			e.consecutiveMETimeouts[mc.MarketID] = 0
			e.marketPaused[mc.MarketID] = false
		}

		// 3. Promote OS_REGISTERED orders to RESTING after MEConfirmationTimeout,
		// ONLY IF the Matching Engine is currently probed healthy for this market.
		meHealthy := (!e.marketPaused[mc.MarketID] && e.consecutiveMETimeouts[mc.MarketID] == 0)
		e.reconciler.CheckOSRegisteredTimeouts(mc.MarketID, e.cfg.PendingTimeout, meHealthy)
	}

	e.publishSnapshot()
}

func (e *Engine) handleCancellingCheck(ctx context.Context) {
	for _, mc := range e.cfg.Markets {
		e.reconciler.CheckCancellingTimeouts(ctx, mc.MarketID)
	}
}
