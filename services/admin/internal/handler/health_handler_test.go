package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/handler"
	"tradedrift/services/admin/internal/repository"
)

// Mock Outbox and Saga repositories for health tests
type mockOutboxRepo struct{}

func (m *mockOutboxRepo) Insert(ctx context.Context, event *domain.OutboxEvent) error { return nil }
func (m *mockOutboxRepo) FetchDue(ctx context.Context, workerToken string, limit int) ([]*domain.OutboxEvent, error) {
	return nil, nil
}
func (m *mockOutboxRepo) MarkPublished(ctx context.Context, id string) error { return nil }
func (m *mockOutboxRepo) UpdateRetry(ctx context.Context, id string, nextAttemptAt time.Time, attemptCount int, lastError string) error {
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
func (m *mockSagaRepo) UpdateRetry(ctx context.Context, id string, nextAttemptAt time.Time, attemptCount int, lastError string) error {
	return nil
}
func (m *mockSagaRepo) MarkCompleted(ctx context.Context, id string) error               { return nil }
func (m *mockSagaRepo) MarkExhausted(ctx context.Context, id string, lastError string) error { return nil }
func (m *mockSagaRepo) GetQueueStats(ctx context.Context) (*repository.SagaQueueStats, error) {
	return &repository.SagaQueueStats{PendingCount: 1, RetryingCount: 0, ExhaustedCount: 0}, nil
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

func TestHealthHandler_SystemHealth_AllServicesUP(t *testing.T) {
	// Mock upstream service servers returning 200 OK
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

	h := handler.NewHealthHandler(nil, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, cfg, zap.NewNop())

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
	// Trade is 200 OK
	upServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upServer.Close()

	// Portfolio returns 503
	degradedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer degradedServer.Close()

	cfg := handler.HealthConfig{
		TradeHealthURL: upServer.URL,
		PortHealthURL:  degradedServer.URL,
	}

	h := handler.NewHealthHandler(nil, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, cfg, zap.NewNop())

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
	if resp.OverallStatus != "DEGRADED" && resp.OverallStatus != "UNHEALTHY" {
		t.Errorf("expected overall status DEGRADED/UNHEALTHY, got: %s", resp.OverallStatus)
	}
}

func TestHealthHandler_SystemHealth_OneServiceUnreachable(t *testing.T) {
	// Point to unreachable port
	cfg := handler.HealthConfig{
		TradeHealthURL: "http://127.0.0.1:59999/ready", // closed port
	}

	h := handler.NewHealthHandler(nil, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, cfg, zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/health", nil)
	rr := httptest.NewRecorder()

	h.HandleSystemHealth(rr, req)

	// Admin endpoint still responds promptly
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
	// Server that sleeps longer than probe timeout
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer slowServer.Close()

	cfg := handler.HealthConfig{
		TradeHealthURL: slowServer.URL,
	}

	h := handler.NewHealthHandler(nil, nil, nil, &mockOutboxRepo{}, &mockSagaRepo{}, cfg, zap.NewNop())

	start := time.Now()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/health", nil)
	rr := httptest.NewRecorder()

	h.HandleSystemHealth(rr, req)
	duration := time.Since(start)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from admin, got: %d", rr.Code)
	}

	// Probe timeout is 1.5s, whole handler timeout is 3s — should not hang indefinitely
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
