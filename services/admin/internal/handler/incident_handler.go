package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/repository"
	"tradedrift/services/admin/internal/service"
)

// IncidentHandler serves HTTP endpoints for platform incident inspection, resolution, and correlation.
type IncidentHandler struct {
	incidentSvc *service.IncidentService
	log         *zap.Logger
}

// NewIncidentHandler constructs a new IncidentHandler.
func NewIncidentHandler(incidentSvc *service.IncidentService, log *zap.Logger) *IncidentHandler {
	return &IncidentHandler{incidentSvc: incidentSvc, log: log}
}

// HandleListIncidents serves GET /api/v1/admin/incidents
func (h *IncidentHandler) HandleListIncidents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := repository.IncidentFilter{
		ServiceName: q.Get("service"),
		Status:      domain.IncidentStatus(q.Get("status")),
		Severity:    domain.IncidentSeverity(q.Get("severity")),
	}

	if limitStr := q.Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil {
			filter.Limit = l
		}
	}
	if offsetStr := q.Get("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil {
			filter.Offset = o
		}
	}
	if sinceStr := q.Get("since"); sinceStr != "" {
		if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			filter.Since = &t
		}
	}

	incidents, err := h.incidentSvc.ListIncidents(r.Context(), filter)
	if err != nil {
		h.log.Error("incident handler: failed to list incidents", zap.Error(err))
		writeJSON(w, http.StatusInternalServerError, errorResponse("failed to query incidents"))
		return
	}

	if incidents == nil {
		incidents = []*domain.Incident{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"incidents": incidents,
		"count":     len(incidents),
	})
}

// HandleGetIncident serves GET /api/v1/admin/incidents/{incident_id}
func (h *IncidentHandler) HandleGetIncident(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("incident_id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse("incident_id is required"))
		return
	}

	inc, err := h.incidentSvc.GetIncidentByID(r.Context(), id)
	if err != nil {
		HandleServiceError(w, err, h.log)
		return
	}

	writeJSON(w, http.StatusOK, inc)
}

// HandleGetCorrelatedIncident serves GET /api/v1/admin/incidents/{incident_id}/correlated
func (h *IncidentHandler) HandleGetCorrelatedIncident(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("incident_id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse("incident_id is required", "INVALID_ARGUMENT"))
		return
	}

	windowBefore := 15 * time.Minute
	windowAfter := 15 * time.Minute

	if winStr := r.URL.Query().Get("window_minutes"); winStr != "" {
		if m, err := strconv.Atoi(winStr); err == nil && m > 0 {
			windowBefore = time.Duration(m) * time.Minute
			windowAfter = time.Duration(m) * time.Minute
		}
	}

	correlated, err := h.incidentSvc.GetCorrelatedIncident(r.Context(), id, windowBefore, windowAfter)
	if err != nil {
		HandleServiceError(w, err, h.log)
		return
	}

	writeJSON(w, http.StatusOK, correlated)
}

// ResolveIncidentRequest carries operator resolution input.
type ResolveIncidentRequest struct {
	RootCause string `json:"root_cause"`
}

// HandleResolveIncident serves POST /api/v1/admin/incidents/{incident_id}/resolve
func (h *IncidentHandler) HandleResolveIncident(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("incident_id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse("incident_id is required", "INVALID_ARGUMENT"))
		return
	}

	var body ResolveIncidentRequest
	if r.Body != nil && r.Body != http.NoBody {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, errorResponse("malformed json request body", "INVALID_ARGUMENT"))
			return
		}
	}

	err := h.incidentSvc.ResolveIncident(r.Context(), id, body.RootCause)
	if err != nil {
		HandleServiceError(w, err, h.log)
		return
	}

	inc, _ := h.incidentSvc.GetIncidentByID(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{
		"message":  "incident resolved successfully",
		"incident": inc,
	})
}

// HandleGetIncidentStats serves GET /api/v1/admin/incidents/stats
func (h *IncidentHandler) HandleGetIncidentStats(w http.ResponseWriter, r *http.Request) {
	since := time.Now().UTC().Add(-30 * 24 * time.Hour) // default: past 30 days
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			since = t
		}
	}

	stats, err := h.incidentSvc.GetIncidentStats(r.Context(), since)
	if err != nil {
		HandleServiceError(w, err, h.log)
		return
	}

	writeJSON(w, http.StatusOK, stats)
}
