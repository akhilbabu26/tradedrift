package reconciler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/platform/refprice"
	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/meclient"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/pricing"
	"tradedrift/services/liquidity-engine/internal/reconciler"
)

func TestRetryCancelOrStale_TransitionsToStale(t *testing.T) {
	tracker := order.NewTracker()
	cfg := testConfig()
	logger := zap.NewNop()

	rec := reconciler.NewReconciler(tracker, nil, nil, nil, cfg, logger, &mockMetrics{})

	// Add an order with CancelRetries already at limit (3)
	levelID := "MM-BTC-USDT-ASK-01"
	orderID := "00000000-0000-0000-0000-000000000099"
	tracker.SetPending(levelID, orderID, "MM-BTC-USDT-ASK-01-G001", 1, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: "BTC-USDT",
		Side:     "SELL",
		Price:    decimal.RequireFromString("96500.00"),
		Quantity: decimal.RequireFromString("0.85"),
	})
	tracker.SetCancelling(levelID)
	liveOrder := tracker.Get(levelID)
	liveOrder.CancelRetries = 3

	rec.RetryCancelOrStale(context.TODO(), liveOrder, &cfg.Markets[0])

	// Must be STALE, not removed from tracker
	if liveOrder.Status != order.StatusStale {
		t.Errorf("expected status STALE, got %s", liveOrder.Status)
	}
	if tracker.Get(levelID) == nil {
		t.Error("STALE order must NOT be removed from tracker")
	}
}

func TestConfirmOSRegisteredOrders_GatedByMEHealth(t *testing.T) {
	tracker := order.NewTracker()
	cfg := testConfig()
	logger := zap.NewNop()

	rec := reconciler.NewReconciler(tracker, nil, nil, nil, cfg, logger, &mockMetrics{})

	levelID := "MM-BTC-USDT-BID-01"
	orderID := "00000000-0000-0000-0000-000000000002"
	tracker.SetPending(levelID, orderID, "MM-BTC-USDT-BID-01-G001", 1, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: "BTC-USDT",
		Side:     "BUY",
		Price:    decimal.RequireFromString("96400.00"),
		Quantity: decimal.RequireFromString("1.0"),
	})
	tracker.SetOSRegistered(levelID, orderID, decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))

	liveOrder := tracker.Get(levelID)

	// Simulate elapsed timeout
	liveOrder.OSRegisteredSince = time.Now().Add(-1 * time.Second)

	// Case A: ME is unhealthy -> must NOT promote to RESTING
	rec.ConfirmOSRegisteredOrders("BTC-USDT", false)
	if liveOrder.Status != order.StatusOSRegistered {
		t.Errorf("expected status to remain OS_REGISTERED when ME is unhealthy, got %s", liveOrder.Status)
	}

	// Case B: meClient is nil -> fail-closed (must NOT promote to RESTING even if healthy)
	rec.ConfirmOSRegisteredOrders("BTC-USDT", true)
	if liveOrder.Status != order.StatusOSRegistered {
		t.Errorf("expected status to remain OS_REGISTERED when meClient is nil (fail-closed), got %s", liveOrder.Status)
	}

	// Case C: ME client configured and snapshot confirms order -> promotes to RESTING
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(meclient.MarketSnapshot{
			MarketID: "BTC-USDT",
			State:    "LIVE",
			Orders: []meclient.MMOrderSummary{
				{
					LevelID:       levelID,
					OrderID:       orderID,
					ClientOrderID: "MM-BTC-USDT-BID-01-G001",
				},
			},
		})
	}))
	defer srv.Close()

	recWithME := reconciler.NewReconciler(tracker, nil, nil, nil, cfg, logger, &mockMetrics{})
	recWithME.SetMEClient(meclient.New(srv.URL, logger))

	recWithME.ConfirmOSRegisteredOrders("BTC-USDT", true)
	if liveOrder.Status != order.StatusResting {
		t.Errorf("expected status to transition to RESTING when ME snapshot confirms order, got %s", liveOrder.Status)
	}
}

func TestConfirmRestingFromSnapshot(t *testing.T) {
	tracker := order.NewTracker()
	cfg := testConfig()
	logger := zap.NewNop()

	rec := reconciler.NewReconciler(tracker, nil, nil, nil, cfg, logger, &mockMetrics{})

	levelID := "MM-BTC-USDT-BID-01"
	orderID := "me-order-123"
	coid := "MM-BTC-USDT-BID-01-G001"
	tracker.SetPending(levelID, orderID, coid, 1, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: "BTC-USDT",
		Side:     "BUY",
		Price:    decimal.RequireFromString("96400.00"),
		Quantity: decimal.RequireFromString("1.0"),
	})
	tracker.SetOSRegistered(levelID, orderID, decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))

	// Non-matching snapshot: does not promote
	snapOther := &meclient.MarketSnapshot{
		MarketID:   "BTC-USDT",
		State:      "LIVE",
		Sequence:   1,
		OrderCount: 1,
		Orders: []meclient.MMOrderSummary{
			{
				OrderID:       "other-order",
				ClientOrderID: "MM-BTC-USDT-BID-02-G001",
				LevelID:       "MM-BTC-USDT-BID-02",
			},
		},
	}
	rec.ConfirmRestingFromSnapshot("BTC-USDT", snapOther)
	if tracker.Get(levelID).Status != order.StatusOSRegistered {
		t.Errorf("expected OS_REGISTERED, got %s", tracker.Get(levelID).Status)
	}

	// Matching snapshot: promotes to RESTING
	snapMatch := &meclient.MarketSnapshot{
		MarketID:   "BTC-USDT",
		State:      "LIVE",
		Sequence:   2,
		OrderCount: 1,
		Orders: []meclient.MMOrderSummary{
			{
				OrderID:       orderID,
				ClientOrderID: coid,
				LevelID:       levelID,
			},
		},
	}
	rec.ConfirmRestingFromSnapshot("BTC-USDT", snapMatch)
	if tracker.Get(levelID).Status != order.StatusResting {
		t.Errorf("expected RESTING, got %s", tracker.Get(levelID).Status)
	}
}

func TestSyncWithMESnapshot_Hysteresis(t *testing.T) {
	tracker := order.NewTracker()
	cfg := testConfig()
	logger := zap.NewNop()

	osClient := newMockOrderSvc()
	rec := reconciler.NewReconciler(tracker, nil, osClient, nil, cfg, logger, &mockMetrics{})

	levelID := "MM-BTC-USDT-BID-01"
	orderID := "me-order-123"
	coid := "MM-BTC-USDT-BID-01-G001"
	tracker.SetPending(levelID, orderID, coid, 1, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: "BTC-USDT",
		Side:     "BUY",
		Price:    decimal.RequireFromString("96400.00"),
		Quantity: decimal.RequireFromString("1.0"),
	})
	tracker.SetResting(levelID, orderID, decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))

	emptySnap := &meclient.MarketSnapshot{
		MarketID: "BTC-USDT",
		State:    "LIVE",
		Sequence: 10,
	}

	// Cycle 1: missing_cycle becomes 1, order is NOT removed
	rec.SyncWithMESnapshot(context.Background(), "BTC-USDT", emptySnap, &cfg.Markets[0])
	if rec.GetMissingCycles(levelID) != 1 {
		t.Fatalf("expected missingCycles=1, got %d", rec.GetMissingCycles(levelID))
	}
	if tracker.Get(levelID) == nil {
		t.Fatal("order should NOT be removed after only 1 missing cycle (hysteresis)")
	}

	// Cycle 2: missing_cycle reaches 2 -> handleMissingRestingOrder executes -> notFoundCount=1 -> holding slot
	rec.SyncWithMESnapshot(context.Background(), "BTC-USDT", emptySnap, &cfg.Markets[0])
	if tracker.Get(levelID) == nil {
		t.Fatal("order should NOT be removed after only 1 OS NOT_FOUND (hysteresis)")
	}
	if rec.GetNotFoundCount(levelID) != 1 {
		t.Fatalf("expected notFoundCount=1, got %d", rec.GetNotFoundCount(levelID))
	}

	// Cycle 3: missing_cycle becomes 1 again
	rec.SyncWithMESnapshot(context.Background(), "BTC-USDT", emptySnap, &cfg.Markets[0])
	if tracker.Get(levelID) == nil {
		t.Fatal("order should NOT be removed on cycle 3")
	}

	// Cycle 4: missing_cycle reaches 2 again -> handleMissingRestingOrder executes -> notFoundCount reaches 2 -> removed
	rec.SyncWithMESnapshot(context.Background(), "BTC-USDT", emptySnap, &cfg.Markets[0])
	if tracker.Get(levelID) != nil {
		t.Error("order should be removed after 2 consecutive missing cycles and 2 OS not found checks")
	}
}

func TestApplyCreate_CapitalCaps(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, osClient := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]

	// 1. BID Cap Enforcement
	mc.MaxBidExposureUSDT = decimal.RequireFromString("100000.00")

	level1 := pricing.PriceLevel{
		LevelID:  "MM-BTC-USDT-BID-01",
		MarketID: marketID,
		Side:     "BUY",
		Price:    decimal.RequireFromString("60000.00"),
		Quantity: decimal.RequireFromString("1.0"),
	}
	diff1 := order.DiffEntry{
		Action:       order.DiffCreate,
		LevelID:      level1.LevelID,
		DesiredLevel: &level1,
	}

	// First create: 60,000 USDT <= 100,000 USDT -> should succeed
	if err := rec.ApplyCreate(context.Background(), diff1, mc); err != nil {
		t.Fatalf("first applyCreate failed: %v", err)
	}
	if tracker.Get(level1.LevelID) == nil {
		t.Fatalf("expected level 1 to be registered in tracker")
	}
	if len(prod.createdOrders) != 1 {
		t.Fatalf("expected 1 Kafka create command, got %d", len(prod.createdOrders))
	}

	level2 := pricing.PriceLevel{
		LevelID:  "MM-BTC-USDT-BID-02",
		MarketID: marketID,
		Side:     "BUY",
		Price:    decimal.RequireFromString("50000.00"),
		Quantity: decimal.RequireFromString("1.0"),
	}
	diff2 := order.DiffEntry{
		Action:       order.DiffCreate,
		LevelID:      level2.LevelID,
		DesiredLevel: &level2,
	}

	// Second create: 60,000 + 50,000 = 110,000 USDT > 100,000 USDT -> should be skipped gracefully (nil err)
	if err := rec.ApplyCreate(context.Background(), diff2, mc); err != nil {
		t.Fatalf("second applyCreate should return nil on cap skip, got err: %v", err)
	}
	if tracker.Get(level2.LevelID) != nil {
		t.Fatalf("expected level 2 to be skipped and NOT registered in tracker")
	}
	if len(prod.createdOrders) != 1 {
		t.Fatalf("expected still only 1 Kafka create command, got %d", len(prod.createdOrders))
	}

	// 2. ASK Cap Enforcement
	mc.MaxAskExposureBase = decimal.RequireFromString("1.5")

	ask1 := pricing.PriceLevel{
		LevelID:  "MM-BTC-USDT-ASK-01",
		MarketID: marketID,
		Side:     "SELL",
		Price:    decimal.RequireFromString("70000.00"),
		Quantity: decimal.RequireFromString("1.0"),
	}
	diffAsk1 := order.DiffEntry{
		Action:       order.DiffCreate,
		LevelID:      ask1.LevelID,
		DesiredLevel: &ask1,
	}

	// First ask: 1.0 <= 1.5 -> should succeed
	if err := rec.ApplyCreate(context.Background(), diffAsk1, mc); err != nil {
		t.Fatalf("first ask applyCreate failed: %v", err)
	}
	if tracker.Get(ask1.LevelID) == nil {
		t.Fatalf("expected ask 1 to be registered in tracker")
	}
	if len(prod.createdOrders) != 2 {
		t.Fatalf("expected 2 Kafka create commands total, got %d", len(prod.createdOrders))
	}

	ask2 := pricing.PriceLevel{
		LevelID:  "MM-BTC-USDT-ASK-02",
		MarketID: marketID,
		Side:     "SELL",
		Price:    decimal.RequireFromString("71000.00"),
		Quantity: decimal.RequireFromString("1.0"),
	}
	diffAsk2 := order.DiffEntry{
		Action:       order.DiffCreate,
		LevelID:      ask2.LevelID,
		DesiredLevel: &ask2,
	}

	// Second ask: 1.0 + 1.0 = 2.0 > 1.5 -> should be skipped gracefully
	if err := rec.ApplyCreate(context.Background(), diffAsk2, mc); err != nil {
		t.Fatalf("second ask applyCreate should return nil on cap skip, got err: %v", err)
	}
	if tracker.Get(ask2.LevelID) != nil {
		t.Fatalf("expected ask 2 to be skipped and NOT registered in tracker")
	}
	if len(prod.createdOrders) != 2 {
		t.Fatalf("expected Kafka create count to remain 2, got %d", len(prod.createdOrders))
	}
	_ = osClient
}

func TestCheckExpiredOrders(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, osClient := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.Repricing = config.RepricingConfig{
		OrderLifetime: 30 * time.Minute,
		MaxBatch:      2,
	}

	// Order 1: Expired (40 minutes old)
	levelID1 := "MM-BTC-USDT-BID-01"
	oid1 := "00000000-0000-0000-0000-000000000001"
	tracker.SetPending(levelID1, oid1, "MM-BTC-USDT-BID-01-G001", 1, pricing.PriceLevel{
		LevelID:    levelID1,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("96000.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetResting(levelID1, oid1, decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	tracker.Get(levelID1).CreatedAt = time.Now().Add(-40 * time.Minute)

	// Order 2: Fresh (5 minutes old)
	levelID2 := "MM-BTC-USDT-BID-02"
	oid2 := "00000000-0000-0000-0000-000000000002"
	tracker.SetPending(levelID2, oid2, "MM-BTC-USDT-BID-02-G001", 1, pricing.PriceLevel{
		LevelID:    levelID2,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("95000.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetResting(levelID2, oid2, decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	tracker.Get(levelID2).CreatedAt = time.Now().Add(-5 * time.Minute)

	// Call CheckExpiredOrders
	expired := rec.CheckExpiredOrders(context.Background(), marketID)
	if expired != 1 {
		t.Fatalf("expected exactly 1 expired order, got %d", expired)
	}

	// Order 1 must transition to CANCELLING with QueuedCorrection set
	o1 := tracker.Get(levelID1)
	if o1.Status != order.StatusCancelling {
		t.Errorf("expected order 1 status CANCELLING, got %s", o1.Status)
	}
	if o1.QueuedCorrection == nil {
		t.Fatal("expected QueuedCorrection to be populated on expired order")
	}
	if !o1.QueuedCorrection.Price.LessThan(mc.ReferencePrice) {
		t.Errorf("expected queued correction price %s to be below ref %s", o1.QueuedCorrection.Price, mc.ReferencePrice)
	}
	if len(prod.cancelledOrders) != 1 || prod.cancelledOrders[0] != oid1 {
		t.Errorf("expected Kafka cancel published for order 1 (%s), got %v", oid1, prod.cancelledOrders)
	}
	if len(osClient.cancelCalled) != 1 || osClient.cancelCalled[0] != oid1 {
		t.Errorf("expected OS CancelMMOrder called for %s, got %v", oid1, osClient.cancelCalled)
	}

	// Order 2 must remain RESTING
	o2 := tracker.Get(levelID2)
	if o2.Status != order.StatusResting {
		t.Errorf("expected fresh order 2 to remain RESTING, got %s", o2.Status)
	}
	if o2.QueuedCorrection != nil {
		t.Error("fresh order 2 must NOT have QueuedCorrection")
	}
}

type mockRefProvider struct {
	entries map[string]refprice.Entry
}

func (m *mockRefProvider) Get(marketID string) (refprice.Entry, bool) {
	e, ok := m.entries[marketID]
	return e, ok
}

func TestReconciler_RefProviderIntegration(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, _ := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.Repricing = config.RepricingConfig{
		SmallBps: 10,
		LargeBps: 100,
		MaxBatch: 2,
	}

	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{
			marketID: {
				MarketID:  marketID,
				Price:     decimal.RequireFromString("100.00"),
				Version:   1,
				FetchedAt: time.Now(),
				State:     refprice.StateFresh,
			},
		},
	}
	rec.SetRefProvider(mockProv)

	// Mock ME server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(meclient.MarketSnapshot{
			MarketID: marketID,
			State:    "LIVE",
			Orders:   nil,
		})
	}))
	defer srv.Close()
	rec.SetMEClient(meclient.New(srv.URL, zap.NewNop()))

	// Initial cycle: establishes baseline ref=100.00
	_, err := rec.ReconcileMarket(context.Background(), marketID, 12, 12)
	if err != nil {
		t.Fatalf("initial reconcile failed: %v", err)
	}

	// 1. Move price < SmallBps (100.05 = 5 bps): KEEP action
	prod.cancelledOrders = nil
	mockProv.entries[marketID] = refprice.Entry{
		MarketID:  marketID,
		Price:     decimal.RequireFromString("100.05"),
		Version:   2,
		FetchedAt: time.Now(),
		State:     refprice.StateFresh,
	}
	_, err = rec.ReconcileMarket(context.Background(), marketID, 12, 12)
	if err != nil {
		t.Fatalf("small move reconcile failed: %v", err)
	}
	if len(prod.cancelledOrders) != 0 {
		t.Errorf("expected 0 cancels on KEEP, got %d", len(prod.cancelledOrders))
	}

	// 2. State STALE: HOLD action (no cancels or aggressive reprice)
	mockProv.entries[marketID] = refprice.Entry{
		MarketID:  marketID,
		Price:     decimal.RequireFromString("105.00"),
		Version:   3,
		FetchedAt: time.Now().Add(-6 * time.Minute),
		State:     refprice.StateStale,
	}
	_, err = rec.ReconcileMarket(context.Background(), marketID, 12, 12)
	if err != nil {
		t.Fatalf("stale reconcile failed: %v", err)
	}
	if len(prod.cancelledOrders) != 0 {
		t.Errorf("expected 0 cancels on HOLD (stale ref), got %d", len(prod.cancelledOrders))
	}

	// 3. State PAUSED: PAUSE action (0 commands published)
	mockProv.entries[marketID] = refprice.Entry{
		MarketID:  marketID,
		Price:     decimal.RequireFromString("105.00"),
		Version:   4,
		FetchedAt: time.Now().Add(-15 * time.Minute),
		State:     refprice.StatePaused,
	}
	cmds, err := rec.ReconcileMarket(context.Background(), marketID, 12, 12)
	if err != nil {
		t.Fatalf("paused reconcile failed: %v", err)
	}
	if cmds != 0 {
		t.Errorf("expected 0 commands on PAUSE, got %d", cmds)
	}

	// 4. Large move (100.00 -> 103.00 = 300 bps): CONTROLLED_REBASE
	// Promote orders in tracker to RESTING so they can be rebased
	for _, o := range tracker.All(marketID) {
		tracker.SetResting(o.LevelID, o.OrderID, o.RemainingQty, o.RemainingQty)
	}

	mockProv.entries[marketID] = refprice.Entry{
		MarketID:  marketID,
		Price:     decimal.RequireFromString("103.00"),
		Version:   5,
		FetchedAt: time.Now(),
		State:     refprice.StateFresh,
	}
	prod.cancelledOrders = nil
	_, err = rec.ReconcileMarket(context.Background(), marketID, 12, 12)
	if err != nil {
		t.Fatalf("rebase reconcile failed: %v", err)
	}

	// MaxBatch is 2: exactly 2 orders rebased (cancelled and queued for correction)
	if len(prod.cancelledOrders) != 2 {
		t.Errorf("expected exactly 2 cancelled orders (MaxBatch=2) during controlled rebase, got %d", len(prod.cancelledOrders))
	}
}
