package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	walletv1 "tradedrift/platform/api/gen/wallet/v1"
	"tradedrift/platform/config"
	"tradedrift/platform/logger"
	platformpg "tradedrift/platform/postgres"
	"tradedrift/services/wallet/internal/handler"
	"tradedrift/services/wallet/internal/publisher"
	walletpg "tradedrift/services/wallet/internal/repository/postgres"
	"tradedrift/services/wallet/internal/service"
)

func main() {
	// 0. Load .env if present
	config.LoadEnv()

	// 1. Logger
	logLevel := config.GetEnv("LOG_LEVEL", "info")
	if err := config.ValidateLogLevel(logLevel); err != nil {
		logLevel = "info"
	}
	appLogger := logger.New(logLevel)
	defer appLogger.Sync()

	appLogger.Info("Starting Wallet Service...")

	// 2. Config
	dbDSN := config.GetEnv("WALLET_POSTGRES_DSN", "postgres://postgres:postgres@localhost:5432/tradedrift?sslmode=disable")
	grpcPort := config.GetEnv("WALLET_PORT", ":50052")
	migrationDir := config.GetEnv("WALLET_MIGRATIONS_DIR", "services/wallet/migration")
	kafkaBrokers := parseBrokers(config.GetEnv("KAFKA_BROKERS", "localhost:9092"))
	kafkaTopicTradeSettled := config.GetEnv("KAFKA_TOPIC_TRADE_SETTLED", "trades.settled.v1")
	kafkaTopicPortfolioUserTrades := config.GetEnv("KAFKA_TOPIC_PORTFOLIO_USER_TRADES", "portfolio.user.trades.v1")

	if _, err := os.Stat(migrationDir); os.IsNotExist(err) {
		if _, err := os.Stat("migration"); err == nil {
			migrationDir = "migration"
		}
	}

	// 3. Graceful shutdown context
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 4. Migrations
	appLogger.Info("Running wallet migrations...", zap.String("dir", migrationDir))
	if err := platformpg.RunMigrations(dbDSN, migrationDir); err != nil {
		appLogger.Fatal("Failed to apply wallet migrations", zap.Error(err))
	}
	appLogger.Info("Wallet migrations applied")

	// 5. Postgres pool
	poolCtx, cancelPool := context.WithTimeout(ctx, 10*time.Second)
	defer cancelPool()
	dbPool, err := platformpg.NewPool(poolCtx, dbDSN, platformpg.PoolConfig{
		MaxConns: 20, // Wallet gets more connections — high-frequency reads + publisher
	})
	if err != nil {
		appLogger.Fatal("Failed to connect to PostgreSQL", zap.Error(err))
	}
	defer dbPool.Close()
	appLogger.Info("Connected to PostgreSQL")

	// 6. Wire service and handler
	walletService := service.NewService(dbPool, appLogger)
	grpcHandler := handler.NewGRPCHandler(walletService, appLogger)

	var wg sync.WaitGroup

	// 7. gRPC server
	lis, err := net.Listen("tcp", grpcPort)
	if err != nil {
		appLogger.Fatal("Failed to bind TCP port", zap.String("port", grpcPort), zap.Error(err))
	}
	grpcServer := grpc.NewServer()
	walletv1.RegisterWalletServiceServer(grpcServer, grpcHandler)

	wg.Add(1)
	go func() {
		defer wg.Done()
		appLogger.Info("Wallet gRPC server listening", zap.String("port", grpcPort))
		if err := grpcServer.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			appLogger.Error("gRPC server failed", zap.Error(err))
		}
	}()

	// 8. Outbox publisher — polls the outbox table and publishes to trades.settled.v1 & portfolio.user.trades.v1
	outboxRepo := walletpg.NewOutboxRepository(dbPool)
	outboxPub := publisher.NewOutboxPublisher(outboxRepo, kafkaBrokers, kafkaTopicTradeSettled, kafkaTopicPortfolioUserTrades, appLogger)

	wg.Add(1)
	go func() {
		defer wg.Done()
		outboxPub.Run(ctx)
	}()

	appLogger.Info("Wallet Service running",
		zap.String("grpc_port", grpcPort),
		zap.Strings("kafka_brokers", kafkaBrokers),
		zap.String("kafka_topic_trade_settled", kafkaTopicTradeSettled),
		zap.String("kafka_topic_portfolio_user_trades", kafkaTopicPortfolioUserTrades),
	)

	// 9. Block until shutdown signal
	<-ctx.Done()
	appLogger.Info("Shutdown signal received, stopping Wallet Service...")

	// GracefulStop finishes in-flight gRPC calls before returning.
	grpcServer.GracefulStop()

	// ctx cancellation propagates to outbox publisher's Run() loop — it exits cleanly.
	wg.Wait()

	if err := outboxPub.Close(); err != nil {
		appLogger.Error("Outbox publisher close error", zap.Error(err))
	}

	appLogger.Info("Wallet Service stopped cleanly")
}

func parseBrokers(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
