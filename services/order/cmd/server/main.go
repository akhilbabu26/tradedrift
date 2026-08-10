package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"sync"
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
	"tradedrift/services/order/internal/kafka/publisher"
	repoPostgres "tradedrift/services/order/internal/repository/postgres"
	"tradedrift/services/order/internal/service"
	"tradedrift/services/order/internal/wallet"
)

func main() {
	// 0. Load Environment Variables
	platformconfig.LoadEnv()
	cfg := orderconfig.Load()

	// 1. Initialize Logger
	if err := platformconfig.ValidateLogLevel(cfg.LogLevel); err != nil {
		cfg.LogLevel = "info"
	}
	appLogger := logger.New(cfg.LogLevel)
	defer appLogger.Sync()

	appLogger.Info("Starting Order Service...")

	// Explicit 30s timeout for server startup initialization
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStartup()

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
	dbPool, err := postgres.NewPool(startupCtx, cfg.PostgresDSN, postgres.PoolConfig{
		MaxConns: 20,
	})
	if err != nil {
		appLogger.Fatal("Failed to connect to PostgreSQL pool", zap.Error(err))
	}
	defer dbPool.Close()
	appLogger.Info("Connected to PostgreSQL pool")

	// 4. Wallet gRPC Client
	walletClient, err := wallet.NewClient(cfg.WalletGRPCAddr, appLogger)
	if err != nil {
		appLogger.Fatal("Failed to initialize Wallet Service client", zap.Error(err))
	}
	defer walletClient.Close()
	appLogger.Info("Wallet Service gRPC client initialized", zap.String("addr", cfg.WalletGRPCAddr))

	// 5. Wire Repository, Service, Handler, Kafka Producer & Outbox Publisher
	orderRepo := repoPostgres.NewOrderRepository(dbPool, appLogger)
	orderService := service.NewService(orderRepo, walletClient, appLogger)
	grpcHandler := handler.NewGRPCHandler(orderService, appLogger)

	kafkaProducer := publisher.NewLogProducer(appLogger)
	defer kafkaProducer.Close()

	// 6. Background Outbox Worker Lifecycle Context with WaitGroup Sync
	outboxCtx, cancelOutbox := context.WithCancel(context.Background())
	outboxPublisher := publisher.NewOutboxPublisher(orderRepo, kafkaProducer, appLogger, 200*time.Millisecond)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		outboxPublisher.Start(outboxCtx)
	}()

	// 7. Start gRPC Server
	lis, err := net.Listen("tcp", cfg.GRPCPort)
	if err != nil {
		appLogger.Fatal("Failed to bind TCP port", zap.String("port", cfg.GRPCPort), zap.Error(err))
	}

	grpcServer := grpc.NewServer()
	orderv1.RegisterOrderServiceServer(grpcServer, grpcHandler)

	// 8. Graceful Shutdown Signal Trap (gRPC GracefulStop -> Outbox Worker Stop -> Cleanup)
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan
		appLogger.Info("Shutting down Order Service gracefully...")

		// Step A: Stop accepting new RPCs & drain active requests
		stopped := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			close(stopped)
		}()

		select {
		case <-stopped:
			appLogger.Info("gRPC server stopped gracefully")
		case <-time.After(5 * time.Second):
			appLogger.Warn("gRPC GracefulStop timed out after 5s, forcing Stop()")
			grpcServer.Stop()
		}

		// Step B: Stop Outbox Worker and wait for goroutine to exit
		cancelOutbox()
		wg.Wait()
		appLogger.Info("Outbox Publisher worker stopped cleanly")
	}()

	appLogger.Info("Order gRPC server listening", zap.String("port", cfg.GRPCPort))
	if err := grpcServer.Serve(lis); err != nil && err != grpc.ErrServerStopped {
		appLogger.Fatal("Order gRPC server failed", zap.Error(err))
	}
}
