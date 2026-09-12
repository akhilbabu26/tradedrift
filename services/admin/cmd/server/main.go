package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	platformjwt "tradedrift/platform/jwt"
	platformpg "tradedrift/platform/postgres"
	"tradedrift/services/admin/internal/client"
	"tradedrift/services/admin/internal/config"
	"tradedrift/services/admin/internal/handler"
	postgresRepo "tradedrift/services/admin/internal/repository/postgres"
	"tradedrift/services/admin/internal/service"
)

func main() {
	// 1. Logger
	log, err := zap.NewProduction()
	if err != nil {
		fmt.Printf("Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer log.Sync()
	log.Info("Starting TradeDrift Admin Service...")

	// 2. Config
	cfg := config.Load()

	// 3. Root shutdown context
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 4. Database Migrations (Fail-fast: do not continue if migrations fail)
	migrationDir := "migrations"
	if _, err := os.Stat(migrationDir); os.IsNotExist(err) {
		migrationDir = "services/admin/migrations"
	}
	log.Info("Running admin database migrations...", zap.String("dir", migrationDir))
	if err := platformpg.RunMigrations(cfg.PostgresDSN, migrationDir); err != nil {
		log.Fatal("Could not apply database migrations; halting startup", zap.Error(err))
	}

	// 5. Database Connection Pool
	poolCfg, err := pgxpool.ParseConfig(cfg.PostgresDSN)
	if err != nil {
		log.Fatal("Failed to parse postgres DSN", zap.Error(err))
	}
	poolCfg.MaxConns = 25
	poolCfg.MinConns = 5

	dbPool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		log.Fatal("Failed to connect to PostgreSQL", zap.Error(err))
	}
	defer dbPool.Close()

	// 6. Repositories
	txMgr := postgresRepo.NewTxManager(dbPool)
	opsRepo := postgresRepo.NewOperationsRepo(dbPool)
	outboxRepo := postgresRepo.NewOutboxRepo(dbPool)
	sagaRepo := postgresRepo.NewSagaRepo(dbPool)

	// 7. gRPC Clients (Fail-fast: verify client creation succeeds)
	authCli, err := client.NewAuthClient(cfg.AuthGRPCAddr)
	if err != nil {
		log.Fatal("Failed to initialize Auth gRPC client", zap.String("addr", cfg.AuthGRPCAddr), zap.Error(err))
	}
	defer authCli.Close()

	walletCli, err := client.NewWalletClient(cfg.WalletGRPCAddr)
	if err != nil {
		log.Fatal("Failed to initialize Wallet gRPC client", zap.String("addr", cfg.WalletGRPCAddr), zap.Error(err))
	}
	defer walletCli.Close()

	// 8. Services
	adminSvc := service.NewAdminService(txMgr, opsRepo, authCli, walletCli, log)

	// 9. Background Workers
	outboxPub := service.NewOutboxPublisher(outboxRepo, cfg.KafkaBrokers, log, cfg.OutboxInterval)
	outboxPub.Start(ctx)
	defer outboxPub.Stop()

	sagaWorker := service.NewSagaWorker(sagaRepo, opsRepo, authCli, log, cfg.SagaInterval)
	sagaWorker.Start(ctx)
	defer sagaWorker.Stop()

	// 10. HTTP Layer
	healthCfg := handler.HealthConfig{
		AuthGRPCAddr:   cfg.AuthGRPCAddr,
		WalletGRPCAddr: cfg.WalletGRPCAddr,
		TradeHealthURL: cfg.TradeHealthURL,
		PortHealthURL:  cfg.PortHealthURL,
		LiqHealthURL:   cfg.LiqHealthURL,
		NotifHealthURL: cfg.NotifHealthURL,
		KafkaBrokers:   cfg.SplitKafkaBrokers(),
	}
	healthHdr := handler.NewHealthHandler(dbPool, authCli, walletCli, outboxRepo, sagaRepo, healthCfg, log)
	adminHdr := handler.NewAdminHandler(adminSvc, log)
	jwtValidator := platformjwt.NewHMACValidator([]byte(cfg.JWTSecret))

	router := handler.NewRouter(adminHdr, healthHdr, jwtValidator, log)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Info("Admin Service listening for requests", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("HTTP server failed", zap.Error(err))
		}
	}()

	// 11. Coordinated Graceful Shutdown
	<-ctx.Done()
	log.Info("Shutdown signal received: initiating graceful drainage...")

	// Step A: Immediately mark readiness as 503 so load balancers stop sending traffic
	healthHdr.SetShuttingDown()
	log.Info("Readiness probe set to 503 (shutting down)")

	// Step B: Drain inflight HTTP requests
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("HTTP server shutdown error", zap.Error(err))
	}

	log.Info("Admin Service terminated cleanly")
}
