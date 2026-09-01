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
