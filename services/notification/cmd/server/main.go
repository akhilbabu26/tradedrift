package main

import (
	"context"
	"net"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	notificationv1 "tradedrift/platform/api/gen/notification/v1"
	"tradedrift/platform/config"
	"tradedrift/platform/logger"
	platformpg "tradedrift/platform/postgres"
	platformredis "tradedrift/platform/redis"
	notificationconfig "tradedrift/services/notification/internal/config"
	"tradedrift/services/notification/internal/handler"
	notificationkafka "tradedrift/services/notification/internal/kafka"
	"tradedrift/services/notification/internal/publisher"
	postgresrepo "tradedrift/services/notification/internal/repository/postgres"
	"tradedrift/services/notification/internal/service"
)

func main() {
	// ── 0. Load Configuration ────────────────────────────────────────────────
	config.LoadEnv()
	cfg, err := notificationconfig.Load()
	if err != nil {
		panic("invalid notification configuration: " + err.Error())
	}

	// ── 1. Logger ────────────────────────────────────────────────────────────
	appLogger := logger.New(cfg.LogLevel)
	defer appLogger.Sync()
	appLogger.Info("Starting Notification Service...")

	// ── 2. Graceful Shutdown Context ─────────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── 3. Database Migrations ───────────────────────────────────────────────
	appLogger.Info("Applying notification database migrations...", zap.String("dir", cfg.MigrationsDir))
	if err := platformpg.RunMigrations(cfg.PostgresDSN, cfg.MigrationsDir); err != nil {
		appLogger.Fatal("Failed to apply notification migrations", zap.Error(err))
	}
	appLogger.Info("Notification database migrations applied successfully")

	// ── 4. PostgreSQL Connection Pool ────────────────────────────────────────
	poolCtx, cancelPool := context.WithTimeout(ctx, 10*time.Second)
	defer cancelPool()
	dbPool, err := platformpg.NewPool(poolCtx, cfg.PostgresDSN, platformpg.PoolConfig{
		MaxConns: 15,
	})
	if err != nil {
		appLogger.Fatal("Failed to connect to notification postgres pool", zap.Error(err))
	}
	defer dbPool.Close()
	appLogger.Info("Notification postgres pool connected")

	// ── 5. Redis Client ──────────────────────────────────────────────────────
	redisClient, err := platformredis.NewClient(poolCtx, platformredis.Config{
		Addr:           cfg.RedisAddr,
		SentinelMaster: cfg.RedisSentinelMaster,
		Password:       cfg.RedisPassword,
		DB:             cfg.RedisDB,
	})
	if err != nil {
		appLogger.Fatal("Failed to connect to Redis", zap.String("addr", cfg.RedisAddr), zap.Error(err))
	}
	defer redisClient.Close()
	appLogger.Info("Notification Redis client connected", zap.String("addr", cfg.RedisAddr))

	// ── 6. Domain Services & Adapters ────────────────────────────────────────
	repo := postgresrepo.NewRepository(dbPool)
	svc := service.NewService(repo, appLogger)
	grpcHandler := handler.NewNotificationHandler(svc, appLogger)

	var wg sync.WaitGroup

	// ── 7. Start gRPC Server (:50059) ────────────────────────────────────────
	grpcServer := grpc.NewServer()
	notificationv1.RegisterNotificationServiceServer(grpcServer, grpcHandler)

	wg.Add(1)
	go func() {
		defer wg.Done()
		lis, err := net.Listen("tcp", cfg.GRPCPort)
		if err != nil {
			appLogger.Fatal("gRPC listen failed", zap.String("port", cfg.GRPCPort), zap.Error(err))
		}
		appLogger.Info("Notification gRPC server listening", zap.String("port", cfg.GRPCPort))
		if err := grpcServer.Serve(lis); err != nil {
			appLogger.Error("gRPC server error", zap.Error(err))
		}
	}()

	// ── 8. Start HTTP Metrics & Health Server (:9092) ────────────────────────
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	metricsMux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		if err := dbPool.Ping(r.Context()); err != nil {
			http.Error(w, "database unreachable", http.StatusServiceUnavailable)
			return
		}
		if err := redisClient.Ping(r.Context()).Err(); err != nil {
			http.Error(w, "redis unreachable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("READY"))
	})

	metricsServer := &http.Server{
		Addr:         cfg.MetricsPort,
		Handler:      metricsMux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		appLogger.Info("Notification metrics & health server listening", zap.String("port", cfg.MetricsPort))
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			appLogger.Error("metrics server failed", zap.Error(err))
		}
	}()

	// ── 9. Start Transactional Outbox Publisher ──────────────────────────────
	outboxPub := publisher.NewPublisher(repo, redisClient, appLogger, publisher.DefaultConfig())
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := outboxPub.Start(ctx); err != nil && err != context.Canceled {
			appLogger.Error("Outbox publisher exited with error", zap.Error(err))
		}
	}()

	// ── 10. Start Kafka Domain Consumers ─────────────────────────────────────
	kafkaConsumer := notificationkafka.NewConsumer(
		notificationkafka.ConsumerConfig{
			Brokers:  cfg.KafkaBrokers,
			GroupID:  cfg.KafkaGroupID,
			DLQTopic: cfg.KafkaDLQTopic,
		},
		svc,
		appLogger,
	)
	kafkaConsumer.Start(ctx)

	// ── 11. Graceful Teardown ────────────────────────────────────────────────
	<-ctx.Done()
	appLogger.Info("Shutdown signal received; draining notification service resources...")

	grpcServer.GracefulStop()
	appLogger.Info("Notification gRPC server stopped")

	metricsCtx, cancelMetrics := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelMetrics()
	_ = metricsServer.Shutdown(metricsCtx)
	appLogger.Info("Notification metrics server stopped")

	if err := kafkaConsumer.Close(); err != nil {
		appLogger.Error("Error closing Kafka consumer", zap.Error(err))
	}

	wg.Wait()
	appLogger.Info("Notification service shutdown complete")
}
