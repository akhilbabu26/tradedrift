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
	"google.golang.org/grpc/reflection"

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

	// 1. Initialize Zap Structured Logger
	appLogger := logger.New(cfg.LogLevel)
	defer appLogger.Sync()

	appLogger.Info("Starting Order Service...",
		zap.String("grpc_port", cfg.GRPCPort),
	)

	// 2. Connect to PostgreSQL Database Pool with 30s Timeout
	initCtx, initCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer initCancel()

	dbPool, err := postgres.NewPool(initCtx, cfg.PostgresDSN, postgres.PoolConfig{
		MaxConns: 25,
		MinConns: 5,
	})
	if err != nil {
		appLogger.Fatal("Failed to connect to PostgreSQL database", zap.Error(err))
	}
	defer dbPool.Close()

	// 3. Run Database Migrations (Goose)
	appLogger.Info("Running order database migrations...", zap.String("dir", cfg.MigrationsDir))
	if err := postgres.RunMigrations(cfg.PostgresDSN, cfg.MigrationsDir); err != nil {
		appLogger.Fatal("Failed to apply order database migrations", zap.Error(err))
	}
	appLogger.Info("Order database migrations applied successfully")

	// 4. Initialize Dependencies (Repository, Wallet Client, Service, Handler, Publisher)
	orderRepo := repoPostgres.NewOrderRepository(dbPool, appLogger)

	walletClient, err := wallet.NewClient(cfg.WalletGRPCAddr, appLogger)
	if err != nil {
		appLogger.Fatal("Failed to create Wallet Service gRPC client", zap.Error(err))
	}
	defer walletClient.Close()
	appLogger.Info("Wallet Service gRPC client initialized", zap.String("addr", cfg.WalletGRPCAddr))

	orderSvc := service.NewService(orderRepo, walletClient, appLogger)
	grpcHandler := handler.NewGRPCHandler(orderSvc, appLogger)
	kafkaProducer := publisher.NewKafkaProducer(cfg.KafkaBrokers, appLogger)
	defer kafkaProducer.Close()

	// 5. Background Outbox Worker Lifecycle Context with WaitGroup Sync
	outboxCtx, cancelOutbox := context.WithCancel(context.Background())
	outboxPublisher := publisher.NewOutboxPublisher(orderRepo, kafkaProducer, appLogger, 200*time.Millisecond)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		outboxPublisher.Start(outboxCtx)
	}()

	// 6. Start gRPC Server
	lis, err := net.Listen("tcp", cfg.GRPCPort)
	if err != nil {
		appLogger.Fatal("Failed to bind TCP port", zap.String("port", cfg.GRPCPort), zap.Error(err))
	}

	grpcServer := grpc.NewServer()
	orderv1.RegisterOrderServiceServer(grpcServer, grpcHandler)
	reflection.Register(grpcServer) // Enable gRPC Server Reflection for Postman / gRPC tools

	// 7. Graceful Shutdown Signal Trap (gRPC GracefulStop -> Outbox Worker Stop -> Cleanup)
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
		case <-time.After(10 * time.Second):
			appLogger.Warn("gRPC server shutdown timed out, forcing stop")
			grpcServer.Stop()
		}

		// Step B: Signal Outbox publisher worker to stop & wait for loop termination
		appLogger.Info("Stopping Outbox worker goroutine...")
		cancelOutbox()
		wg.Wait()
		appLogger.Info("Outbox worker stopped cleanly")
	}()

	appLogger.Info("Order gRPC server listening", zap.String("port", cfg.GRPCPort))
	if err := grpcServer.Serve(lis); err != nil && err != grpc.ErrServerStopped {
		appLogger.Fatal("gRPC server serve failure", zap.Error(err))
	}
}
