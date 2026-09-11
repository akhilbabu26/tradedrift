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

	platformpg "tradedrift/platform/postgres"
	"tradedrift/services/wallet-topup/internal/client"
	"tradedrift/services/wallet-topup/internal/config"
	"tradedrift/services/wallet-topup/internal/handler"
	"tradedrift/services/wallet-topup/internal/payment/mock"
	postgresRepo "tradedrift/services/wallet-topup/internal/repository/postgres"
	"tradedrift/services/wallet-topup/internal/service"
	"tradedrift/services/wallet-topup/internal/webhook"
)

func main() {
	// 1. Logger
	log, _ := zap.NewProduction()
	defer log.Sync()

	log.Info("Starting Wallet Top-Up Service...")

	// 2. Config
	cfg := config.Load()

	// 3. Graceful shutdown context
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 4. Migrations
	migrationDir := "migrations"
	if _, err := os.Stat(migrationDir); os.IsNotExist(err) {
		migrationDir = "services/wallet-topup/migrations"
	}
	log.Info("Running topup migrations...", zap.String("dir", migrationDir))
	if err := platformpg.RunMigrations(cfg.PostgresDSN, migrationDir); err != nil {
		log.Warn("Could not run database migrations automatically (continuing if already migrated)", zap.Error(err))
	}

	// 5. Database Connection Pool
	poolConfig, err := pgxpool.ParseConfig(cfg.PostgresDSN)
	if err != nil {
		log.Fatal("Failed to parse postgres DSN", zap.Error(err))
	}
	poolConfig.MaxConns = 25
	poolConfig.MinConns = 5

	dbPool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		log.Fatal("Failed to connect to PostgreSQL", zap.Error(err))
	}
	defer dbPool.Close()

	// 6. Repositories & Transaction Manager
	dailyLimitRepo := postgresRepo.NewDailyLimitRepo(dbPool)
	orderRepo := postgresRepo.NewTopUpOrderRepo(dbPool)
	webhookRepo := postgresRepo.NewWebhookEventRepo(dbPool)
	txManager := postgresRepo.NewPostgresTxManager(dbPool)

	// 7. Payment Provider & Webhook Verifier
	mockProvider := mock.NewMockPaymentProvider(cfg.WebhookSecret)
	verifier := webhook.NewVerifier(mockProvider)

	// 8. Wallet gRPC Client (Non-blocking async connection)
	walletClient, err := client.NewWalletClient(cfg.WalletGRPCAddr)
	if err != nil {
		log.Error("Failed to initialize wallet gRPC client",
			zap.String("addr", cfg.WalletGRPCAddr),
			zap.Error(err),
		)
	} else {
		defer walletClient.Close()
	}

	// 9. Services & Workers
	topupService := service.NewTopUpService(dailyLimitRepo, orderRepo, txManager, mockProvider, cfg.DailyLimitINR, log)
	webhookService := service.NewWebhookService(verifier, webhookRepo, orderRepo, txManager, cfg.DailyLimitINR, log)

	if walletClient != nil {
		reconciler := service.NewReconcilerWorker(
			orderRepo,
			walletClient,
			log,
			cfg.ReconcilerInterval,
			50,
			60*time.Second,
		)
		reconciler.Start(ctx)
		defer reconciler.Stop()
	}

	expiryWorker := service.NewExpiryWorker(orderRepo, txManager, log, cfg.ExpiryInterval)
	expiryWorker.Start(ctx)
	defer expiryWorker.Stop()


	// 10. HTTP Handlers & Router
	topupHandler := handler.NewTopUpHandler(topupService, log)
	webhookHandler := handler.NewWebhookHandler(webhookService, log)
	router := handler.NewRouter(topupHandler, webhookHandler, log)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.Port),
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("Wallet Top-Up HTTP server listening", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("HTTP server failed", zap.Error(err))
		}
	}()

	<-ctx.Done()
	log.Info("Shutting down Wallet Top-Up Service...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("HTTP server shutdown error", zap.Error(err))
	}

	log.Info("Wallet Top-Up Service stopped cleanly")
}
