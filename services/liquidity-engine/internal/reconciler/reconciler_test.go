package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/config"
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

	rec := NewReconciler(tracker, nil, nil, cfg, logger, &mockMetrics{})

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

func TestCheckOSRegisteredTimeouts_GatedByMEHealth(t *testing.T) {
	tracker := order.NewTracker()
	cfg := testConfig()
	logger := zap.NewNop()

	rec := NewReconciler(tracker, nil, nil, cfg, logger, &mockMetrics{})

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
	rec.CheckOSRegisteredTimeouts("BTC-USDT", 10*time.Millisecond, false)
	if liveOrder.Status != order.StatusOSRegistered {
		t.Errorf("expected status to remain OS_REGISTERED when ME is unhealthy, got %s", liveOrder.Status)
	}

	// Case B: ME is healthy -> promotes to RESTING
	rec.CheckOSRegisteredTimeouts("BTC-USDT", 10*time.Millisecond, true)
	if liveOrder.Status != order.StatusResting {
		t.Errorf("expected status to transition to RESTING when ME is healthy, got %s", liveOrder.Status)
	}
}
