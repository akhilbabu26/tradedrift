package main

import (
	"context"
	"net"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	marketv1 "tradedrift/platform/api/gen/market/v1"
	"tradedrift/platform/config"
	"tradedrift/platform/logger"
	"tradedrift/platform/postgres"
	marketconfig "tradedrift/services/market/internal/config"
	"tradedrift/services/market/internal/handler"
	postgresrepo "tradedrift/services/market/internal/repository/postgres"
	"tradedrift/services/market/internal/service"
)

func main() {
	// 0. Auto-load .env configuration file if present
	config.LoadEnv()
	cfg := marketconfig.Load()

	// 1. Initialize Logger
	appLogger := logger.New(cfg.LogLevel)
	defer appLogger.Sync()

	appLogger.Info("Starting Market Service...")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 2. Run database migrations
	appLogger.Info("Applying market database migrations...", zap.String("dir", cfg.MigrationsDir))
	if err := postgres.RunMigrations(cfg.PostgresDSN, cfg.MigrationsDir); err != nil {
		appLogger.Fatal("Failed to apply market migrations", zap.Error(err))
	}
	appLogger.Info("Market database migrations applied successfully")

	// 3. PostgreSQL connection pool
	poolCtx, cancelPool := context.WithTimeout(ctx, 10*time.Second)
	defer cancelPool()

	dbPool, err := postgres.NewPool(poolCtx, cfg.PostgresDSN, postgres.PoolConfig{
		MaxConns: 20, // High-frequency ticker & candle queries
	})
	if err != nil {
		appLogger.Fatal("Failed to connect to postgres pool", zap.Error(err))
	}
	defer dbPool.Close()

	// 4. Initialize Repositories and Service
	marketRepo := postgresrepo.NewMarketRepository(dbPool)
	candleRepo := postgresrepo.NewCandleRepository(dbPool)
	marketSvc := service.NewMarketService(marketRepo, candleRepo)

	// 5. Wire gRPC Server & Handler
	grpcHandler := handler.NewGRPCHandler(marketSvc, appLogger)
	grpcServer := grpc.NewServer()
	marketv1.RegisterMarketServiceServer(grpcServer, grpcHandler)

	lis, err := net.Listen("tcp", cfg.GRPCPort)
	if err != nil {
		appLogger.Fatal("Failed to bind gRPC listener", zap.String("port", cfg.GRPCPort), zap.Error(err))
	}

	// 6. Start gRPC Listener with Error Channel
	serverErrCh := make(chan error, 1)
	go func() {
		appLogger.Info("Market Service gRPC listening", zap.String("port", cfg.GRPCPort))
		if err := grpcServer.Serve(lis); err != nil && err != grpc.ErrServerStopped {
			serverErrCh <- err
		}
	}()

	// 7. Wait for shutdown signal or unexpected server failure
	select {
	case <-ctx.Done():
		appLogger.Info("Shutdown signal received, gracefully stopping Market Service...")
	case err := <-serverErrCh:
		appLogger.Error("gRPC server encountered unexpected error", zap.Error(err))
	}

	grpcServer.GracefulStop()
	appLogger.Info("Market Service stopped cleanly")
}
