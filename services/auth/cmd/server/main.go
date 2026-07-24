package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	platformredis "tradedrift/platform/redis"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	authv1 "tradedrift/platform/api/gen/auth/v1"
	"tradedrift/platform/config"
	"tradedrift/platform/logger"
	"tradedrift/platform/postgres"
	platformjwt "tradedrift/platform/jwt"
	platformgrpc "tradedrift/platform/jwt/grpc"
	"tradedrift/services/auth/internal/handler"
	"tradedrift/services/auth/internal/mail"
	"tradedrift/services/auth/internal/otp"
	postgresRepo "tradedrift/services/auth/internal/repository/postgres"
	"tradedrift/services/auth/internal/service"
)

// mockWalletClient satisfies service.WalletClient for bootstrapping.
type mockWalletClient struct {
	log *zap.Logger
}

func (m *mockWalletClient) InitializeWallet(ctx context.Context, userID string) error {
	m.log.Info("Initialized simulator wallets successfully for user", zap.String("userID", userID))
	return nil
}

func main() {
	// 1. Initialize Logger
	logLevel := config.GetEnv("LOG_LEVEL", "info")
	if err := config.ValidateLogLevel(logLevel); err != nil {
		logLevel = "info"
	}
	appLogger := logger.New(logLevel)
	defer appLogger.Sync()

	appLogger.Info("Starting Authentication Service...")

	// 2. Load configurations using platform config
	dbDSN := config.GetEnv("POSTGRES_DSN", "postgres://postgres:postgres@localhost:5432/tradedrift?sslmode=disable")
	redisAddr := config.GetEnv("REDIS_ADDR", "localhost:6379")
	redisSentinelMaster := config.GetEnv("REDIS_SENTINEL_MASTER", "")
	grpcPort := config.GetEnv("PORT", ":50051")
	migrationDir := config.GetEnv("MIGRATIONS_DIR", "migrations")

	// Critical configuration: JWT_SECRET must never silently default in production
	jwtSecretStr, err := config.GetEnvOrError("JWT_SECRET")
	if err != nil {
		appLogger.Fatal("Configuration validation failed", zap.Error(err))
	}
	jwtSecret := []byte(jwtSecretStr)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 3. Apply startup migrations using platform postgres Goose tool
	appLogger.Info("Running database migrations...", zap.String("dir", migrationDir))
	if err := postgres.RunMigrations(dbDSN, migrationDir); err != nil {
		appLogger.Fatal("Failed to apply database migrations", zap.Error(err))
	}
	appLogger.Info("Database migrations applied successfully")

	// 4. Initialize PostgreSQL Connection Pool using platform client
	dbPool, err := postgres.NewPool(ctx, dbDSN, postgres.PoolConfig{
		MaxConns: 10,
	})
	if err != nil {
		appLogger.Fatal("Failed to initialize PostgreSQL pool", zap.Error(err))
	}
	defer dbPool.Close()
	appLogger.Info("Successfully connected to PostgreSQL")

	// 5. Initialize Redis Client (supports standalone and Sentinel HA)
	rdb, err := platformredis.NewClient(ctx, platformredis.Config{
		Addr:           redisAddr,
		SentinelMaster: redisSentinelMaster,
	})
	if err != nil {
		appLogger.Fatal("Redis cache is unreachable", zap.Error(err))
	}
	defer rdb.Close()
	appLogger.Info("Successfully connected to Redis")

	// 6. Instantiate Repository Adapters
	userRepo := postgresRepo.NewUserRepository(dbPool)
	tokenRepo := postgresRepo.NewTokenRepository(dbPool)

	// 7. Instantiate Domain Core Services
	otpMgr := otp.NewManager(rdb, 5*time.Minute)
	mailer := mail.NewLogMailer()
	walletCl := &mockWalletClient{log: appLogger}

	authService := service.NewService(
		userRepo,
		tokenRepo,
		otpMgr,
		mailer,
		walletCl,
		rdb,
		jwtSecret,
		15*time.Minute,  // Access Token TTL
		7*24*time.Hour, // Refresh Token TTL
		appLogger,
	)

	// 8. Wire platform JWT verification middleware
	redisValidator := platformjwt.NewRedisValidator(jwtSecret, rdb, authService)

	// Public methods bypassed by the authentication interceptor
	publicMethods := map[string]bool{
		"/tradedrift.auth.v1.AuthService/Register":               true,
		"/tradedrift.auth.v1.AuthService/VerifyEmail":            true,
		"/tradedrift.auth.v1.AuthService/ResendVerificationCode": true,
		"/tradedrift.auth.v1.AuthService/Login":                  true,
		"/tradedrift.auth.v1.AuthService/RefreshToken":           true,
		"/tradedrift.auth.v1.AuthService/ForgotPassword":         true,
		"/tradedrift.auth.v1.AuthService/ResetPassword":          true,
	}

	authInterceptor := platformgrpc.UnaryAuthInterceptor(redisValidator, publicMethods)

	// 9. Start gRPC Server
	lis, err := net.Listen("tcp", grpcPort)
	if err != nil {
		appLogger.Fatal("Failed to bind TCP port", zap.String("port", grpcPort), zap.Error(err))
	}

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(authInterceptor),
	)

	grpcHandler := handler.NewGRPCHandler(authService, appLogger)
	authv1.RegisterAuthServiceServer(grpcServer, grpcHandler)

	// 10. Handle Graceful Shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		appLogger.Info("Stopping gRPC server gracefully...")
		grpcServer.GracefulStop()
		appLogger.Info("Service terminated cleanly")
	}()

	appLogger.Info("gRPC server listening", zap.String("port", grpcPort))
	if err := grpcServer.Serve(lis); err != nil && err != grpc.ErrServerStopped {
		appLogger.Fatal("gRPC server run failed", zap.Error(err))
	}
}
