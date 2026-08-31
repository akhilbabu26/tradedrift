// Package health provides the /healthz, /readyz, and /status HTTP endpoints.
package health

import (
	"encoding/json"
	"net/http"
	"time"

	"tradedrift/services/liquidity-engine/internal/engine"
)

// HealthChecker is the interface for components that provide health status.
type HealthChecker interface {
	State() engine.EngineState
	IsReady() bool
	MarketStates() map[string]string
	InventoryLastRefresh() time.Time
	MaxBalanceStaleness() time.Duration
}

// Server provides /healthz (liveness), /readyz (readiness), and /status endpoints.
type Server struct {
	checker HealthChecker
}

// New creates a health server using the given checker.
func New(checker HealthChecker) *Server {
	return &Server{checker: checker}
}

// Handler returns an http.ServeMux for the health endpoints.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleLiveness)
	mux.HandleFunc("/readyz", s.handleReadiness)
	mux.HandleFunc("/status", s.handleStatus)
	return mux
}

func (s *Server) handleLiveness(w http.ResponseWriter, r *http.Request) {
	state := s.checker.State()
	if state == engine.StateStopped {
		http.Error(w, "stopped", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	state := s.checker.State()
	if state == engine.StatePaused || state == engine.StateSyncing || state == engine.StateStarting || state == engine.StateStopped {
		http.Error(w, state.String(), http.StatusServiceUnavailable)
		return
	}

	if !s.checker.IsReady() {
		http.Error(w, "insufficient resting orders", http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ready"))
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	lastRefresh := s.checker.InventoryLastRefresh()
	stale := s.checker.MaxBalanceStaleness()
	isStale := lastRefresh.IsZero() || time.Since(lastRefresh) > stale

	resp := map[string]interface{}{
		"state":                  s.checker.State().String(),
		"ready":                  s.checker.IsReady(),
		"markets":                s.checker.MarketStates(),
		"inventory_last_refresh": lastRefresh.Format(time.RFC3339),
		"inventory_stale":        isStale,
		"uptime_s":               time.Since(startTime).Seconds(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

var startTime = time.Now()
