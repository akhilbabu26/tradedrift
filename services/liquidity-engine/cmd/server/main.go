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

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/engine"
	"tradedrift/services/liquidity-engine/internal/health"
	"tradedrift/services/liquidity-engine/internal/inventory"
	"tradedrift/services/liquidity-engine/internal/kafka"
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
	tradeEvents := make(chan kafka.TradeEvent, 256)
	consumer := kafka.NewConsumer(cfg.KafkaBrokers, cfg.KafkaGroupID, tradeEvents, logger)

	// ── Reconciler ────────────────────────────────────────────────────
	rec := reconciler.NewReconciler(tracker, producer, orderSvc, &cfg, logger, m)

	// ── Engine ────────────────────────────────────────────────────────
	eng := engine.NewEngine(&cfg, tracker, inv, rec, producer, consumer, walletSvc, m, logger)

	// ── Context with graceful shutdown ────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
