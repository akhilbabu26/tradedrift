package handler

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	platformjwt "tradedrift/platform/jwt"
)

// RouterOption configures optional Phase 3 handlers.
type RouterOption func(*routerOptions)

type routerOptions struct {
	incidentHandler  *IncidentHandler
	analyticsHandler *AnalyticsHandler
	topologyHandler  *TopologyHandler
}

// WithIncidentHandler injects the IncidentHandler.
func WithIncidentHandler(h *IncidentHandler) RouterOption {
	return func(o *routerOptions) {
		o.incidentHandler = h
	}
}

// WithAnalyticsHandler injects the AnalyticsHandler.
func WithAnalyticsHandler(h *AnalyticsHandler) RouterOption {
	return func(o *routerOptions) {
		o.analyticsHandler = h
	}
}

// WithTopologyHandler injects the TopologyHandler.
func WithTopologyHandler(h *TopologyHandler) RouterOption {
	return func(o *routerOptions) {
		o.topologyHandler = h
	}
}

// NewRouter wires all admin endpoints, health routes, and Phase 3 intelligence routes with structured middleware.
func NewRouter(
	adminHandler *AdminHandler,
	healthHandler *HealthHandler,
	jwtValidator *platformjwt.HMACValidator,
	log *zap.Logger,
	opts ...RouterOption,
) http.Handler {
	options := &routerOptions{}
	for _, opt := range opts {
		opt(options)
	}

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

	// ─── 6. Phase 3: Incidents & Correlation ─────────────────────────────────────
	if options.incidentHandler != nil {
		mux.HandleFunc("GET /api/v1/admin/incidents", adminAuth(options.incidentHandler.HandleListIncidents))
		mux.HandleFunc("GET /api/v1/admin/incidents/stats", adminAuth(options.incidentHandler.HandleGetIncidentStats))
		mux.HandleFunc("GET /api/v1/admin/incidents/{incident_id}", adminAuth(options.incidentHandler.HandleGetIncident))
		mux.HandleFunc("GET /api/v1/admin/incidents/{incident_id}/correlated", adminAuth(options.incidentHandler.HandleGetCorrelatedIncident))
		mux.HandleFunc("POST /api/v1/admin/incidents/{incident_id}/resolve", adminAuth(options.incidentHandler.HandleResolveIncident))
	}

	// ─── 7. Phase 3: Business Analytics & Risk Signals ───────────────────────────
	if options.analyticsHandler != nil {
		mux.HandleFunc("GET /api/v1/admin/analytics/overview", adminAuth(options.analyticsHandler.HandleGetOperationsOverview))
		mux.HandleFunc("GET /api/v1/admin/analytics/risk-signals", adminAuth(options.analyticsHandler.HandleGetRiskSignals))
	}

	// ─── 8. Phase 3: Service Dependency Topology ─────────────────────────────────
	if options.topologyHandler != nil {
		mux.HandleFunc("GET /api/v1/admin/topology", adminAuth(options.topologyHandler.HandleGetTopology))
	}

	// Wrap entire router with metrics and structured logging middleware
	var h http.Handler = mux
	h = MetricsMiddleware()(h)
	h = StructuredLoggingMiddleware(log)(h)
	return h
}
