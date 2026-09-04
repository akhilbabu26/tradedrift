package main

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	authv1   "tradedrift/platform/api/gen/auth/v1"
	marketv1 "tradedrift/platform/api/gen/market/v1"
	orderv1  "tradedrift/platform/api/gen/order/v1"
	portfoliov1 "tradedrift/platform/api/gen/portfolio/v1"
	tradev1  "tradedrift/platform/api/gen/trade/v1"
	walletv1 "tradedrift/platform/api/gen/wallet/v1"
	"tradedrift/platform/config"
	platformjwt "tradedrift/platform/jwt"
	"tradedrift/platform/logger"

	authhandler   "tradedrift/services/gateway/internal/handler/auth"
	markethandler "tradedrift/services/gateway/internal/handler/market"
	orderhandler  "tradedrift/services/gateway/internal/handler/order"
	tradehandler  "tradedrift/services/gateway/internal/handler/trade"
	portfoliohandler "tradedrift/services/gateway/internal/handler/portfolio"
	wallethandler "tradedrift/services/gateway/internal/handler/wallet"
	"tradedrift/services/gateway/internal/middleware"
	"tradedrift/services/gateway/internal/ws"

	"github.com/redis/go-redis/v9"
)

func formatTarget(addr string) string {
	addr = strings.TrimSpace(addr)
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	return addr
}

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
	authAddr   := formatTarget(config.GetEnv("AUTH_ADDR",    "127.0.0.1:50051"))
	walletAddr := formatTarget(config.GetEnv("WALLET_ADDR", "127.0.0.1:50052"))
	orderAddr  := formatTarget(config.GetEnv("ORDER_ADDR",  "127.0.0.1:50053"))
	marketAddr := formatTarget(config.GetEnv("MARKET_ADDR", "127.0.0.1:50054"))
	tradeAddr  := formatTarget(config.GetEnv("TRADE_ADDR",  "127.0.0.1:50057"))
	portfolioAddr := formatTarget(config.GetEnv("PORTFOLIO_ADDR", "127.0.0.1:50058"))
	allowedOrigins := []string{
		config.GetEnv("CORS_ORIGIN", "http://localhost:5173"),
	}

	jwtSecretStr, err := config.GetEnvOrError("JWT_SECRET")
	if err != nil {
		appLogger.Fatal("JWT_SECRET is required", zap.Error(err))
	}

	// 3. Connect to gRPC Microservices
	authConn, err := grpc.NewClient(authAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		appLogger.Fatal("Failed to connect to Auth service", zap.String("addr", authAddr), zap.Error(err))
	}
	defer authConn.Close()

	walletConn, err := grpc.NewClient(walletAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		appLogger.Fatal("Failed to connect to Wallet service", zap.String("addr", walletAddr), zap.Error(err))
	}
	defer walletConn.Close()

	orderConn, err := grpc.NewClient(orderAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		appLogger.Fatal("Failed to connect to Order service", zap.String("addr", orderAddr), zap.Error(err))
	}
	defer orderConn.Close()

	marketConn, err := grpc.NewClient(marketAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		appLogger.Fatal("Failed to connect to Market service", zap.String("addr", marketAddr), zap.Error(err))
	}
	defer marketConn.Close()

	tradeConn, err := grpc.NewClient(tradeAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		appLogger.Fatal("Failed to connect to Trade service", zap.String("addr", tradeAddr), zap.Error(err))
	}

	portfolioConn, err := grpc.NewClient(portfolioAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		appLogger.Fatal("Failed to connect to Portfolio service", zap.String("addr", portfolioAddr), zap.Error(err))
	}
	defer portfolioConn.Close()

	defer tradeConn.Close()

	// 4. Instantiate Handlers
	authH   := authhandler.NewHandler(authv1.NewAuthServiceClient(authConn))
	walletH := wallethandler.NewHandler(walletv1.NewWalletServiceClient(walletConn))
	orderH  := orderhandler.NewHandler(orderv1.NewOrderServiceClient(orderConn))
	marketH := markethandler.NewHandler(marketv1.NewMarketServiceClient(marketConn))
	tradeH  := tradehandler.NewHandler(tradev1.NewTradeServiceClient(tradeConn))
	portfolioH := portfoliohandler.NewHandler(portfoliov1.NewPortfolioServiceClient(portfolioConn))

	// 5. Redis and WebSocket Subsystem Setup
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	redisAddr := config.GetEnv("REDIS_ADDR", "localhost:6379")
	redisClient := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	defer redisClient.Close()

	kafkaBrokersStr := config.GetEnv("KAFKA_BROKERS", "localhost:9092")
	var kafkaBrokers []string
	for _, b := range strings.Split(kafkaBrokersStr, ",") {
		if trimmed := strings.TrimSpace(b); trimmed != "" {
			kafkaBrokers = append(kafkaBrokers, trimmed)
		}
	}

	// Horizontal Scaling Fanout: Each Gateway replica requires a stable instance-unique
	// consumer group ID (e.g. POD_NAME or HOSTNAME) so Kafka broadcasts all trades to all Gateway replicas.
	gatewayInstanceID := config.GetEnv("GATEWAY_INSTANCE_ID", config.GetEnv("HOSTNAME", "gateway-01"))
	kafkaGroupID := config.GetEnv("KAFKA_GROUP_ID", fmt.Sprintf("gateway-websocket-%s", gatewayInstanceID))
	appLogger.Info("Configured WebSocket Kafka trade consumer group", zap.String("group_id", kafkaGroupID))

	wsStreamer := ws.NewStreamer(nil, redisClient, kafkaBrokers, "trades.executed", kafkaGroupID, appLogger)
	wsHub := ws.NewHub(appLogger, wsStreamer)
	wsStreamer.SetHub(wsHub)
	wsStreamer.Start(ctx)

	corsOrigin := config.GetEnv("CORS_ORIGIN", "http://localhost:5173")
	wsH := ws.NewHandler(wsHub, jwtSecretStr, corsOrigin, appLogger)

	jwtValidator := platformjwt.NewHMACValidator([]byte(jwtSecretStr))
	rateLimiter  := middleware.NewRateLimiter(ctx, rate.Every(time.Second), 20)
	authMW       := middleware.Auth(jwtValidator)

	protected := func(h http.Handler) http.Handler {
		return authMW(h)
	}

	global := func(h http.Handler) http.Handler {
		h = rateLimiter.Middleware(h)
		h = middleware.CORS(allowedOrigins)(h)
		h = middleware.Recovery(appLogger)(h)
		h = middleware.Logger(appLogger)(h)
		h = middleware.RequestID(h)
		return h
	}

	// 6. Router
	mux := http.NewServeMux()

	// WebSocket Gateway — RFC 6455
	mux.HandleFunc("GET /ws", wsH.ServeWS)

	// Auth — public
	mux.HandleFunc("POST /api/v1/auth/register",        authH.Register)
	mux.HandleFunc("POST /api/v1/auth/verify",          authH.VerifyEmail)
	mux.HandleFunc("POST /api/v1/auth/resend",          authH.ResendVerification)
	mux.HandleFunc("POST /api/v1/auth/login",           authH.Login)
	mux.HandleFunc("POST /api/v1/auth/refresh",         authH.RefreshToken)
	mux.HandleFunc("POST /api/v1/auth/forgot-password", authH.ForgotPassword)
	mux.HandleFunc("POST /api/v1/auth/reset-password",  authH.ResetPassword)

	// Auth — protected
	mux.Handle("POST /api/v1/auth/logout",          protected(http.HandlerFunc(authH.Logout)))
	mux.Handle("POST /api/v1/auth/logout-all",      protected(http.HandlerFunc(authH.LogoutAll)))
	mux.Handle("POST /api/v1/auth/change-password", protected(http.HandlerFunc(authH.ChangePassword)))

	// Wallet — public
	mux.HandleFunc("GET /api/v1/wallet/assets", walletH.GetSupportedAssets)

	// Wallet — protected
	mux.Handle("GET /api/v1/wallet/balances",         protected(http.HandlerFunc(walletH.GetBalances)))
	mux.Handle("GET /api/v1/wallet/balances/{asset}", protected(http.HandlerFunc(walletH.GetBalance)))

	// Order — protected
	mux.Handle("POST /api/v1/orders",               protected(http.HandlerFunc(orderH.CreateOrder)))
	mux.Handle("GET /api/v1/orders",                protected(http.HandlerFunc(orderH.ListOrders)))
	mux.Handle("GET /api/v1/orders/{id}",           protected(http.HandlerFunc(orderH.GetOrder)))
	mux.Handle("POST /api/v1/orders/{id}/cancel",   protected(http.HandlerFunc(orderH.CancelOrder)))

	// Market — public
	mux.HandleFunc("GET /api/v1/markets",              marketH.ListMarkets)
	mux.HandleFunc("GET /api/v1/markets/{id}",         marketH.GetMarket)
	mux.HandleFunc("GET /api/v1/markets/{id}/ticker",  marketH.GetTicker)
	mux.HandleFunc("GET /api/v1/markets/{id}/candles", marketH.GetCandles)
	// Market trade tape — public (TI-7: no buyer_id/seller_id in response)
	mux.HandleFunc("GET /api/v1/markets/{id}/trades",  tradeH.ListMarketTrades)

	// Trades — protected
	mux.Handle("GET /api/v1/trades",     protected(http.HandlerFunc(tradeH.ListUserTrades)))
	mux.Handle("GET /api/v1/trades/{id}", protected(http.HandlerFunc(tradeH.GetTrade)))

	// Portfolio — protected
	mux.Handle("GET /api/v1/portfolio/summary",  protected(http.HandlerFunc(portfolioH.GetPortfolioSummary)))
	mux.Handle("GET /api/v1/portfolio/holdings", protected(http.HandlerFunc(portfolioH.GetPortfolioHoldings)))

	// 7. HTTP Server
	srv := &http.Server{
		Addr:         httpPort,
		Handler:      global(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 8. Start + Graceful Shutdown
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
