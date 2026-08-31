// Package engine is the top-level orchestrator for the Liquidity Engine.
// It owns the single event loop goroutine that serialises all state mutations.
//
// State machine:
//
//	STARTING -> SYNCING -> RUNNING -> PAUSED -> RUNNING
//	                             ^
//	                      DEGRADED (inventory skew or paused market, but still running)
//
// CONCURRENCY MODEL:
//   - Single goroutine (Run loop) owns all tracker and inventory mutations.
//   - Kafka consumer sends to e.events channel.
//   - Ticker callbacks post events to e.events (no direct state mutation).
//   - All timeout checks happen inside the Run loop.
//   - Read-only HTTP health/readiness endpoints read lock-free from an atomic StatusSnapshot.
package engine

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/inventory"
	"tradedrift/services/liquidity-engine/internal/kafka"
	"tradedrift/services/liquidity-engine/internal/meclient"
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
	StateDegraded             // running but inventory skew active or partial market pause
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
	evReconcileTick eventKind = iota
	evWalletTick
	evPendingCheck
	evCancellingCheck
	evResyncTick
	evTargetedReconcile
)

type loopEvent struct {
	kind     eventKind
	marketID string
}

// EngineMetrics is the metrics interface the engine uses.
type EngineMetrics interface {
	reconciler.ReconcilerMetrics
	SetEngineState(state string)
	SetLevelCount(marketID, side string, count int)
	ObserveReconcileDuration(marketID string, ms float64)
	IncMELivenessTimeout(marketID string)
	IncMEHealthProbe(status string)
	IncMarketPause(marketID, action string)
}

// StatusSnapshot represents an immutable, thread-safe snapshot of engine and market statuses.
type StatusSnapshot struct {
	State        EngineState
	IsReady      bool
	MarketStates map[string]string
	ActiveBids   map[string]int
	ActiveAsks   map[string]int
}

// Engine is the main Liquidity Engine orchestrator.
type Engine struct {
	cfg                   *config.Config
	tracker               *order.Tracker
	inv                   *inventory.Manager
	reconciler            *reconciler.Reconciler
	producer              *kafka.Producer
	consumer              *kafka.Consumer
	tradeEvents           <-chan kafka.TradeEnvelope
	walletSvc             *walletservice.Client
	meClient              *meclient.Client
	metrics               EngineMetrics
	logger                *zap.Logger
	events                chan loopEvent
	state                 EngineState
	stateMu               sync.RWMutex
	consecutiveMETimeouts map[string]int
	marketPaused          map[string]bool // per-market pause on ME liveness failure
	snapshot              atomic.Pointer[StatusSnapshot]

	// Fix 7: Bounded TradeID deduplication (retention up to 1000 recent trades)
	processedTrades map[string]struct{}
	tradeHistory    []string
	maxTradeHistory int

	// Fix 5 (v4): Dirty markets coalescing map for targeted reconciliation
	dirtyMarkets          map[string]bool
	targetedDebounceTimer *time.Timer
}

// NewEngine creates a fully wired Engine.
func NewEngine(
	cfg *config.Config,
	tracker *order.Tracker,
	inv *inventory.Manager,
	rec *reconciler.Reconciler,
	producer *kafka.Producer,
	consumer *kafka.Consumer,
	tradeEvents <-chan kafka.TradeEnvelope,
	walletSvc *walletservice.Client,
	meClient *meclient.Client,
	metrics EngineMetrics,
	logger *zap.Logger,
) *Engine {
	e := &Engine{
		cfg:                   cfg,
		tracker:               tracker,
		inv:                   inv,
		reconciler:            rec,
		producer:              producer,
		consumer:              consumer,
		tradeEvents:           tradeEvents,
		walletSvc:             walletSvc,
		meClient:              meClient,
		metrics:               metrics,
		logger:                logger,
		events:                make(chan loopEvent, 256),
		state:                 StateStarting,
		consecutiveMETimeouts: make(map[string]int),
		marketPaused:          make(map[string]bool),
		processedTrades:       make(map[string]struct{}),
		tradeHistory:          make([]string, 0, 1000),
		maxTradeHistory:       1000,
		dirtyMarkets:          make(map[string]bool),
	}
	e.publishSnapshot()
	return e
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

// setState updates the engine state. Must be called from the event loop only.
func (e *Engine) setState(s EngineState) {
	e.stateMu.Lock()
	e.state = s
	e.stateMu.Unlock()
	e.metrics.SetEngineState(s.String())
	e.logger.Info("engine state changed", zap.String("state", s.String()))
	e.publishSnapshot()
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

	// Initial Matching Engine health probe before starting quoting
	if e.meClient != nil {
		probeCtx, cancelProbe := context.WithTimeout(ctx, 2*time.Second)
		healthMap, probeErr := e.meClient.CheckAllMarkets(probeCtx)
		cancelProbe()

		if probeErr != nil {
			e.metrics.IncMEHealthProbe("failure")
		} else {
			e.metrics.IncMEHealthProbe("success")
		}

		threshold := e.cfg.MELivenessThreshold
		if threshold <= 0 {
			threshold = 3
		}

		for _, mc := range e.cfg.Markets {
			if probeErr != nil || healthMap == nil || !healthMap[mc.MarketID] {
				e.consecutiveMETimeouts[mc.MarketID] = threshold
				e.marketPaused[mc.MarketID] = true
				e.metrics.IncMarketPause(mc.MarketID, "pause")
				e.logger.Warn("Matching Engine unreachable/unhealthy on startup — pausing market order creation",
					zap.String("market_id", mc.MarketID),
					zap.Error(probeErr))
			} else {
				e.consecutiveMETimeouts[mc.MarketID] = 0
				e.marketPaused[mc.MarketID] = false
			}
		}
	}

	// Start periodic tickers
	reconcileTicker := time.NewTicker(e.cfg.ReconcileInterval)
	walletTicker := time.NewTicker(e.cfg.WalletRefreshInterval)
	pendingTicker := time.NewTicker(e.cfg.PendingTimeout / 2)
	cancellingTicker := time.NewTicker(e.cfg.CancellingTimeout / 2)
	resyncInterval := e.cfg.MaxOrderStateStaleness / 2
	if resyncInterval <= 0 {
		resyncInterval = 45 * time.Second
	}
	resyncTicker := time.NewTicker(resyncInterval)
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

		case env, ok := <-e.tradeEvents:
			if !ok {
				e.logger.Info("trade events channel closed, shutting down")
				e.setState(StateStopped)
				return nil
			}
			e.handleTrade(env)

		case ev := <-e.events:
			switch ev.kind {
			case evTargetedReconcile:
				if e.state == StateRunning || e.state == StateDegraded {
					for marketID, dirty := range e.dirtyMarkets {
						if dirty {
							delete(e.dirtyMarkets, marketID)
							e.runReconcileMarket(ctx, marketID)
						}
					}
				}

			case evReconcileTick:
				if e.state == StateRunning || e.state == StateDegraded {
					// Periodic full reconcile resets dirty markets
					clear(e.dirtyMarkets)
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
					e.publishSnapshot()
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
			case <-ctx.Done():
				return
			}
		}
	}
}

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

// publishSnapshot builds an immutable StatusSnapshot and stores it atomically.
// MUST be called from the event loop goroutine only.
func (e *Engine) publishSnapshot() {
	snap := &StatusSnapshot{
		State:        e.state,
		MarketStates: make(map[string]string, len(e.cfg.Markets)),
		ActiveBids:   make(map[string]int, len(e.cfg.Markets)),
		ActiveAsks:   make(map[string]int, len(e.cfg.Markets)),
	}

	invStale := e.inv.IsStale(e.cfg.MaxBalanceStaleness)
	anyMarketActive := false

	for _, mc := range e.cfg.Markets {
		bids := e.tracker.ActiveCount(mc.MarketID, "BUY")
		asks := e.tracker.ActiveCount(mc.MarketID, "SELL")
		snap.ActiveBids[mc.MarketID] = bids
		snap.ActiveAsks[mc.MarketID] = asks

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
		if bids >= e.cfg.MinReadyBids && asks >= e.cfg.MinReadyAsks {
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

// ReadyBids returns the number of RESTING bid orders for a market. Thread-safe lock-free read.
func (e *Engine) ReadyBids(marketID string) int {
	if snap := e.snapshot.Load(); snap != nil {
		return snap.ActiveBids[marketID]
	}
	return 0
}

// ReadyAsks returns the number of RESTING ask orders for a market. Thread-safe lock-free read.
func (e *Engine) ReadyAsks(marketID string) int {
	if snap := e.snapshot.Load(); snap != nil {
		return snap.ActiveAsks[marketID]
	}
	return 0
}

// MarketStates returns the operational status of each configured market. Thread-safe lock-free read.
func (e *Engine) MarketStates() map[string]string {
	if snap := e.snapshot.Load(); snap != nil {
		return snap.MarketStates
	}
	return make(map[string]string)
}

// IsReady returns true if the engine is running and at least one market has active resting orders. Thread-safe lock-free read.
func (e *Engine) IsReady() bool {
	if snap := e.snapshot.Load(); snap != nil {
		return snap.IsReady
	}
	return false
}

// InventoryLastRefresh returns when the wallet balance was last fetched.
func (e *Engine) InventoryLastRefresh() time.Time {
	return e.inv.LastRefresh()
}

// MaxBalanceStaleness returns the configured max balance staleness.
func (e *Engine) MaxBalanceStaleness() time.Duration {
	return e.cfg.MaxBalanceStaleness
}
