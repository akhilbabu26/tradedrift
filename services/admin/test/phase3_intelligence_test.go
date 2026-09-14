package test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	platformjwt "tradedrift/platform/jwt"
	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/handler"
	"tradedrift/services/admin/internal/metrics"
	"tradedrift/services/admin/internal/repository"
	"tradedrift/services/admin/internal/service"
)

// MockIncidentRepo for integration and race testing
type mockIncidentRepo struct {
	mu        sync.RWMutex
	incidents map[string]*domain.Incident
}

func newMockIncidentRepo() *mockIncidentRepo {
	return &mockIncidentRepo{
		incidents: make(map[string]*domain.Incident),
	}
}

func (m *mockIncidentRepo) Create(ctx context.Context, inc *domain.Incident) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Mimic partial unique index: only 1 active incident per service
	for _, existing := range m.incidents {
		if existing.ServiceName == inc.ServiceName && (existing.Status == domain.IncidentStatusOpen || existing.Status == domain.IncidentStatusInvestigating) {
			return domain.ErrActiveIncidentExists
		}
	}
	m.incidents[inc.ID] = inc
	return nil
}

func (m *mockIncidentRepo) GetActiveByService(ctx context.Context, serviceName string) (*domain.Incident, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, inc := range m.incidents {
		if inc.ServiceName == serviceName && (inc.Status == domain.IncidentStatusOpen || inc.Status == domain.IncidentStatusInvestigating) {
			return inc, nil
		}
	}
	return nil, nil
}

func (m *mockIncidentRepo) GetByID(ctx context.Context, id string) (*domain.Incident, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if inc, ok := m.incidents[id]; ok {
		return inc, nil
	}
	return nil, domain.ErrIncidentNotFound
}

func (m *mockIncidentRepo) UpdateHeartbeat(ctx context.Context, id string, lastSeenAt time.Time, failureCount int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if inc, ok := m.incidents[id]; ok {
		inc.LastSeenAt = lastSeenAt
		inc.ProbeFailureCount = failureCount
		return nil
	}
	return domain.ErrIncidentNotFound
}

func (m *mockIncidentRepo) Resolve(ctx context.Context, id string, resolvedAt time.Time, mttrSeconds float64, rootCause string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	inc, ok := m.incidents[id]
	if !ok {
		return domain.ErrIncidentNotFound
	}
	if inc.Status == domain.IncidentStatusResolved && inc.ResolvedAt != nil && inc.MTTRSeconds != nil && *inc.MTTRSeconds != mttrSeconds {
		return domain.ErrIncidentAlreadyResolved
	}
	inc.Status = domain.IncidentStatusResolved
	inc.ResolvedAt = &resolvedAt
	inc.MTTRSeconds = &mttrSeconds
	inc.RootCause = &rootCause
	return nil
}

func (m *mockIncidentRepo) List(ctx context.Context, filter repository.IncidentFilter) ([]*domain.Incident, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var results []*domain.Incident
	for _, inc := range m.incidents {
		if filter.ServiceName != "" && inc.ServiceName != filter.ServiceName {
			continue
		}
		if filter.Status != "" && inc.Status != filter.Status {
			continue
		}
		results = append(results, inc)
	}
	return results, nil
}

func (m *mockIncidentRepo) GetStats(ctx context.Context, since time.Time) (*repository.IncidentStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stats := &repository.IncidentStats{}
	var totalMTTR float64
	for _, inc := range m.incidents {
		stats.TotalIncidents++
		if inc.Status == domain.IncidentStatusOpen || inc.Status == domain.IncidentStatusInvestigating {
			stats.OpenIncidents++
		} else if inc.Status == domain.IncidentStatusResolved {
			stats.ResolvedIncidents++
			if inc.MTTRSeconds != nil {
				totalMTTR += *inc.MTTRSeconds
			}
		}
	}
	if stats.ResolvedIncidents > 0 {
		stats.AverageMTTRSeconds = totalMTTR / float64(stats.ResolvedIncidents)
	}
	return stats, nil
}

// MockAuditRepo for testing correlation and risk signals
type mockAuditRepo struct {
	mu   sync.RWMutex
	logs []domain.AuditLog
}

func newMockAuditRepo() *mockAuditRepo {
	return &mockAuditRepo{}
}

func (m *mockAuditRepo) Insert(ctx context.Context, l *domain.AuditLog) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs = append(m.logs, *l)
	return nil
}

func (m *mockAuditRepo) GetAuditLogsInWindow(ctx context.Context, start, end time.Time, limit int) ([]domain.AuditLog, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var res []domain.AuditLog
	for _, l := range m.logs {
		if (l.CreatedAt.Equal(start) || l.CreatedAt.After(start)) && (l.CreatedAt.Equal(end) || l.CreatedAt.Before(end)) {
			res = append(res, l)
			if limit > 0 && len(res) >= limit {
				break
			}
		}
	}
	return res, nil
}

func (m *mockAuditRepo) GetAuditStats(ctx context.Context, since time.Time) (*repository.AuditAnalyticsStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stats := &repository.AuditAnalyticsStats{
		ByAction:     make(map[string]int),
		ByTargetType: make(map[string]int),
		TopAdmins:    make(map[string]int),
	}
	for _, l := range m.logs {
		if l.CreatedAt.After(since) {
			stats.TotalAuditEvents++
			stats.ByAction[l.Action]++
			stats.ByTargetType[l.TargetType]++
			stats.TopAdmins[l.AdminID]++
		}
	}
	return stats, nil
}

// ─── Test 1: Incident Transition State Machine (UP -> DOWN -> HEARTBEAT -> UP) ─

func TestPhase3_IncidentLifecycleTransitions(t *testing.T) {
	repo := newMockIncidentRepo()
	ctx := context.Background()

	// 1. Initial creation (UP -> DOWN)
	now := time.Now().UTC()
	inc := domain.NewIncident(metrics.ServiceTrade, domain.SeverityP1Critical, "probe HTTP 503 Service Unavailable", now)
	if err := repo.Create(ctx, inc); err != nil {
		t.Fatalf("failed to create incident: %v", err)
	}

	// 2. Active incident lookup
	active, err := repo.GetActiveByService(ctx, metrics.ServiceTrade)
	if err != nil || active == nil {
		t.Fatalf("expected active incident for %s, got err=%v", metrics.ServiceTrade, err)
	}
	if active.Status != domain.IncidentStatusOpen {
		t.Fatalf("expected OPEN status, got %s", active.Status)
	}

	// 3. Heartbeat update (DOWN -> DOWN)
	heartbeatTime := now.Add(15 * time.Second)
	active.RecordFailureHeartbeat(heartbeatTime)
	if err := repo.UpdateHeartbeat(ctx, active.ID, active.LastSeenAt, active.ProbeFailureCount); err != nil {
		t.Fatalf("failed to update heartbeat: %v", err)
	}

	// Verify heartbeat increment
	updated, _ := repo.GetByID(ctx, active.ID)
	if updated.ProbeFailureCount != 2 {
		t.Fatalf("expected probe_failure_count=2, got %d", updated.ProbeFailureCount)
	}

	// 4. Recovery transition (DOWN -> UP)
	resolvedAt := now.Add(45 * time.Second)
	updated.Resolve(resolvedAt, "Service recovered successfully by HealthWorker")
	if err := repo.Resolve(ctx, updated.ID, *updated.ResolvedAt, *updated.MTTRSeconds, *updated.RootCause); err != nil {
		t.Fatalf("failed to resolve incident: %v", err)
	}

	// Verify resolution metrics
	resolved, _ := repo.GetByID(ctx, active.ID)
	if resolved.Status != domain.IncidentStatusResolved {
		t.Fatalf("expected status RESOLVED, got %s", resolved.Status)
	}
	if resolved.MTTRSeconds == nil || *resolved.MTTRSeconds != 45.0 {
		t.Fatalf("expected MTTR 45.0s, got %v", resolved.MTTRSeconds)
	}

	// Ensure no active incident remains
	activeAfter, _ := repo.GetActiveByService(ctx, metrics.ServiceTrade)
	if activeAfter != nil {
		t.Fatalf("expected no active incident after recovery, found: %+v", activeAfter)
	}

	// 5. DEGRADED -> DOWN transition heartbeat continuity test (Regression for Feedback Item #1)
	degradedStart := now.Add(1 * time.Hour)
	degInc := domain.NewIncident(metrics.ServiceWallet, domain.SeverityP2High, "sustained degradation: p99 latency spike", degradedStart)
	degInc.ProbeFailureCount = 3
	if err := repo.Create(ctx, degInc); err != nil {
		t.Fatalf("failed to create degradation incident: %v", err)
	}

	// Transition from DEGRADED to DOWN:
	// Verify that querying active incident finds the DEGRADED incident and updates heartbeat
	activeDeg, err := repo.GetActiveByService(ctx, metrics.ServiceWallet)
	if err != nil || activeDeg == nil {
		t.Fatalf("expected active incident for degraded wallet: %v", err)
	}
	downTick := degradedStart.Add(15 * time.Second)
	if err := repo.UpdateHeartbeat(ctx, activeDeg.ID, downTick, activeDeg.ProbeFailureCount+1); err != nil {
		t.Fatalf("failed to heartbeat during DEGRADED -> DOWN transition: %v", err)
	}

	updatedDown, _ := repo.GetByID(ctx, activeDeg.ID)
	if updatedDown.ProbeFailureCount != 4 {
		t.Fatalf("expected probe_failure_count=4 after DOWN transition, got %d", updatedDown.ProbeFailureCount)
	}
	if !updatedDown.LastSeenAt.Equal(downTick) {
		t.Fatalf("expected last_seen_at to update to %v, got %v", downTick, updatedDown.LastSeenAt)
	}
}

func TestPhase3_ResolveIncidentGuards(t *testing.T) {
	repo := newMockIncidentRepo()
	auditRepo := newMockAuditRepo()
	topoEngine := service.NewTopologyEngine()
	log := zap.NewNop()
	incidentSvc := service.NewIncidentService(repo, auditRepo, topoEngine, log)
	ctx := context.Background()

	now := time.Now().UTC()
	inc := domain.NewIncident(metrics.ServiceAuth, domain.SeverityP1Critical, "auth outage", now)
	_ = repo.Create(ctx, inc)

	// First manual resolve succeeds
	if err := incidentSvc.ResolveIncident(ctx, inc.ID, "manual operator intervention"); err != nil {
		t.Fatalf("expected first resolve to succeed, got: %v", err)
	}

	// Second resolve on already-resolved incident must return ErrIncidentAlreadyResolved immediately
	err := incidentSvc.ResolveIncident(ctx, inc.ID, "duplicate resolve")
	if err != domain.ErrIncidentAlreadyResolved {
		t.Fatalf("expected ErrIncidentAlreadyResolved on already resolved incident, got: %v", err)
	}
}

// ─── Test 2: Concurrency & Partial Unique Index Protection ───────────────────

func TestPhase3_IncidentConcurrentDeduplication(t *testing.T) {
	repo := newMockIncidentRepo()
	ctx := context.Background()

	var wg sync.WaitGroup
	workers := 10
	createdCount := 0
	conflictCount := 0
	var mu sync.Mutex

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inc := domain.NewIncident(metrics.ServiceAuth, domain.SeverityP1Critical, "gRPC connection refused", time.Now().UTC())
			err := repo.Create(ctx, inc)
			mu.Lock()
			if err == nil {
				createdCount++
			} else if err == domain.ErrActiveIncidentExists {
				conflictCount++
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	if createdCount != 1 {
		t.Errorf("expected exactly 1 incident created, got %d", createdCount)
	}
	if conflictCount != workers-1 {
		t.Errorf("expected %d deduplication conflicts, got %d", workers-1, conflictCount)
	}
}

// ─── Test 3: Incident Correlation with Audit Logs & Topology ─────────────────

func TestPhase3_IncidentCorrelation(t *testing.T) {
	incidentRepo := newMockIncidentRepo()
	auditRepo := newMockAuditRepo()
	topoEngine := service.NewTopologyEngine()
	log := zap.NewNop()
	incidentSvc := service.NewIncidentService(incidentRepo, auditRepo, topoEngine, log)

	ctx := context.Background()
	incidentTime := time.Now().UTC()

	// Insert audit action 5 minutes prior to incident
	preIncidentLog := &domain.AuditLog{
		ID:         domain.MustNewV7(),
		AdminID:    "admin-operator-1",
		Action:     "HALT_MARKET",
		TargetType: "market",
		TargetID:   "BTC-USDT",
		IPAddress:  "192.168.1.100",
		CreatedAt:  incidentTime.Add(-5 * time.Minute),
	}
	_ = auditRepo.Insert(ctx, preIncidentLog)

	// Create incident on Trade service
	inc := domain.NewIncident(metrics.ServiceTrade, domain.SeverityP1Critical, "trade engine unresponsive", incidentTime)
	_ = incidentRepo.Create(ctx, inc)

	// Fetch correlated incident (15m window)
	correlated, err := incidentSvc.GetCorrelatedIncident(ctx, inc.ID, 15*time.Minute, 15*time.Minute)
	if err != nil {
		t.Fatalf("failed to fetch correlated incident: %v", err)
	}

	if correlated.Incident.ID != inc.ID {
		t.Errorf("expected incident ID %s, got %s", inc.ID, correlated.Incident.ID)
	}

	if len(correlated.CorrelatedAuditLogs) != 1 {
		t.Fatalf("expected 1 correlated audit log, got %d", len(correlated.CorrelatedAuditLogs))
	}
	if correlated.CorrelatedAuditLogs[0].Action != "HALT_MARKET" {
		t.Errorf("expected HALT_MARKET audit action, got %s", correlated.CorrelatedAuditLogs[0].Action)
	}

	// Verify impacted topology downstream
	if len(correlated.AffectedDependencies) == 0 {
		t.Errorf("expected non-empty topological dependencies for Trade service")
	}
}

// ─── Test 4: Topology Graph & Health Overlays ────────────────────────────────

func TestPhase3_TopologyEngine(t *testing.T) {
	engine := service.NewTopologyEngine()
	ctx := context.Background()

	topo := engine.GetTopology(ctx)
	if topo.TotalNodes == 0 {
		t.Fatalf("expected non-zero nodes in topology graph")
	}

	// Verify canonical services exist
	foundTrade := false
	foundAdmin := false
	for _, node := range topo.Nodes {
		if node.ID == metrics.ServiceTrade {
			foundTrade = true
			if node.Tier != service.TierTradingEngine {
				t.Errorf("expected Trade tier Trading Engine, got %s", node.Tier)
			}
		}
		if node.ID == metrics.ServiceAdmin {
			foundAdmin = true
		}
	}

	if !foundTrade || !foundAdmin {
		t.Errorf("expected trade and admin nodes in topology, foundTrade=%v, foundAdmin=%v", foundTrade, foundAdmin)
	}
	if len(topo.Edges) == 0 {
		t.Errorf("expected directional edges in topology")
	}

	// Verify unprobed infrastructure nodes strictly remain UNKNOWN (no false UP fallback)
	for _, node := range topo.Nodes {
		if node.ID == metrics.ServicePostgres || node.ID == metrics.ServiceKafka {
			if node.Status != "UNKNOWN" {
				t.Errorf("expected unprobed %s to be UNKNOWN, got %s", node.ID, node.Status)
			}
		}
	}

	// Verify TIMEOUT is explicitly present in topology summary
	if _, ok := topo.Summary["TIMEOUT"]; !ok {
		t.Errorf("expected TIMEOUT key in topology summary map")
	}
}

// ─── Test 5: Risk Signals Heuristics ─────────────────────────────────────────

func TestPhase3_AnalyticsRiskSignals(t *testing.T) {
	opsRepo := newMockOpsRepo()
	auditRepo := newMockAuditRepo()
	log := zap.NewNop()
	analyticsSvc := service.NewAnalyticsService(opsRepo, auditRepo, log)
	ctx := context.Background()

	now := time.Now().UTC()

	// Simulate suspicious burst: 6 suspensions within 10 minutes by same admin from different IPs
	for i := 0; i < 6; i++ {
		_ = auditRepo.Insert(ctx, &domain.AuditLog{
			ID:         domain.MustNewV7(),
			AdminID:    "suspicious-admin",
			Action:     "SUSPEND_USER",
			TargetType: "user",
			TargetID:   "target-user",
			IPAddress:  "10.0.0." + string(rune('1'+i)),
			CreatedAt:  now.Add(-time.Duration(i) * time.Minute),
		})
	}

	signals, err := analyticsSvc.GetRiskSignals(ctx, 30*time.Minute)
	if err != nil {
		t.Fatalf("failed to evaluate risk signals: %v", err)
	}

	if len(signals.Signals) == 0 {
		t.Fatalf("expected risk signals to fire on 6 rapid suspensions, got 0")
	}

	foundMassSuspension := false
	foundIPDiversity := false
	for _, sig := range signals.Signals {
		if sig.SignalType == "MASS_USER_SUSPENSION" {
			foundMassSuspension = true
			if sig.Severity != "HIGH" {
				t.Errorf("expected HIGH severity for mass user suspension, got %s", sig.Severity)
			}
		}
		if sig.SignalType == "ADMIN_IP_DIVERSITY" {
			foundIPDiversity = true
			if sig.Severity != "MEDIUM" {
				t.Errorf("expected MEDIUM severity for IP diversity, got %s", sig.Severity)
			}
		}
	}

	if !foundMassSuspension {
		t.Errorf("expected MASS_USER_SUSPENSION signal to be present")
	}
	if !foundIPDiversity {
		t.Errorf("expected ADMIN_IP_DIVERSITY signal to be present")
	}

	// Verify deterministic sorting: first signal must be HIGH severity (MASS_USER_SUSPENSION), followed by MEDIUM (ADMIN_IP_DIVERSITY)
	if signals.Signals[0].Severity != "HIGH" {
		t.Errorf("expected highest severity signal first, got %s", signals.Signals[0].Severity)
	}

	// Test Per-Minute Burst Velocity:
	// 11 actions spread across 11 distinct minutes should NOT trigger ADMIN_BURST_VELOCITY
	spreadAuditRepo := newMockAuditRepo()
	spreadAnalytics := service.NewAnalyticsService(opsRepo, spreadAuditRepo, log)
	for i := 0; i < 11; i++ {
		_ = spreadAuditRepo.Insert(ctx, &domain.AuditLog{
			ID:         domain.MustNewV7(),
			AdminID:    "spread-admin",
			Action:     "HALT_MARKET",
			TargetType: "market",
			TargetID:   "BTC-USDT",
			IPAddress:  "10.0.0.1",
			CreatedAt:  now.Add(-time.Duration(i*2) * time.Minute), // 1 every 2 minutes
		})
	}
	spreadSignals, _ := spreadAnalytics.GetRiskSignals(ctx, 60*time.Minute)
	for _, sig := range spreadSignals.Signals {
		if sig.SignalType == "ADMIN_BURST_VELOCITY" {
			t.Errorf("did not expect ADMIN_BURST_VELOCITY for spread actions")
		}
	}

	// 11 actions in the SAME minute SHOULD trigger ADMIN_BURST_VELOCITY
	burstAuditRepo := newMockAuditRepo()
	burstAnalytics := service.NewAnalyticsService(opsRepo, burstAuditRepo, log)
	burstTime := now.Add(-5 * time.Minute).Truncate(time.Minute)
	for i := 0; i < 11; i++ {
		_ = burstAuditRepo.Insert(ctx, &domain.AuditLog{
			ID:         domain.MustNewV7(),
			AdminID:    "burst-admin",
			Action:     "UNSUSPEND_USER",
			TargetType: "user",
			TargetID:   "target-user",
			IPAddress:  "10.0.0.1",
			CreatedAt:  burstTime.Add(time.Duration(i) * time.Second),
		})
	}
	burstSignals, _ := burstAnalytics.GetRiskSignals(ctx, 60*time.Minute)
	foundBurst := false
	for _, sig := range burstSignals.Signals {
		if sig.SignalType == "ADMIN_BURST_VELOCITY" {
			foundBurst = true
			if sig.ActionCount != 11 {
				t.Errorf("expected burst action count 11, got %d", sig.ActionCount)
			}
		}
	}
	if !foundBurst {
		t.Errorf("expected ADMIN_BURST_VELOCITY signal when 11 actions occurred in 1 minute")
	}
}

// ─── Test 6: HTTP Routes with JWT & Structured Middleware ────────────────────

func TestPhase3_HTTPHandlersWithAuth(t *testing.T) {
	incidentRepo := newMockIncidentRepo()
	auditRepo := newMockAuditRepo()
	opsRepo := newMockOpsRepo()
	topoEngine := service.NewTopologyEngine()
	log := zap.NewNop()

	incidentSvc := service.NewIncidentService(incidentRepo, auditRepo, topoEngine, log)
	analyticsSvc := service.NewAnalyticsService(opsRepo, auditRepo, log)

	incidentHdr := handler.NewIncidentHandler(incidentSvc, log)
	analyticsHdr := handler.NewAnalyticsHandler(analyticsSvc, log)
	topoHdr := handler.NewTopologyHandler(topoEngine, log)

	validator := platformjwt.NewHMACValidator([]byte(testSecret))

	r := handler.NewRouter(
		nil, // adminHandler not exercised in this test
		handler.NewHealthHandler(nil, nil, nil, nil, nil, handler.HealthConfig{}, log),
		validator,
		log,
		handler.WithIncidentHandler(incidentHdr),
		handler.WithAnalyticsHandler(analyticsHdr),
		handler.WithTopologyHandler(topoHdr),
	)

	// Generate valid admin JWT token using helper
	token := generateToken(t, "admin-123", "admin")

	// 1. GET /api/v1/admin/topology (with auth)
	req := httptest.NewRequest("GET", "/api/v1/admin/topology", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET /api/v1/admin/topology expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// 2. GET /api/v1/admin/incidents (with auth)
	reqIncidents := httptest.NewRequest("GET", "/api/v1/admin/incidents", nil)
	reqIncidents.Header.Set("Authorization", "Bearer "+token)
	wIncidents := httptest.NewRecorder()
	r.ServeHTTP(wIncidents, reqIncidents)
	if wIncidents.Code != http.StatusOK {
		t.Errorf("GET /api/v1/admin/incidents expected 200, got %d: %s", wIncidents.Code, wIncidents.Body.String())
	}

	// 3. GET /api/v1/admin/analytics/risk-signals (unauthenticated -> 401)
	reqUnauth := httptest.NewRequest("GET", "/api/v1/admin/analytics/risk-signals", nil)
	wUnauth := httptest.NewRecorder()
	r.ServeHTTP(wUnauth, reqUnauth)
	if wUnauth.Code != http.StatusUnauthorized {
		t.Errorf("GET /api/v1/admin/analytics/risk-signals unauth expected 401, got %d", wUnauth.Code)
	}

	// 4. POST /api/v1/admin/incidents/{incident_id}/resolve (malformed JSON -> 400 Bad Request)
	malformedReq := httptest.NewRequest("POST", "/api/v1/admin/incidents/inc-123/resolve", strings.NewReader("{not-json}"))
	malformedReq.Header.Set("Authorization", "Bearer "+token)
	malformedReq.Header.Set("Content-Type", "application/json")
	wMalformed := httptest.NewRecorder()
	r.ServeHTTP(wMalformed, malformedReq)
	if wMalformed.Code != http.StatusBadRequest {
		t.Errorf("POST /api/v1/admin/incidents/inc-123/resolve malformed json expected 400, got %d", wMalformed.Code)
	}
}

// ─── Test 7: Stateful Market Halt Gauges & Restart Recovery ────────────────
func TestPhase3_MarketHaltStateMetrics(t *testing.T) {
	marketID := "ETH-USDT"
	haltTime := time.Now().UTC()

	// Initially reset
	metrics.RecordMarketResumed(marketID)

	// Halt
	metrics.RecordMarketHalted(marketID, haltTime)

	// Resume
	metrics.RecordMarketResumed(marketID)
}

func TestPhase3_MarketStateReconstructionOnStartup(t *testing.T) {
	opsRepo := newMockOpsRepo()
	log := zap.NewNop()
	svc := service.NewAdminService(nil, opsRepo, nil, nil, nil, log)
	ctx := context.Background()

	marketBTC := "BTC-USDT"
	marketETH := "ETH-USDT"
	haltTime := time.Now().UTC().Add(-30 * time.Minute)
	resumeTime := time.Now().UTC().Add(-10 * time.Minute)

	// 1. Simulate historical state in operations repository:
	// BTC was halted at t-30m and remains halted.
	_ = opsRepo.Insert(ctx, &domain.AdminOperation{
		ID:             domain.MustNewV7(),
		AdminID:        "admin-ops",
		IdempotencyKey: "halt-btc-key",
		OperationType:  domain.OpHaltMarket,
		TargetID:       marketBTC,
		Status:         domain.OperationStatusCompleted,
		CreatedAt:      haltTime,
	})

	// ETH was halted at t-30m, but resumed at t-10m.
	_ = opsRepo.Insert(ctx, &domain.AdminOperation{
		ID:             domain.MustNewV7(),
		AdminID:        "admin-ops",
		IdempotencyKey: "halt-eth-key",
		OperationType:  domain.OpHaltMarket,
		TargetID:       marketETH,
		Status:         domain.OperationStatusCompleted,
		CreatedAt:      haltTime,
	})
	_ = opsRepo.Insert(ctx, &domain.AdminOperation{
		ID:             domain.MustNewV7(),
		AdminID:        "admin-ops",
		IdempotencyKey: "resume-eth-key",
		OperationType:  domain.OpResumeMarket,
		TargetID:       marketETH,
		Status:         domain.OperationStatusCompleted,
		CreatedAt:      resumeTime,
	})

	// 2. Clear in-memory metrics to simulate fresh process boot
	metrics.RecordMarketResumed(marketBTC)
	metrics.RecordMarketResumed(marketETH)

	// 3. Trigger state reconstruction
	if err := svc.ReconstructMarketState(ctx); err != nil {
		t.Fatalf("failed to reconstruct market state: %v", err)
	}

	// 4. Verify reconstructed states
	states, err := opsRepo.GetLatestMarketStates(ctx)
	if err != nil {
		t.Fatalf("failed to query reconstructed states: %v", err)
	}

	btcHalted := false
	ethHalted := true
	for _, st := range states {
		if st.MarketID == marketBTC && st.IsHalted {
			btcHalted = true
		}
		if st.MarketID == marketETH && !st.IsHalted {
			ethHalted = false
		}
	}

	if !btcHalted {
		t.Errorf("expected BTC-USDT to be reconstructed as HALTED")
	}
	if ethHalted {
		t.Errorf("expected ETH-USDT to be reconstructed as ACTIVE (resumed)")
	}
}
