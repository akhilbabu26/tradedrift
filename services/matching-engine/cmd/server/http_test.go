package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/recovery"
)

func setupTestServer() (*market.MarketManager, *atomic.Bool, *atomic.Bool, *http.Server) {
	manager := market.NewMarketManager()

	btc := manager.Add(market.MarketConfig{
		MarketID:  "BTC-USDT",
		TickSize:  decimal.RequireFromString("0.01"),
		LotSize:   decimal.RequireFromString("0.001"),
		Partition: 0,
	})
	_ = btc.SetLive()

	eth := manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		TickSize:  decimal.RequireFromString("0.01"),
		LotSize:   decimal.RequireFromString("0.001"),
		Partition: 1,
	})
	eth.SetBlocked(errors.New("retention gap at offset 43562"))

	sol := manager.Add(market.MarketConfig{
		MarketID:  "SOL-USDT",
		TickSize:  decimal.RequireFromString("0.01"),
		LotSize:   decimal.RequireFromString("0.001"),
		Partition: 2,
	})
	_ = sol.SetLive()

	var isReady atomic.Bool
	isReady.Store(true)

	var isDegraded atomic.Bool

	server := newHTTPServer(":0", manager, &isReady, &isDegraded)
	return manager, &isReady, &isDegraded, server
}

func TestReadyz_MarketSpecificEndpoints(t *testing.T) {
	_, _, _, server := setupTestServer()

	// 1. Live market: BTC-USDT -> 200 OK
	{
		req := httptest.NewRequest(http.MethodGet, "/readyz?market=BTC-USDT", nil)
		rec := httptest.NewRecorder()
		server.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 for live BTC-USDT, got %d", rec.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if body["status"] != "ready" || body["mode"] != "LIVE" || body["market_id"] != "BTC-USDT" {
			t.Fatalf("unexpected body for live market: %v", body)
		}
	}

	// 2. Blocked market: ETH-USDT -> 503 Service Unavailable
	{
		req := httptest.NewRequest(http.MethodGet, "/readyz?market=ETH-USDT", nil)
		rec := httptest.NewRecorder()
		server.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 for blocked ETH-USDT, got %d", rec.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if body["status"] != "not_ready" || body["mode"] != "BLOCKED" || body["market_id"] != "ETH-USDT" {
			t.Fatalf("unexpected body for blocked market: %v", body)
		}
		if body["reason"] == nil || body["reason"] == "" {
			t.Fatal("expected non-empty reason for blocked market")
		}
	}

	// 3. Unknown market -> 404 Not Found
	{
		req := httptest.NewRequest(http.MethodGet, "/readyz?market=DOGE-USDT", nil)
		rec := httptest.NewRecorder()
		server.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for unknown market, got %d", rec.Code)
		}
	}
}

func TestReadyz_GlobalEndpoints(t *testing.T) {
	manager, isReady, _, server := setupTestServer()

	// 1. Partially degraded state: ETH-USDT is BLOCKED, BTC and SOL are LIVE -> 503 Service Unavailable (degraded)
	{
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		server.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 for degraded state (blocked market present), got %d", rec.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if body["status"] != "degraded" {
			t.Fatalf("expected status 'degraded', got %v", body["status"])
		}
		blocked, ok := body["blocked_markets"].([]any)
		if !ok || len(blocked) != 1 || blocked[0] != "ETH-USDT" {
			t.Fatalf("expected blocked_markets=['ETH-USDT'], got %v", body["blocked_markets"])
		}
	}

	// 2. One LIVE market and one RECONCILING market -> 503 Service Unavailable (degraded)
	{
		mRecon := market.NewMarketManager()
		_ = mRecon.Add(market.MarketConfig{MarketID: "BTC-USDT"}).SetLive()
		ethEngine := mRecon.Add(market.MarketConfig{MarketID: "ETH-USDT"})
		_ = ethEngine.SetReconciling("gen-test-1")
		serverRecon := newHTTPServer(":0", mRecon, isReady, nil)

		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		serverRecon.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 for reconciling degraded state, got %d", rec.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if body["status"] != "degraded" {
			t.Fatalf("expected status 'degraded', got %v", body["status"])
		}
		reconciling, ok := body["reconciling_markets"].([]any)
		if !ok || len(reconciling) != 1 || reconciling[0] != "ETH-USDT" {
			t.Fatalf("expected reconciling_markets=['ETH-USDT'], got %v", body["reconciling_markets"])
		}
	}

	// 3. All markets blocked -> 503 Service Unavailable (not_ready, liveCount == 0)
	{
		mAllBlocked := market.NewMarketManager()
		b1 := mAllBlocked.Add(market.MarketConfig{MarketID: "BTC-USDT"})
		b1.SetBlocked(errors.New("gap 1"))
		b2 := mAllBlocked.Add(market.MarketConfig{MarketID: "ETH-USDT"})
		b2.SetBlocked(errors.New("gap 2"))
		serverAllBlocked := newHTTPServer(":0", mAllBlocked, isReady, nil)

		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		serverAllBlocked.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 when all markets blocked, got %d", rec.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if body["status"] != "not_ready" {
			t.Fatalf("expected status 'not_ready', got %v", body["status"])
		}
	}

	// 4. All markets recovering -> 503 Service Unavailable (not_ready, recoveryCount > 0)
	{
		mRecovering := market.NewMarketManager()
		_ = mRecovering.Add(market.MarketConfig{MarketID: "BTC-USDT"}) // default mode is ModeRecovery
		_ = mRecovering.Add(market.MarketConfig{MarketID: "ETH-USDT"})
		serverRecovering := newHTTPServer(":0", mRecovering, isReady, nil)

		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		serverRecovering.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 when all recovering, got %d", rec.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if body["status"] != "not_ready" {
			t.Fatalf("expected status 'not_ready', got %v", body["status"])
		}
	}

	// 5. Not ready when isReady is false (startup / replaying) -> 503
	{
		isReady.Store(false)
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		server.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 when isReady=false, got %d", rec.Code)
		}
		isReady.Store(true)
	}

	// 6. All markets live -> 200 OK ready
	{
		managerAll := market.NewMarketManager()
		_ = managerAll.Add(market.MarketConfig{MarketID: "BTC-USDT"}).SetLive()
		_ = managerAll.Add(market.MarketConfig{MarketID: "SOL-USDT"}).SetLive()
		serverAll := newHTTPServer(":0", managerAll, isReady, nil)

		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		serverAll.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 when all live, got %d", rec.Code)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if body["status"] != "ready" {
			t.Fatalf("expected status 'ready', got %v", body["status"])
		}
	}

	// 7. Liveness (/healthz) remains 200 OK even when a market is BLOCKED
	{
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		server.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for /healthz liveness even when degraded, got %d", rec.Code)
		}
	}
	_ = manager
}

// -------------------------------------------------------------------------
// Reconciliation HTTP Endpoint Tests (Phase 2 Verification)
// -------------------------------------------------------------------------

type mockHttpDB struct {
	reconciliationRecords map[string]string // marketID/gen -> status
	manifestChecksums     map[string]string // marketID/gen -> checksum
	resetGenerations      map[string]string // marketID -> latest reset generation
}

type mockHttpTx struct {
	pgx.Tx
	db *mockHttpDB
}

func (m *mockHttpDB) Begin(ctx context.Context) (pgx.Tx, error) {
	return &mockHttpTx{db: m}, nil
}

func (tx *mockHttpTx) Rollback(ctx context.Context) error {
	return nil
}

func (tx *mockHttpTx) Commit(ctx context.Context) error {
	return nil
}

func (tx *mockHttpTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "UPDATE market_reconciliation_audit") {
		marketID := args[2].(string)
		gen := args[3].(string)
		key := marketID + "/" + gen
		tx.db.reconciliationRecords[key] = "APPROVED"
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	return pgconn.NewCommandTag("OK 1"), nil
}

func (tx *mockHttpTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return tx.db.QueryRow(ctx, sql, args...)
}

func (m *mockHttpDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("OK 1"), nil
}

func (m *mockHttpDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "FROM market_reconciliation_audit") {
		marketID := args[0].(string)
		gen := args[1].(string)
		key := marketID + "/" + gen
		status, ok := m.reconciliationRecords[key]
		if !ok {
			return &mockHttpRow{err: pgx.ErrNoRows}
		}
		chk := m.manifestChecksums[key]
		return &mockHttpRow{vals: []any{gen, 1, int64(45000), chk, status}}
	}

	if strings.Contains(sql, "FROM market_reset_audit") {
		marketID := args[0].(string)
		gen, ok := m.resetGenerations[marketID]
		if !ok {
			return &mockHttpRow{err: pgx.ErrNoRows}
		}
		return &mockHttpRow{vals: []any{gen}}
	}

	return &mockHttpRow{err: fmt.Errorf("unmocked query: %s", sql)}
}

func (m *mockHttpDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unmocked Query in mockHttpDB: %s", sql)
}

type mockHttpRow struct {
	vals []any
	err  error
}

func (r *mockHttpRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, d := range dest {
		if i < len(r.vals) {
			switch ptr := d.(type) {
			case *string:
				if s, ok := r.vals[i].(string); ok {
					*ptr = s
				}
			case *int:
				if v, ok := r.vals[i].(int); ok {
					*ptr = v
				}
			case *int64:
				if v, ok := r.vals[i].(int64); ok {
					*ptr = v
				}
			case *uint64:
				if v, ok := r.vals[i].(uint64); ok {
					*ptr = v
				}
			}
		}
	}
	return nil
}

type mockHttpOrderVerifier struct{}

func (m *mockHttpOrderVerifier) VerifyOrdersReconciled(ctx context.Context, marketID string, manifestChecksum string) error {
	return nil
}

type mockHttpSettlementVerifier struct{}

func (m *mockHttpSettlementVerifier) VerifySettlementInvariants(ctx context.Context, marketID string) error {
	return nil
}

type mockHttpLeaseChecker struct{}

func (m *mockHttpLeaseChecker) HasLease(partition int) bool {
	return true
}

func TestApproveReconciliation_NotConfigured_Returns501(t *testing.T) {
	manager := market.NewMarketManager()
	var isReady atomic.Bool
	isReady.Store(true)

	// Server constructed without reconService
	server := newHTTPServer(":0", manager, &isReady, nil)

	req := httptest.NewRequest(http.MethodPost, "/admin/markets/ETH-USDT/approve-reconciliation", bytes.NewReader([]byte("{}")))
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 Not Implemented, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp["reason"] != "reconciliation service not configured" {
		t.Fatalf("unexpected reason: %v", resp["reason"])
	}
}

func TestApproveReconciliation_WiredAndProtected_HTTPIntegration(t *testing.T) {
	manager := market.NewMarketManager()
	eth := manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		TickSize:  decimal.RequireFromString("0.01"),
		LotSize:   decimal.RequireFromString("0.001"),
		Partition: 1,
	})
	if err := eth.SetReconciling("reset-baseline-captured"); err != nil {
		t.Fatalf("SetReconciling error: %v", err)
	}

	secret := "test-admin-secret-configured-32bytes!!"
	validChecksum := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	db := &mockHttpDB{
		reconciliationRecords: map[string]string{
			"ETH-USDT/gen-http-1": "PENDING",
		},
		manifestChecksums: map[string]string{
			"ETH-USDT/gen-http-1": validChecksum,
		},
		resetGenerations: map[string]string{
			"ETH-USDT": "gen-http-1",
		},
	}

	reconService := recovery.NewReconciliationService(
		db,
		manager,
		&mockHttpLeaseChecker{},
		&mockHttpOrderVerifier{},
		&mockHttpSettlementVerifier{},
		secret,
	)

	var isReady atomic.Bool
	isReady.Store(true)

	// Server constructed with wired reconService
	server := newHTTPServer(":0", manager, &isReady, nil, reconService)

	// 1. Missing Authorization header -> 401 Unauthorized
	{
		body := `{"generation":"gen-http-1","manifest_checksum":"` + validChecksum + `","manifest_order_count":98,"manifest_total_releasable_usd":293825.0,"operator_id":"op-1"}`
		req := httptest.NewRequest(http.MethodPost, "/admin/markets/ETH-USDT/approve-reconciliation", strings.NewReader(body))
		rec := httptest.NewRecorder()
		server.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized for missing secret, got %d (body: %s)", rec.Code, rec.Body.String())
		}
	}

	// 2. Invalid Bearer Token -> 401 Unauthorized
	{
		body := `{"generation":"gen-http-1","manifest_checksum":"` + validChecksum + `","manifest_order_count":98,"manifest_total_releasable_usd":293825.0,"operator_id":"op-1"}`
		req := httptest.NewRequest(http.MethodPost, "/admin/markets/ETH-USDT/approve-reconciliation", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer invalid-wrong-secret")
		rec := httptest.NewRecorder()
		server.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized for invalid secret, got %d (body: %s)", rec.Code, rec.Body.String())
		}
	}

	// 3. Malformed JSON Body -> 400 Bad Request
	{
		req := httptest.NewRequest(http.MethodPost, "/admin/markets/ETH-USDT/approve-reconciliation", strings.NewReader("{invalid-json"))
		req.Header.Set("Authorization", "Bearer "+secret)
		rec := httptest.NewRecorder()
		server.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request for invalid json, got %d (body: %s)", rec.Code, rec.Body.String())
		}
	}

	// 4. Malformed Manifest Checksum (not 64-char hex) -> 422 Unprocessable Entity
	{
		body := `{"generation":"gen-http-1","manifest_checksum":"bad-checksum","manifest_order_count":98,"manifest_total_releasable_usd":293825.0,"operator_id":"op-1"}`
		req := httptest.NewRequest(http.MethodPost, "/admin/markets/ETH-USDT/approve-reconciliation", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+secret)
		rec := httptest.NewRecorder()
		server.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 Unprocessable Entity for bad checksum, got %d (body: %s)", rec.Code, rec.Body.String())
		}
	}

	// 5. Valid Secret, Valid Checksum, Valid Invariants -> 200 OK & Market Becomes LIVE
	{
		body := `{"generation":"gen-http-1","manifest_checksum":"` + validChecksum + `","manifest_order_count":98,"manifest_total_releasable_usd":293825.0,"operator_id":"op-1"}`
		req := httptest.NewRequest(http.MethodPost, "/admin/markets/ETH-USDT/approve-reconciliation", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+secret)
		rec := httptest.NewRecorder()
		server.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d (body: %s)", rec.Code, rec.Body.String())
		}
		var resp recovery.ReconciliationApprovalResult
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal error: %v", err)
		}
		if resp.Status != "APPROVED" || resp.MarketID != "ETH-USDT" || resp.Generation != "gen-http-1" {
			t.Fatalf("unexpected approval result: %+v", resp)
		}
		if eth.Mode() != market.ModeLive {
			t.Fatalf("expected market ETH-USDT to be ModeLive, got %v", eth.Mode())
		}
	}
}
