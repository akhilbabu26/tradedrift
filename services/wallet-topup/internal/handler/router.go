package handler

import (
	"net/http"

	"go.uber.org/zap"
)

func NewRouter(topupHandler *TopUpHandler, webhookHandler *WebhookHandler, log *zap.Logger) http.Handler {
	mux := http.NewServeMux()

	// TopUp user endpoints
	mux.HandleFunc("POST /api/v1/topups", RequireAuth(topupHandler.HandleCreateTopUp))
	mux.HandleFunc("POST /api/v1/topups/verify", RequireAuth(topupHandler.HandleVerifyCheckout))
	mux.HandleFunc("GET /api/v1/topups", RequireAuth(topupHandler.HandleListTopUps))
	mux.HandleFunc("GET /api/v1/topups/daily-usage", RequireAuth(topupHandler.HandleGetDailyUsage))
	mux.HandleFunc("GET /api/v1/topups/{id}", RequireAuth(topupHandler.HandleGetTopUpByID))

	// Webhook endpoints
	mux.HandleFunc("POST /api/v1/webhooks/payment/{provider}", webhookHandler.HandleProviderWebhook)

	// Health check (liveness and readiness)
	healthHandler := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "ok", "service": "wallet-topup"})
	}
	mux.HandleFunc("GET /health", healthHandler)
	mux.HandleFunc("GET /healthz", healthHandler)
	mux.HandleFunc("GET /readyz", healthHandler)

	// Internal microservice router behind API Gateway; CORS is handled centrally by API Gateway.
	return LoggingMiddleware(log, mux)
}
