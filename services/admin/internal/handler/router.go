package handler

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	platformjwt "tradedrift/platform/jwt"
)

// NewRouter wires all admin endpoints and health routes with structured middleware.
func NewRouter(
	adminHandler *AdminHandler,
	healthHandler *HealthHandler,
	jwtValidator *platformjwt.HMACValidator,
	log *zap.Logger,
) http.Handler {
	mux := http.NewServeMux()

	adminAuth := RequireAdmin(jwtValidator)

	// ─── 1. Health & Probes & Metrics (Unauthenticated) ──────────────────────────
	mux.HandleFunc("GET /health", healthHandler.HandleLiveness)
	mux.HandleFunc("GET /ready", healthHandler.HandleReadiness)
	mux.Handle("GET /metrics", promhttp.Handler())

	// ─── 2. System Diagnostic Health (Authenticated) ─────────────────────────────
	mux.HandleFunc("GET /api/v1/admin/system/health", adminAuth(healthHandler.HandleSystemHealth))

	// ─── 3. User Management ──────────────────────────────────────────────────────
	mux.HandleFunc("POST /api/v1/admin/users/{user_id}/suspend",
		adminAuth(RequireIdempotencyKey(adminHandler.HandleSuspendUser)))

	mux.HandleFunc("POST /api/v1/admin/users/{user_id}/unsuspend",
		adminAuth(RequireIdempotencyKey(adminHandler.HandleUnsuspendUser)))

	// ─── 4. Wallet Management ────────────────────────────────────────────────────
	mux.HandleFunc("POST /api/v1/admin/users/{user_id}/wallets/{asset}/freeze",
		adminAuth(RequireIdempotencyKey(adminHandler.HandleFreezeWallet)))

	mux.HandleFunc("POST /api/v1/admin/users/{user_id}/wallets/{asset}/unfreeze",
		adminAuth(RequireIdempotencyKey(adminHandler.HandleUnfreezeWallet)))

	// ─── 5. Market Management ────────────────────────────────────────────────────
	mux.HandleFunc("POST /api/v1/admin/markets/{market_id}/halt",
		adminAuth(RequireIdempotencyKey(adminHandler.HandleHaltMarket)))

	mux.HandleFunc("POST /api/v1/admin/markets/{market_id}/resume",
		adminAuth(RequireIdempotencyKey(adminHandler.HandleResumeMarket)))

	// Wrap entire router with metrics and structured logging middleware
	var h http.Handler = mux
	h = MetricsMiddleware()(h)
	h = StructuredLoggingMiddleware(log)(h)
	return h
}
