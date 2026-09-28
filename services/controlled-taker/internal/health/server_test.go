package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
	"tradedrift/services/controlled-taker/internal/config"
)

type mockRedisReader struct {
	pingErr  error
	depths   map[string]*redisdepth.DepthSnapshot
	depthErr error
}

func (m *mockRedisReader) Ping(ctx context.Context) error {
	return m.pingErr
}

func (m *mockRedisReader) GetDepth(ctx context.Context, marketID string) (*redisdepth.DepthSnapshot, error) {
	if m.depthErr != nil {
		return nil, m.depthErr
	}
	if d, ok := m.depths[marketID]; ok {
		return d, nil
	}
	return nil, errors.New("depth not found")
}

type mockOrderPinger struct {
	pingErr error
}

func (m *mockOrderPinger) Ping(ctx context.Context) error {
	return m.pingErr
}

func sampleMarkets() []config.MarketConfig {
	return []config.MarketConfig{
		{MarketID: "BTC-USDT"},
		{MarketID: "ETH-USDT"},
		{MarketID: "SOL-USDT"},
	}
}

func freshDepth(marketID string) *redisdepth.DepthSnapshot {
	return &redisdepth.DepthSnapshot{
		MarketID:   marketID,
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(100), Quantity: decimal.NewFromFloat(1.0)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(101), Quantity: decimal.NewFromFloat(1.0)},
		},
	}
}

func TestHealthz(t *testing.T) {
	server := NewServer("8080", &mockRedisReader{}, &mockOrderPinger{}, sampleMarkets(), zap.NewNop())
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	server.handleHealthz(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /healthz, got %d", w.Code)
	}

	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Fatalf("expected status: ok, got: %s", resp["status"])
	}
}

func TestReadyz_AllMarketsReady(t *testing.T) {
	redis := &mockRedisReader{
		depths: map[string]*redisdepth.DepthSnapshot{
			"BTC-USDT": freshDepth("BTC-USDT"),
			"ETH-USDT": freshDepth("ETH-USDT"),
			"SOL-USDT": freshDepth("SOL-USDT"),
		},
	}
	orders := &mockOrderPinger{}
	server := NewServer("8080", redis, orders, sampleMarkets(), zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	server.handleReadyz(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for /readyz when all ready, got %d", w.Code)
	}

	var resp struct {
		Ready   bool            `json:"ready"`
		Markets map[string]bool `json:"markets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Ready {
		t.Fatalf("expected ready: true, got false")
	}
	if !resp.Markets["BTC-USDT"] || !resp.Markets["ETH-USDT"] || !resp.Markets["SOL-USDT"] {
		t.Fatalf("expected all markets to be true, got %v", resp.Markets)
	}
}

func TestReadyz_PartialMarketsReady(t *testing.T) {
	redis := &mockRedisReader{
		depths: map[string]*redisdepth.DepthSnapshot{
			"BTC-USDT": freshDepth("BTC-USDT"),
			// ETH and SOL missing
		},
	}
	orders := &mockOrderPinger{}
	server := NewServer("8080", redis, orders, sampleMarkets(), zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	server.handleReadyz(w, req)

	// Since BTC-USDT is ready, service is operational (HTTP 200 with per-market reporting)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for partial readiness, got %d", w.Code)
	}

	var resp struct {
		Ready   bool            `json:"ready"`
		Markets map[string]bool `json:"markets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if !resp.Ready {
		t.Fatalf("expected ready: true when at least one market is ready")
	}
	if !resp.Markets["BTC-USDT"] {
		t.Fatalf("expected BTC-USDT to be true")
	}
	if resp.Markets["ETH-USDT"] || resp.Markets["SOL-USDT"] {
		t.Fatalf("expected ETH and SOL to be false, got %v", resp.Markets)
	}
}

func TestReadyz_NoMarketsReady(t *testing.T) {
	redis := &mockRedisReader{
		depths: map[string]*redisdepth.DepthSnapshot{},
	}
	orders := &mockOrderPinger{}
	server := NewServer("8080", redis, orders, sampleMarkets(), zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	server.handleReadyz(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable when no markets ready, got %d", w.Code)
	}
}

func TestReadyz_RedisDown(t *testing.T) {
	redis := &mockRedisReader{
		pingErr: errors.New("connection refused"),
	}
	orders := &mockOrderPinger{}
	server := NewServer("8080", redis, orders, sampleMarkets(), zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	server.handleReadyz(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when Redis is down, got %d", w.Code)
	}
}

func TestReadyz_OrderServiceDown(t *testing.T) {
	redis := &mockRedisReader{
		depths: map[string]*redisdepth.DepthSnapshot{
			"BTC-USDT": freshDepth("BTC-USDT"),
		},
	}
	orders := &mockOrderPinger{
		pingErr: errors.New("order service unavailable"),
	}
	server := NewServer("8080", redis, orders, sampleMarkets(), zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	server.handleReadyz(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when Order Service is down, got %d", w.Code)
	}
}

func TestReadyz_NilOrderClient(t *testing.T) {
	redis := &mockRedisReader{
		depths: map[string]*redisdepth.DepthSnapshot{
			"BTC-USDT": freshDepth("BTC-USDT"),
		},
	}
	server := NewServer("8080", redis, nil, sampleMarkets(), zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	server.handleReadyz(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when Order Service client is nil, got %d", w.Code)
	}
}

func TestReadyz_NilRedisReader(t *testing.T) {
	orders := &mockOrderPinger{}
	server := NewServer("8080", nil, orders, sampleMarkets(), zap.NewNop())

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()

	server.handleReadyz(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when Redis reader is nil, got %d", w.Code)
	}
}
