package main

import (
	"context"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	authv1   "tradedrift/platform/api/gen/auth/v1"
	walletv1 "tradedrift/platform/api/gen/wallet/v1"
	"tradedrift/platform/config"
	platformjwt "tradedrift/platform/jwt"
	"tradedrift/platform/logger"

	"tradedrift/services/gateway/internal/handler"
	"tradedrift/services/gateway/internal/middleware"
)

func main() {
	// 0. Load .env if present
	config.LoadEnv()

	// 1. Logger
	logLevel := config.GetEnv("LOG_LEVEL", "info")
	appLogger := logger.New(logLevel)
	defer appLogger.Sync()

	appLogger.Info("Starting API Gateway...")

	// 2. Config
	httpPort   := config.GetEnv("GATEWAY_PORT", ":8080")
	authAddr   := config.GetEnv("AUTH_ADDR",    "localhost:50051")
	walletAddr := config.GetEnv("WALLET_ADDR",  "localhost:50052")
	allowedOrigins := []string{
		config.GetEnv("CORS_ORIGIN", "http://localhost:3000"),
	}

	jwtSecretStr, err := config.GetEnvOrError("JWT_SECRET")
	if err != nil {
		appLogger.Fatal("JWT_SECRET is required", zap.Error(err))
	}

	// 3. gRPC — Auth service
	authConn, err := grpc.NewClient(authAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		appLogger.Fatal("Failed to connect to Auth service", zap.String("addr", authAddr), zap.Error(err))
	}
	defer authConn.Close()
	appLogger.Info("Connected to Auth service", zap.String("addr", authAddr))

	// 4. gRPC — Wallet service
	walletConn, err := grpc.NewClient(walletAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		appLogger.Fatal("Failed to connect to Wallet service", zap.String("addr", walletAddr), zap.Error(err))
	}
	defer walletConn.Close()
	appLogger.Info("Connected to Wallet service", zap.String("addr", walletAddr))

	// 5. Handlers
	authHandler   := handler.NewAuthHandler(authv1.NewAuthServiceClient(authConn))
	walletHandler := handler.NewWalletHandler(walletv1.NewWalletServiceClient(walletConn))

	// 6. Middleware setup
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	jwtValidator := platformjwt.NewHMACValidator([]byte(jwtSecretStr))
	rateLimiter  := middleware.NewRateLimiter(ctx, rate.Every(time.Second), 20)
	authMW       := middleware.Auth(jwtValidator)

	// protected wraps a handler with JWT auth
	protected := func(h http.Handler) http.Handler {
		return authMW(h)
	}

	// global applies to every request.
	// Wrapping is inside-out: last wrapped = outermost = first to execute.
	// Execution order: RequestID → Logger → Recovery → CORS → RateLimiter → Handler
	global := func(h http.Handler) http.Handler {
		h = rateLimiter.Middleware(h)
		h = middleware.CORS(allowedOrigins)(h)
		h = middleware.Recovery(appLogger)(h)
		h = middleware.Logger(appLogger)(h)
		h = middleware.RequestID(h)
		return h
	}

	// 7. Router
	mux := http.NewServeMux()

	// Auth — public
	mux.HandleFunc("POST /api/v1/auth/register",        authHandler.Register)
	mux.HandleFunc("POST /api/v1/auth/verify",          authHandler.VerifyEmail)
	mux.HandleFunc("POST /api/v1/auth/resend",          authHandler.ResendVerification)
	mux.HandleFunc("POST /api/v1/auth/login",           authHandler.Login)
	mux.HandleFunc("POST /api/v1/auth/refresh",         authHandler.RefreshToken)
	mux.HandleFunc("POST /api/v1/auth/forgot-password", authHandler.ForgotPassword)
	mux.HandleFunc("POST /api/v1/auth/reset-password",  authHandler.ResetPassword)

	// Auth — protected
	mux.Handle("POST /api/v1/auth/logout",          protected(http.HandlerFunc(authHandler.Logout)))
	mux.Handle("POST /api/v1/auth/logout-all",      protected(http.HandlerFunc(authHandler.LogoutAll)))
	mux.Handle("POST /api/v1/auth/change-password", protected(http.HandlerFunc(authHandler.ChangePassword)))

	// Wallet — public
	mux.HandleFunc("GET /api/v1/wallet/assets", walletHandler.GetSupportedAssets)

	// Wallet — protected
	mux.Handle("GET /api/v1/wallet/balances",         protected(http.HandlerFunc(walletHandler.GetBalances)))
	mux.Handle("GET /api/v1/wallet/balances/{asset}", protected(http.HandlerFunc(walletHandler.GetBalance)))

	// 8. HTTP server
	srv := &http.Server{
		Addr:         httpPort,
		Handler:      global(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 9. Start + graceful shutdown
	go func() {
		appLogger.Info("API Gateway listening", zap.String("port", httpPort))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			appLogger.Fatal("HTTP server error", zap.Error(err))
		}
	}()

	<-ctx.Done()
	appLogger.Info("Shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		appLogger.Error("Graceful shutdown failed", zap.Error(err))
	}
	appLogger.Info("API Gateway stopped cleanly")
}
