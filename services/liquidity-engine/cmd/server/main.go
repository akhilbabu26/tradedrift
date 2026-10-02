// cmd/server/main.go is the LE service entrypoint.
// It wires all dependencies and starts the engine event loop.
//
// SINGLETON REQUIREMENT: This service must run as exactly 1 replica.
// Horizontal scaling is not supported in V1. Set replicas: 1 in Kubernetes.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"tradedrift/platform/refprice"
	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/engine"
	"tradedrift/services/liquidity-engine/internal/health"
	"tradedrift/services/liquidity-engine/internal/inventory"
	"tradedrift/services/liquidity-engine/internal/kafka"
	"tradedrift/services/liquidity-engine/internal/meclient"
	"tradedrift/services/liquidity-engine/internal/metrics"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/orderservice"
	"tradedrift/services/liquidity-engine/internal/reconciler"
	"tradedrift/services/liquidity-engine/internal/walletservice"
)

func main() {
	// ── Logger ────────────────────────────────────────────────────────
	logger, err := zap.NewProduction()
	if err != nil {
		panic("failed to create logger: " + err.Error())
	}
	defer logger.Sync()

	logger.Info("liquidity engine initializing",
		zap.String("service", "liquidity-engine"),
		zap.String("version", "v1.1.0"))

	// ── Config ────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("config load failed", zap.Error(err))
	}
	if err := cfg.ValidatePartitions(); err != nil {
		logger.Fatal("partition config invalid", zap.Error(err))
	}

	logger.Info("config loaded",
		zap.Strings("brokers", cfg.KafkaBrokers),
		zap.String("wallet_addr", cfg.WalletGRPCAddr),
		zap.String("order_addr", cfg.OrderGRPCAddr))

	// ── Metrics ───────────────────────────────────────────────────────
	m := metrics.New()

	// ── Order Service Client ──────────────────────────────────────────
	orderSvc, err := orderservice.NewClient(cfg.OrderGRPCAddr, logger)
	if err != nil {
		logger.Fatal("failed to connect to Order Service", zap.Error(err))
	}
	defer orderSvc.Close()

	// ── Wallet Service Client ─────────────────────────────────────────
	walletSvc, err := walletservice.NewClient(cfg.WalletGRPCAddr, logger)
	if err != nil {
		logger.Fatal("failed to connect to Wallet Service", zap.Error(err))
	}
	defer walletSvc.Close()

	// ── ME Client ─────────────────────────────────────────────────────
	meClient := meclient.New(cfg.MEHTTPAddr, logger)

	// ── Tracker + Inventory ───────────────────────────────────────────
	tracker := order.NewTracker()
	inv := inventory.NewManager(tracker, logger)

	// ── Kafka Producer ────────────────────────────────────────────────
	marketPartitions := make([]kafka.MarketPartition, len(cfg.Markets))
	for i, mc := range cfg.Markets {
		marketPartitions[i] = kafka.MarketPartition{
			MarketID:  mc.MarketID,
			Partition: mc.Partition,
		}
	}
	producer := kafka.NewProducer(cfg.KafkaBrokers, marketPartitions, logger)
	defer producer.Close()

	// ── Kafka Consumer ────────────────────────────────────────────────
	tradeEvents := make(chan kafka.TradeEnvelope, 256)
	consumer := kafka.NewConsumer(cfg.KafkaBrokers, cfg.KafkaGroupID, tradeEvents, logger)

	// ── Reconciler ────────────────────────────────────────────────────
	rec := reconciler.NewReconciler(tracker, producer, orderSvc, meClient, &cfg, logger, m)

	// ── Context with graceful shutdown ────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── Reference Price Provider ──────────────────────────────────────
	marketIDs := make([]string, len(cfg.Markets))
	for i, mc := range cfg.Markets {
		marketIDs[i] = mc.MarketID
	}
	refCfg := refprice.Config{
		RefreshInterval: cfg.RefPrice.RefreshInterval,
		StaleThreshold:  cfg.RefPrice.StaleThreshold,
		PauseThreshold:  cfg.RefPrice.PauseThreshold,
		FetchTimeout:    cfg.RefPrice.FetchTimeout,
	}
	if refCfg.RefreshInterval <= 0 {
		refCfg.RefreshInterval = 30 * time.Second
	}
	if refCfg.StaleThreshold <= 0 {
		refCfg.StaleThreshold = 5 * time.Minute
	}
	if refCfg.PauseThreshold <= 0 {
		refCfg.PauseThreshold = 10 * time.Minute
	}
	if refCfg.FetchTimeout <= 0 {
		refCfg.FetchTimeout = 5 * time.Second
	}
	plan := refprice.PlanDemo
	if cfg.RefPrice.Plan == "pro" {
		plan = refprice.PlanPro
	}
	refFetcher, err := refprice.NewCoinGeckoFetcherWithConfig(refprice.CoinGeckoConfig{
		Plan:    plan,
		APIKey:  cfg.RefPrice.APIKey,
		BaseURL: cfg.RefPrice.APIURL,
		Timeout: refCfg.FetchTimeout,
		Markets: marketIDs,
	})
	if err != nil {
		logger.Fatal("failed to initialize reference price fetcher", zap.Error(err))
	}
	refProv, err := refprice.NewProvider(refCfg, refFetcher, marketIDs, logger)
	if err != nil {
		logger.Fatal("failed to initialize reference price provider", zap.Error(err))
	}

	// ── Redis Anchor Publisher ────────────────────────────────────────
	// Publishes live reference anchors to Redis (refprice:anchor:{marketID})
	// with TTL=60s and monotonic version protection for Order Service validation.
	anchorPub, closePub := refprice.NewRedisAnchorPublisherFromAddr(cfg.RedisAddr, logger)
	defer closePub()
	refProv.WithPublisher(anchorPub)

	// ── Gated Startup Live Fetch ──────────────────────────────────────
	// Invariant (Reviewer Directive 1): Synchronous, bounded startup fetch ensures LE quotes
	// from fresh external live prices before reconciler or ME book can drift.
	// If live fetch succeeds, it populates provider cache, stamps version 1,
	// and publishes the Redis anchor for Order Service.
	// If it times out or fails (e.g. offline sandbox, rate limit), it falls back
	// to configured static seeds with an explicit operator warning.
	fetchTimeout := 3 * time.Second
	if err := refProv.FetchInitial(ctx, fetchTimeout); err != nil {
		logger.Warn("gated startup live reference fetch failed — falling back to configured seeds",
			zap.Duration("timeout", fetchTimeout),
			zap.Error(err))
		for _, mc := range cfg.Markets {
			seed := cfg.RefPrice.SeedForMarket(mc.MarketID)
			if seed.IsZero() {
				seed = mc.ReferencePrice
			}
			if !seed.IsZero() {
				refProv.SeedIfAbsent(mc.MarketID, seed)
			}
		}
	} else {
		logger.Info("gated startup live reference fetch succeeded — quoting directly from live external reference prices")
	}

	go refProv.Run(ctx)
	rec.SetRefProvider(refProv)

	// ── Engine ────────────────────────────────────────────────────────
	eng := engine.NewEngine(&cfg, tracker, inv, rec, producer, consumer, tradeEvents, walletSvc, meClient, m, logger)

	// ── HTTP Servers ──────────────────────────────────────────────────
	// Health server (Port 8080: /healthz, /readyz, /status)
	healthServer := &http.Server{
		Addr:    ":" + cfg.HealthPort,
		Handler: health.New(eng).Handler(),
	}
	go func() {
		logger.Info("health server listening", zap.String("port", cfg.HealthPort))
		if err := healthServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("health server error", zap.Error(err))
		}
	}()

	// Metrics server (Port 9090)
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsServer := &http.Server{
		Addr:    ":" + cfg.MetricsPort,
		Handler: metricsMux,
	}
	go func() {
		logger.Info("metrics server listening", zap.String("port", cfg.MetricsPort))
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server error", zap.Error(err))
		}
	}()

	// ── Start Engine ──────────────────────────────────────────────────
	logger.Info("starting engine event loop")
	if err := eng.Run(ctx); err != nil {
		logger.Error("engine stopped with error", zap.Error(err))
	}

	// ── Graceful Shutdown ─────────────────────────────────────────────
	logger.Info("shutting down HTTP servers")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	healthServer.Shutdown(shutdownCtx)
	metricsServer.Shutdown(shutdownCtx)

	logger.Info("liquidity engine stopped cleanly")
}
