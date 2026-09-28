package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	platformconfig "tradedrift/platform/config"
	"tradedrift/services/controlled-taker/internal/clients/orderservice"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
	"tradedrift/services/controlled-taker/internal/config"
	"tradedrift/services/controlled-taker/internal/engine"
	"tradedrift/services/controlled-taker/internal/health"
	"tradedrift/services/controlled-taker/internal/metrics"
)

func main() {
	// 0. Auto-load .env file if present
	platformconfig.LoadEnv()

	// 1. Initialize Zap Logger
	logCfg := zap.NewProductionConfig()
	logCfg.EncoderConfig.EncodeTime = zapcore.RFC3339TimeEncoder
	logger, err := logCfg.Build()
	if err != nil {
		panic(err)
	}
	defer logger.Sync()

	logger.Info("Starting Controlled Taker Service...")

	// 2. Load Configuration
	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("Failed to load configuration", zap.Error(err))
	}

	// 3. Connect to Redis Depth Reader
	redisReader, err := redisdepth.NewReader(cfg.RedisAddr, logger)
	if err != nil {
		logger.Fatal("Failed to initialize Redis depth reader", zap.Error(err))
	}
	defer redisReader.Close()
	logger.Info("Connected to Redis successfully", zap.String("addr", cfg.RedisAddr))

	// 4. Connect to Order Service gRPC Client
	ordersClient, err := orderservice.NewClient(cfg.OrderGRPCAddr, logger)
	if err != nil {
		logger.Fatal("Failed to initialize Order Service gRPC client", zap.Error(err))
	}
	defer ordersClient.Close()
	logger.Info("Connected to Order Service gRPC", zap.String("addr", cfg.OrderGRPCAddr))

	// 5. Start Health & Metrics Servers
	healthServer := health.NewServer(cfg.HealthPort, redisReader, ordersClient, cfg.Markets, logger)
	healthServer.Start()

	metricsServer := metrics.NewServer(cfg.MetricsPort, logger)
	metricsServer.Start()

	// 6. Initialize Controlled Taker Engine
	takerEngine := engine.New(cfg, ordersClient, redisReader, logger)

	// 7. Context with Graceful Shutdown on SIGTERM/SIGINT
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info("Shutdown signal received", zap.String("signal", sig.String()))
		cancel()
	}()

	// 8. Run Engine Loop
	takerEngine.Start(ctx)

	// 9. Clean Shutdown of HTTP Servers
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	_ = healthServer.Stop(shutdownCtx)
	_ = metricsServer.Stop(shutdownCtx)

	logger.Info("Controlled Taker Service exited gracefully")
}
