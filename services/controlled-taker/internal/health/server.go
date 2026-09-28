// Package health provides HTTP /healthz and /readyz endpoints.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"go.uber.org/zap"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
	"tradedrift/services/controlled-taker/internal/config"
)

// OrderPinger defines the readiness check contract for Order Service.
type OrderPinger interface {
	Ping(ctx context.Context) error
}

// RedisDepthPinger defines the readiness check contract for Redis depth reader.
type RedisDepthPinger interface {
	Ping(ctx context.Context) error
	GetDepth(ctx context.Context, marketID string) (*redisdepth.DepthSnapshot, error)
}

// Server serves health and readiness probes.
type Server struct {
	httpServer  *http.Server
	redisReader RedisDepthPinger
	orderClient OrderPinger
	markets     []config.MarketConfig
	logger      *zap.Logger
}

// NewServer creates a new health probe server.
func NewServer(port string, redisReader RedisDepthPinger, orderClient OrderPinger, markets []config.MarketConfig, logger *zap.Logger) *Server {
	s := &Server{
		redisReader: redisReader,
		orderClient: orderClient,
		markets:     markets,
		logger:      logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)

	s.httpServer = &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	return s
}

// Start starts listening in a background goroutine.
func (s *Server) Start() {
	go func() {
		s.logger.Info("Health server listening", zap.String("addr", s.httpServer.Addr))
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("Health server error", zap.Error(err))
		}
	}()
}

// Stop gracefully shuts down the health server.
func (s *Server) Stop(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	// 1. Check Redis Connectivity
	if s.redisReader == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ready":  false,
			"reason": "redis reader is not initialized",
		})
		return
	}
	if err := s.redisReader.Ping(ctx); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ready":  false,
			"reason": "redis unreachable: " + err.Error(),
		})
		return
	}

	// 2. Check Order Service Connectivity
	if s.orderClient == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ready":  false,
			"reason": "order service client is not initialized",
		})
		return
	}
	if err := s.orderClient.Ping(ctx); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ready":  false,
			"reason": "order service unreachable: " + err.Error(),
		})
		return
	}

	// 3. Check order book depth across all configured markets
	marketStatus := make(map[string]bool, len(s.markets))
	anyMarketReady := false

	for _, m := range s.markets {
		depth, err := s.redisReader.GetDepth(ctx, m.MarketID)
		isMarketReady := false
		if err == nil && depth != nil && len(depth.Bids) > 0 && len(depth.Asks) > 0 {
			if !depth.SnapshotAt.IsZero() && time.Since(depth.SnapshotAt) <= 5*time.Second {
				isMarketReady = true
				anyMarketReady = true
			}
		}
		marketStatus[m.MarketID] = isMarketReady
	}

	if !anyMarketReady {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ready":   false,
			"markets": marketStatus,
			"reason":  "no configured market has active and fresh (<5s) order book depth",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ready":   true,
		"markets": marketStatus,
	})
}
