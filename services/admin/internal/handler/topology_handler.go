package handler

import (
	"net/http"

	"go.uber.org/zap"

	"tradedrift/services/admin/internal/service"
)

// TopologyHandler serves the platform dependency graph with dynamic health overlays.
type TopologyHandler struct {
	topologyEng *service.TopologyEngine
	log         *zap.Logger
}

// NewTopologyHandler constructs a TopologyHandler.
func NewTopologyHandler(topologyEng *service.TopologyEngine, log *zap.Logger) *TopologyHandler {
	return &TopologyHandler{topologyEng: topologyEng, log: log}
}

// HandleGetTopology serves GET /api/v1/admin/system/topology
func (h *TopologyHandler) HandleGetTopology(w http.ResponseWriter, r *http.Request) {
	if h.topologyEng == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse("topology engine not initialized"))
		return
	}

	topology := h.topologyEng.GetTopology(r.Context())
	writeJSON(w, http.StatusOK, topology)
}
