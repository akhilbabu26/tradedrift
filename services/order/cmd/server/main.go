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

	orderv1 "tradedrift/platform/api/gen/order/v1"
	platformconfig "tradedrift/platform/config"
	"tradedrift/platform/logger"
	"tradedrift/platform/postgres"
	orderconfig "tradedrift/services/order/internal/config"
	"tradedrift/services/order/internal/handler"
	repoPostgres "tradedrift/services/order/internal/repository/postgres"
	"tradedrift/services/order/internal/service"
	"tradedrift/services/order/internal/wallet"
)

func main() {
	// 0. Load environment variables
	platformconfig.LoadEnv()
	cfg := orderconfig.Load()

	// 1. Initialize Logger
	if err := platformconfig.ValidateLogLevel(cfg.LogLevel); err != nil {
		cfg.LogLevel = "info"
	}
	appLogger := logger.New(cfg.LogLevel)
	defer appLogger.Sync()

	appLogger.Info("Starting Order Service...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 2. Run Database Migrations
	migrationDir := cfg.MigrationsDir
	if _, err := os.Stat(migrationDir); os.IsNotExist(err) {
		if _, err := os.Stat("migration"); err == nil {
			migrationDir = "migration"
		}
	}
	appLogger.Info("Running order database migrations...", zap.String("dir", migrationDir))
	if err := postgres.RunMigrations(cfg.PostgresDSN, migrationDir); err != nil {
		appLogger.Fatal("Failed to apply order database migrations", zap.Error(err))
	}
	appLogger.Info("Order database migrations applied successfully")

	// 3. PostgreSQL Pool Connection
	dbPool, err := postgres.NewPool(ctx, cfg.PostgresDSN, postgres.PoolConfig{
		MaxConns: 20,
	})
	if err != nil {
		appLogger.Fatal("Failed to connect to PostgreSQL", zap.Error(err))
	}
	defer dbPool.Close()
	appLogger.Info("Connected to PostgreSQL")

	// 4. Wallet gRPC Client
	walletClient, err := wallet.NewClient(cfg.WalletGRPCAddr, appLogger)
	if err != nil {
		appLogger.Fatal("Failed to connect to Wallet Service", zap.Error(err))
	}
	defer walletClient.Close()
	appLogger.Info("Connected to Wallet Service gRPC", zap.String("addr", cfg.WalletGRPCAddr))

	// 5. Wire Repository, Service, and Handler
	orderRepo := repoPostgres.NewOrderRepository(dbPool, appLogger)
	orderService := service.NewService(orderRepo, walletClient, appLogger)
	grpcHandler := handler.NewGRPCHandler(orderService, appLogger)

	// 6. Start gRPC Server
	lis, err := net.Listen("tcp", cfg.GRPCPort)
	if err != nil {
		appLogger.Fatal("Failed to bind TCP port", zap.String("port", cfg.GRPCPort), zap.Error(err))
	}

	grpcServer := grpc.NewServer()
	orderv1.RegisterOrderServiceServer(grpcServer, grpcHandler)

	// 7. Graceful Shutdown Signal Trap
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		appLogger.Info("Shutting down Order Service gracefully...")
		grpcServer.GracefulStop()
		appLogger.Info("Order Service stopped")
	}()

	appLogger.Info("Order gRPC server listening", zap.String("port", cfg.GRPCPort))
	if err := grpcServer.Serve(lis); err != nil && err != grpc.ErrServerStopped {
		appLogger.Fatal("Order gRPC server failed", zap.Error(err))
	}
}
