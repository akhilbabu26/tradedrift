package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	statusRpc "google.golang.org/grpc/status"

	"tradedrift/services/admin/internal/client"
	"tradedrift/services/admin/internal/metrics"
	"tradedrift/services/admin/internal/repository"
)

// HealthWorkerConfig holds endpoints for autonomous background health checking.
type HealthWorkerConfig struct {
	AuthGRPCAddr   string
	WalletGRPCAddr string
	TradeHealthURL string
	PortHealthURL  string
	LiqHealthURL   string
	NotifHealthURL string
	KafkaBrokers   []string
}

// ServiceStatusReport describes the operational state of a single platform component.
type ServiceStatusReport struct {
	Name       string        `json:"name"`
	Status     string        `json:"status"` // "UP", "DEGRADED", "DOWN", "TIMEOUT", "UNKNOWN"
	LatencyMs  int64         `json:"latency_ms"`
	Latency    time.Duration `json:"-"`
	HTTPStatus int           `json:"http_status,omitempty"`
	Error      string        `json:"error,omitempty"`
}

// AdminHealthReport summarizes Admin service infrastructure components.
type AdminHealthReport struct {
	Status   string                         `json:"status"` // "UP", "DEGRADED", "DOWN"
	Postgres ServiceStatusReport            `json:"postgres"`
	Kafka    ServiceStatusReport            `json:"kafka"`
	Outbox   *repository.OutboxBacklogStats `json:"outbox,omitempty"`
	Saga     *repository.SagaQueueStats     `json:"saga,omitempty"`
}

// SystemHealthResponse is the comprehensive diagnostic view of the TradeDrift platform.
type SystemHealthResponse struct {
	OverallStatus string                         `json:"overall_status"` // "HEALTHY", "DEGRADED", "UNHEALTHY"
	Timestamp     string                         `json:"timestamp"`
	Services      map[string]ServiceStatusReport `json:"services"`
	Admin         AdminHealthReport              `json:"admin"`
}

// DBPinger defines the interface required to verify database connectivity.
type DBPinger interface {
	Ping(ctx context.Context) error
}

// HealthWorker autonomously monitors downstream services and internal admin health,
// updating Prometheus telemetry metrics on an active background schedule.
// Lifecycle contract: Start must be called at most once. Stop must be called after Start.
// Workers cannot be restarted after Stop.
type HealthWorker struct {
	dbPool     DBPinger
	authCli    *client.AuthClient
	walletCli  *client.WalletClient
	outboxRepo repository.OutboxRepository
	sagaRepo   repository.SagaRepository
	cfg        HealthWorkerConfig
	log        *zap.Logger
	httpClient *http.Client
	interval   time.Duration

	done         chan struct{}
	workerCancel context.CancelFunc
	wg           sync.WaitGroup
	startOnce    sync.Once
	stopOnce     sync.Once
	probeMu      sync.Mutex

	mu           sync.RWMutex
	latestHealth *SystemHealthResponse
}

// NewHealthWorker creates a new autonomous background health monitor.
func NewHealthWorker(
	dbPool DBPinger,
	authCli *client.AuthClient,
	walletCli *client.WalletClient,
	outboxRepo repository.OutboxRepository,
	sagaRepo repository.SagaRepository,
	cfg HealthWorkerConfig,
	log *zap.Logger,
	interval time.Duration,
) *HealthWorker {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	return &HealthWorker{
		dbPool:     dbPool,
		authCli:    authCli,
		walletCli:  walletCli,
		outboxRepo: outboxRepo,
		sagaRepo:   sagaRepo,
		cfg:        cfg,
		log:        log,
		httpClient: &http.Client{Timeout: 1500 * time.Millisecond},
		interval:   interval,
		done:       make(chan struct{}),
	}
}

// Start launches the background health probe ticker. It is idempotent.
func (w *HealthWorker) Start(ctx context.Context) {
	w.startOnce.Do(func() {
		workerCtx, cancel := context.WithCancel(ctx)
		w.workerCancel = cancel
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()

			// Run immediate first probe upon startup
			probeCtx, probeCancel := context.WithTimeout(workerCtx, 5*time.Second)
			w.RunProbe(probeCtx)
			probeCancel()

			ticker := time.NewTicker(w.interval)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					tickCtx, tickCancel := context.WithTimeout(workerCtx, 5*time.Second)
					w.RunProbe(tickCtx)
					tickCancel()
				case <-workerCtx.Done():
					return
				case <-w.done:
					return
				}
			}
		}()
	})
}

// Stop signals the health worker to terminate and awaits loop completion. It is idempotent.
func (w *HealthWorker) Stop() {
	w.stopOnce.Do(func() {
		close(w.done)
		if w.workerCancel != nil {
			w.workerCancel()
		}
	})
	w.wg.Wait()
}

// GetLatestHealth returns a safe defensive copy of the most recently cached diagnostic health report.
func (w *HealthWorker) GetLatestHealth() *SystemHealthResponse {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if w.latestHealth == nil {
		return nil
	}
	respCopy := *w.latestHealth
	respCopy.Services = make(map[string]ServiceStatusReport, len(w.latestHealth.Services))
	for k, v := range w.latestHealth.Services {
		respCopy.Services[k] = v
	}
	return &respCopy
}

// RunProbe executes concurrent health checks across all systems and updates metrics.
// Invariant: Probes must not overlap. Execution is strictly serialized via probeMu.
func (w *HealthWorker) RunProbe(ctx context.Context) *SystemHealthResponse {
	w.probeMu.Lock()
	defer w.probeMu.Unlock()

	runTime := time.Now().UTC()
	var probeWg sync.WaitGroup
	var mu sync.Mutex

	services := make(map[string]ServiceStatusReport)
	adminComponents := make(map[string]ServiceStatusReport)

	addServiceReport := func(name string, rep ServiceStatusReport) {
		mu.Lock()
		defer mu.Unlock()
		services[name] = rep
	}

	addAdminReport := func(name string, rep ServiceStatusReport) {
		mu.Lock()
		defer mu.Unlock()
		adminComponents[name] = rep
	}

	// 1. HTTP Services
	httpTargets := []struct {
		name string
		url  string
	}{
		{metrics.ServiceTrade, w.cfg.TradeHealthURL},
		{metrics.ServicePortfolio, w.cfg.PortHealthURL},
		{metrics.ServiceLiquidityEngine, w.cfg.LiqHealthURL},
		{metrics.ServiceNotification, w.cfg.NotifHealthURL},
	}

	for _, t := range httpTargets {
		if t.url == "" {
			addServiceReport(t.name, ServiceStatusReport{
				Name:   t.name,
				Status: "UNKNOWN",
				Error:  "health URL not configured",
			})
			continue
		}
		probeWg.Add(1)
		go func(name, url string) {
			defer probeWg.Done()
			rep := w.probeHTTPService(ctx, name, url)
			addServiceReport(name, rep)
		}(t.name, t.url)
	}

	// 2. Auth gRPC Transport Check (Transport & connection liveness probe)
	if w.authCli != nil {
		probeWg.Add(1)
		go func() {
			defer probeWg.Done()
			start := time.Now()
			status := metrics.StatusUP
			var errStr string
			if err := w.authCli.Ping(ctx); err != nil {
				if statusRpc.Code(err) == codes.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
					status = metrics.StatusTimeout
				} else {
					status = metrics.StatusDown
				}
				errStr = err.Error()
			}
			elapsed := time.Since(start)
			addServiceReport(metrics.ServiceAuth, ServiceStatusReport{
				Name:      metrics.ServiceAuth,
				Status:    status,
				Latency:   elapsed,
				LatencyMs: elapsed.Milliseconds(),
				Error:     errStr,
			})
		}()
	} else if w.cfg.AuthGRPCAddr != "" {
		addServiceReport(metrics.ServiceAuth, ServiceStatusReport{
			Name:   metrics.ServiceAuth,
			Status: "DOWN",
			Error:  "auth gRPC client not initialized",
		})
	} else {
		addServiceReport(metrics.ServiceAuth, ServiceStatusReport{
			Name:   metrics.ServiceAuth,
			Status: "UNKNOWN",
			Error:  "auth gRPC address not configured",
		})
	}

	// 3. Wallet gRPC Application Check (Application-level Health RPC probe)
	if w.walletCli != nil {
		probeWg.Add(1)
		go func() {
			defer probeWg.Done()
			start := time.Now()
			status := metrics.StatusUP
			var errStr string
			if err := w.walletCli.Ping(ctx); err != nil {
				if statusRpc.Code(err) == codes.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
					status = metrics.StatusTimeout
				} else {
					status = metrics.StatusDown
				}
				errStr = err.Error()
			}
			elapsed := time.Since(start)
			addServiceReport(metrics.ServiceWallet, ServiceStatusReport{
				Name:      metrics.ServiceWallet,
				Status:    status,
				Latency:   elapsed,
				LatencyMs: elapsed.Milliseconds(),
				Error:     errStr,
			})
		}()
	} else if w.cfg.WalletGRPCAddr != "" {
		addServiceReport(metrics.ServiceWallet, ServiceStatusReport{
			Name:   metrics.ServiceWallet,
			Status: "DOWN",
			Error:  "wallet gRPC client not initialized",
		})
	} else {
		addServiceReport(metrics.ServiceWallet, ServiceStatusReport{
			Name:   metrics.ServiceWallet,
			Status: "UNKNOWN",
			Error:  "wallet gRPC address not configured",
		})
	}

	// 4. PostgreSQL Probe (Mandatory Admin infrastructure)
	if w.dbPool != nil {
		probeWg.Add(1)
		go func() {
			defer probeWg.Done()
			start := time.Now()
			status := "UP"
			var errStr string
			if err := w.dbPool.Ping(ctx); err != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
					status = metrics.StatusTimeout
				} else {
					status = metrics.StatusDown
				}
				errStr = err.Error()
			}
			elapsed := time.Since(start)
			addAdminReport(metrics.ServicePostgres, ServiceStatusReport{
				Name:      metrics.ServicePostgres,
				Status:    status,
				Latency:   elapsed,
				LatencyMs: elapsed.Milliseconds(),
				Error:     errStr,
			})
		}()
	} else {
		addAdminReport(metrics.ServicePostgres, ServiceStatusReport{
			Name:   metrics.ServicePostgres,
			Status: "UNKNOWN",
			Error:  "postgres connection pool not configured",
		})
	}

	// 5. Kafka Broker Availability Probe (Considered UP if at least one configured broker is reachable)
	probeWg.Add(1)
	go func() {
		defer probeWg.Done()
		start := time.Now()
		status := "DOWN"
		var errStr string
		if len(w.cfg.KafkaBrokers) > 0 {
			for _, broker := range w.cfg.KafkaBrokers {
				brokerCtx, brokerCancel := context.WithTimeout(ctx, 1500*time.Millisecond)
				conn, err := kafka.DialContext(brokerCtx, "tcp", broker)
				brokerCancel()
				if err == nil {
					_ = conn.Close()
					status = "UP"
					errStr = ""
					break
				}
				if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(brokerCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
					status = metrics.StatusTimeout
				} else {
					status = metrics.StatusDown
				}
				errStr = err.Error()
			}
		} else {
			status = "UNKNOWN"
			errStr = "no kafka brokers configured"
		}
		elapsed := time.Since(start)
		addAdminReport(metrics.ServiceKafka, ServiceStatusReport{
			Name:      metrics.ServiceKafka,
			Status:    status,
			Latency:   elapsed,
			LatencyMs: elapsed.Milliseconds(),
			Error:     errStr,
		})
	}()

	probeWg.Wait()

	// 6. Gather Operational Backlog Stats concurrently with dedicated 1-second budget
	statsCtx, statsCancel := context.WithTimeout(ctx, 1*time.Second)
	defer statsCancel()

	var outboxStats *repository.OutboxBacklogStats
	var sagaStats *repository.SagaQueueStats
	var statsWg sync.WaitGroup

	if w.outboxRepo != nil {
		statsWg.Add(1)
		go func() {
			defer statsWg.Done()
			var err error
			outboxStats, err = w.outboxRepo.GetBacklogStats(statsCtx)
			if err != nil {
				w.log.Error("health worker: failed to fetch outbox backlog stats", zap.Error(err))
			} else if outboxStats != nil {
				metrics.OutboxBacklogDepth.Set(float64(outboxStats.PendingCount))
				metrics.OutboxOldestUnpublishedSeconds.Set(outboxStats.OldestAge.Seconds())
			}
		}()
	}

	if w.sagaRepo != nil {
		statsWg.Add(1)
		go func() {
			defer statsWg.Done()
			var err error
			sagaStats, err = w.sagaRepo.GetQueueStats(statsCtx)
			if err != nil {
				w.log.Error("health worker: failed to fetch saga queue stats", zap.Error(err))
			} else if sagaStats != nil {
				metrics.RecordSagaQueueStats(sagaStats.PendingCount, sagaStats.RetryingCount, sagaStats.ExhaustedCount)
			}
		}()
	}

	statsWg.Wait()

	// 7. Compute Admin Infrastructure Status
	postgresStatus := adminComponents[metrics.ServicePostgres].Status
	kafkaStatus := adminComponents[metrics.ServiceKafka].Status

	adminStatus := "UP"
	if postgresStatus != metrics.StatusUP || kafkaStatus != metrics.StatusUP {
		adminStatus = "DEGRADED"
	}

	// 8. Compute Overall Platform Status
	overall := "HEALTHY"
	for name, rep := range services {
		if rep.Status == "DOWN" || rep.Status == "TIMEOUT" {
			if name == metrics.ServiceTrade || name == metrics.ServicePortfolio || name == metrics.ServiceLiquidityEngine || name == metrics.ServiceAuth || name == metrics.ServiceWallet {
				overall = "UNHEALTHY"
			} else if overall == "HEALTHY" {
				overall = "DEGRADED"
			}
		} else if (rep.Status == "DEGRADED" || rep.Status == "UNKNOWN") && overall == "HEALTHY" {
			overall = "DEGRADED"
		}
	}
	// Postgres DOWN, UNKNOWN, or TIMEOUT marks platform UNHEALTHY
	if postgresStatus != metrics.StatusUP {
		overall = "UNHEALTHY"
	}
	// Kafka DOWN, UNKNOWN, or TIMEOUT degrades platform operations
	if kafkaStatus != metrics.StatusUP {
		if overall == "HEALTHY" {
			overall = "DEGRADED"
		}
	}

	// 9. Record Prometheus Telemetry
	// Update 1-hot gauges, latencies, and last run timestamp for all services
	for name, rep := range services {
		metrics.RecordHealthProbe(name, rep.Status, rep.Latency, runTime)
		if rep.Status != "UP" {
			metrics.RecordHealthFailure(name, classifyFailureReason(rep))
		}
	}
	for name, rep := range adminComponents {
		metrics.RecordHealthProbe(name, rep.Status, rep.Latency, runTime)
		if rep.Status != "UP" {
			metrics.RecordHealthFailure(name, classifyFailureReason(rep))
		}
	}
	metrics.SetSystemOverallStatus(overall)

	resp := &SystemHealthResponse{
		OverallStatus: overall,
		Timestamp:     runTime.Format(time.RFC3339),
		Services:      services,
		Admin: AdminHealthReport{
			Status:   adminStatus,
			Postgres: adminComponents[metrics.ServicePostgres],
			Kafka:    adminComponents[metrics.ServiceKafka],
			Outbox:   outboxStats,
			Saga:     sagaStats,
		},
	}

	w.mu.Lock()
	w.latestHealth = resp
	w.mu.Unlock()

	return resp
}

func (w *HealthWorker) probeHTTPService(ctx context.Context, name, url string) ServiceStatusReport {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ServiceStatusReport{Name: name, Status: "DOWN", Error: err.Error()}
	}

	resp, err := w.httpClient.Do(req)
	elapsed := time.Since(start)
	durationMs := elapsed.Milliseconds()
	if err != nil {
		if ctx.Err() != nil {
			return ServiceStatusReport{Name: name, Status: "TIMEOUT", Latency: elapsed, LatencyMs: durationMs, Error: "request timed out"}
		}
		return ServiceStatusReport{Name: name, Status: "DOWN", Latency: elapsed, LatencyMs: durationMs, Error: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return ServiceStatusReport{Name: name, Status: "UP", Latency: elapsed, LatencyMs: durationMs, HTTPStatus: resp.StatusCode}
	}

	if resp.StatusCode >= 500 {
		return ServiceStatusReport{
			Name:       name,
			Status:     "DOWN",
			Latency:    elapsed,
			LatencyMs:  durationMs,
			HTTPStatus: resp.StatusCode,
			Error:      fmt.Sprintf("service reported outage (HTTP %d)", resp.StatusCode),
		}
	}

	return ServiceStatusReport{
		Name:       name,
		Status:     "DEGRADED",
		Latency:    elapsed,
		LatencyMs:  durationMs,
		HTTPStatus: resp.StatusCode,
		Error:      fmt.Sprintf("service reported degradation (HTTP %d)", resp.StatusCode),
	}
}

func classifyFailureReason(rep ServiceStatusReport) string {
	if rep.Status == "TIMEOUT" || strings.Contains(strings.ToLower(rep.Error), "timeout") {
		return "timeout"
	}
	if rep.HTTPStatus == http.StatusServiceUnavailable {
		return "http_503"
	}
	if rep.HTTPStatus >= 500 {
		return "http_5xx"
	}
	if strings.Contains(strings.ToLower(rep.Error), "connection refused") ||
		strings.Contains(strings.ToLower(rep.Error), "no such host") ||
		strings.Contains(strings.ToLower(rep.Error), "dial") {
		return "connection_error"
	}
	if strings.Contains(strings.ToLower(rep.Error), "grpc") {
		return "grpc_error"
	}
	return "unknown"
}
