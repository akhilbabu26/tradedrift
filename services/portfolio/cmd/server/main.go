package main

import (
	"context"
	"net"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	marketv1 "tradedrift/platform/api/gen/market/v1"
	portfoliov1 "tradedrift/platform/api/gen/portfolio/v1"
	walletv1 "tradedrift/platform/api/gen/wallet/v1"
	"tradedrift/platform/config"
	"tradedrift/platform/logger"
	platformpg "tradedrift/platform/postgres"
	portfolioconfig "tradedrift/services/portfolio/internal/config"
	portfoliohandler "tradedrift/services/portfolio/internal/handler"
	portfoliokafka "tradedrift/services/portfolio/internal/kafka"
	portfoliopg "tradedrift/services/portfolio/internal/repository/postgres"
	portfoliosvc "tradedrift/services/portfolio/internal/service"
)

func main() {
	// ── 0. Load Configuration ────────────────────────────────────────────────
	config.LoadEnv()
	cfg, err := portfolioconfig.Load()
	if err != nil {
		panic("invalid configuration: " + err.Error())
	}

	// ── 1. Logger ────────────────────────────────────────────────────────────
	appLogger := logger.New(cfg.LogLevel)
	defer appLogger.Sync()
	appLogger.Info("Starting Portfolio Service...")

	// ── 2. Graceful Shutdown Context ─────────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── 3. Database Migrations ───────────────────────────────────────────────
	appLogger.Info("Applying portfolio database migrations...", zap.String("dir", cfg.MigrationsDir))
	if err := platformpg.RunMigrations(cfg.PostgresDSN, cfg.MigrationsDir); err != nil {
		appLogger.Fatal("Failed to apply portfolio migrations", zap.Error(err))
	}
	appLogger.Info("Portfolio database migrations applied successfully")

	// ── 4. PostgreSQL Connection Pool ────────────────────────────────────────
	poolCtx, cancelPool := context.WithTimeout(ctx, 10*time.Second)
	defer cancelPool()
	dbPool, err := platformpg.NewPool(poolCtx, cfg.PostgresDSN, platformpg.PoolConfig{
		MaxConns: 15,
	})
	if err != nil {
		appLogger.Fatal("Failed to connect to postgres pool", zap.Error(err))
	}
	defer dbPool.Close()
	appLogger.Info("Portfolio postgres pool connected")

	// ── 5. Dial Wallet Service gRPC Client ───────────────────────────────────
	walletConn, err := grpc.NewClient(cfg.WalletGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		appLogger.Fatal("Failed to connect to wallet service gRPC", zap.String("addr", cfg.WalletGRPCAddr), zap.Error(err))
	}
	defer walletConn.Close()
	walletClient := walletv1.NewWalletServiceClient(walletConn)

	// ── 6. Dial Market Service gRPC Client ───────────────────────────────────
	marketConn, err := grpc.NewClient(cfg.MarketGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		appLogger.Fatal("Failed to connect to market service gRPC", zap.String("addr", cfg.MarketGRPCAddr), zap.Error(err))
	}
	defer marketConn.Close()
	marketClient := marketv1.NewMarketServiceClient(marketConn)

	// ── 7. Initialize Repository, Service & Handler ──────────────────────────
	repo := portfoliopg.New(dbPool)
	svc := portfoliosvc.New(repo, walletClient, marketClient)
	handler := portfoliohandler.New(svc, appLogger)

	var wg sync.WaitGroup

	// ── 8. Start gRPC Server (:50058) ────────────────────────────────────────
	grpcServer := grpc.NewServer()
	portfoliov1.RegisterPortfolioServiceServer(grpcServer, handler)

	wg.Add(1)
	go func() {
		defer wg.Done()
		lis, err := net.Listen("tcp", cfg.GRPCPort)
		if err != nil {
			appLogger.Fatal("gRPC listen failed", zap.String("port", cfg.GRPCPort), zap.Error(err))
		}
		appLogger.Info("Portfolio gRPC server listening", zap.String("port", cfg.GRPCPort))
		if err := grpcServer.Serve(lis); err != nil {
			appLogger.Error("gRPC server error", zap.Error(err))
		}
	}()

	// ── 9. Start HTTP Metrics & Health Server (:9091) ────────────────────────
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	metricsMux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		if err := dbPool.Ping(r.Context()); err != nil {
			http.Error(w, "database unreachable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("READY"))
	})

	metricsServer := &http.Server{
		Addr:         cfg.MetricsPort,
		Handler:      metricsMux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		appLogger.Info("Portfolio metrics & health server listening", zap.String("port", cfg.MetricsPort))
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			appLogger.Error("metrics server failed", zap.Error(err))
		}
	}()

	// ── 10. Start Kafka Consumer Loop ────────────────────────────────────────
	consumer := portfoliokafka.NewConsumer(
		cfg.KafkaBrokers,
		cfg.KafkaGroupID,
		cfg.KafkaTopicPortfolioUserTrades,
		cfg.KafkaTopicTradeDLQ,
		repo,
		appLogger,
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := consumer.Start(ctx); err != nil {
			appLogger.Error("consumer loop exited with error", zap.Error(err))
		}
	}()

	// ── 11. Start Transactional Outbox Publisher Loop ────────────────────────
	publisher := portfoliokafka.NewOutboxPublisher(
		cfg.KafkaBrokers,
		cfg.KafkaTopicPortfolioUpdated,
		repo,
		appLogger,
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := publisher.Start(ctx); err != nil {
			appLogger.Error("outbox publisher loop exited with error", zap.Error(err))
		}
	}()

	// ── 12. Await OS Signals & Graceful Teardown ─────────────────────────────
	<-ctx.Done()
	appLogger.Info("Shutdown signal received; draining portfolio service resources...")

	grpcServer.GracefulStop()
	appLogger.Info("Portfolio gRPC server stopped")

	metricsCtx, cancelMetrics := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelMetrics()
	_ = metricsServer.Shutdown(metricsCtx)
	appLogger.Info("Portfolio metrics server stopped")

	wg.Wait()

	if err := consumer.Close(); err != nil {
		appLogger.Error("error closing consumer", zap.Error(err))
	}
	if err := publisher.Close(); err != nil {
		appLogger.Error("error closing outbox publisher", zap.Error(err))
	}

	appLogger.Info("Portfolio service shutdown complete")
}
