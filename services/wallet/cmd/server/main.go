package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	walletv1 "tradedrift/platform/api/gen/wallet/v1"
	"tradedrift/platform/config"
	"tradedrift/platform/logger"
	"tradedrift/platform/postgres"
	"tradedrift/services/wallet/internal/handler"
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
	dbDSN    := config.GetEnv("WALLET_POSTGRES_DSN", "postgres://postgres:postgres@localhost:5432/tradedrift?sslmode=disable")
	grpcPort := config.GetEnv("WALLET_PORT", ":50052")
	migrationDir := config.GetEnv("WALLET_MIGRATIONS_DIR", "services/wallet/migration")
	if _, err := os.Stat(migrationDir); os.IsNotExist(err) {
		if _, err := os.Stat("migration"); err == nil {
			migrationDir = "migration"
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 3. Run migrations
	appLogger.Info("Running wallet migrations...", zap.String("dir", migrationDir))
	if err := postgres.RunMigrations(dbDSN, migrationDir); err != nil {
		appLogger.Fatal("Failed to apply wallet migrations", zap.Error(err))
	}
	appLogger.Info("Wallet migrations applied")

	// 4. Postgres pool
	dbPool, err := postgres.NewPool(ctx, dbDSN, postgres.PoolConfig{
		MaxConns: 20, // Wallet gets more connections — high-frequency reads
	})
	if err != nil {
		appLogger.Fatal("Failed to connect to PostgreSQL", zap.Error(err))
	}
	defer dbPool.Close()
	appLogger.Info("Connected to PostgreSQL")

	// 5. Wire service and handler
	walletService := service.NewService(dbPool, appLogger)
	grpcHandler  := handler.NewGRPCHandler(walletService, appLogger)

	// 6. gRPC server (no auth interceptor — internal service, protected by network policy)
	lis, err := net.Listen("tcp", grpcPort)
	if err != nil {
		appLogger.Fatal("Failed to bind TCP port", zap.String("port", grpcPort), zap.Error(err))
	}

	grpcServer := grpc.NewServer()
	walletv1.RegisterWalletServiceServer(grpcServer, grpcHandler)

	// 7. Graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		appLogger.Info("Shutting down Wallet Service gracefully...")
		grpcServer.GracefulStop()
		appLogger.Info("Wallet Service stopped")
	}()

	appLogger.Info("Wallet gRPC server listening", zap.String("port", grpcPort))
	if err := grpcServer.Serve(lis); err != nil && err != grpc.ErrServerStopped {
		appLogger.Fatal("gRPC server failed", zap.Error(err))
	}
}
