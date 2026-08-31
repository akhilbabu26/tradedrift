// Package health provides the /healthz and /readyz HTTP endpoints.
package health

import (
	"encoding/json"
	"net/http"
	"time"
)

// HealthChecker is the interface for components that provide health status.
type HealthChecker interface {
	State() string
	IsReady() bool
	InventoryLastRefresh() time.Time
	MaxBalanceStaleness() time.Duration
}

// Server provides /healthz (liveness) and /readyz (readiness) endpoints.
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
	if state == "STOPPED" {
		http.Error(w, "stopped", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (s *Server) handleReadiness(w http.ResponseWriter, r *http.Request) {
	state := s.checker.State()
	if state == "PAUSED" || state == "SYNCING" || state == "STARTING" {
		http.Error(w, state, http.StatusServiceUnavailable)
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

	resp := map[string]interface{}{
		"state":                  s.checker.State(),
		"ready":                  s.checker.IsReady(),
		"inventory_last_refresh": lastRefresh.Format(time.RFC3339),
		"inventory_stale":        time.Since(lastRefresh) > stale,
		"uptime_s":               time.Since(startTime).Seconds(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

var startTime = time.Now()
