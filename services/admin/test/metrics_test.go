package test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"go.uber.org/zap"

	platformjwt "tradedrift/platform/jwt"
	"tradedrift/services/admin/internal/handler"
	"tradedrift/services/admin/internal/metrics"
	"tradedrift/services/admin/internal/repository"
	"tradedrift/services/admin/internal/service"
)

func TestMetrics_ExpositionEndpoint(t *testing.T) {
	// 1. Setup minimal router with /metrics
	healthCfg := handler.HealthConfig{}
	healthHdr := handler.NewHealthHandler(nil, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, healthCfg, zap.NewNop())
	adminHdr := handler.NewAdminHandler(nil, zap.NewNop())
	jwtVal := platformjwt.NewHMACValidator([]byte("test-secret-32-bytes-long-key!!"))

	r := handler.NewRouter(adminHdr, healthHdr, jwtVal, zap.NewNop())

	// 2. Perform GET /health to generate an HTTP request metric
	reqHealth := httptest.NewRequest(http.MethodGet, "/health", nil)
	rrHealth := httptest.NewRecorder()
	r.ServeHTTP(rrHealth, reqHealth)

	// 3. Set a platform overall status metric value
	metrics.SetSystemOverallStatus("HEALTHY")

	// 4. Perform GET /metrics
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 from /metrics, got %d", rr.Code)
	}

	body := rr.Body.String()

	// Verify Prometheus exposition format and tradedrift admin metrics presence
	if !strings.Contains(body, "tradedrift_admin_http_requests_total") {
		t.Errorf("expected metric tradedrift_admin_http_requests_total in output")
	}
	if !strings.Contains(body, "tradedrift_admin_system_overall_status") {
		t.Errorf("expected metric tradedrift_admin_system_overall_status in output")
	}
}

func TestMetrics_RouteNormalization(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		path     string
		expected string
	}{
		{
			name:     "Registered pattern Go 1.22",
			pattern:  "POST /api/v1/admin/users/{user_id}/suspend",
			path:     "/api/v1/admin/users/01918342-9999-7fff-8888-000000000000/suspend",
			expected: "/api/v1/admin/users/{user_id}/suspend",
		},
		{
			name:     "Fallback suspend user path",
			pattern:  "",
			path:     "/api/v1/admin/users/01918342-9999-7fff-8888-000000000000/suspend",
			expected: "/api/v1/admin/users/{user_id}/suspend",
		},
		{
			name:     "Fallback unsuspend user path",
			pattern:  "",
			path:     "/api/v1/admin/users/user-abc-123/unsuspend",
			expected: "/api/v1/admin/users/{user_id}/unsuspend",
		},
		{
			name:     "Fallback freeze wallet path",
			pattern:  "",
			path:     "/api/v1/admin/users/user-xyz/wallets/USDT/freeze",
			expected: "/api/v1/admin/users/{user_id}/wallets/{asset}/freeze",
		},
		{
			name:     "Fallback unfreeze wallet path",
			pattern:  "",
			path:     "/api/v1/admin/users/user-xyz/wallets/BTC/unfreeze",
			expected: "/api/v1/admin/users/{user_id}/wallets/{asset}/unfreeze",
		},
		{
			name:     "Fallback halt market path",
			pattern:  "",
			path:     "/api/v1/admin/markets/BTC-USDT/halt",
			expected: "/api/v1/admin/markets/{market_id}/halt",
		},
		{
			name:     "Fallback resume market path",
			pattern:  "",
			path:     "/api/v1/admin/markets/ETH-USDT/resume",
			expected: "/api/v1/admin/markets/{market_id}/resume",
		},
		{
			name:     "Static /health path",
			pattern:  "",
			path:     "/health",
			expected: "/health",
		},
		{
			name:     "Static /metrics path",
			pattern:  "",
			path:     "/metrics",
			expected: "/metrics",
		},
		{
			name:     "Unmatched dynamic path",
			pattern:  "",
			path:     "/random/api/path/404",
			expected: "unmatched",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			normalized := handler.NormalizeRoute(tc.pattern, tc.path)
			if normalized != tc.expected {
				t.Fatalf("NormalizeRoute(%q, %q) = %q; expected %q", tc.pattern, tc.path, normalized, tc.expected)
			}
		})
	}
}

func TestMetrics_HealthStatus1HotInvariant(t *testing.T) {
	serviceName := "test_service_auth"
	now := time.Now().UTC()

	// 1. Record UP status
	metrics.RecordHealthProbe(serviceName, "UP", 50*time.Millisecond, now)

	up := testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues(serviceName, "UP"))
	down := testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues(serviceName, "DOWN"))
	degraded := testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues(serviceName, "DEGRADED"))
	timeout := testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues(serviceName, "TIMEOUT"))
	unknown := testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues(serviceName, "UNKNOWN"))

	if up != 1.0 || down != 0.0 || degraded != 0.0 || timeout != 0.0 || unknown != 0.0 {
		t.Fatalf("expected 1-hot for UP (UP=1, others=0), got UP=%v, DOWN=%v, DEGRADED=%v, TIMEOUT=%v, UNKNOWN=%v", up, down, degraded, timeout, unknown)
	}
	if sum := up + down + degraded + timeout + unknown; sum != 1.0 {
		t.Fatalf("expected sum of 1-hot boolean gauges to be exactly 1.0, got %v", sum)
	}

	// 2. Transition to DOWN status
	now2 := now.Add(15 * time.Second)
	metrics.RecordHealthProbe(serviceName, "DOWN", 120*time.Millisecond, now2)

	up = testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues(serviceName, "UP"))
	down = testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues(serviceName, "DOWN"))
	degraded = testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues(serviceName, "DEGRADED"))
	timeout = testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues(serviceName, "TIMEOUT"))
	unknown = testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues(serviceName, "UNKNOWN"))

	if down != 1.0 || up != 0.0 || degraded != 0.0 || timeout != 0.0 || unknown != 0.0 {
		t.Fatalf("expected 1-hot for DOWN (DOWN=1, others=0), got DOWN=%v, UP=%v, DEGRADED=%v, TIMEOUT=%v, UNKNOWN=%v", down, up, degraded, timeout, unknown)
	}
	if sum := up + down + degraded + timeout + unknown; sum != 1.0 {
		t.Fatalf("expected sum of 1-hot boolean gauges to be exactly 1.0, got %v", sum)
	}

	// 3. Transition to TIMEOUT status
	now3 := now2.Add(15 * time.Second)
	metrics.RecordHealthProbe(serviceName, "TIMEOUT", 1500*time.Millisecond, now3)

	up = testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues(serviceName, "UP"))
	down = testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues(serviceName, "DOWN"))
	degraded = testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues(serviceName, "DEGRADED"))
	timeout = testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues(serviceName, "TIMEOUT"))
	unknown = testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues(serviceName, "UNKNOWN"))

	if timeout != 1.0 || up != 0.0 || down != 0.0 || degraded != 0.0 || unknown != 0.0 {
		t.Fatalf("expected 1-hot for TIMEOUT (TIMEOUT=1, others=0), got TIMEOUT=%v, UP=%v, DOWN=%v, DEGRADED=%v, UNKNOWN=%v", timeout, up, down, degraded, unknown)
	}
	if sum := up + down + degraded + timeout + unknown; sum != 1.0 {
		t.Fatalf("expected sum of 1-hot boolean gauges to be exactly 1.0, got %v", sum)
	}

	// Verify last run timestamp
	lastRun := testutil.ToFloat64(metrics.HealthProbeLastRunTimestampSeconds.WithLabelValues(serviceName))
	if lastRun != float64(now3.Unix()) {
		t.Fatalf("expected last run timestamp %v, got %v", now3.Unix(), lastRun)
	}
}

func TestMetrics_OperationsAndBacklog(t *testing.T) {
	opType := "suspend_user"

	// 1. In-flight and completion lifecycle
	metrics.RecordOperationStart(opType)
	inFlight := testutil.ToFloat64(metrics.OperationsInFlight.WithLabelValues(opType))
	if inFlight != 1.0 {
		t.Errorf("expected in_flight=1.0, got %v", inFlight)
	}

	metrics.RecordOperationComplete(opType, true, 45*time.Millisecond)
	inFlight = testutil.ToFloat64(metrics.OperationsInFlight.WithLabelValues(opType))
	if inFlight != 0.0 {
		t.Errorf("expected in_flight=0.0 after completion, got %v", inFlight)
	}

	total := testutil.ToFloat64(metrics.OperationsTotal.WithLabelValues(opType, "COMPLETED"))
	if total != 1.0 {
		t.Errorf("expected operations_total[COMPLETED]=1.0, got %v", total)
	}

	// 2. Outbox retry counter
	topic := "user.events"
	metrics.RecordOutboxRetry(topic)
	retries := testutil.ToFloat64(metrics.OutboxRetriesTotal.WithLabelValues(topic))
	if retries != 1.0 {
		t.Errorf("expected outbox_retries_total=1.0, got %v", retries)
	}

	// 3. Saga queue stats
	metrics.RecordSagaQueueStats(5, 2, 1)
	pending := testutil.ToFloat64(metrics.SagaQueueCount.WithLabelValues("PENDING"))
	retrying := testutil.ToFloat64(metrics.SagaQueueCount.WithLabelValues("RETRYING"))
	exhausted := testutil.ToFloat64(metrics.SagaQueueCount.WithLabelValues("EXHAUSTED"))
	depth := testutil.ToFloat64(metrics.SagaPendingQueueDepth)

	if pending != 5.0 || retrying != 2.0 || exhausted != 1.0 {
		t.Errorf("expected saga queue counts (5, 2, 1), got (%v, %v, %v)", pending, retrying, exhausted)
	}
	if depth != 7.0 { // pending + retrying
		t.Errorf("expected saga pending queue depth=7.0, got %v", depth)
	}
}

func TestHealthWorker_AutonomousExecutionAndCaching(t *testing.T) {
	// Mock HTTP dependency server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	}))
	defer ts.Close()

	workerCfg := service.HealthWorkerConfig{
		TradeHealthURL: ts.URL,
		PortHealthURL:  ts.URL,
		LiqHealthURL:   ts.URL,
		NotifHealthURL: ts.URL,
	}

	hw := service.NewHealthWorker(nil, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, workerCfg, zap.NewNop(), 50*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hw.Start(ctx)

	// Wait for worker initial run
	time.Sleep(100 * time.Millisecond)

	cached := hw.GetLatestHealth()
	if cached == nil {
		t.Fatalf("expected cached health report to be non-nil")
	}

	if cached.Services["trade"].Status != "UP" {
		t.Errorf("expected trade service status UP, got %s", cached.Services["trade"].Status)
	}

	// Attach to HealthHandler and verify HandleSystemHealth serves cached report
	healthHdr := handler.NewHealthHandler(nil, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, handler.HealthConfig{}, zap.NewNop())
	healthHdr.SetHealthWorker(hw)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/health", nil)
	rr := httptest.NewRecorder()
	healthHdr.HandleSystemHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200 from HandleSystemHealth with HealthWorker, got %d", rr.Code)
	}

	if !strings.Contains(rr.Body.String(), `"overall_status"`) {
		t.Errorf("expected overall_status in JSON response")
	}

	// Stop worker cleanly
	hw.Stop()
}

func TestMetrics_SubmillisecondLatencyPreserved(t *testing.T) {
	serviceName := "subms_test_service"
	now := time.Now().UTC()
	dur := 450 * time.Microsecond // 0.00045 seconds
	metrics.RecordHealthProbe(serviceName, "UP", dur, now)

	latencySec := testutil.ToFloat64(metrics.HealthProbeLatencySeconds.WithLabelValues(serviceName))
	expectedSec := 0.00045
	if latencySec == 0.0 {
		t.Fatalf("expected non-zero latency for 450µs probe, got 0.0")
	}
	if diff := latencySec - expectedSec; diff < -0.00001 || diff > 0.00001 {
		t.Fatalf("expected latency near %v, got %v", expectedSec, latencySec)
	}
}

type mockFailingOutboxRepo struct {
	mockOutboxRepo
}

func (m *mockFailingOutboxRepo) GetBacklogStats(ctx context.Context) (*repository.OutboxBacklogStats, error) {
	return nil, errors.New("simulated postgres connection error")
}

type mockFailingSagaRepo struct {
	mockSagaRepo
}

func (m *mockFailingSagaRepo) GetQueueStats(ctx context.Context) (*repository.SagaQueueStats, error) {
	return nil, errors.New("simulated postgres timeout error")
}

func TestHealthWorker_DBStatsErrorResilience(t *testing.T) {
	workerCfg := service.HealthWorkerConfig{}
	// Provide failing outbox and saga repositories to verify worker loop resilience
	hw := service.NewHealthWorker(nil, nil, nil, &mockFailingOutboxRepo{}, &mockFailingSagaRepo{}, workerCfg, zap.NewNop(), 50*time.Millisecond)

	ctx := context.Background()
	resp := hw.RunProbe(ctx)
	if resp == nil {
		t.Fatalf("expected non-nil health response despite backlog query failures")
	}
	if resp.Admin.Outbox != nil {
		t.Errorf("expected nil Outbox stats when query failed, got %+v", resp.Admin.Outbox)
	}
	if resp.Admin.Saga != nil {
		t.Errorf("expected nil Saga stats when query failed, got %+v", resp.Admin.Saga)
	}
}

func TestHealthWorker_DependencyRecovery(t *testing.T) {
	var statusCode int32 = http.StatusServiceUnavailable
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := int(atomic.LoadInt32(&statusCode))
		w.WriteHeader(code)
		if code == http.StatusOK {
			_, _ = w.Write([]byte(`{"status":"ready"}`))
		} else {
			_, _ = w.Write([]byte(`{"status":"error"}`))
		}
	}))
	defer ts.Close()

	workerCfg := service.HealthWorkerConfig{
		TradeHealthURL: ts.URL,
	}

	hw := service.NewHealthWorker(nil, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, workerCfg, zap.NewNop(), 50*time.Millisecond)

	// Step 1: Service is DOWN (503)
	resp1 := hw.RunProbe(context.Background())
	if resp1.Services["trade"].Status != "DOWN" {
		t.Fatalf("expected trade status DOWN on 503, got %s", resp1.Services["trade"].Status)
	}
	downVal := testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues("trade", "DOWN"))
	upVal := testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues("trade", "UP"))
	if downVal != 1.0 || upVal != 0.0 {
		t.Fatalf("expected trade DOWN=1, UP=0; got DOWN=%v, UP=%v", downVal, upVal)
	}

	// Step 2: Service recovers to UP (200)
	atomic.StoreInt32(&statusCode, http.StatusOK)
	resp2 := hw.RunProbe(context.Background())
	if resp2.Services["trade"].Status != "UP" {
		t.Fatalf("expected trade status UP on recovery, got %s", resp2.Services["trade"].Status)
	}
	downVal = testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues("trade", "DOWN"))
	upVal = testutil.ToFloat64(metrics.SystemHealthStatus.WithLabelValues("trade", "UP"))
	if downVal != 0.0 || upVal != 1.0 {
		t.Fatalf("expected trade DOWN=0, UP=1 on recovery; got DOWN=%v, UP=%v", downVal, upVal)
	}
}

func TestMetrics_PrometheusConsistency(t *testing.T) {
	// Seed sample values for all exported metrics to ensure active registration
	metrics.RecordHTTPRequest("GET", "/health", 200, 10*time.Millisecond)
	metrics.RecordOperationStart("test_op")
	metrics.RecordOperationComplete("test_op", true, 20*time.Millisecond)
	metrics.RecordOutboxPublish("test_topic", 15*time.Millisecond)
	metrics.RecordOutboxRetry("test_topic")
	metrics.RecordOutboxFailed("test_topic")
	metrics.RecordSagaComplete("test_task", 30*time.Millisecond)
	metrics.RecordSagaExhausted("test_task", 30*time.Millisecond)
	metrics.RecordSagaQueueStats(1, 0, 0)
	metrics.SetSystemOverallStatus("HEALTHY")
	metrics.RecordHealthProbe("test_svc", "UP", 5*time.Millisecond, time.Now())
	metrics.RecordHealthFailure("test_svc", "timeout")

	metricFamilies, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather prometheus metric families: %v", err)
	}

	gathered := make(map[string]bool)
	for _, mf := range metricFamilies {
		gathered[mf.GetName()] = true
	}

	// Metrics referenced in Prometheus alert rules (alerts.yml) and Grafana dashboards
	requiredMetrics := []string{
		"tradedrift_admin_http_requests_total",
		"tradedrift_admin_http_request_duration_seconds",
		"tradedrift_admin_operations_total",
		"tradedrift_admin_operations_in_flight",
		"tradedrift_admin_operation_duration_seconds",
		"tradedrift_admin_outbox_events_total",
		"tradedrift_admin_outbox_publish_duration_seconds",
		"tradedrift_admin_outbox_retries_total",
		"tradedrift_admin_outbox_backlog_depth",
		"tradedrift_admin_outbox_oldest_unpublished_seconds",
		"tradedrift_admin_saga_tasks_total",
		"tradedrift_admin_saga_task_duration_seconds",
		"tradedrift_admin_saga_pending_queue_depth",
		"tradedrift_admin_saga_queue_count",
		"tradedrift_admin_system_overall_status",
		"tradedrift_admin_system_health_status",
		"tradedrift_admin_health_probe_latency_seconds",
		"tradedrift_admin_health_probe_duration_seconds",
		"tradedrift_admin_health_probe_last_run_timestamp_seconds",
		"tradedrift_admin_health_probe_failures_total",
	}

	for _, metricName := range requiredMetrics {
		if !gathered[metricName] {
			t.Errorf("critical telemetry metric %q missing from Prometheus DefaultGatherer", metricName)
		}
	}
}

func TestWorkers_StopIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. HealthWorker
	hw := service.NewHealthWorker(nil, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, service.HealthWorkerConfig{}, zap.NewNop(), time.Hour)
	hw.Start(ctx)
	// Calling Stop twice must not panic
	hw.Stop()
	hw.Stop()

	// 2. SagaWorker
	sw := service.NewSagaWorker(nil, &mockSagaRepo{}, nil, nil, zap.NewNop(), time.Hour)
	sw.Start(ctx)
	sw.Stop()
	sw.Stop()

	// 3. OutboxPublisher
	op := service.NewOutboxPublisher(&mockOutboxRepo{}, "localhost:9092", zap.NewNop(), time.Hour)
	op.Start(ctx)
	op.Stop()
	op.Stop()
}

func TestHealthWorker_ProbeOverlapProtection(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(15 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	}))
	defer ts.Close()

	hw := service.NewHealthWorker(nil, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, service.HealthWorkerConfig{
		TradeHealthURL: ts.URL,
	}, zap.NewNop(), time.Hour)

	var wg sync.WaitGroup
	errCh := make(chan error, 10)

	// Fire 5 concurrent probes simultaneously
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := hw.RunProbe(context.Background())
			if resp == nil {
				errCh <- errors.New("received nil response during concurrent probing")
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}
}

func TestWorkers_StartIdempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hw := service.NewHealthWorker(&mockDBPinger{}, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, service.HealthWorkerConfig{}, zap.NewNop(), time.Hour)
	hw.Start(ctx)
	hw.Start(ctx)
	hw.Stop()
	hw.Stop()

	sw := service.NewSagaWorker(nil, &mockSagaRepo{}, nil, nil, zap.NewNop(), time.Hour)
	sw.Start(ctx)
	sw.Start(ctx)
	sw.Stop()
	sw.Stop()

	op := service.NewOutboxPublisher(&mockOutboxRepo{}, "127.0.0.1:9092", zap.NewNop(), time.Hour)
	op.Start(ctx)
	op.Start(ctx)
	op.Stop()
	op.Stop()
}

func TestHealthWorker_KafkaDownDegradesOverallStatus(t *testing.T) {
	upServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	}))
	defer upServer.Close()

	// Configure Kafka broker pointing to unreachable port
	workerCfg := service.HealthWorkerConfig{
		TradeHealthURL: upServer.URL,
		PortHealthURL:  upServer.URL,
		LiqHealthURL:   upServer.URL,
		NotifHealthURL: upServer.URL,
		KafkaBrokers:   []string{"127.0.0.1:59998"},
	}

	hw := service.NewHealthWorker(&mockDBPinger{}, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, workerCfg, zap.NewNop(), time.Hour)
	resp := hw.RunProbe(context.Background())

	if resp.Admin.Kafka.Status != "DOWN" {
		t.Fatalf("expected Kafka status DOWN, got %s", resp.Admin.Kafka.Status)
	}
	// With Kafka DOWN and core services UP, overall must be DEGRADED
	if resp.OverallStatus != "DEGRADED" {
		t.Fatalf("expected overall status DEGRADED when Kafka is DOWN, got %s", resp.OverallStatus)
	}

	overallGauge := testutil.ToFloat64(metrics.SystemOverallStatus)
	if overallGauge != 1.0 {
		t.Fatalf("expected system_overall_status gauge 1.0 (DEGRADED), got %v", overallGauge)
	}
}

type mockFailingDBPinger struct{}

func (m *mockFailingDBPinger) Ping(ctx context.Context) error {
	return errors.New("simulated postgres connection failure")
}

func TestHealthWorker_PostgresUnknownOrDownCausesUnhealthy(t *testing.T) {
	upServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	}))
	defer upServer.Close()

	workerCfg := service.HealthWorkerConfig{
		TradeHealthURL: upServer.URL,
		PortHealthURL:  upServer.URL,
		LiqHealthURL:   upServer.URL,
		NotifHealthURL: upServer.URL,
	}

	// 1. Postgres UNKNOWN (nil dbPool) must cause overall UNHEALTHY
	hwUnknown := service.NewHealthWorker(nil, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, workerCfg, zap.NewNop(), time.Hour)
	respUnknown := hwUnknown.RunProbe(context.Background())
	if respUnknown.Admin.Postgres.Status != "UNKNOWN" {
		t.Fatalf("expected Postgres status UNKNOWN, got %s", respUnknown.Admin.Postgres.Status)
	}
	if respUnknown.OverallStatus != "UNHEALTHY" {
		t.Fatalf("expected overall status UNHEALTHY when Postgres is UNKNOWN, got %s", respUnknown.OverallStatus)
	}

	// 2. Postgres DOWN (failing ping) must cause overall UNHEALTHY
	hwDown := service.NewHealthWorker(&mockFailingDBPinger{}, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, workerCfg, zap.NewNop(), time.Hour)
	respDown := hwDown.RunProbe(context.Background())
	if respDown.Admin.Postgres.Status != "DOWN" {
		t.Fatalf("expected Postgres status DOWN, got %s", respDown.Admin.Postgres.Status)
	}
	if respDown.OverallStatus != "UNHEALTHY" {
		t.Fatalf("expected overall status UNHEALTHY when Postgres is DOWN, got %s", respDown.OverallStatus)
	}
}

func TestHealthWorker_KafkaTimeout(t *testing.T) {
	workerCfg := service.HealthWorkerConfig{
		KafkaBrokers: []string{"192.0.2.1:9092"},
	}

	hw := service.NewHealthWorker(&mockDBPinger{}, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, workerCfg, zap.NewNop(), time.Hour)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-1*time.Millisecond))
	defer cancel()

	resp := hw.RunProbe(ctx)
	if resp.Admin.Kafka.Status != "TIMEOUT" {
		t.Fatalf("expected Kafka status TIMEOUT on deadline exceeded, got %s", resp.Admin.Kafka.Status)
	}
}

