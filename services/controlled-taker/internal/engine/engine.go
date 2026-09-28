package engine

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
	"tradedrift/services/controlled-taker/internal/clients/orderservice"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
	"tradedrift/services/controlled-taker/internal/config"
)

// Engine coordinates the lifecycle of all per-market workers.
type Engine struct {
	cfg          config.Config
	ordersClient *orderservice.Client
	redisReader  *redisdepth.Reader
	workers      []*MarketWorker
	logger       *zap.Logger
}

// New creates an Engine orchestrator.
func New(
	cfg config.Config,
	ordersClient *orderservice.Client,
	redisReader *redisdepth.Reader,
	logger *zap.Logger,
) *Engine {
	selector := NewDirectionSelectorWithThresholds(cfg.InventoryModerateBiasUSDT, cfg.InventoryHeavyBiasUSDT)

	workers := make([]*MarketWorker, 0, len(cfg.Markets))
	for _, m := range cfg.Markets {
		w := NewMarketWorker(m, cfg, ordersClient, redisReader, selector, logger)
		workers = append(workers, w)
	}

	return &Engine{
		cfg:          cfg,
		ordersClient: ordersClient,
		redisReader:  redisReader,
		workers:      workers,
		logger:       logger,
	}
}

// Start launches all isolated market workers with anti-burst startup staggering.
func (e *Engine) Start(ctx context.Context) {
	if !e.cfg.Enabled {
		e.logger.Info("Controlled Taker Service is DISABLED via configuration; skipping worker startup")
		return
	}

	e.logger.Info("Starting Controlled Taker Engine", zap.Int("active_markets", len(e.workers)))

	var wg sync.WaitGroup
	baseDelay := e.cfg.WarmupDelay

	for i, w := range e.workers {
		wg.Add(1)
		// Stagger worker start by +5s per market to prevent cold reboot burst
		stagger := baseDelay + time.Duration(i*5)*time.Second
		go func(worker *MarketWorker, delay time.Duration) {
			defer wg.Done()
			worker.Run(ctx, delay)
		}(w, stagger)
	}

	<-ctx.Done()
	e.logger.Info("Controlled Taker Engine shutting down; waiting for workers to exit")
	wg.Wait()
	e.logger.Info("All market workers exited cleanly")
}
