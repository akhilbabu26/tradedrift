package test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/handler"
	"tradedrift/services/admin/internal/repository"
	"tradedrift/services/admin/internal/service"
)

// Mock Outbox and Saga repositories for health tests
type mockOutboxRepo struct{}

func (m *mockOutboxRepo) Insert(ctx context.Context, event *domain.OutboxEvent) error { return nil }
func (m *mockOutboxRepo) FetchDue(ctx context.Context, workerToken string, limit int) ([]*domain.OutboxEvent, error) {
	return nil, nil
}
func (m *mockOutboxRepo) MarkPublished(ctx context.Context, id string, workerToken string) error {
	return nil
}
func (m *mockOutboxRepo) UpdateRetry(ctx context.Context, id string, workerToken string, nextAttemptAt time.Time, attemptCount int, lastError string) error {
	return nil
}
func (m *mockOutboxRepo) GetBacklogStats(ctx context.Context) (*repository.OutboxBacklogStats, error) {
	return &repository.OutboxBacklogStats{PendingCount: 5, OldestAge: 2 * time.Second}, nil
}

type mockSagaRepo struct{}

func (m *mockSagaRepo) Insert(ctx context.Context, task *domain.SagaTask) error { return nil }
func (m *mockSagaRepo) FetchDue(ctx context.Context, workerToken string, limit int) ([]*domain.SagaTask, error) {
	return nil, nil
}
func (m *mockSagaRepo) UpdateRetry(ctx context.Context, id string, workerToken string, nextAttemptAt time.Time, attemptCount int, lastError string) error {
	return nil
}
func (m *mockSagaRepo) MarkCompleted(ctx context.Context, id string, workerToken string) error {
	return nil
}
func (m *mockSagaRepo) MarkExhausted(ctx context.Context, id string, workerToken string, lastError string) error {
	return nil
}
func (m *mockSagaRepo) GetQueueStats(ctx context.Context) (*repository.SagaQueueStats, error) {
	return &repository.SagaQueueStats{PendingCount: 1, RetryingCount: 0, ExhaustedCount: 0}, nil
}

type mockDBPinger struct{}

func (m *mockDBPinger) Ping(ctx context.Context) error { return nil }

func newTestHealthHandler(cfg handler.HealthConfig) *handler.HealthHandler {
	workerCfg := service.HealthWorkerConfig{
		AuthGRPCAddr:   cfg.AuthGRPCAddr,
		WalletGRPCAddr: cfg.WalletGRPCAddr,
		TradeHealthURL: cfg.TradeHealthURL,
		PortHealthURL:  cfg.PortHealthURL,
		LiqHealthURL:   cfg.LiqHealthURL,
		NotifHealthURL: cfg.NotifHealthURL,
		KafkaBrokers:   cfg.KafkaBrokers,
	}
	hw := service.NewHealthWorker(&mockDBPinger{}, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, workerCfg, zap.NewNop(), time.Hour)
	// Seed initial snapshot so GetLatestHealth() returns cached report without fallback
	_ = hw.RunProbe(context.Background())
	h := handler.NewHealthHandler(nil, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, cfg, zap.NewNop())
	h.SetHealthWorker(hw)
	return h
}

func TestHealthHandler_Liveness(t *testing.T) {
	h := handler.NewHealthHandler(nil, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, handler.HealthConfig{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	h.HandleLiveness(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", rr.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("expected status 'ok', got: %s", body["status"])
	}
}

func TestHealthHandler_Readiness_ShuttingDown(t *testing.T) {
	h := handler.NewHealthHandler(nil, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, handler.HealthConfig{}, zap.NewNop())
	h.SetShuttingDown()

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr := httptest.NewRecorder()

	h.HandleReadiness(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable when shutting down, got: %d", rr.Code)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if body["status"] != "not_ready" {
		t.Fatalf("expected status 'not_ready', got: %v", body["status"])
	}
	if body["reason"] != "shutting_down" {
		t.Fatalf("expected reason 'shutting_down', got: %v", body["reason"])
	}
}

func TestHealthHandler_Readiness_KafkaFallback(t *testing.T) {
	// Start a local TCP listener to act as an available fallback broker
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on local port: %v", err)
	}
	defer ln.Close()

	// Broker 1 is dead, Broker 2 is alive
	cfg := handler.HealthConfig{
		KafkaBrokers: []string{"127.0.0.1:59998", ln.Addr().String()},
	}

	// Attach mock dbPool, and leave auth/wallet nil
	// Notice that without auth/wallet clients initialized, readiness overall will be not_ready,
	// but checks["kafka"] must be "ok"!
	h := handler.NewHealthHandler(nil, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, cfg, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rr := httptest.NewRecorder()
	h.HandleReadiness(rr, req)

	var body map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	checks, ok := body["checks"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected checks map in response")
	}

	if checks["kafka"] != "ok" {
		t.Fatalf("expected kafka check to be 'ok' when fallback broker is reachable, got %v", checks["kafka"])
	}
}

func TestHealthHandler_SystemHealth_AllServicesUP(t *testing.T) {
	upServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	}))
	defer upServer.Close()

	cfg := handler.HealthConfig{
		TradeHealthURL: upServer.URL,
		PortHealthURL:  upServer.URL,
		LiqHealthURL:   upServer.URL,
		NotifHealthURL: upServer.URL,
	}

	h := newTestHealthHandler(cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/health", nil)
	rr := httptest.NewRecorder()

	h.HandleSystemHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", rr.Code)
	}

	var resp handler.SystemHealthResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}

	if resp.Services["trade"].Status != "UP" {
		t.Errorf("expected trade status UP, got: %s", resp.Services["trade"].Status)
	}
	if resp.Services["portfolio"].Status != "UP" {
		t.Errorf("expected portfolio status UP, got: %s", resp.Services["portfolio"].Status)
	}
}

func TestHealthHandler_SystemHealth_OneServiceDegraded(t *testing.T) {
	upServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upServer.Close()

	degradedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests) // 429 Rate Limited -> DEGRADED
	}))
	defer degradedServer.Close()

	cfg := handler.HealthConfig{
		TradeHealthURL: upServer.URL,
		PortHealthURL:  degradedServer.URL,
	}

	h := newTestHealthHandler(cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/health", nil)
	rr := httptest.NewRecorder()

	h.HandleSystemHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK response from admin, got: %d", rr.Code)
	}

	var resp handler.SystemHealthResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}

	if resp.Services["portfolio"].Status != "DEGRADED" {
		t.Errorf("expected portfolio status DEGRADED, got: %s", resp.Services["portfolio"].Status)
	}
	if resp.OverallStatus != "DEGRADED" {
		t.Errorf("expected overall status DEGRADED, got: %s", resp.OverallStatus)
	}
}

func TestHealthHandler_SystemHealth_HTTP503ClassifiedAsDOWN(t *testing.T) {
	downServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable) // 503 Service Unavailable -> DOWN
	}))
	defer downServer.Close()

	cfg := handler.HealthConfig{
		TradeHealthURL: downServer.URL,
	}

	h := newTestHealthHandler(cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/health", nil)
	rr := httptest.NewRecorder()

	h.HandleSystemHealth(rr, req)

	var resp handler.SystemHealthResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}

	if resp.Services["trade"].Status != "DOWN" {
		t.Errorf("expected trade status DOWN for 503 response, got: %s", resp.Services["trade"].Status)
	}
	// Core service DOWN escalates overall status to UNHEALTHY
	if resp.OverallStatus != "UNHEALTHY" {
		t.Errorf("expected overall status UNHEALTHY when core service is DOWN, got: %s", resp.OverallStatus)
	}
}

func TestHealthHandler_SystemHealth_OptionalServiceDownCausesDegradedNotUnhealthy(t *testing.T) {
	upServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upServer.Close()

	notifDownServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer notifDownServer.Close()

	cfg := handler.HealthConfig{
		TradeHealthURL: upServer.URL,
		PortHealthURL:  upServer.URL,
		LiqHealthURL:   upServer.URL,
		NotifHealthURL: notifDownServer.URL, // Optional auxiliary service
	}

	h := newTestHealthHandler(cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/health", nil)
	rr := httptest.NewRecorder()

	h.HandleSystemHealth(rr, req)

	var resp handler.SystemHealthResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}

	if resp.Services["notification"].Status != "DOWN" {
		t.Errorf("expected notification status DOWN, got: %s", resp.Services["notification"].Status)
	}
	// Auxiliary service failure results in DEGRADED, not UNHEALTHY
	if resp.OverallStatus != "DEGRADED" {
		t.Errorf("expected overall status DEGRADED when only optional service fails, got: %s", resp.OverallStatus)
	}
}

func TestHealthHandler_SystemHealth_OneServiceUnreachable(t *testing.T) {
	cfg := handler.HealthConfig{
		TradeHealthURL: "http://127.0.0.1:59999/ready",
	}

	h := newTestHealthHandler(cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/health", nil)
	rr := httptest.NewRecorder()

	h.HandleSystemHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected admin endpoint to respond with 200 OK, got: %d", rr.Code)
	}

	var resp handler.SystemHealthResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}

	if resp.Services["trade"].Status != "DOWN" {
		t.Errorf("expected trade status DOWN, got: %s", resp.Services["trade"].Status)
	}
	if resp.Services["trade"].Error == "" {
		t.Error("expected error message for unreachable service")
	}
}

func TestHealthHandler_SystemHealth_TimeoutHandled(t *testing.T) {
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer slowServer.Close()

	cfg := handler.HealthConfig{
		TradeHealthURL: slowServer.URL,
	}

	h := newTestHealthHandler(cfg)

	start := time.Now()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/health", nil)
	rr := httptest.NewRecorder()

	h.HandleSystemHealth(rr, req)
	duration := time.Since(start)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from admin, got: %d", rr.Code)
	}

	if duration > 4*time.Second {
		t.Errorf("handler took too long to return (%v), timeout not enforced", duration)
	}

	var resp handler.SystemHealthResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}

	if resp.Services["trade"].Status != "DOWN" && resp.Services["trade"].Status != "TIMEOUT" {
		t.Errorf("expected trade status DOWN or TIMEOUT, got: %s", resp.Services["trade"].Status)
	}
}

func TestHealthHandler_SystemHealth_MissingURL_ReportedAsUnknownAndDegraded(t *testing.T) {
	// Only trade is configured; portfolio, liquidity_engine, notification are empty
	tradeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer tradeServer.Close()

	cfg := handler.HealthConfig{
		TradeHealthURL: tradeServer.URL,
		// PortHealthURL is intentionally left empty
		// LiqHealthURL is intentionally left empty
		// NotifHealthURL is intentionally left empty
	}

	h := newTestHealthHandler(cfg)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/health", nil)
	rr := httptest.NewRecorder()

	h.HandleSystemHealth(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from admin, got: %d", rr.Code)
	}

	var resp handler.SystemHealthResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode json: %v", err)
	}

	// Unconfigured services must be marked UNKNOWN and not omitted!
	portRep, ok := resp.Services["portfolio"]
	if !ok {
		t.Fatalf("expected 'portfolio' to be present in services map even if unconfigured")
	}
	if portRep.Status != "UNKNOWN" {
		t.Errorf("expected portfolio status 'UNKNOWN', got: %s", portRep.Status)
	}
	if portRep.Error != "health URL not configured" {
		t.Errorf("expected error 'health URL not configured', got: %s", portRep.Error)
	}

	// Overall status must be degraded because required URLs are unconfigured
	if resp.OverallStatus != "DEGRADED" {
		t.Errorf("expected overall status DEGRADED due to unconfigured services, got: %s", resp.OverallStatus)
	}

	// Verify Admin section separation
	if resp.Admin.Status != "UP" && resp.Admin.Status != "DEGRADED" {
		t.Errorf("expected valid admin status, got: %s", resp.Admin.Status)
	}
}
