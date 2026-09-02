package main

import (
	"context"
	"net"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	tradev1 "tradedrift/platform/api/gen/trade/v1"
	"tradedrift/platform/config"
	"tradedrift/platform/logger"
	platformpg "tradedrift/platform/postgres"
	tradeconfig "tradedrift/services/trade/internal/config"
	tradehandler "tradedrift/services/trade/internal/handler"
	tradekafka "tradedrift/services/trade/internal/kafka"
	tradepg "tradedrift/services/trade/internal/repository/postgres"
	tradesvc "tradedrift/services/trade/internal/service"
)

func main() {
	// ── 0. Load environment ───────────────────────────────────────────────────
	config.LoadEnv()
	cfg, err := tradeconfig.Load()
	if err != nil {
		panic("invalid configuration: " + err.Error())
	}

	// ── 1. Logger ─────────────────────────────────────────────────────────────
	appLogger := logger.New(cfg.LogLevel)
	defer appLogger.Sync()

	appLogger.Info("Starting Trade Service...")

	// ── 2. Graceful shutdown context ──────────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── 3. Database migrations ────────────────────────────────────────────────
	appLogger.Info("Applying trade database migrations...", zap.String("dir", cfg.MigrationsDir))
	if err := platformpg.RunMigrations(cfg.PostgresDSN, cfg.MigrationsDir); err != nil {
		appLogger.Fatal("Failed to apply trade migrations", zap.Error(err))
	}
	appLogger.Info("Trade database migrations applied successfully")

	// ── 4. PostgreSQL connection pool ─────────────────────────────────────────
	poolCtx, cancelPool := context.WithTimeout(ctx, 10*time.Second)
	defer cancelPool()
	dbPool, err := platformpg.NewPool(poolCtx, cfg.PostgresDSN, platformpg.PoolConfig{
		// 15 connections: up to ~3 Kafka partition writers (INSERT) +
		// headroom for concurrent gRPC read queries.
		MaxConns: 15,
	})
	if err != nil {
		appLogger.Fatal("Failed to connect to postgres pool", zap.Error(err))
	}
	defer dbPool.Close()
	appLogger.Info("Trade postgres pool connected")

	// ── 5. Repository + Service + Handler ─────────────────────────────────────
	repo    := tradepg.NewRepository(dbPool)
	svc     := tradesvc.NewService(repo, appLogger)
	handler := tradehandler.NewGRPCHandler(svc, appLogger)

	var wg sync.WaitGroup

	// ── 6. gRPC server ────────────────────────────────────────────────────────
	grpcServer := grpc.NewServer()
	tradev1.RegisterTradeServiceServer(grpcServer, handler)

	wg.Add(1)
	go func() {
		defer wg.Done()
		lis, err := net.Listen("tcp", cfg.GRPCPort)
		if err != nil {
			appLogger.Fatal("gRPC listen failed", zap.String("port", cfg.GRPCPort), zap.Error(err))
		}
		appLogger.Info("Trade gRPC server listening", zap.String("port", cfg.GRPCPort))
		if err := grpcServer.Serve(lis); err != nil {
			appLogger.Error("gRPC server error", zap.Error(err))
		}
	}()

	// ── 7. Kafka consumer ─────────────────────────────────────────────────────
	consumer := tradekafka.NewConsumer(
		cfg.KafkaBrokers,
		cfg.KafkaGroupID,
		cfg.KafkaTopic,
		cfg.KafkaDLQTopic,
		repo,
		appLogger,
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		consumer.Start(ctx)
	}()

	// ── 8. Running ────────────────────────────────────────────────────────────
	appLogger.Info("Trade Service running",
		zap.String("grpc_port", cfg.GRPCPort),
		zap.Strings("brokers", cfg.KafkaBrokers),
		zap.String("topic", cfg.KafkaTopic),
		zap.String("dlq_topic", cfg.KafkaDLQTopic),
		zap.String("group", cfg.KafkaGroupID),
	)

	// ── 9. Block until shutdown signal ────────────────────────────────────────
	<-ctx.Done()
	appLogger.Info("Shutdown signal received, stopping Trade Service...")

	// Stop gRPC gracefully — finishes in-flight RPCs before returning.
	grpcServer.GracefulStop()

	// Context cancellation stops new message fetches and terminates in-flight
	// processing. Any uncommitted Kafka offsets will be cleanly replayed on startup.
	wg.Wait()

	if err := consumer.Close(); err != nil {
		appLogger.Error("Kafka consumer close error", zap.Error(err))
	}

	appLogger.Info("Trade Service stopped cleanly")
}
