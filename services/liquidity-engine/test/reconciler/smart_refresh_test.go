package reconciler_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"tradedrift/platform/refprice"
	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/pricing"
)

func TestCheckConsumedOrders_TriggersReplenishment(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, osClient := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.Repricing = config.RepricingConfig{
		RefreshCooldown: 5 * time.Second,
		MaxBatch:        4,
	}

	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{
			marketID: {
				MarketID:  marketID,
				Price:     decimal.RequireFromString("90000.00"),
				Version:   1,
				FetchedAt: time.Now(),
				State:     refprice.StateFresh,
			},
		},
	}
	rec.SetRefProvider(mockProv)

	// Level 1: 60% consumed (remaining 0.40 out of 1.00)
	levelID1 := "MM-BTC-USDT-BID-01"
	oid1 := "00000000-0000-0000-0000-000000000001"
	tracker.SetPending(levelID1, oid1, "MM-BTC-USDT-BID-01-G001", 1, pricing.PriceLevel{
		LevelID:    levelID1,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("89900.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetResting(levelID1, oid1, decimal.RequireFromString("1.0"), decimal.RequireFromString("0.40"))

	// Level 2: Healthy (remaining 0.90 out of 1.00)
	levelID2 := "MM-BTC-USDT-BID-02"
	oid2 := "00000000-0000-0000-0000-000000000002"
	tracker.SetPending(levelID2, oid2, "MM-BTC-USDT-BID-02-G001", 1, pricing.PriceLevel{
		LevelID:    levelID2,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("89800.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetResting(levelID2, oid2, decimal.RequireFromString("1.0"), decimal.RequireFromString("0.90"))

	replenished := rec.CheckConsumedOrders(context.Background(), marketID)
	if replenished != 1 {
		t.Fatalf("expected 1 replenished order, got %d", replenished)
	}

	// Level 1 must transition to CANCELLING
	o1 := tracker.Get(levelID1)
	if o1.Status != order.StatusCancelling {
		t.Errorf("expected level 1 status CANCELLING, got %s", o1.Status)
	}
	if len(prod.cancelledOrders) != 1 || prod.cancelledOrders[0] != oid1 {
		t.Errorf("expected Kafka cancel published for order 1 (%s), got %v", oid1, prod.cancelledOrders)
	}
	if len(osClient.cancelCalled) != 1 || osClient.cancelCalled[0] != oid1 {
		t.Errorf("expected OS CancelMMOrder called for %s, got %v", oid1, osClient.cancelCalled)
	}

	// Level 2 must remain RESTING
	o2 := tracker.Get(levelID2)
	if o2.Status != order.StatusResting {
		t.Errorf("expected level 2 to remain RESTING, got %s", o2.Status)
	}
}

func TestCheckConsumedOrders_CooldownBlocksSecondRefresh(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, _, _ := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.Repricing = config.RepricingConfig{
		RefreshCooldown: 5 * time.Second,
		MaxBatch:        4,
	}

	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{
			marketID: {
				MarketID:  marketID,
				Price:     decimal.RequireFromString("90000.00"),
				Version:   1,
				FetchedAt: time.Now(),
				State:     refprice.StateFresh,
			},
		},
	}
	rec.SetRefProvider(mockProv)

	levelID := "MM-BTC-USDT-BID-01"
	oid := "00000000-0000-0000-0000-000000000001"
	tracker.SetPending(levelID, oid, "MM-BTC-USDT-BID-01-G001", 1, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("89900.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetResting(levelID, oid, decimal.RequireFromString("1.0"), decimal.RequireFromString("0.30"))

	// First call: succeeds
	replenished := rec.CheckConsumedOrders(context.Background(), marketID)
	if replenished != 1 {
		t.Fatalf("first call: expected 1 replenished, got %d", replenished)
	}

	// Reset status to RESTING to simulate another check within cooldown window
	tracker.Get(levelID).Status = order.StatusResting

	// Second call immediately after: blocked by cooldown
	replenished2 := rec.CheckConsumedOrders(context.Background(), marketID)
	if replenished2 != 0 {
		t.Fatalf("second call: expected 0 replenished due to cooldown, got %d", replenished2)
	}
}

func TestCheckConsumedOrders_DustRemaining_TriggersReplenishment(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, _ := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.MinOrderSize = decimal.RequireFromString("0.001")
	mc.Repricing = config.RepricingConfig{
		RefreshCooldown: 5 * time.Second,
		MaxBatch:        4,
	}

	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{
			marketID: {
				MarketID:  marketID,
				Price:     decimal.RequireFromString("90000.00"),
				Version:   1,
				FetchedAt: time.Now(),
				State:     refprice.StateFresh,
			},
		},
	}
	rec.SetRefProvider(mockProv)

	// Remaining is below MinOrderSize (0.0005 < 0.0010)
	levelID := "MM-BTC-USDT-BID-01"
	oid := "00000000-0000-0000-0000-000000000001"
	tracker.SetPending(levelID, oid, "MM-BTC-USDT-BID-01-G001", 1, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("89900.00"),
		Quantity:   decimal.RequireFromString("0.85"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetResting(levelID, oid, decimal.RequireFromString("0.85"), decimal.RequireFromString("0.0005"))

	replenished := rec.CheckConsumedOrders(context.Background(), marketID)
	if replenished != 1 {
		t.Fatalf("expected 1 replenished for dust order, got %d", replenished)
	}
	if len(prod.cancelledOrders) != 1 || prod.cancelledOrders[0] != oid {
		t.Fatalf("expected cancel sent for dust order")
	}
}

func TestCheckConsumedOrders_MaxBatchRespected(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, _, _ := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.Repricing = config.RepricingConfig{
		RefreshCooldown: 5 * time.Second,
		MaxBatch:        2, // MaxBatch = 2
	}

	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{
			marketID: {
				MarketID:  marketID,
				Price:     decimal.RequireFromString("90000.00"),
				Version:   1,
				FetchedAt: time.Now(),
				State:     refprice.StateFresh,
			},
		},
	}
	rec.SetRefProvider(mockProv)

	// Create 4 consumed orders
	for i := 1; i <= 4; i++ {
		levelID := order.ClientOrderID("MM-BTC-USDT-BID", i)
		oid := "uuid-" + levelID
		tracker.SetPending(levelID, oid, levelID+"-G001", 1, pricing.PriceLevel{
			LevelID:    levelID,
			MarketID:   marketID,
			Side:       "BUY",
			Price:      decimal.RequireFromString("89900.00"),
			Quantity:   decimal.RequireFromString("1.0"),
			Zone:       "LOW",
			RefVersion: 1,
		})
		tracker.SetResting(levelID, oid, decimal.RequireFromString("1.0"), decimal.RequireFromString("0.20"))
	}

	replenished := rec.CheckConsumedOrders(context.Background(), marketID)
	if replenished != 2 {
		t.Fatalf("expected exactly MaxBatch=2 replenished orders, got %d", replenished)
	}
}

func TestCheckConsumedOrders_ConfigurableThreshold(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, _, _ := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	// Set threshold to 0.30 (requires 70% consumption, remaining <= 0.30)
	mc.Repricing = config.RepricingConfig{
		RefreshCooldown:             5 * time.Second,
		MaxBatch:                    4,
		RefreshConsumptionThreshold: decimal.RequireFromString("0.30"),
	}

	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{
			marketID: {
				MarketID:  marketID,
				Price:     decimal.RequireFromString("90000.00"),
				Version:   1,
				FetchedAt: time.Now(),
				State:     refprice.StateFresh,
			},
		},
	}
	rec.SetRefProvider(mockProv)

	// Order 1: 0.40 remaining out of 1.0. Under 50% default it would refresh, but with 0.30 it should NOT.
	levelID1 := "MM-BTC-USDT-BID-01"
	oid1 := "00000000-0000-0000-0000-000000000001"
	tracker.SetPending(levelID1, oid1, "MM-BTC-USDT-BID-01-G001", 1, pricing.PriceLevel{
		LevelID:    levelID1,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("89900.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetResting(levelID1, oid1, decimal.RequireFromString("1.0"), decimal.RequireFromString("0.40"))

	// Order 2: 0.25 remaining out of 1.0. Less than 0.30, so it SHOULD refresh.
	levelID2 := "MM-BTC-USDT-BID-02"
	oid2 := "00000000-0000-0000-0000-000000000002"
	tracker.SetPending(levelID2, oid2, "MM-BTC-USDT-BID-02-G001", 1, pricing.PriceLevel{
		LevelID:    levelID2,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("89800.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetResting(levelID2, oid2, decimal.RequireFromString("1.0"), decimal.RequireFromString("0.25"))

	replenished := rec.CheckConsumedOrders(context.Background(), marketID)
	if replenished != 1 {
		t.Fatalf("expected exactly 1 order replenished under 0.30 threshold, got %d", replenished)
	}
}

type refreshTrackingMetrics struct {
	reasons map[string]int
}

func (m *refreshTrackingMetrics) IncStaleOrders(marketID string)                      {}
func (m *refreshTrackingMetrics) IncReconcileCreate(marketID string)                  {}
func (m *refreshTrackingMetrics) IncReconcileCancel(marketID string)                  {}
func (m *refreshTrackingMetrics) IncReconcileCorrect(marketID string)                 {}
func (m *refreshTrackingMetrics) IncReconcileNoop(marketID string)                    {}
func (m *refreshTrackingMetrics) IncOrdersFilled(marketID, side string)               {}
func (m *refreshTrackingMetrics) IncDuplicateMMLevel(marketID string)                  {}
func (m *refreshTrackingMetrics) IncRebaseGenerationFailure(marketID, levelID string) {}
func (m *refreshTrackingMetrics) IncExpiryGenerationFailure(marketID string)          {}
func (m *refreshTrackingMetrics) IncQuantityInvariantViolation(marketID string)       {}
func (m *refreshTrackingMetrics) IncCreateBlocked(marketID, reason string)             {}
func (m *refreshTrackingMetrics) IncRefresh(marketID, reason string) {
	if m.reasons == nil {
		m.reasons = make(map[string]int)
	}
	m.reasons[reason]++
}
func (m *refreshTrackingMetrics) SetExposure(marketID string, bidUSDT, askBase, bidCap, askCap decimal.Decimal) {}
func (m *refreshTrackingMetrics) IncRepriceEvent(marketID, action string) {}
func (m *refreshTrackingMetrics) ObserveRebaseDuration(marketID, action string, seconds float64) {}
func (m *refreshTrackingMetrics) IncRebaseTargetChanges(marketID string)                         {}
func (m *refreshTrackingMetrics) SetRebaseLevelsRemaining(marketID string, count int)            {}
func (m *refreshTrackingMetrics) IncRebaseLevelsRepriced(marketID, zone string)                  {}
func (m *refreshTrackingMetrics) IncCancelOSFailure(marketID string)                              {}
func (m *refreshTrackingMetrics) IncCancelMEPublish(marketID string)                              {}

func TestCheckConsumedOrders_CategorizedRefreshMetrics(t *testing.T) {
	marketID := "BTC-USDT"
	metrics := &refreshTrackingMetrics{}
	rec, tracker, cfg, _, _ := newInvTestRecWithMetrics(t, marketID, metrics)
	mc := &cfg.Markets[0]
	mc.MinOrderSize = decimal.RequireFromString("0.001")
	mc.Repricing = config.RepricingConfig{
		RefreshCooldown:             5 * time.Second,
		MaxBatch:                    4,
		RefreshConsumptionThreshold: decimal.RequireFromString("0.50"),
		OrderLifetime:               10 * time.Millisecond,
	}

	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{
			marketID: {
				MarketID:  marketID,
				Price:     decimal.RequireFromString("90000.00"),
				Version:   1,
				FetchedAt: time.Now(),
				State:     refprice.StateFresh,
			},
		},
	}
	rec.SetRefProvider(mockProv)

	// Level 1: 0.40 remaining out of 1.0 (50% threshold) -> reason="consumption"
	levelID1 := "MM-BTC-USDT-BID-01"
	oid1 := "00000000-0000-0000-0000-000000000001"
	tracker.SetPending(levelID1, oid1, "MM-BTC-USDT-BID-01-G001", 1, pricing.PriceLevel{
		LevelID:    levelID1,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("89900.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetResting(levelID1, oid1, decimal.RequireFromString("1.0"), decimal.RequireFromString("0.40"))

	// Level 2: 0.0005 remaining (below min order size 0.001) -> reason="dust"
	levelID2 := "MM-BTC-USDT-BID-02"
	oid2 := "00000000-0000-0000-0000-000000000002"
	tracker.SetPending(levelID2, oid2, "MM-BTC-USDT-BID-02-G001", 1, pricing.PriceLevel{
		LevelID:    levelID2,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("89800.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetResting(levelID2, oid2, decimal.RequireFromString("1.0"), decimal.RequireFromString("0.0005"))

	replenished := rec.CheckConsumedOrders(context.Background(), marketID)
	if replenished != 2 {
		t.Fatalf("expected 2 orders replenished, got %d", replenished)
	}

	if metrics.reasons["consumption"] != 1 {
		t.Errorf("expected 1 refresh for reason 'consumption', got %d", metrics.reasons["consumption"])
	}
	if metrics.reasons["dust"] != 1 {
		t.Errorf("expected 1 refresh for reason 'dust', got %d", metrics.reasons["dust"])
	}

	// Level 3: Age expiry
	levelID3 := "MM-BTC-USDT-ASK-01"
	oid3 := "00000000-0000-0000-0000-000000000003"
	tracker.SetPending(levelID3, oid3, "MM-BTC-USDT-ASK-01-G001", 1, pricing.PriceLevel{
		LevelID:    levelID3,
		MarketID:   marketID,
		Side:       "SELL",
		Price:      decimal.RequireFromString("90100.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetResting(levelID3, oid3, decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	o3 := tracker.Get(levelID3)
	o3.CreatedAt = time.Now().Add(-100 * time.Millisecond) // expired past 10ms

	expired := rec.CheckExpiredOrders(context.Background(), marketID)
	if expired != 1 {
		t.Fatalf("expected 1 order expired, got %d", expired)
	}
	if metrics.reasons["age"] != 1 {
		t.Errorf("expected 1 refresh for reason 'age', got %d", metrics.reasons["age"])
	}
}
