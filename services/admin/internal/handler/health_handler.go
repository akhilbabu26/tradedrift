package handler

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"tradedrift/services/admin/internal/client"
	"tradedrift/services/admin/internal/repository"
)

// HealthConfig holds endpoint configurations for system health monitoring.
type HealthConfig struct {
	AuthGRPCAddr    string
	WalletGRPCAddr  string
	TradeHealthURL  string
	PortHealthURL   string
	LiqHealthURL    string
	NotifHealthURL  string
	KafkaBrokers    []string
}

// HealthHandler serves /health, /ready, and /api/v1/admin/system/health.
type HealthHandler struct {
	dbPool         *pgxpool.Pool
	authCli        *client.AuthClient
	walletCli      *client.WalletClient
	outboxRepo     repository.OutboxRepository
	sagaRepo       repository.SagaRepository
	cfg            HealthConfig
	log            *zap.Logger
	httpClient     *http.Client
	isShuttingDown atomic.Bool
}

// NewHealthHandler constructs the health handler.
func NewHealthHandler(
	dbPool *pgxpool.Pool,
	authCli *client.AuthClient,
	walletCli *client.WalletClient,
	outboxRepo repository.OutboxRepository,
	sagaRepo repository.SagaRepository,
	cfg HealthConfig,
	log *zap.Logger,
) *HealthHandler {
	return &HealthHandler{
		dbPool:     dbPool,
		authCli:    authCli,
		walletCli:  walletCli,
		outboxRepo: outboxRepo,
		sagaRepo:   sagaRepo,
		cfg:        cfg,
		log:        log,
		httpClient: &http.Client{Timeout: 1500 * time.Millisecond},
	}
}

// SetShuttingDown marks the service as transitioning to offline (for /ready -> 503).
func (h *HealthHandler) SetShuttingDown() {
	h.isShuttingDown.Store(true)
}

// HandleLiveness returns 200 OK as long as the process is alive.
func (h *HealthHandler) HandleLiveness(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleReadiness checks Admin Service's direct dependencies (PostgreSQL, Kafka, Auth, Wallet).
// Returns 200 if ready to serve, 503 if any required dependency is unavailable or shutting down.
func (h *HealthHandler) HandleReadiness(w http.ResponseWriter, r *http.Request) {
	if h.isShuttingDown.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status": "not_ready",
			"reason": "shutting_down",
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	checks := make(map[string]string)
	allReady := true

	// 1. PostgreSQL check
	if h.dbPool == nil {
		checks["postgres"] = "unavailable"
		allReady = false
	} else if err := h.dbPool.Ping(ctx); err != nil {
		checks["postgres"] = "unavailable"
		allReady = false
	} else {
		checks["postgres"] = "ok"
	}

	// 2. Kafka check
	if len(h.cfg.KafkaBrokers) > 0 {
		conn, err := kafka.DialContext(ctx, "tcp", h.cfg.KafkaBrokers[0])
		if err != nil {
			checks["kafka"] = "unavailable"
			allReady = false
		} else {
			_ = conn.Close()
			checks["kafka"] = "ok"
		}
	} else {
		checks["kafka"] = "ok"
	}

	// 3. Auth gRPC client check
	if h.authCli == nil {
		checks["auth"] = "unavailable"
		allReady = false
	} else if err := h.authCli.Ping(ctx); err != nil {
		checks["auth"] = "unavailable"
		allReady = false
	} else {
		checks["auth"] = "ok"
	}

	// 4. Wallet gRPC client check
	if h.walletCli == nil {
		checks["wallet"] = "unavailable"
		allReady = false
	} else if err := h.walletCli.Ping(ctx); err != nil {
		checks["wallet"] = "unavailable"
		allReady = false
	} else {
		checks["wallet"] = "ok"
	}

	status := "ready"
	httpCode := http.StatusOK
	if !allReady {
		status = "not_ready"
		httpCode = http.StatusServiceUnavailable
	}

	writeJSON(w, httpCode, map[string]interface{}{
		"status": status,
		"checks": checks,
	})
}

// ServiceStatusReport is the health status of a single TradeDrift subsystem.
type ServiceStatusReport struct {
	Name        string `json:"name"`
	Status      string `json:"status"` // "UP", "DEGRADED", "DOWN", "TIMEOUT"
	LatencyMs   int64  `json:"latency_ms"`
	HTTPStatus  int    `json:"http_status,omitempty"`
	Error       string `json:"error,omitempty"`
}

// SystemHealthResponse is the comprehensive diagnostic view of the whole TradeDrift platform.
type SystemHealthResponse struct {
	OverallStatus string                          `json:"overall_status"` // "HEALTHY", "DEGRADED", "UNHEALTHY"
	Timestamp     string                          `json:"timestamp"`
	Services      map[string]ServiceStatusReport `json:"services"`
	AdminInternal map[string]interface{}          `json:"admin_internal"`
}

// HandleSystemHealth aggregates the health of all TradeDrift services concurrently with individual timeouts.
func (h *HealthHandler) HandleSystemHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var mu sync.Mutex
	services := make(map[string]ServiceStatusReport)

	addReport := func(name string, rep ServiceStatusReport) {
		mu.Lock()
		defer mu.Unlock()
		services[name] = rep
	}

	// Target endpoints to probe concurrently
	probes := []struct {
		name string
		url  string
	}{
		{"trade", h.cfg.TradeHealthURL},
		{"portfolio", h.cfg.PortHealthURL},
		{"liquidity_engine", h.cfg.LiqHealthURL},
		{"notification", h.cfg.NotifHealthURL},
	}

	// 1. Concurrently probe HTTP-based services
	for _, p := range probes {
		if p.url == "" {
			continue
		}
		wg.Add(1)
		go func(name, url string) {
			defer wg.Done()
			rep := h.probeHTTPService(ctx, name, url)
			addReport(name, rep)
		}(p.name, p.url)
	}

	// 2. Concurrently probe gRPC services
	if h.authCli != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			status := "UP"
			var errStr string
			if err := h.authCli.Ping(ctx); err != nil {
				status = "DOWN"
				errStr = err.Error()
			}
			addReport("auth", ServiceStatusReport{
				Name:      "auth",
				Status:    status,
				LatencyMs: time.Since(start).Milliseconds(),
				Error:     errStr,
			})
		}()
	}

	if h.walletCli != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			status := "UP"
			var errStr string
			if err := h.walletCli.Ping(ctx); err != nil {
				status = "DOWN"
				errStr = err.Error()
			}
			addReport("wallet", ServiceStatusReport{
				Name:      "wallet",
				Status:    status,
				LatencyMs: time.Since(start).Milliseconds(),
				Error:     errStr,
			})
		}()
	}

	// 3. Concurrently probe Postgres
	if h.dbPool != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			status := "UP"
			var errStr string
			if err := h.dbPool.Ping(ctx); err != nil {
				status = "DOWN"
				errStr = err.Error()
			}
			addReport("postgres", ServiceStatusReport{
				Name:      "postgres",
				Status:    status,
				LatencyMs: time.Since(start).Milliseconds(),
				Error:     errStr,
			})
		}()
	}

	// 4. Concurrently probe Kafka
	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()
		status := "UP"
		var errStr string
		if len(h.cfg.KafkaBrokers) > 0 {
			conn, err := kafka.DialContext(ctx, "tcp", h.cfg.KafkaBrokers[0])
			if err != nil {
				status = "DOWN"
				errStr = err.Error()
			} else {
				_ = conn.Close()
			}
		}
		addReport("kafka", ServiceStatusReport{
			Name:      "kafka",
			Status:    status,
			LatencyMs: time.Since(start).Milliseconds(),
			Error:     errStr,
		})
	}()

	wg.Wait()

	// 5. Gather internal admin operational metrics
	outboxStats, _ := h.outboxRepo.GetBacklogStats(ctx)
	sagaStats, _ := h.sagaRepo.GetQueueStats(ctx)

	// 6. Compute overall aggregated status
	overall := "HEALTHY"
	downCount := 0
	for _, rep := range services {
		if rep.Status == "DOWN" || rep.Status == "TIMEOUT" {
			downCount++
		} else if rep.Status == "DEGRADED" && overall == "HEALTHY" {
			overall = "DEGRADED"
		}
	}
	if downCount > 0 {
		if services["postgres"].Status == "DOWN" || services["auth"].Status == "DOWN" || services["wallet"].Status == "DOWN" {
			overall = "UNHEALTHY"
		} else {
			overall = "DEGRADED"
		}
	}

	resp := SystemHealthResponse{
		OverallStatus: overall,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		Services:      services,
		AdminInternal: map[string]interface{}{
			"outbox": outboxStats,
			"saga":   sagaStats,
		},
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *HealthHandler) probeHTTPService(ctx context.Context, name, url string) ServiceStatusReport {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ServiceStatusReport{Name: name, Status: "DOWN", Error: err.Error()}
	}

	resp, err := h.httpClient.Do(req)
	duration := time.Since(start).Milliseconds()
	if err != nil {
		if ctx.Err() != nil {
			return ServiceStatusReport{Name: name, Status: "TIMEOUT", LatencyMs: duration, Error: "request timed out"}
		}
		return ServiceStatusReport{Name: name, Status: "DOWN", LatencyMs: duration, Error: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return ServiceStatusReport{Name: name, Status: "UP", LatencyMs: duration, HTTPStatus: resp.StatusCode}
	}
	return ServiceStatusReport{Name: name, Status: "DEGRADED", LatencyMs: duration, HTTPStatus: resp.StatusCode}
}
