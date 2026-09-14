package service

import (
	"context"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

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
	dbPool       DBPinger
	authCli      *client.AuthClient
	walletCli    *client.WalletClient
	outboxRepo   repository.OutboxRepository
	sagaRepo     repository.SagaRepository
	incidentRepo repository.IncidentRepository
	cfg          HealthWorkerConfig
	log          *zap.Logger
	httpClient   *http.Client
	interval     time.Duration

	done         chan struct{}
	workerCancel context.CancelFunc
	wg           sync.WaitGroup
	startOnce    sync.Once
	stopOnce     sync.Once
	probeMu      sync.Mutex

	prevStatus    map[string]string
	degradedCount map[string]int
	mu            sync.RWMutex
	latestHealth  *SystemHealthResponse
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
		dbPool:        dbPool,
		authCli:       authCli,
		walletCli:     walletCli,
		outboxRepo:    outboxRepo,
		sagaRepo:      sagaRepo,
		cfg:           cfg,
		log:           log,
		httpClient:    &http.Client{Timeout: 1500 * time.Millisecond},
		interval:      interval,
		done:          make(chan struct{}),
		prevStatus:    make(map[string]string),
		degradedCount: make(map[string]int),
	}
}

// SetIncidentRepo configures the incident repository for autonomous incident tracking.
func (w *HealthWorker) SetIncidentRepo(repo repository.IncidentRepository) {
	w.incidentRepo = repo
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

	// 1. HTTP Services (trade, portfolio, liquidity, notification)
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
			addServiceReport(name, w.probeHTTPService(ctx, name, url))
		}(t.name, t.url)
	}

	// 2. Auth gRPC Transport Check
	probeWg.Add(1)
	go func() {
		defer probeWg.Done()
		w.probeAuthGRPC(ctx, addServiceReport)
	}()

	// 3. Wallet gRPC Application Health Check
	probeWg.Add(1)
	go func() {
		defer probeWg.Done()
		w.probeWalletGRPC(ctx, addServiceReport)
	}()

	// 4. PostgreSQL Probe (Mandatory Admin infrastructure)
	probeWg.Add(1)
	go func() {
		defer probeWg.Done()
		w.probePostgres(ctx, addAdminReport)
	}()

	// 5. Kafka Broker Availability Probe
	probeWg.Add(1)
	go func() {
		defer probeWg.Done()
		addAdminReport(metrics.ServiceKafka, probeKafka(ctx, w.cfg.KafkaBrokers))
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
	if kafkaStatus != metrics.StatusUP && overall == "HEALTHY" {
		overall = "DEGRADED"
	}

	// 9. Record Prometheus Telemetry
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
		Timestamp:     runTime.Format("2006-01-02T15:04:05Z07:00"),
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

	// 10. Autonomous Incident Tracking (Transition-Based & Concurrency-Safe)
	// Implementation lives in incident_transitions.go
	w.processIncidentTransitions(ctx, runTime, services, adminComponents)

	return resp
}
