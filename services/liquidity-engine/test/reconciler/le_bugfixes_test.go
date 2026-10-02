package reconciler_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/platform/refprice"
	"tradedrift/services/liquidity-engine/internal/meclient"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/orderservice"
	"tradedrift/services/liquidity-engine/internal/pricing"
	"tradedrift/services/liquidity-engine/internal/reconciler"
)

// mockLEProvider implements reconciler.ReferencePriceProvider for deterministic testing.
type mockLEProvider struct {
	entry refprice.Entry
	ok    bool
}

func (m *mockLEProvider) Get(marketID string) (refprice.Entry, bool) {
	return m.entry, m.ok
}

// ── LE-1 & LE-2: Provider Failure Persistence and FetchedAt Authority ─────────

func TestLE1_RefStateFreshnessPersistedDuringProviderFailure(t *testing.T) {
	marketID := "BTC-USDT"
	rec, _, cfg, _, _ := newInvTestRec(t, marketID)
	cfg.RefPrice.PauseThreshold = 50 * time.Millisecond
	cfg.RefPrice.StaleThreshold = 20 * time.Millisecond

	// 1. Initial successful fetch
	t0 := time.Now().Add(-100 * time.Millisecond) // old enough to exceed pause threshold
	mock := &mockLEProvider{
		entry: refprice.Entry{
			MarketID:  marketID,
			Price:     decimal.RequireFromString("95000.00"),
			Version:   1,
			FetchedAt: t0,
			State:     refprice.StateFresh,
		},
		ok: true,
	}
	rec.SetRefProvider(mock)

	// Snapshot while live
	mc := cfg.ForMarket(marketID)
	snap := rec.CurrentReferenceSnapshot(mc)
	if snap.Freshness != reconciler.FreshnessFresh {
		t.Fatalf("expected initial FRESH, got %v", snap.Freshness)
	}

	// 2. Provider fails (Get() returns false)
	mock.ok = false

	// Check reference again: because age (100ms) >= PauseThreshold (50ms),
	// it must derive PAUSED and persist it into RefState.Freshness
	snap2 := rec.CurrentReferenceSnapshot(mc)
	if snap2.Freshness != reconciler.FreshnessPaused {
		t.Fatalf("expected PAUSED freshness on provider failure with age > PauseThreshold, got %v", snap2.Freshness)
	}

	// Verify LE-1: state.Freshness is persisted in RefState!
	refState := rec.GetRefState(marketID)
	if refState == nil || refState.Freshness != reconciler.FreshnessPaused {
		t.Fatalf("LE-1 violation: refState.Freshness was not persisted, got %+v", refState)
	}

	// Verify LE-8: isMarketPaused() reads PAUSED
	if !rec.IsMarketPaused(marketID) {
		t.Fatalf("LE-8 violation: isMarketPaused returned false when provider failed past pause threshold")
	}
}

func TestLE2_ReconcileMarket_PreservesProviderFetchedAt(t *testing.T) {
	marketID := "BTC-USDT"
	rec, _, cfg, _, _ := newInvTestRec(t, marketID)

	// Set provider with specific FetchedAt
	fixedFetchedAt := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	mock := &mockLEProvider{
		entry: refprice.Entry{
			MarketID:  marketID,
			Price:     decimal.RequireFromString("95000.00"),
			Version:   1,
			FetchedAt: fixedFetchedAt,
			State:     refprice.StateFresh,
		},
		ok: true,
	}
	rec.SetRefProvider(mock)

	mc := cfg.ForMarket(marketID)
	snap := rec.CurrentReferenceSnapshot(mc)
	if snap.Freshness != reconciler.FreshnessFresh {
		t.Fatalf("expected FRESH, got %v", snap.Freshness)
	}

	_, _ = rec.ReconcileMarket(context.Background(), marketID, 0, 0)

	refState := rec.GetRefState(marketID)
	if refState == nil {
		t.Fatalf("expected refState to exist")
	}

	// LE-2 INVARIANT: refState.LastUpdated must equal provider's FetchedAt, NOT time.Now()
	if !refState.LastUpdated.Equal(fixedFetchedAt) {
		t.Fatalf("LE-2 violation: refState.LastUpdated = %v; expected provider FetchedAt = %v",
			refState.LastUpdated, fixedFetchedAt)
	}
}

// ── LE-3 & LE-4: parseMERemainingQuantity and ConfirmRestingFromSnapshot ───────

func TestLE3_ParseMERemainingQuantity(t *testing.T) {
	fallback := decimal.RequireFromString("0.40")

	// Case 1: "0" is authoritative information (fully consumed)
	res0 := reconciler.ParseMERemainingQuantity(meclient.MMOrderSummary{RemainingQuantity: "0"}, fallback)
	if !res0.IsZero() {
		t.Fatalf("LE-3 violation: expected 0 for '0', got %s", res0.String())
	}

	// Case 2: "0.35" valid remaining
	resVal := reconciler.ParseMERemainingQuantity(meclient.MMOrderSummary{RemainingQuantity: "0.35"}, fallback)
	if !resVal.Equal(decimal.RequireFromString("0.35")) {
		t.Fatalf("expected 0.35, got %s", resVal.String())
	}

	// Case 3: Empty string falls back
	resEmpty := reconciler.ParseMERemainingQuantity(meclient.MMOrderSummary{RemainingQuantity: ""}, fallback)
	if !resEmpty.Equal(fallback) {
		t.Fatalf("expected fallback 0.40, got %s", resEmpty.String())
	}

	// Case 4: Negative value is rejected and falls back
	resNeg := reconciler.ParseMERemainingQuantity(meclient.MMOrderSummary{RemainingQuantity: "-1.5"}, fallback)
	if !resNeg.Equal(fallback) {
		t.Fatalf("expected fallback 0.40 for negative quantity, got %s", resNeg.String())
	}

	// Case 5 (LE-01): meOrder.Quantity is NEVER used as fallback (it is original size, not remaining size)
	resQty := reconciler.ParseMERemainingQuantity(meclient.MMOrderSummary{Quantity: "1.0"}, fallback)
	if !resQty.Equal(fallback) {
		t.Fatalf("LE-01 violation: expected fallback %s when RemainingQuantity is empty, got %s", fallback.String(), resQty.String())
	}
}

func TestLE4_ConfirmRestingFromSnapshot_UsesMERemainingQuantity(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, _, _, _ := newInvTestRec(t, marketID)

	levelID := "BID-01"
	orderID := "ord-100"
	clientOrderID := "coid-100"

	// Order registered with 1.0 remaining
	tracker.SetPending(levelID, orderID, clientOrderID, 1, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: marketID,
		Side:     "BUY",
		Price:    decimal.RequireFromString("95000.00"),
		Quantity: decimal.RequireFromString("1.0"),
	})
	tracker.SetOSRegistered(levelID, orderID, decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))

	// ME snapshot confirms order with partial fill (0.35 remaining)
	snap := &meclient.MarketSnapshot{
		MarketID: marketID,
		State:    "LIVE",
		Sequence: 1,
		Orders: []meclient.MMOrderSummary{
			{
				OrderID:           orderID,
				ClientOrderID:     clientOrderID,
				LevelID:           levelID,
				RemainingQuantity: "0.35",
			},
		},
	}

	rec.ConfirmRestingFromSnapshot(marketID, snap)

	o := tracker.Get(levelID)
	if o == nil {
		t.Fatalf("order not found in tracker")
	}
	if o.Status != order.StatusResting {
		t.Fatalf("expected RESTING, got %v", o.Status)
	}

	// LE-4 INVARIANT: tracker RemainingQty must reflect ME's 0.35, NOT stale 1.0
	if !o.RemainingQty.Equal(decimal.RequireFromString("0.35")) {
		t.Fatalf("LE-4 violation: tracker RemainingQty = %s, expected 0.35 from ME snapshot", o.RemainingQty.String())
	}
}

// ── LE-5: ErrOrderNotFound Preserves RemainingQty ─────────────────────────────

type mockOrderSvcNotFound struct {
	cancelCalled []string
}

func (m *mockOrderSvcNotFound) GetOrderByClientID(ctx context.Context, clientOrderID string) (*orderservice.OrderState, error) {
	return nil, orderservice.ErrOrderNotFound
}
func (m *mockOrderSvcNotFound) CancelMMOrder(ctx context.Context, orderID string) error {
	m.cancelCalled = append(m.cancelCalled, orderID)
	return nil
}
func (m *mockOrderSvcNotFound) CreateMMOrder(ctx context.Context, marketID, side, price, quantity, clientOrderID string) (*orderservice.OrderState, error) {
	return &orderservice.OrderState{
		OrderID:       "mock-os-order-id-" + clientOrderID,
		ClientOrderID: clientOrderID,
		Status:        "OPEN",
	}, nil
}
func (m *mockOrderSvcNotFound) ListMMOrders(ctx context.Context, marketID string) ([]order.OSOrder, error) {
	return nil, nil
}
func (m *mockOrderSvcNotFound) RecoverHighestGenerations(ctx context.Context, marketID string) (map[string]int, error) {
	return nil, nil
}

type countingMetrics struct {
	ordersFilled int
}

func (c *countingMetrics) IncStaleOrders(marketID string)                      {}
func (c *countingMetrics) IncReconcileCreate(marketID string)                  {}
func (c *countingMetrics) IncReconcileCancel(marketID string)                  {}
func (c *countingMetrics) IncReconcileCorrect(marketID string)                 {}
func (c *countingMetrics) IncReconcileNoop(marketID string)                    {}
func (c *countingMetrics) IncOrdersFilled(marketID, side string)               { c.ordersFilled++ }
func (c *countingMetrics) IncDuplicateMMLevel(marketID string)                  {}
func (c *countingMetrics) IncRebaseGenerationFailure(marketID, levelID string) {}
func (c *countingMetrics) IncExpiryGenerationFailure(marketID string)          {}
func (c *countingMetrics) IncQuantityInvariantViolation(marketID string)       {}

func TestLE5_HandleCancellingTimeout_ErrOrderNotFound_PreservesQuantity(t *testing.T) {
	marketID := "BTC-USDT"
	tracker := order.NewTracker()
	cfg := testConfig()
	prod := &mockProducer{}
	osClient := &mockOrderSvcNotFound{}
	metrics := &countingMetrics{}
	rec := reconciler.NewReconciler(tracker, prod, osClient, nil, cfg, zap.NewNop(), metrics)

	levelID := "BID-01"
	orderID := "ord-cancel-100"
	clientOrderID := "coid-cancel-100"

	// Place order in CANCELLING with remaining 0.5 and a queued correction
	tracker.SetPending(levelID, orderID, clientOrderID, 1, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: marketID,
		Side:     "BUY",
		Price:    decimal.RequireFromString("95000.00"),
		Quantity: decimal.RequireFromString("1.0"),
	})
	tracker.SetResting(levelID, orderID, decimal.RequireFromString("1.0"), decimal.RequireFromString("0.5"))
	tracker.SetCancelling(levelID)
	tracker.QueueCorrection(levelID, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: marketID,
		Side:     "BUY",
		Price:    decimal.RequireFromString("95100.00"),
		Quantity: decimal.RequireFromString("1.0"),
	})

	o := tracker.Get(levelID)
	mc := cfg.ForMarket(marketID)

	rec.HandleCancellingTimeout(context.Background(), o, mc)

	// LE-5 INVARIANT:
	// 1. ErrOrderNotFound must NOT increment OrdersFilled metric!
	if metrics.ordersFilled != 0 {
		t.Fatalf("LE-5 violation: OrdersFilled was incremented on ErrOrderNotFound: count = %d", metrics.ordersFilled)
	}

	// 2. Replacement create must be published with quantity = 0.5 (authoritative remaining), NOT 0 or skipped
	if len(prod.publishedCreates) != 1 {
		t.Fatalf("LE-5 violation: expected replacement create to be published, got %d creates", len(prod.publishedCreates))
	}
	if prod.publishedCreates[0].Quantity != "0.5" {
		t.Fatalf("LE-5 violation: replacement create quantity = %s, expected '0.5'", prod.publishedCreates[0].Quantity)
	}
}

// ── LE-9: Final Creation Safety Gate in canCreateLevel ────────────────────────

func TestLE9_CanCreateLevel_FinalCreationSafetyGate(t *testing.T) {
	marketID := "BTC-USDT"
	rec, _, cfg, _, _ := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)
	mc.MinOrderSize = decimal.RequireFromString("0.001")
	mc.LotSize = decimal.RequireFromString("0.0001")

	// 1. Invalid price <= 0
	ok, _ := rec.CanCreateLevel(mc, pricing.PriceLevel{
		LevelID:  "BID-01",
		MarketID: marketID,
		Side:     "BUY",
		Price:    decimal.Zero,
		Quantity: decimal.RequireFromString("0.1"),
	})
	if ok {
		t.Fatalf("LE-9 violation: permitted zero price")
	}

	// 2. Invalid quantity <= 0
	ok, _ = rec.CanCreateLevel(mc, pricing.PriceLevel{
		LevelID:  "BID-01",
		MarketID: marketID,
		Side:     "BUY",
		Price:    decimal.RequireFromString("95000.00"),
		Quantity: decimal.RequireFromString("-0.5"),
	})
	if ok {
		t.Fatalf("LE-9 violation: permitted negative quantity")
	}

	// 3. Invalid side
	ok, _ = rec.CanCreateLevel(mc, pricing.PriceLevel{
		LevelID:  "BID-01",
		MarketID: marketID,
		Side:     "HOLD",
		Price:    decimal.RequireFromString("95000.00"),
		Quantity: decimal.RequireFromString("0.1"),
	})
	if ok {
		t.Fatalf("LE-9 violation: permitted invalid side 'HOLD'")
	}

	// 4. Market mismatch
	ok, _ = rec.CanCreateLevel(mc, pricing.PriceLevel{
		LevelID:  "BID-01",
		MarketID: "ETH-USDT",
		Side:     "BUY",
		Price:    decimal.RequireFromString("95000.00"),
		Quantity: decimal.RequireFromString("0.1"),
	})
	if ok {
		t.Fatalf("LE-9 violation: permitted market mismatch")
	}

	// 4b. Empty MarketID rejected (LE-15)
	ok, _ = rec.CanCreateLevel(mc, pricing.PriceLevel{
		LevelID:  "BID-01",
		MarketID: "",
		Side:     "BUY",
		Price:    decimal.RequireFromString("95000.00"),
		Quantity: decimal.RequireFromString("0.1"),
	})
	if ok {
		t.Fatalf("LE-15 violation: permitted empty MarketID")
	}

	// 5. Quantity < MinOrderSize
	ok, _ = rec.CanCreateLevel(mc, pricing.PriceLevel{
		LevelID:  "BID-01",
		MarketID: marketID,
		Side:     "BUY",
		Price:    decimal.RequireFromString("95000.00"),
		Quantity: decimal.RequireFromString("0.0005"),
	})
	if ok {
		t.Fatalf("LE-9 violation: permitted quantity below MinOrderSize")
	}

	// 6. Valid level passes
	ok, reason := rec.CanCreateLevel(mc, pricing.PriceLevel{
		LevelID:  "BID-01",
		MarketID: marketID,
		Side:     "BUY",
		Price:    decimal.RequireFromString("95000.00"),
		Quantity: decimal.RequireFromString("0.01"),
	})
	if !ok {
		t.Fatalf("expected valid level to pass, failed with: %s", reason)
	}
}
