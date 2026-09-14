package handler

import (
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/admin/internal/service"
)

// AnalyticsHandler exposes PostgreSQL-backed operational business metrics and risk signals.
type AnalyticsHandler struct {
	analyticsSvc *service.AnalyticsService
	log          *zap.Logger
}

// NewAnalyticsHandler constructs an AnalyticsHandler.
func NewAnalyticsHandler(analyticsSvc *service.AnalyticsService, log *zap.Logger) *AnalyticsHandler {
	return &AnalyticsHandler{analyticsSvc: analyticsSvc, log: log}
}

// HandleGetOperationsOverview serves GET /api/v1/admin/analytics/overview
func (h *AnalyticsHandler) HandleGetOperationsOverview(w http.ResponseWriter, r *http.Request) {
	windowHours := 24
	if hStr := r.URL.Query().Get("hours"); hStr != "" {
		if val, err := strconv.Atoi(hStr); err == nil && val > 0 && val <= 720 {
			windowHours = val
		}
	}

	since := time.Now().UTC().Add(-time.Duration(windowHours) * time.Hour)
	overview, err := h.analyticsSvc.GetOperationsOverview(r.Context(), since)
	if err != nil {
		h.log.Error("analytics handler: failed to compile operations overview", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, errorResponse("failed to compute analytics overview"))
		return
	}

	writeJSON(w, http.StatusOK, overview)
}

// HandleGetRiskSignals serves GET /api/v1/admin/analytics/risk
func (h *AnalyticsHandler) HandleGetRiskSignals(w http.ResponseWriter, r *http.Request) {
	windowMinutes := 60
	if mStr := r.URL.Query().Get("minutes"); mStr != "" {
		if val, err := strconv.Atoi(mStr); err == nil && val > 0 && val <= 1440 {
			windowMinutes = val
		}
	}

	window := time.Duration(windowMinutes) * time.Minute
	signals, err := h.analyticsSvc.GetRiskSignals(r.Context(), window)
	if err != nil {
		h.log.Error("analytics handler: failed to evaluate risk signals", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, errorResponse("failed to evaluate risk signals"))
		return
	}

	writeJSON(w, http.StatusOK, signals)
}
