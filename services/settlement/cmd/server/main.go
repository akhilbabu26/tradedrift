package main

import (
	"context"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"

	"tradedrift/platform/config"
	"tradedrift/platform/logger"
	"tradedrift/platform/postgres"
	settlementclient "tradedrift/services/settlement/internal/client"
	settlementconfig "tradedrift/services/settlement/internal/config"
	settlementkafka "tradedrift/services/settlement/internal/kafka"
	settlementpostgres "tradedrift/services/settlement/internal/repository/postgres"
	"tradedrift/services/settlement/internal/service"
)

func main() {
	// ── 0. Load environment ──────────────────────────────────────────────────
	config.LoadEnv()
	cfg, err := settlementconfig.Load()
	if err != nil {
		// Can't use the structured logger yet — config failed before logger was created.
		panic("invalid configuration: " + err.Error())
	}

	// ── 1. Logger ────────────────────────────────────────────────────────────
	appLogger := logger.New(cfg.LogLevel)
	defer appLogger.Sync()

	appLogger.Info("Starting Settlement Service...")

	// ── 2. Graceful shutdown context ─────────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── 3. Database migrations ───────────────────────────────────────────────
	appLogger.Info("Applying settlement database migrations...",
		zap.String("dir", cfg.MigrationsDir),
	)
	if err := postgres.RunMigrations(cfg.PostgresDSN, cfg.MigrationsDir); err != nil {
		appLogger.Fatal("Failed to apply settlement migrations", zap.Error(err))
	}
	appLogger.Info("Settlement database migrations applied successfully")

	// ── 4. PostgreSQL connection pool ────────────────────────────────────────
	poolCtx, cancelPool := context.WithTimeout(ctx, 10*time.Second)
	defer cancelPool()

	dbPool, err := postgres.NewPool(poolCtx, cfg.PostgresDSN, postgres.PoolConfig{
		MaxConns: 10, // Settlement only needs Phase 1 + Phase 3 short transactions
	})
	if err != nil {
		appLogger.Fatal("Failed to connect to postgres pool", zap.Error(err))
	}
	defer dbPool.Close()

	appLogger.Info("Settlement postgres pool connected")

	// ── 5. Repository ─────────────────────────────────────────────────────────
	repo := settlementpostgres.NewRepository(dbPool)

	// ── 6. Wallet gRPC client ─────────────────────────────────────────────────
	walletClient, err := settlementclient.NewWalletClient(cfg.WalletGRPCAddr)
	if err != nil {
		appLogger.Fatal("Failed to connect to Wallet Service",
			zap.String("addr", cfg.WalletGRPCAddr),
			zap.Error(err),
		)
	}
	defer walletClient.Close()

	appLogger.Info("Wallet gRPC client connected",
		zap.String("addr", cfg.WalletGRPCAddr),
	)

	// ── 7. Settlement Service ─────────────────────────────────────────────────
	svc := service.NewService(repo, walletClient, appLogger, cfg.WalletGRPCTimeout)

	// ── 8. WaitGroup to track background goroutines ───────────────────────────
	// Ensures deterministic shutdown: main() blocks on wg.Wait() after
	// ctx is cancelled, letting every goroutine finish its in-flight work.
	var wg sync.WaitGroup

	// ── 9. Recovery goroutine (stale PENDING rows safety net) ─────────────────
	// Runs every 60s. Retries Wallet.SettleTrade for any PENDING row
	// older than 60 seconds that the Kafka consumer missed (e.g. crash between
	// Phase 2 and Phase 3). FOR UPDATE SKIP LOCKED prevents racing the consumer.
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		appLogger.Info("Settlement recovery goroutine started (60s interval)")
		for {
			select {
			case <-ticker.C:
				svc.RecoverStalePending(ctx)
			case <-ctx.Done():
				appLogger.Info("Settlement recovery goroutine stopping")
				return
			}
		}
	}()

	// ── 10. Kafka consumer ────────────────────────────────────────────────────
	consumer := settlementkafka.NewConsumer(
		cfg.KafkaBrokers,
		cfg.KafkaGroupID,
		cfg.KafkaTopic,
		svc,
		appLogger,
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		consumer.Start(ctx)
	}()

	// ── 11. Block until shutdown signal ───────────────────────────────────────
	appLogger.Info("Settlement Service running",
		zap.Strings("brokers", cfg.KafkaBrokers),
		zap.String("topic", cfg.KafkaTopic),
		zap.String("group", cfg.KafkaGroupID),
		zap.String("wallet", cfg.WalletGRPCAddr),
		zap.Duration("grpc_timeout", cfg.WalletGRPCTimeout),
	)

	<-ctx.Done()
	appLogger.Info("Shutdown signal received, waiting for goroutines to finish...")

	// ── 12. Deterministic shutdown ────────────────────────────────────────────
	// Wait for consumer and recovery goroutine to finish their in-flight work
	// before closing Kafka, Wallet, and PostgreSQL connections.
	// ctx is already cancelled — goroutines will exit at their next ctx.Done() check.
	wg.Wait()

	// Close Kafka reader after goroutines exit — prevents new FetchMessage calls
	// and unblocks any in-progress fetch immediately.
	if err := consumer.Close(); err != nil {
		appLogger.Error("Error closing Kafka consumer", zap.Error(err))
	}

	appLogger.Info("Settlement Service stopped cleanly")
}
