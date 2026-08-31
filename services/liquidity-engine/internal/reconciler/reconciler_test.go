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
	orderID := "MM-BTC-USDT-ASK-01"
	tracker.SetPending(orderID, "MM-BTC-USDT-ASK-01-G001", 1, pricing.PriceLevel{
		LevelID:  orderID,
		MarketID: "BTC-USDT",
		Side:     "SELL",
		Price:    decimal.RequireFromString("96500.00"),
		Quantity: decimal.RequireFromString("0.85"),
	})
	tracker.SetCancelling(orderID)
	liveOrder := tracker.Get(orderID)
	liveOrder.CancelRetries = 3

	rec.retryCancelOrStale(context.TODO(), liveOrder, &cfg.Markets[0])

	// Must be STALE, not removed from tracker
	if liveOrder.Status != order.StatusStale {
		t.Errorf("expected status STALE, got %s", liveOrder.Status)
	}
	if tracker.Get(orderID) == nil {
		t.Error("STALE order must NOT be removed from tracker")
	}
}
