package recovery_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/orderbook"
	"tradedrift/services/matching-engine/internal/recovery"
)

// mockTestDB provides a controlled pgxConn for approver tests.
type mockTestDB struct {
	reconciliationRecords map[string]string // marketID/gen -> status
	manifestChecksums     map[string]string // marketID/gen -> checksum
	resetGenerations      map[string]string // marketID -> latest reset generation
	queryAuditErr         error
	queryResetErr         error
	commitErr             error
	updateExecErr         error
	ordersBrokenCount     int64
	ordersBuyFills        string
	ordersSellFills       string
	ordersCount           int64
	ordersErr             error
	settlementsCount      int64
	settlementsErr        error
}

func newMockTestDB() *mockTestDB {
	return &mockTestDB{
		reconciliationRecords: make(map[string]string),
		manifestChecksums:     make(map[string]string),
		resetGenerations:      make(map[string]string),
		ordersBuyFills:        "0",
		ordersSellFills:       "0",
	}
}

type mockTestTx struct {
	pgx.Tx
	db *mockTestDB
}

func (m *mockTestDB) Begin(ctx context.Context) (pgx.Tx, error) {
	return &mockTestTx{db: m}, nil
}

func (tx *mockTestTx) Rollback(ctx context.Context) error {
	return nil
}

func (tx *mockTestTx) Commit(ctx context.Context) error {
	if tx.db.commitErr != nil {
		return tx.db.commitErr
	}
	return nil
}

func (tx *mockTestTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if tx.db.updateExecErr != nil {
		return pgconn.CommandTag{}, tx.db.updateExecErr
	}
	if strings.Contains(sql, "UPDATE market_reconciliation_audit") {
		marketID := args[2].(string)
		gen := args[3].(string)
		key := marketID + "/" + gen
		tx.db.reconciliationRecords[key] = "APPROVED"
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	return pgconn.NewCommandTag("OK 1"), nil
}

func (tx *mockTestTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return tx.db.QueryRow(ctx, sql, args...)
}

func (m *mockTestDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("OK 1"), nil
}

func (m *mockTestDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return nil, fmt.Errorf("unmocked query in mockTestDB: %s", sql)
}

func (m *mockTestDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "FROM market_reconciliation_audit") {
		if m.queryAuditErr != nil {
			return &mockTestRow{err: m.queryAuditErr}
		}
		marketID := args[0].(string)
		gen := args[1].(string)
		key := marketID + "/" + gen
		status, ok := m.reconciliationRecords[key]
		if !ok {
			return &mockTestRow{err: pgx.ErrNoRows}
		}
		chk := m.manifestChecksums[key]
		return &mockTestRow{vals: []any{gen, 1, int64(45000), chk, status}}
	}

	if strings.Contains(sql, "FROM market_reset_audit") {
		if m.queryResetErr != nil {
			return &mockTestRow{err: m.queryResetErr}
		}
		marketID := args[0].(string)
		gen, ok := m.resetGenerations[marketID]
		if !ok {
			return &mockTestRow{err: pgx.ErrNoRows}
		}
		return &mockTestRow{vals: []any{gen}}
	}

	if strings.Contains(sql, "FROM orders") {
		if m.ordersErr != nil {
			return &mockTestRow{err: m.ordersErr}
		}
		if strings.Contains(sql, "quantity != (filled_quantity") {
			return &mockTestRow{vals: []any{m.ordersBrokenCount}}
		}
		if strings.Contains(sql, "CASE WHEN side = 'BUY'") {
			buy := m.ordersBuyFills
			sell := m.ordersSellFills
			if buy == "" {
				buy = "0"
			}
			if sell == "" {
				sell = "0"
			}
			return &mockTestRow{vals: []any{buy, sell}}
		}
		if strings.Contains(sql, "COUNT(*)") && !strings.Contains(sql, "status IN") {
			return &mockTestRow{vals: []any{m.ordersCount}}
		}
		return &mockTestRow{vals: []any{m.ordersCount}}
	}

	if strings.Contains(sql, "FROM settled_trades") {
		if m.settlementsErr != nil {
			return &mockTestRow{err: m.settlementsErr}
		}
		if strings.Contains(sql, "quantity <= 0") {
			return &mockTestRow{vals: []any{int64(0)}}
		}
		return &mockTestRow{vals: []any{m.settlementsCount}}
	}

	return &mockTestRow{err: fmt.Errorf("unhandled query: %s", sql)}
}

type mockTestRow struct {
	vals []any
	err  error
}

func (r *mockTestRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, d := range dest {
		switch target := d.(type) {
		case *string:
			*target = r.vals[i].(string)
		case *int:
			*target = r.vals[i].(int)
		case *int64:
			*target = r.vals[i].(int64)
		case *uint64:
			*target = r.vals[i].(uint64)
		}
	}
	return nil
}

type testLeaseChecker struct {
	hasLease bool
}

func (t *testLeaseChecker) HasLease(p int) bool {
	return t.hasLease
}

type testOrderVerifier struct {
	err error
}

func (t *testOrderVerifier) VerifyOrdersReconciled(ctx context.Context, marketID string, manifestChecksum string) error {
	return t.err
}

type testSettlementVerifier struct {
	err error
}

func (t *testSettlementVerifier) VerifySettlementInvariants(ctx context.Context, marketID string) error {
	return t.err
}

const (
	validChecksumA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	validChecksumB = "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"
	testSecret     = "very-secure-admin-secret"
)

// 1. Missing admin secret and unauthorized requests
func TestApprover_AdminSecret_AuthEnforcement(t *testing.T) {
	db := newMockTestDB()
	manager := market.NewMarketManager()
	eth := manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	_ = eth.SetReconciling("initial reset")

	// A: Service with empty admin secret -> rejected
	serviceNoSecret := recovery.NewReconciliationService(db, manager, &testLeaseChecker{hasLease: true}, &testOrderVerifier{}, &testSettlementVerifier{}, "")
	_, err := serviceNoSecret.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-1",
		ManifestChecksum: validChecksumA,
		AuthToken:        "any-token",
	})
	if err == nil || !strings.Contains(err.Error(), "admin secret not configured") {
		t.Fatalf("expected admin secret not configured error, got %v", err)
	}

	// B: Service with configured secret, request missing token -> rejected
	service := recovery.NewReconciliationService(db, manager, &testLeaseChecker{hasLease: true}, &testOrderVerifier{}, &testSettlementVerifier{}, testSecret)
	_, err = service.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-1",
		ManifestChecksum: validChecksumA,
		AuthToken:        "",
	})
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("expected unauthorized error on missing token, got %v", err)
	}

	// C: Request with wrong token -> rejected
	_, err = service.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-1",
		ManifestChecksum: validChecksumA,
		AuthToken:        "wrong-secret",
	})
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("expected unauthorized error on wrong token, got %v", err)
	}
}

// 2. Nil order or settlement verifier
func TestApprover_NilVerifiers_FailClosed(t *testing.T) {
	db := newMockTestDB()
	manager := market.NewMarketManager()
	eth := manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	_ = eth.SetReconciling("initial reset")

	// Nil order verifier
	serviceNilOrder := recovery.NewReconciliationService(db, manager, &testLeaseChecker{hasLease: true}, nil, &testSettlementVerifier{}, testSecret)
	_, err := serviceNilOrder.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-1",
		ManifestChecksum: validChecksumA,
		AuthToken:        testSecret,
	})
	if err == nil || !strings.Contains(err.Error(), "order verifier is nil") {
		t.Fatalf("expected order verifier is nil error, got %v", err)
	}

	// Nil settlement verifier
	serviceNilSettlement := recovery.NewReconciliationService(db, manager, &testLeaseChecker{hasLease: true}, &testOrderVerifier{}, nil, testSecret)
	_, err = serviceNilSettlement.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-1",
		ManifestChecksum: validChecksumA,
		AuthToken:        testSecret,
	})
	if err == nil || !strings.Contains(err.Error(), "settlement verifier is nil") {
		t.Fatalf("expected settlement verifier is nil error, got %v", err)
	}
}

// 3. Verifier query failure and invariant failure
func TestApprover_VerifierFailures_FailClosed(t *testing.T) {
	db := newMockTestDB()
	db.reconciliationRecords["ETH-USDT/gen-1"] = "PENDING"
	db.manifestChecksums["ETH-USDT/gen-1"] = validChecksumA
	db.resetGenerations["ETH-USDT"] = "gen-1"

	manager := market.NewMarketManager()
	eth := manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	_ = eth.SetReconciling("initial reset")

	// A: Order verifier error (e.g. database down or 5 active orders remain)
	serviceOrderErr := recovery.NewReconciliationService(db, manager, &testLeaseChecker{hasLease: true},
		&testOrderVerifier{err: errors.New("Order Service has 5 active nonterminal orders")},
		&testSettlementVerifier{},
		testSecret,
	)
	_, err := serviceOrderErr.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-1",
		ManifestChecksum: validChecksumA,
		AuthToken:        testSecret,
	})
	if err == nil || !strings.Contains(err.Error(), "authoritative order verification failed") {
		t.Fatalf("expected order verification failure, got %v", err)
	}

	// B: Settlement verifier error
	serviceSettlementErr := recovery.NewReconciliationService(db, manager, &testLeaseChecker{hasLease: true},
		&testOrderVerifier{},
		&testSettlementVerifier{err: errors.New("unsettled trades detected: 2 pending fills")},
		testSecret,
	)
	_, err = serviceSettlementErr.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-1",
		ManifestChecksum: validChecksumA,
		AuthToken:        testSecret,
	})
	if err == nil || !strings.Contains(err.Error(), "settlement invariant check failed") {
		t.Fatalf("expected settlement verification failure, got %v", err)
	}
}

// 4. Empty, malformed, and mismatched manifest checksums
func TestApprover_ManifestChecksum_Validation(t *testing.T) {
	db := newMockTestDB()
	db.reconciliationRecords["ETH-USDT/gen-1"] = "PENDING"
	db.manifestChecksums["ETH-USDT/gen-1"] = validChecksumA
	db.resetGenerations["ETH-USDT"] = "gen-1"

	manager := market.NewMarketManager()
	eth := manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	_ = eth.SetReconciling("initial reset")

	service := recovery.NewReconciliationService(db, manager, &testLeaseChecker{hasLease: true}, &testOrderVerifier{}, &testSettlementVerifier{}, testSecret)

	// A: Empty request checksum
	_, err := service.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-1",
		ManifestChecksum: "",
		AuthToken:        testSecret,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid manifest_checksum") {
		t.Fatalf("expected invalid manifest_checksum on empty string, got %v", err)
	}

	// B: Malformed checksum (non-hex characters)
	_, err = service.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-1",
		ManifestChecksum: "zzzb0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		AuthToken:        testSecret,
	})
	if err == nil || !strings.Contains(err.Error(), "invalid manifest_checksum") {
		t.Fatalf("expected invalid manifest_checksum on non-hex string, got %v", err)
	}

	// C: Checksum mismatch (valid SHA-256, but differs from DB record)
	_, err = service.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-1",
		ManifestChecksum: validChecksumB,
		AuthToken:        testSecret,
	})
	if err == nil || !strings.Contains(err.Error(), "manifest checksum mismatch") {
		t.Fatalf("expected manifest checksum mismatch error, got %v", err)
	}

	// D: Valid matching checksum succeeds
	res, err := service.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-1",
		ManifestChecksum: validChecksumA,
		AuthToken:        testSecret,
	})
	if err != nil {
		t.Fatalf("expected approval to succeed with matching checksum, got %v", err)
	}
	if !res.Approved || eth.Mode() != market.ModeLive {
		t.Fatalf("expected engine to be LIVE and approved, got Approved=%v Mode=%v", res.Approved, eth.Mode())
	}
}

// 5. Repeated approval when the engine remains in ModeReconciling
func TestApprover_RepeatedApproval_ActivatesReconcilingEngine(t *testing.T) {
	db := newMockTestDB()
	db.reconciliationRecords["ETH-USDT/gen-1"] = "APPROVED"
	db.manifestChecksums["ETH-USDT/gen-1"] = validChecksumA
	db.resetGenerations["ETH-USDT"] = "gen-1"

	manager := market.NewMarketManager()
	eth := manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	// In-memory engine is still in ModeReconciling (simulating restart or retry after crash)
	_ = eth.SetReconciling("pending live transition")

	service := recovery.NewReconciliationService(db, manager, &testLeaseChecker{hasLease: true}, &testOrderVerifier{}, &testSettlementVerifier{}, testSecret)

	res, err := service.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-1",
		ManifestChecksum: validChecksumA,
		AuthToken:        testSecret,
	})
	if err != nil {
		t.Fatalf("repeated approval failed: %v", err)
	}
	if !res.AlreadyApproved {
		t.Fatalf("expected AlreadyApproved=true, got %+v", res)
	}
	if eth.Mode() != market.ModeLive {
		t.Fatalf("expected engine to be activated to ModeLive on retry, got %v", eth.Mode())
	}
}

// 6. Concurrent block / fatal halt during approval
func TestApprover_ConcurrentBlockOrHalt_Rejected(t *testing.T) {
	db := newMockTestDB()
	db.reconciliationRecords["ETH-USDT/gen-1"] = "PENDING"
	db.manifestChecksums["ETH-USDT/gen-1"] = validChecksumA
	db.resetGenerations["ETH-USDT"] = "gen-1"

	manager := market.NewMarketManager()
	eth := manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	_ = eth.SetReconciling("pending")

	service := recovery.NewReconciliationService(db, manager, &testLeaseChecker{hasLease: true}, &testOrderVerifier{}, &testSettlementVerifier{}, testSecret)

	// Block the engine
	eth.SetBlocked(errors.New("partition gap detected"))

	_, err := service.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-1",
		ManifestChecksum: validChecksumA,
		AuthToken:        testSecret,
	})
	if err == nil || !strings.Contains(err.Error(), "engine for ETH-USDT is blocked") {
		t.Fatalf("expected blocked engine error, got %v", err)
	}

	// Test fatal halt
	eth2 := manager.Add(market.MarketConfig{MarketID: "SOL-USDT", Partition: 2})
	_ = eth2.SetReconciling("pending")
	eth2.TriggerFatalHalt()

	_, err = service.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "SOL-USDT",
		Generation:       "gen-1",
		ManifestChecksum: validChecksumA,
		AuthToken:        testSecret,
	})
	if err == nil || !strings.Contains(err.Error(), "engine for SOL-USDT is halted") {
		t.Fatalf("expected halted engine error, got %v", err)
	}
}

// 7. Lease loss during approval
func TestApprover_LeaseLoss_FailsClosed(t *testing.T) {
	db := newMockTestDB()
	db.reconciliationRecords["ETH-USDT/gen-1"] = "PENDING"
	db.manifestChecksums["ETH-USDT/gen-1"] = validChecksumA
	db.resetGenerations["ETH-USDT"] = "gen-1"

	manager := market.NewMarketManager()
	eth := manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	_ = eth.SetReconciling("pending")

	// Lease checker reports false
	service := recovery.NewReconciliationService(db, manager, &testLeaseChecker{hasLease: false}, &testOrderVerifier{}, &testSettlementVerifier{}, testSecret)

	_, err := service.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-1",
		ManifestChecksum: validChecksumA,
		AuthToken:        testSecret,
	})
	if err == nil || !strings.Contains(err.Error(), "lease loss") {
		t.Fatalf("expected lease loss error, got %v", err)
	}
}

// 8. Concrete Postgres verifiers unit test
func TestConcretePostgresVerifiers(t *testing.T) {
	db := newMockTestDB()

	// Order Verifier
	db.ordersCount = 0
	orderVerifier := recovery.NewPostgresOrderVerifier(db)
	if err := orderVerifier.VerifyOrdersReconciled(context.Background(), "ETH-USDT", validChecksumA); err != nil {
		t.Fatalf("expected order verifier to pass with 0 active orders, got %v", err)
	}

	db.ordersCount = 3
	if err := orderVerifier.VerifyOrdersReconciled(context.Background(), "ETH-USDT", validChecksumA); err == nil {
		t.Fatal("expected order verifier to fail with 3 active orders, got nil")
	}

	db.ordersErr = errors.New("db connection failure")
	if err := orderVerifier.VerifyOrdersReconciled(context.Background(), "ETH-USDT", validChecksumA); err == nil {
		t.Fatal("expected order verifier to fail on query error, got nil")
	}
	db.ordersErr = nil

	// Fill quantity discrepancy test: BUY != SELL
	db.ordersCount = 0
	db.ordersBuyFills = "10.5"
	db.ordersSellFills = "9.5"
	if err := orderVerifier.VerifyOrdersReconciled(context.Background(), "ETH-USDT", validChecksumA); err == nil || !strings.Contains(err.Error(), "financial invariant violation") {
		t.Fatalf("expected fill quantity discrepancy error, got %v", err)
	}

	// Order quantity integrity violation: quantity != filled + remaining
	db.ordersBuyFills = "10.0"
	db.ordersSellFills = "10.0"
	db.ordersBrokenCount = 2
	if err := orderVerifier.VerifyOrdersReconciled(context.Background(), "ETH-USDT", validChecksumA); err == nil || !strings.Contains(err.Error(), "order quantity invariant violation") {
		t.Fatalf("expected broken order quantity error, got %v", err)
	}
	db.ordersBrokenCount = 0

	// Manifest checksum mismatch test
	db.ordersBuyFills = "0"
	db.ordersSellFills = "0"
	mismatchedChecksum := "0000000000000000000000000000000000000000000000000000000000000000"
	if err := orderVerifier.VerifyOrdersReconciled(context.Background(), "ETH-USDT", mismatchedChecksum); err == nil || !strings.Contains(err.Error(), "manifest checksum verification failed") {
		t.Fatalf("expected manifest checksum verification failure, got %v", err)
	}

	// Settlement Verifier
	db.settlementsCount = 0
	settlementVerifier := recovery.NewPostgresSettlementVerifier(db)
	if err := settlementVerifier.VerifySettlementInvariants(context.Background(), "ETH-USDT"); err != nil {
		t.Fatalf("expected settlement verifier to pass with 0 pending settlements, got %v", err)
	}

	db.settlementsCount = 1
	if err := settlementVerifier.VerifySettlementInvariants(context.Background(), "ETH-USDT"); err == nil {
		t.Fatal("expected settlement verifier to fail with 1 pending settlement, got nil")
	}
}

func TestApprover_NilLeaseChecker_FailsClosed(t *testing.T) {
	db := newMockTestDB()
	manager := market.NewMarketManager()
	eth := manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	_ = eth.SetReconciling("initial reset")

	serviceNilLease := recovery.NewReconciliationService(db, manager, nil, &testOrderVerifier{}, &testSettlementVerifier{}, testSecret)

	// ValidateDependencies must fail
	if err := serviceNilLease.ValidateDependencies(); err == nil || !strings.Contains(err.Error(), "lease checker is missing") {
		t.Fatalf("expected ValidateDependencies to fail with lease checker missing, got %v", err)
	}

	// ApproveReconciliation must fail closed
	_, err := serviceNilLease.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-1",
		ManifestChecksum: validChecksumA,
		AuthToken:        testSecret,
	})
	if err == nil || !strings.Contains(err.Error(), "lease checker is nil") {
		t.Fatalf("expected lease checker is nil error, got %v", err)
	}
}

// 9. Two markets sharing a partition with different expected log-end offsets must fail closed
type mockPartitionAuthorizer struct {
	expectedOffsets map[string]int64
}

func (m *mockPartitionAuthorizer) AuthorizeEmptyReset(ctx context.Context, marketID string, partition int, logEndOffset int64) (recovery.ResetAuthResult, error) {
	exp, ok := m.expectedOffsets[marketID]
	var pExp *int64
	if ok {
		pExp = &exp
	}
	return recovery.ResetAuthResult{
		Authorized:           true,
		Generation:           "gen-" + marketID,
		TokenFingerprint:     "fingerprint",
		ExpectedLogEndOffset: pExp,
	}, nil
}

func TestReset_MismatchedPartitionLogEndBoundary_FailsClosed(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{MarketID: "BTC-USDT", Partition: 0})
	_ = manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 0})

	db := newMockDB()
	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.SetLeaseChecker(&testLeaseChecker{hasLease: true})
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{0}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 1000, nil },
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) { return 0, nil })

	// BTC-USDT expects 500, ETH-USDT expects 600 on the same partition 0
	authorizer := &mockPartitionAuthorizer{
		expectedOffsets: map[string]int64{
			"BTC-USDT": 500,
			"ETH-USDT": 600,
		},
	}
	replayer.SetResetAuthorizer(authorizer)

	var wg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &wg)
	if err == nil || !strings.Contains(err.Error(), "mismatched partition reset boundary") {
		t.Fatalf("expected mismatched partition reset boundary error, got %v", err)
	}

	// Neither engine should have been reset
	btc := manager.Get("BTC-USDT")
	eth := manager.Get("ETH-USDT")
	if btc.Mode() == market.ModeLive || eth.Mode() == market.ModeLive {
		t.Fatal("expected markets NOT to be LIVE after mismatched boundary rejection")
	}
}

// 10. Restart with stale or mismatched approval generation recovers into ModeReconciling
func TestRestart_StaleOrMismatchedApprovalGeneration_RecoversIntoModeReconciling(t *testing.T) {
	db := newMockDB()
	db.checkpoints["orders.commands/1"] = 43562
	db.marketSequences["ETH-USDT"] = 28691
	// Active reset record is for generation gen-active-2
	db.auditRecords["ETH-USDT/gen-active-2"] = 43563
	// Stale approved record in reconciliation table is for older generation gen-stale-1
	db.reconciliationRecords["ETH-USDT/gen-stale-1"] = "APPROVED"
	db.manifestChecksums["ETH-USDT/gen-stale-1"] = validChecksumA

	emptySnap := orderbook.BookSnapshot{
		SchemaVersion: orderbook.CurrentSchemaVersion,
		MarketID:      "ETH-USDT",
		Partition:     1,
		Offset:        43562,
		Sequence:      28691,
		Orders:        []orderbook.SnapshotOrder{},
	}
	snapJSON, _ := json.Marshal(emptySnap)
	snapChecksum, _ := orderbook.Checksum(emptySnap)
	db.snapshots["ETH-USDT"] = snapJSON
	db.snapshotsOffset["ETH-USDT"] = 43562
	db.snapshotsCheck["ETH-USDT"] = snapChecksum

	manager := market.NewMarketManager()
	eth := manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil })

	var wg sync.WaitGroup
	if err := replayer.ReplayAll(context.Background(), &wg); err != nil {
		t.Fatalf("replayer failed: %v", err)
	}

	if eth.Mode() != market.ModeReconciling {
		t.Fatalf("expected engine to recover into ModeReconciling on stale approval generation, got %v", eth.Mode())
	}
	if eth.Mode() == market.ModeLive {
		t.Fatal("critical failure: engine recovered into LIVE mode with stale approval generation!")
	}
}

// 11. Crash after database approval commit and before in-memory activation recovers safely
func TestRestart_CrashAfterApprovalCommit_RevalidatesInvariantsAndActivates(t *testing.T) {
	db := newMockDB()
	db.checkpoints["orders.commands/1"] = 43562
	db.marketSequences["ETH-USDT"] = 28691
	// Reset audit matches reconciliation audit generation
	db.auditRecords["ETH-USDT/gen-active-3"] = 43563
	db.reconciliationRecords["ETH-USDT/gen-active-3"] = "APPROVED"
	db.manifestChecksums["ETH-USDT/gen-active-3"] = validChecksumA

	emptySnap := orderbook.BookSnapshot{
		SchemaVersion: orderbook.CurrentSchemaVersion,
		MarketID:      "ETH-USDT",
		Partition:     1,
		Offset:        43562,
		Sequence:      28691,
		Orders:        []orderbook.SnapshotOrder{},
	}
	snapJSON, _ := json.Marshal(emptySnap)
	snapChecksum, _ := orderbook.Checksum(emptySnap)
	db.snapshots["ETH-USDT"] = snapJSON
	db.snapshotsOffset["ETH-USDT"] = 43562
	db.snapshotsCheck["ETH-USDT"] = snapChecksum

	manager := market.NewMarketManager()
	eth := manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil })

	var wg sync.WaitGroup
	if err := replayer.ReplayAll(context.Background(), &wg); err != nil {
		t.Fatalf("replayer failed: %v", err)
	}

	if eth.Mode() != market.ModeLive {
		t.Fatalf("expected engine to recover into ModeLive when approval matches active reset generation, got %v", eth.Mode())
	}
}

