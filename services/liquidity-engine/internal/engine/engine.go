// Package engine is the top-level orchestrator for the Liquidity Engine.
// It owns the single event loop goroutine that serialises all state mutations.
//
// State machine:
//
//	STARTING -> SYNCING -> RUNNING -> PAUSED -> RUNNING
//	                             ^
//	                      DEGRADED (inventory skew, but still running)
//
// CONCURRENCY MODEL:
//   - Single goroutine (Run loop) owns all tracker and inventory mutations.
//   - Kafka consumer sends to e.events channel.
//   - Ticker callbacks post events to e.events (no direct state mutation).
//   - All timeout checks happen inside the Run loop.
package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/inventory"
	"tradedrift/services/liquidity-engine/internal/kafka"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/reconciler"
	"tradedrift/services/liquidity-engine/internal/walletservice"
)

// EngineState is the lifecycle state of the LE engine.
type EngineState int

const (
	StateStarting EngineState = iota
	StateSyncing              // discovering existing MM orders from Order Service
	StateRunning              // nominal operation
	StateDegraded             // running but inventory skew active
	StatePaused               // ME unresponsive or critical failure; will auto-recover
	StateStopped              // graceful shutdown complete
)

func (s EngineState) String() string {
	switch s {
	case StateStarting:
		return "STARTING"
	case StateSyncing:
		return "SYNCING"
	case StateRunning:
		return "RUNNING"
	case StateDegraded:
		return "DEGRADED"
	case StatePaused:
		return "PAUSED"
	case StateStopped:
		return "STOPPED"
	default:
		return "UNKNOWN"
	}
}

type eventKind int

const (
	evTrade eventKind = iota
	evReconcileTick
	evWalletTick
	evPendingCheck
	evCancellingCheck
	evResyncTick
	evTargetedReconcile
)

type loopEvent struct {
	kind     eventKind
	trade    kafka.TradeEvent
	marketID string
}

// EngineMetrics is the metrics interface the engine uses.
type EngineMetrics interface {
	reconciler.ReconcilerMetrics
	SetEngineState(state string)
	SetLevelCount(marketID, side string, count int)
	ObserveReconcileDuration(marketID string, ms float64)
	IncMELivenessTimeout(marketID string)
}

// Engine is the main Liquidity Engine orchestrator.
type Engine struct {
	cfg                   *config.Config
	tracker               *order.Tracker
	inv                   *inventory.Manager
	reconciler            *reconciler.Reconciler
	producer              *kafka.Producer
	consumer              *kafka.Consumer
	walletSvc             *walletservice.Client
	metrics               EngineMetrics
	logger                *zap.Logger
	events                chan loopEvent
	state                 EngineState
	stateMu               sync.RWMutex
	consecutiveMETimeouts map[string]int

	// Fix 7: Bounded TradeID deduplication (retention up to 1000 recent trades)
	processedTrades map[string]struct{}
	tradeHistory    []string
	maxTradeHistory int

	// Fix 6: Targeted reconciliation debounce timers per market
	targetedTimers map[string]*time.Timer
}

// NewEngine creates a fully wired Engine.
func NewEngine(
	cfg *config.Config,
	tracker *order.Tracker,
	inv *inventory.Manager,
	rec *reconciler.Reconciler,
	producer *kafka.Producer,
	consumer *kafka.Consumer,
	walletSvc *walletservice.Client,
	metrics EngineMetrics,
	logger *zap.Logger,
) *Engine {
	return &Engine{
		cfg:                   cfg,
		tracker:               tracker,
		inv:                   inv,
		reconciler:            rec,
		producer:              producer,
		consumer:              consumer,
		walletSvc:             walletSvc,
		metrics:               metrics,
		logger:                logger,
		events:                make(chan loopEvent, 256),
		state:                 StateStarting,
		consecutiveMETimeouts: make(map[string]int),
		processedTrades:       make(map[string]struct{}),
		tradeHistory:          make([]string, 0, 1000),
		maxTradeHistory:       1000,
		targetedTimers:        make(map[string]*time.Timer),
	}
}

// State returns the current engine state. Safe to call from any goroutine.
func (e *Engine) State() EngineState {
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	return e.state
}

// setState updates the engine state. Must be called from the event loop only.
func (e *Engine) setState(s EngineState) {
	e.stateMu.Lock()
	e.state = s
	e.stateMu.Unlock()
	e.metrics.SetEngineState(s.String())
	e.logger.Info("engine state changed", zap.String("state", s.String()))
}

// Run starts the engine event loop. Blocks until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	e.logger.Info("liquidity engine starting")
	e.setState(StateStarting)

	// Start Kafka consumer in background
	go e.consumer.Run(ctx)

	// Fetch initial wallet balances for MM-001
	if err := e.refreshWalletBalances(ctx); err != nil {
		e.logger.Warn("initial wallet balance fetch failed — will retry on tick", zap.Error(err))
	}

	// SYNCING: discover all existing MM orders from the Order Service
	e.setState(StateSyncing)
	if err := e.syncAllMarkets(ctx); err != nil {
		return fmt.Errorf("initial sync failed: %w", err)
	}

	// Start periodic tickers
	reconcileTicker := time.NewTicker(e.cfg.ReconcileInterval)
	walletTicker := time.NewTicker(e.cfg.WalletRefreshInterval)
	pendingTicker := time.NewTicker(e.cfg.PendingTimeout / 2)
	cancellingTicker := time.NewTicker(e.cfg.CancellingTimeout / 2)
	resyncTicker := time.NewTicker(e.cfg.ReconcileInterval * 10)
	defer reconcileTicker.Stop()
	defer walletTicker.Stop()
	defer pendingTicker.Stop()
	defer cancellingTicker.Stop()
	defer resyncTicker.Stop()

	// Start ticker pump goroutines (post events to channel, no state mutation)
	go e.pumpTicker(ctx, reconcileTicker.C, evReconcileTick)
	go e.pumpTicker(ctx, walletTicker.C, evWalletTick)
	go e.pumpTicker(ctx, pendingTicker.C, evPendingCheck)
	go e.pumpTicker(ctx, cancellingTicker.C, evCancellingCheck)
	go e.pumpTicker(ctx, resyncTicker.C, evResyncTick)

	e.setState(StateRunning)

	// Initial reconciliation
	e.runReconcileAll(ctx)

	// ── EVENT LOOP ───────────────────────────────────────────────────
	for {
		select {
		case <-ctx.Done():
			e.logger.Info("engine shutting down")
			e.setState(StateStopped)
			return nil

		case ev := <-e.events:
			switch ev.kind {
			case evTrade:
				e.handleTrade(ev.trade)

			case evTargetedReconcile:
				if e.state == StateRunning || e.state == StateDegraded {
					e.runReconcileMarket(ctx, ev.marketID)
				}

			case evReconcileTick:
				if e.state == StateRunning || e.state == StateDegraded {
					e.runReconcileAll(ctx)
				}

			case evWalletTick:
				e.handleWalletRefresh(ctx)

			case evPendingCheck:
				e.handlePendingCheck(ctx)

			case evCancellingCheck:
				if e.state == StateRunning || e.state == StateDegraded {
					e.handleCancellingCheck(ctx)
				}

			case evResyncTick:
				if e.state == StateRunning || e.state == StateDegraded {
					if err := e.syncAllMarkets(ctx); err != nil {
						e.logger.Warn("periodic full resync failed", zap.Error(err))
					}
				}
			}
		}
	}
}

func (e *Engine) pumpTicker(ctx context.Context, ch <-chan time.Time, kind eventKind) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			select {
			case e.events <- loopEvent{kind: kind}:
			default:
				e.logger.Warn("event channel full — dropping tick event",
					zap.Int("event_kind", int(kind)))
			}
		}
	}
}

func (e *Engine) handleTrade(ev kafka.TradeEvent) {
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
			e.logger.Info("MM order involved in trade",
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

	e.consecutiveMETimeouts[ev.MarketID] = 0

	// Fix 6: Trigger targeted immediate reconciliation after fill
	if mmOrderMatched {
		e.scheduleTargetedReconcile(ev.MarketID)
	}
}

func (e *Engine) scheduleTargetedReconcile(marketID string) {
	if timer, ok := e.targetedTimers[marketID]; ok {
		timer.Stop()
	}
	debounce := e.cfg.TargetedReconcileDebounce
	if debounce <= 0 {
		debounce = 200 * time.Millisecond
	}
	e.targetedTimers[marketID] = time.AfterFunc(debounce, func() {
		select {
		case e.events <- loopEvent{kind: evTargetedReconcile, marketID: marketID}:
		default:
			e.logger.Warn("event channel full — dropping targeted reconcile event",
				zap.String("market_id", marketID))
		}
	})
}

func (e *Engine) handleWalletRefresh(ctx context.Context) {
	if err := e.refreshWalletBalances(ctx); err != nil {
		e.logger.Warn("periodic wallet balance fetch failed", zap.Error(err))
	}

	if e.inv.IsStale(e.cfg.MaxBalanceStaleness) {
		e.logger.Error("wallet balance stale — pausing affected markets",
			zap.Duration("max_staleness", e.cfg.MaxBalanceStaleness))
		if e.state != StatePaused {
			e.setState(StatePaused)
		}
	} else if e.state == StatePaused {
		e.setState(StateRunning)
	}
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
	for _, mc := range e.cfg.Markets {
		consecutiveTimeouts := e.reconciler.CheckPendingTimeouts(ctx, mc.MarketID)

		// Promote OS_REGISTERED orders to RESTING after MEConfirmationTimeout.
		// V1 proxy: if OS has it and we've waited long enough, assume ME has it.
		// V2: replace with direct OrderRested Kafka event from ME.
		e.reconciler.CheckOSRegisteredTimeouts(mc.MarketID, e.cfg.PendingTimeout)

		if consecutiveTimeouts >= e.cfg.MELivenessThreshold {
			e.consecutiveMETimeouts[mc.MarketID]++
			e.metrics.IncMELivenessTimeout(mc.MarketID)
			e.logger.Error("ME liveness threshold exceeded — pausing",
				zap.String("market_id", mc.MarketID),
				zap.Int("consecutive_timeouts", consecutiveTimeouts))

			if e.state != StatePaused {
				e.setState(StatePaused)
			}
		}
	}
}

func (e *Engine) handleCancellingCheck(ctx context.Context) {
	for _, mc := range e.cfg.Markets {
		e.reconciler.CheckCancellingTimeouts(ctx, mc.MarketID)
	}
}

func (e *Engine) runReconcileAll(ctx context.Context) {
	if e.inv.IsStale(e.cfg.MaxBalanceStaleness) {
		e.logger.Warn("skipping reconcile — inventory is stale")
		return
	}

	allMarkets := make([]string, len(e.cfg.Markets))
	for i, m := range e.cfg.Markets {
		allMarkets[i] = m.MarketID
	}

	effectiveQuote := e.inv.EffectiveAvailableQuote(allMarkets)
	isDegraded := false

	for _, mc := range e.cfg.Markets {
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
	}
}

func (e *Engine) runReconcileMarket(ctx context.Context, marketID string) {
	if e.inv.IsStale(e.cfg.MaxBalanceStaleness) {
		e.logger.Warn("skipping targeted reconcile — inventory is stale", zap.String("market_id", marketID))
		return
	}

	mc := e.cfg.ForMarket(marketID)
	if mc == nil {
		return
	}

	allMarkets := make([]string, len(e.cfg.Markets))
	for i, m := range e.cfg.Markets {
		allMarkets[i] = m.MarketID
	}

	effectiveQuote := e.inv.EffectiveAvailableQuote(allMarkets)
	effectiveBase := e.inv.EffectiveAvailableBase(mc.MarketID)
	skew := inventory.ComputeSkew(mc, effectiveBase, effectiveQuote, e.logger)

	e.metrics.SetLevelCount(mc.MarketID, "BUY", skew.BidCount)
	e.metrics.SetLevelCount(mc.MarketID, "SELL", skew.AskCount)

	start := time.Now()
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
}

func (e *Engine) syncAllMarkets(ctx context.Context) error {
	for _, mc := range e.cfg.Markets {
		if err := e.reconciler.SyncFromOrderService(ctx, mc.MarketID); err != nil {
			return fmt.Errorf("sync %s: %w", mc.MarketID, err)
		}
	}
	return nil
}

// ReadyBids returns the number of RESTING bid orders for a market.
func (e *Engine) ReadyBids(marketID string) int {
	return e.tracker.ActiveCount(marketID, "BUY")
}

// ReadyAsks returns the number of RESTING ask orders for a market.
func (e *Engine) ReadyAsks(marketID string) int {
	return e.tracker.ActiveCount(marketID, "SELL")
}

// IsReady returns true if all markets have at least MinReadyBids and MinReadyAsks resting.
func (e *Engine) IsReady() bool {
	for _, mc := range e.cfg.Markets {
		if e.tracker.ActiveCount(mc.MarketID, "BUY") < e.cfg.MinReadyBids {
			return false
		}
		if e.tracker.ActiveCount(mc.MarketID, "SELL") < e.cfg.MinReadyAsks {
			return false
		}
	}
	return true
}

// InventoryLastRefresh returns when the wallet balance was last fetched.
func (e *Engine) InventoryLastRefresh() time.Time {
	return e.inv.LastRefresh()
}

// MaxBalanceStaleness returns the configured max balance staleness.
func (e *Engine) MaxBalanceStaleness() time.Duration {
	return e.cfg.MaxBalanceStaleness
}
