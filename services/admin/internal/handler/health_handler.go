package handler

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"tradedrift/services/admin/internal/client"
	"tradedrift/services/admin/internal/repository"
	"tradedrift/services/admin/internal/service"
)

// HealthConfig holds endpoint configurations for system health monitoring.
type HealthConfig struct {
	AuthGRPCAddr   string
	WalletGRPCAddr string
	TradeHealthURL string
	PortHealthURL  string
	LiqHealthURL   string
	NotifHealthURL string
	KafkaBrokers   []string
}

// HealthHandler serves /health, /ready, and /api/v1/admin/system/health.
type HealthHandler struct {
	dbPool         *pgxpool.Pool
	authCli        *client.AuthClient
	walletCli      *client.WalletClient
	outboxRepo     repository.OutboxRepository
	sagaRepo       repository.SagaRepository
	healthWorker   *service.HealthWorker
	cfg            HealthConfig
	log            *zap.Logger
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
	}
}

// SetHealthWorker attaches the autonomous background health monitor.
func (h *HealthHandler) SetHealthWorker(hw *service.HealthWorker) {
	h.healthWorker = hw
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

	// 2. Kafka check (considered ready if at least one configured broker is reachable)
	if len(h.cfg.KafkaBrokers) > 0 {
		kafkaOK := false
		for _, broker := range h.cfg.KafkaBrokers {
			conn, err := kafka.DialContext(ctx, "tcp", broker)
			if err == nil {
				_ = conn.Close()
				kafkaOK = true
				break
			}
		}
		if kafkaOK {
			checks["kafka"] = "ok"
		} else {
			checks["kafka"] = "unavailable"
			allReady = false
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

// Type aliases ensuring full backward-compatibility while HealthWorker remains the single source of truth.
type ServiceStatusReport = service.ServiceStatusReport
type AdminHealthReport = service.AdminHealthReport
type SystemHealthResponse = service.SystemHealthResponse

// HandleSystemHealth serves the comprehensive platform diagnostic health report.
// It delegates strictly to the autonomous background HealthWorker (single source of truth).
// Note: This endpoint returns HTTP 200 on successful diagnostic delivery. Downstream component
// outages or degradations are reflected within the payload's "overall_status" and "services" fields.
func (h *HealthHandler) HandleSystemHealth(w http.ResponseWriter, r *http.Request) {
	if h.healthWorker == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "health_monitor_unavailable",
		})
		return
	}

	latest := h.healthWorker.GetLatestHealth()
	if latest == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "health_snapshot_unavailable",
		})
		return
	}

	writeJSON(w, http.StatusOK, latest)
}
