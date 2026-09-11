package handler

import (
	"net/http"

	"go.uber.org/zap"
)

func NewRouter(topupHandler *TopUpHandler, webhookHandler *WebhookHandler, log *zap.Logger) http.Handler {
	mux := http.NewServeMux()

	// TopUp user endpoints
	mux.HandleFunc("POST /api/v1/topups", RequireAuth(topupHandler.HandleCreateTopUp))
	mux.HandleFunc("GET /api/v1/topups/daily-usage", RequireAuth(topupHandler.HandleGetDailyUsage))
	mux.HandleFunc("GET /api/v1/topups/{id}", RequireAuth(topupHandler.HandleGetTopUpByID))

	// Webhook endpoints
	mux.HandleFunc("POST /api/v1/webhooks/payment/{provider}", webhookHandler.HandleProviderWebhook)

	// Health check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	return LoggingMiddleware(log, mux)
}
