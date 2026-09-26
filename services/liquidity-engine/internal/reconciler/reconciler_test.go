package reconciler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/meclient"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/pricing"
)

type mockMetrics struct{}

func (m *mockMetrics) IncStaleOrders(marketID string)              {}
func (m *mockMetrics) IncReconcileCreate(marketID string)          {}
func (m *mockMetrics) IncReconcileCancel(marketID string)          {}
func (m *mockMetrics) IncReconcileCorrect(marketID string)         {}
func (m *mockMetrics) IncReconcileNoop(marketID string)            {}
func (m *mockMetrics) IncOrdersFilled(marketID, side string)       {}
func (m *mockMetrics) IncDuplicateMMLevel(marketID string)          {}

func testConfig() *config.Config {
	return &config.Config{
		PendingTimeout:    10 * time.Millisecond,
		CancellingTimeout: 10 * time.Millisecond,
		CancelRetryLimit:  3,
		Markets: []config.MarketConfig{
			{
				MarketID:       "BTC-USDT",
				TickSize:       decimal.RequireFromString("0.01"),
				LotSize:        decimal.RequireFromString("0.00001"),
				LevelCount:     12,
				MinOrderSize:   decimal.RequireFromString("0.00001"),
				ReferencePrice: decimal.RequireFromString("96450.00"),
				SpreadBps:      4,
			},
		},
	}
}

func TestRetryCancelOrStale_TransitionsToStale(t *testing.T) {
	tracker := order.NewTracker()
	cfg := testConfig()
	logger := zap.NewNop()

	rec := NewReconciler(tracker, nil, nil, nil, cfg, logger, &mockMetrics{})

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

	rec.retryCancelOrStale(context.TODO(), liveOrder, &cfg.Markets[0])

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

	rec := NewReconciler(tracker, nil, nil, nil, cfg, logger, &mockMetrics{})

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

	recWithME := NewReconciler(tracker, nil, nil, nil, cfg, logger, &mockMetrics{})
	recWithME.meClient = meclient.New(srv.URL, logger)

	recWithME.ConfirmOSRegisteredOrders("BTC-USDT", true)
	if liveOrder.Status != order.StatusResting {
		t.Errorf("expected status to transition to RESTING when ME snapshot confirms order, got %s", liveOrder.Status)
	}
}

func TestConfirmRestingFromSnapshot(t *testing.T) {
	tracker := order.NewTracker()
	cfg := testConfig()
	logger := zap.NewNop()

	rec := NewReconciler(tracker, nil, nil, nil, cfg, logger, &mockMetrics{})

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
	rec := NewReconciler(tracker, nil, osClient, nil, cfg, logger, &mockMetrics{})

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
	rec.syncWithMESnapshot(context.Background(), "BTC-USDT", emptySnap, &cfg.Markets[0])
	if rec.missingCycles[levelID] != 1 {
		t.Fatalf("expected missingCycles=1, got %d", rec.missingCycles[levelID])
	}
	if tracker.Get(levelID) == nil {
		t.Fatal("order should NOT be removed after only 1 missing cycle (hysteresis)")
	}

	// Cycle 2: missing_cycle reaches 2 -> handleMissingRestingOrder executes -> notFoundCount=1 -> holding slot
	rec.syncWithMESnapshot(context.Background(), "BTC-USDT", emptySnap, &cfg.Markets[0])
	if tracker.Get(levelID) == nil {
		t.Fatal("order should NOT be removed after only 1 OS NOT_FOUND (hysteresis)")
	}
	if rec.notFoundCount[levelID] != 1 {
		t.Fatalf("expected notFoundCount=1, got %d", rec.notFoundCount[levelID])
	}

	// Cycle 3: missing_cycle becomes 1 again
	rec.syncWithMESnapshot(context.Background(), "BTC-USDT", emptySnap, &cfg.Markets[0])
	if tracker.Get(levelID) == nil {
		t.Fatal("order should NOT be removed on cycle 3")
	}

	// Cycle 4: missing_cycle reaches 2 again -> handleMissingRestingOrder executes -> notFoundCount reaches 2 -> removed
	rec.syncWithMESnapshot(context.Background(), "BTC-USDT", emptySnap, &cfg.Markets[0])
	if tracker.Get(levelID) != nil {
		t.Error("order should be removed after 2 consecutive missing cycles and 2 OS not found checks")
	}
}

