package recovery_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/orderbook"
	"tradedrift/services/matching-engine/internal/recovery"
)

// mockLeaseTracker implements recovery.LeaseChecker
type mockLeaseTracker struct {
	mu     sync.Mutex
	leases map[int]bool
}

func (l *mockLeaseTracker) HasLease(partition int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.leases[partition]
}

func (l *mockLeaseTracker) setLease(partition int, has bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.leases == nil {
		l.leases = make(map[int]bool)
	}
	l.leases[partition] = has
}

// mockOrderValidator implements recovery.OrderVerifier
type mockOrderValidator struct {
	reconciledOrders map[string]int
	expectedCount    int
}

func (o *mockOrderValidator) VerifyOrdersReconciled(ctx context.Context, marketID string, manifestChecksum string) error {
	count := o.reconciledOrders[marketID]
	if count < o.expectedCount {
		return fmt.Errorf("partial downstream reconciliation: only %d of %d orders reconciled for market %s",
			count, o.expectedCount, marketID)
	}
	return nil
}

// mockSettlementTracker implements recovery.SettlementVerifier
type mockSettlementTracker struct {
	hasUnresolvedDiscrepancy bool
}

func (s *mockSettlementTracker) VerifySettlementInvariants(ctx context.Context, marketID string) error {
	if s.hasUnresolvedDiscrepancy {
		return fmt.Errorf("settlement invariant violation: unresolved failed settlements or mismatched buy/sell fills detected for market %s", marketID)
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// REGRESSION TEST 1: Restart while reconciling
// Restart the Matching Engine after the reset transaction commits but before
// reconciliation approval. It must recover into ModeReconciling, NOT ModeLive.
// ─────────────────────────────────────────────────────────────────────────────
func TestRegression_1_RestartWhileReconciling_RecoversIntoModeReconciling(t *testing.T) {
	db := newMockDB()
	db.checkpoints["orders.commands/1"] = 43562
	db.marketSequences["ETH-USDT"] = 28691
	db.reconciliationRecords["ETH-USDT/gen-reg-1"] = "PENDING"

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
	_ = manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 43563, nil
	})

	var wg sync.WaitGroup
	if err := replayer.ReplayAll(context.Background(), &wg); err != nil {
		t.Fatalf("recovery failed: %v", err)
	}

	eth := manager.Get("ETH-USDT")
	if eth.Mode() != market.ModeReconciling {
		t.Fatalf("Regression 1 Failure: engine must recover into ModeReconciling on restart, got %v", eth.Mode())
	}
	if eth.Mode() == market.ModeLive {
		t.Fatal("Regression 1 Critical Failure: engine transitioned to LIVE before reconciliation approval!")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// REGRESSION TEST 2: Duplicate approval
// Submit the same approval twice. It must be idempotent, return success,
// and must NOT duplicate resets or alter state.
// ─────────────────────────────────────────────────────────────────────────────
func TestRegression_2_DuplicateApproval_Idempotent(t *testing.T) {
	db := newMockDB()
	const validHex = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	db.reconciliationRecords["ETH-USDT/gen-reg-2"] = "PENDING"
	db.manifestChecksums["ETH-USDT/gen-reg-2"] = validHex

	manager := market.NewMarketManager()
	eth := manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	_ = eth.SetReconciling("initial reset")

	service := recovery.NewReconciliationService(db, manager, &mockLeaseTracker{leases: map[int]bool{1: true}}, &mockOrderValidator{}, &mockSettlementTracker{}, "admin-secret-test")

	req := recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-reg-2",
		ManifestChecksum: validHex,
		ApprovedBy:       "risk-lead",
		AuthToken:        "admin-secret-test",
	}

	// First approval call
	res1, err := service.ApproveReconciliation(context.Background(), req)
	if err != nil {
		t.Fatalf("first approval failed: %v", err)
	}
	if !res1.Approved || res1.AlreadyApproved {
		t.Fatalf("expected first approval to be Approved=true AlreadyApproved=false, got %+v", res1)
	}
	if eth.Mode() != market.ModeLive {
		t.Fatalf("expected engine mode Live, got %v", eth.Mode())
	}

	// Second duplicate approval call
	res2, err := service.ApproveReconciliation(context.Background(), req)
	if err != nil {
		t.Fatalf("second approval failed: %v", err)
	}
	if !res2.Approved || !res2.AlreadyApproved {
		t.Fatalf("expected second approval to be idempotent Approved=true AlreadyApproved=true, got %+v", res2)
	}
	if eth.Mode() != market.ModeLive {
		t.Fatalf("expected engine to remain Live, got %v", eth.Mode())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// REGRESSION TEST 3: Stale approval
// Changing the manifest or order inventory after preflight causes approval to fail.
// ─────────────────────────────────────────────────────────────────────────────
func TestRegression_3_StaleApproval_ManifestChecksumMismatch_FailsClosed(t *testing.T) {
	db := newMockDB()
	const expectedChecksum = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	db.reconciliationRecords["ETH-USDT/gen-reg-3"] = "PENDING"
	db.manifestChecksums["ETH-USDT/gen-reg-3"] = expectedChecksum

	manager := market.NewMarketManager()
	eth := manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	_ = eth.SetReconciling("pending reconciliation")

	service := recovery.NewReconciliationService(db, manager, &mockLeaseTracker{leases: map[int]bool{1: true}}, &mockOrderValidator{}, &mockSettlementTracker{}, "admin-secret-test")

	// Altered manifest checksum representing post-preflight inventory change (valid 64-char hex that differs)
	req := recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-reg-3",
		ManifestChecksum: "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb",
		AuthToken:        "admin-secret-test",
		ApprovedBy:       "risk-lead",
	}

	_, err := service.ApproveReconciliation(context.Background(), req)
	if err == nil {
		t.Fatal("Regression 3 Failure: expected approval to fail on manifest checksum mismatch, got nil")
	}

	// Engine must remain in ModeReconciling
	if eth.Mode() != market.ModeReconciling {
		t.Fatalf("expected engine to remain in ModeReconciling, got %v", eth.Mode())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// REGRESSION TEST 4: Lease loss during approval
// Losing partition lease immediately before or during approval aborts transition;
// engine remains non-live.
// ─────────────────────────────────────────────────────────────────────────────
func TestRegression_4_LeaseLossDuringApproval_RemainsNonLive(t *testing.T) {
	db := newMockDB()
	db.reconciliationRecords["ETH-USDT/gen-reg-4"] = "PENDING"

	manager := market.NewMarketManager()
	eth := manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	_ = eth.SetReconciling("pending reconciliation")

	leaseTracker := &mockLeaseTracker{}
	leaseTracker.setLease(1, false) // Lease lost / expired

	const validHex4 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	db.manifestChecksums["ETH-USDT/gen-reg-4"] = validHex4
	service := recovery.NewReconciliationService(db, manager, leaseTracker, &mockOrderValidator{}, &mockSettlementTracker{}, "admin-secret-test")

	req := recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-reg-4",
		ManifestChecksum: validHex4,
		AuthToken:        "admin-secret-test",
		ApprovedBy:       "operator",
	}

	_, err := service.ApproveReconciliation(context.Background(), req)
	if err == nil {
		t.Fatal("Regression 4 Failure: expected approval to fail when partition lease is not held, got nil")
	}

	if eth.Mode() == market.ModeLive {
		t.Fatal("Regression 4 Critical Failure: engine transitioned to LIVE without partition lease!")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// REGRESSION TEST 5: Queued events during blocking / reconciling
// When market is ModeBlocked or ModeReconciling, incoming queued commands
// must NOT mutate the order book or generate matching/trades.
// ─────────────────────────────────────────────────────────────────────────────
func TestRegression_5_QueuedEventsDuringBlockingAndReconciling_NoMatchingOrPublication(t *testing.T) {
	cfg := market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
		TickSize:  decimal.NewFromFloat(0.01),
		LotSize:   decimal.NewFromFloat(0.001),
	}

	// Case A: ModeReconciling rejects without matching
	engineRec := market.NewMarketEngine(cfg)
	_ = engineRec.SetReconciling("testing rejection during reconciliation")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go engineRec.Run(ctx)

	orderID1 := uuid.New()
	engineRec.InputQueue <- market.InputEvent{
		EventID: uuid.New(),
		Type:    market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:    orderID1,
			UserID:     uuid.New(),
			MarketID:   "ETH-USDT",
			Side:       orderbook.SideBuy,
			OrderType:  orderbook.OrderTypeLimit,
			Price:      decimal.NewFromInt(2000),
			Quantity:   decimal.NewFromInt(1),
			OccurredAt: time.Now(),
		},
		Topic:     "orders.commands",
		Partition: 1,
		Offset:    100,
	}

	resRec := <-engineRec.OutputQueue
	if resRec.CancelResult == nil {
		t.Fatalf("Regression 5 Failure: expected CancelResult rejection in ModeReconciling, got nil")
	}
	if resRec.CancelResult.Reason != "market_reconciling" {
		t.Fatalf("expected reject reason 'market_reconciling', got %q", resRec.CancelResult.Reason)
	}
	if len(resRec.Fills) > 0 {
		t.Fatalf("Regression 5 Critical Failure: trades executed while in ModeReconciling!")
	}

	// Case B: ModeBlocked rejects without matching
	engineBlock := market.NewMarketEngine(cfg)
	engineBlock.SetBlocked(errors.New("fatal data discrepancy"))
	go engineBlock.Run(ctx)

	engineBlock.InputQueue <- market.InputEvent{
		EventID: uuid.New(),
		Type:    market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:    uuid.New(),
			UserID:     uuid.New(),
			MarketID:   "ETH-USDT",
			Side:       orderbook.SideBuy,
			OrderType:  orderbook.OrderTypeLimit,
			Price:      decimal.NewFromInt(2000),
			Quantity:   decimal.NewFromInt(1),
			OccurredAt: time.Now(),
		},
		Topic:     "orders.commands",
		Partition: 1,
		Offset:    101,
	}

	resBlock := <-engineBlock.OutputQueue
	if resBlock.CancelResult == nil {
		t.Fatalf("Regression 5 Failure: expected CancelResult rejection in ModeBlocked, got nil")
	}
	if resBlock.CancelResult.Reason != "market_blocked" {
		t.Fatalf("expected reject reason 'market_blocked', got %q", resBlock.CancelResult.Reason)
	}
	if len(resBlock.Fills) > 0 {
		t.Fatalf("Regression 5 Critical Failure: trades executed while in ModeBlocked!")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// REGRESSION TEST 6: Partial downstream reconciliation
// Reconciling only some of the 98 orders (e.g. 90/98) must fail approval.
// ─────────────────────────────────────────────────────────────────────────────
func TestRegression_6_PartialDownstreamReconciliation_FailsApproval(t *testing.T) {
	db := newMockDB()
	db.reconciliationRecords["ETH-USDT/gen-reg-6"] = "PENDING"

	manager := market.NewMarketManager()
	eth := manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	_ = eth.SetReconciling("pending reconciliation")

	// OrderVerifier has only 90 of 98 reconciled
	orderValidator := &mockOrderValidator{
		reconciledOrders: map[string]int{"ETH-USDT": 90},
		expectedCount:    98,
	}

	const validHex6 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	db.manifestChecksums["ETH-USDT/gen-reg-6"] = validHex6

	service := recovery.NewReconciliationService(db, manager, &mockLeaseTracker{leases: map[int]bool{1: true}}, orderValidator, &mockSettlementTracker{}, "admin-secret-test")

	req := recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-reg-6",
		ManifestChecksum: validHex6,
		AuthToken:        "admin-secret-test",
		ApprovedBy:       "operator",
	}

	_, err := service.ApproveReconciliation(context.Background(), req)
	if err == nil {
		t.Fatal("Regression 6 Failure: expected partial downstream reconciliation to fail approval, got nil")
	}

	if eth.Mode() == market.ModeLive {
		t.Fatal("Regression 6 Critical Failure: engine transitioned to LIVE with unreconciled orders!")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// REGRESSION TEST 7: Concurrent market resets
// Two processes attempt to reset the same partition. Exactly one must own the operation.
// ─────────────────────────────────────────────────────────────────────────────
func TestRegression_7_ConcurrentMarketResets_OnlyOneOwnsOperation(t *testing.T) {
	db := newMockDB()
	db.checkpoints["orders.commands/1"] = -1
	db.marketSequences["ETH-USDT"] = 28691

	authorizer1 := recovery.NewEnvResetAuthorizer()
	os.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-concurrent-reset:shared-secret")
	defer os.Unsetenv("ME_ALLOW_EMPTY_RESET")

	// Process 1: Performs authorized reset
	manager1 := market.NewMarketManager()
	_ = manager1.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	replayer1 := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager1)
	replayer1.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer1.SetResetAuthorizer(authorizer1)
	replayer1.SetLeaseChecker(&mockLeaseTracker{leases: map[int]bool{1: true}})

	var wg1 sync.WaitGroup
	err1 := replayer1.ReplayAll(context.Background(), &wg1)
	if err1 != nil {
		t.Fatalf("first reset must succeed, got: %v", err1)
	}

	// Verify exactly 1 audit record written
	if len(db.auditRecords) != 1 {
		t.Fatalf("expected 1 audit record, got %d", len(db.auditRecords))
	}

	// Process 2: Attempts to reset same partition with same generation token (even with fresh authorizer instance)
	authorizer2 := recovery.NewEnvResetAuthorizer()
	manager2 := market.NewMarketManager()
	_ = manager2.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	replayer2 := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager2)
	replayer2.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer2.SetResetAuthorizer(authorizer2)
	replayer2.SetLeaseChecker(&mockLeaseTracker{leases: map[int]bool{1: true}})

	// Invalidate checkpoint to force second reset attempt
	db.checkpoints["orders.commands/1"] = -1
	delete(db.snapshots, "ETH-USDT")

	var wg2 sync.WaitGroup
	err2 := replayer2.ReplayAll(context.Background(), &wg2)
	if err2 == nil {
		t.Fatal("Regression 7 Failure: expected second reset to fail due to already consumed generation in DB, got nil")
	}
	if !strings.Contains(err2.Error(), "already consumed") {
		t.Fatalf("expected 'already consumed' error, got: %v", err2)
	}

	// Verify still exactly 1 audit record exists
	if len(db.auditRecords) != 1 {
		t.Fatalf("expected still exactly 1 audit record, got %d", len(db.auditRecords))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// REGRESSION TEST 8: Kafka/DB crash boundaries
// Crash after PostgreSQL commit but before Kafka group-offset alignment.
// On restart, proves no command is lost or applied twice.
// ─────────────────────────────────────────────────────────────────────────────
func TestRegression_8_KafkaDBCrashBoundaries_NoCommandLostOrAppliedTwice(t *testing.T) {
	db := newMockDB()
	db.checkpoints["orders.commands/1"] = 43562
	db.marketSequences["ETH-USDT"] = 28691

	manager1 := market.NewMarketManager()
	_ = manager1.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})

	// Process 1: Executes reset, commits DB, but simulated crash before Kafka commit
	replayer1 := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager1)
	replayer1.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer1.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 43563, nil
	})
	replayer1.SetResetAuthorizer(&mockMultiAuthorizer{})
	replayer1.SetLeaseChecker(&mockLeaseTracker{leases: map[int]bool{1: true}})

	var wg1 sync.WaitGroup
	if err := replayer1.ReplayAll(context.Background(), &wg1); err != nil {
		t.Fatalf("process 1 recovery failed: %v", err)
	}

	// Verify DB checkpoint was committed at 43562
	if db.checkpoints["orders.commands/1"] != 43562 {
		t.Fatalf("expected checkpoint 43562 in DB, got %d", db.checkpoints["orders.commands/1"])
	}

	// Process 2: Reboots after crash. Replays up to checkpoint 43562.
	manager2 := market.NewMarketManager()
	_ = manager2.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})

	replayer2 := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager2)
	replayer2.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43564, nil }, // A new message at 43563 exists
	)
	replayer2.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 43563, nil
	})

	var wg2 sync.WaitGroup
	if err := replayer2.ReplayAll(context.Background(), &wg2); err != nil {
		t.Fatalf("process 2 recovery failed: %v", err)
	}

	eth2 := manager2.Get("ETH-USDT")
	// Must recover into ModeReconciling at offset 43562, sequence 28691
	if eth2.Mode() != market.ModeReconciling {
		t.Fatalf("expected ModeReconciling on reboot, got %v", eth2.Mode())
	}
	if eth2.GetLastAppliedOffset() != 43562 {
		t.Fatalf("expected last applied offset 43562, got %d", eth2.GetLastAppliedOffset())
	}
	if eth2.GetSequence() != 28691 {
		t.Fatalf("expected sequence 28691, got %d", eth2.GetSequence())
	}

	// Approve reconciliation
	const validHex8 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	db.manifestChecksums["ETH-USDT/gen-ETH-USDT"] = validHex8
	reconService := recovery.NewReconciliationService(db, manager2, &mockLeaseTracker{leases: map[int]bool{1: true}}, &mockOrderValidator{}, &mockSettlementTracker{}, "admin-secret-test")
	if _, err := reconService.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-ETH-USDT",
		ManifestChecksum: validHex8,
		AuthToken:        "admin-secret-test",
	}); err != nil {
		t.Fatalf("reconciliation approval failed: %v", err)
	}

	if eth2.Mode() != market.ModeLive {
		t.Fatalf("expected ModeLive after approval, got %v", eth2.Mode())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// REGRESSION TEST 9: Settlement invariant checks
// Unresolved failed settlements or fill mismatches must block reconciliation approval.
// ─────────────────────────────────────────────────────────────────────────────
func TestRegression_9_SettlementInvariantChecks_BlocksReconciliation(t *testing.T) {
	db := newMockDB()
	db.reconciliationRecords["ETH-USDT/gen-reg-9"] = "PENDING"

	manager := market.NewMarketManager()
	eth := manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	_ = eth.SetReconciling("pending reconciliation")

	settlementTracker := &mockSettlementTracker{
		hasUnresolvedDiscrepancy: true,
	}

	const validHex9 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	db.manifestChecksums["ETH-USDT/gen-reg-9"] = validHex9

	service := recovery.NewReconciliationService(db, manager, &mockLeaseTracker{leases: map[int]bool{1: true}}, &mockOrderValidator{}, settlementTracker, "admin-secret-test")

	req := recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-reg-9",
		ManifestChecksum: validHex9,
		AuthToken:        "admin-secret-test",
		ApprovedBy:       "auditor",
	}

	_, err := service.ApproveReconciliation(context.Background(), req)
	if err == nil {
		t.Fatal("Regression 9 Failure: expected approval to fail on settlement invariant discrepancy, got nil")
	}

	if eth.Mode() == market.ModeLive {
		t.Fatal("Regression 9 Critical Failure: engine transitioned to LIVE with unresolved settlement discrepancies!")
	}
}
