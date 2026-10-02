package reconciler_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/orderservice"
	"tradedrift/services/liquidity-engine/internal/pricing"
)

func TestResolveReplacementQuantity_Invariants(t *testing.T) {
	rec, _, cfg, _, _ := newInvTestRec(t, "BTC-USDT")
	marketID := "BTC-USDT"
	levelID := "BUY-01"
	minSize := decimal.RequireFromString("0.0001")
	lotSize := decimal.RequireFromString("0.0001")

	tests := []struct {
		name        string
		desired     decimal.Decimal
		remaining   decimal.Decimal
		original    decimal.Decimal
		expectedQty decimal.Decimal
		expectedOk  bool
	}{
		{
			name:        "Full remaining: 1.0",
			desired:     decimal.RequireFromString("1.0000"),
			remaining:   decimal.RequireFromString("1.0000"),
			original:    decimal.RequireFromString("1.0000"),
			expectedQty: decimal.RequireFromString("1.0000"),
			expectedOk:  true,
		},
		{
			name:        "Partial fill: remaining 0.35, desired 1.0 -> capped to 0.35",
			desired:     decimal.RequireFromString("1.0000"),
			remaining:   decimal.RequireFromString("0.3500"),
			original:    decimal.RequireFromString("1.0000"),
			expectedQty: decimal.RequireFromString("0.3500"),
			expectedOk:  true,
		},
		{
			name:        "Desired smaller than remaining: desired 0.20, remaining 0.35 -> uses desired 0.20",
			desired:     decimal.RequireFromString("0.2000"),
			remaining:   decimal.RequireFromString("0.3500"),
			original:    decimal.RequireFromString("1.0000"),
			expectedQty: decimal.RequireFromString("0.2000"),
			expectedOk:  true,
		},
		{
			name:        "Zero remaining: fully filled -> no replacement",
			desired:     decimal.RequireFromString("1.0000"),
			remaining:   decimal.Zero,
			original:    decimal.RequireFromString("1.0000"),
			expectedQty: decimal.Zero,
			expectedOk:  false,
		},
		{
			name:        "Negative remaining: invalid -> no replacement",
			desired:     decimal.RequireFromString("1.0000"),
			remaining:   decimal.RequireFromString("-0.0500"),
			original:    decimal.RequireFromString("1.0000"),
			expectedQty: decimal.Zero,
			expectedOk:  false,
		},
		{
			name:        "Remaining exceeds original: 1.5 > 1.0 -> capped to 1.0",
			desired:     decimal.RequireFromString("2.0000"),
			remaining:   decimal.RequireFromString("1.5000"),
			original:    decimal.RequireFromString("1.0000"),
			expectedQty: decimal.RequireFromString("1.0000"),
			expectedOk:  true,
		},
		{
			name:        "Below min order size: dust -> no replacement",
			desired:     decimal.RequireFromString("1.0000"),
			remaining:   decimal.RequireFromString("0.00005"),
			original:    decimal.RequireFromString("1.0000"),
			expectedQty: decimal.Zero,
			expectedOk:  false,
		},
		{
			name:        "Lot size normalization: 0.35005 -> 0.3500",
			desired:     decimal.RequireFromString("1.0000"),
			remaining:   decimal.RequireFromString("0.35005"),
			original:    decimal.RequireFromString("1.0000"),
			expectedQty: decimal.RequireFromString("0.3500"),
			expectedOk:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			qty, ok := rec.ResolveReplacementQuantity(tc.desired, tc.remaining, tc.original, minSize, lotSize, marketID, levelID)
			if ok != tc.expectedOk {
				t.Fatalf("expected ok=%v, got %v", tc.expectedOk, ok)
			}
			if ok && !qty.Equal(tc.expectedQty) {
				t.Fatalf("expected qty=%s, got %s", tc.expectedQty.String(), qty.String())
			}
		})
	}
	_ = cfg
}

func TestLERefPrice_NormalDiffCorrect_PartialFill(t *testing.T) {
	h := newLERefPriceHarness(t, "95000.00", 500*time.Millisecond, 1*time.Second)
	ctx := context.Background()

	levelID := "BUY-01"
	orderID := "ord-diffcorrect-01"
	clientOrderID := "BTC-USDT-BUY-01-1"

	origQty := decimal.RequireFromString("1.00000")
	remQty := decimal.RequireFromString("0.35000")
	oldPrice := decimal.RequireFromString("94000.00")
	newPrice := decimal.RequireFromString("94100.00")

	// Order is RESTING with partial fill (remaining = 0.35 BTC)
	h.tracker.SetPending(levelID, orderID, clientOrderID, 1, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: h.marketID,
		Side:     "BUY",
		Price:    oldPrice,
		Quantity: origQty,
	})
	h.tracker.SetResting(levelID, orderID, origQty, remQty)

	// Ladder generates desired PriceLevel with new price and full quantity 1.0
	desiredLevel := pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   h.marketID,
		Side:       "BUY",
		Price:      newPrice,
		Quantity:   origQty, // Ladder produces full 1.0
		RefVersion: 1,
	}

	// Trigger ReconcileBatch / DiffCorrect
	diffEntry := order.DiffEntry{
		Action:       order.DiffCorrect,
		LevelID:      levelID,
		DesiredLevel: &desiredLevel,
		ExistingCOID: clientOrderID,
		ExistingOID:  orderID,
	}

	mc := h.cfg.ForMarket(h.marketID)
	published, err := h.rec.ApplyEntry(ctx, diffEntry, mc)
	if err != nil {
		t.Fatalf("ApplyEntry failed: %v", err)
	}
	if !published {
		t.Fatalf("expected cancel command to be published")
	}

	// LE-01 Invariant: QueuedCorrection must hold RemainingQty (0.35), NEVER ladder quantity (1.0)!
	tracked := h.tracker.Get(levelID)
	if tracked == nil || tracked.QueuedCorrection == nil {
		t.Fatalf("expected tracked order to have QueuedCorrection")
	}
	if !tracked.QueuedCorrection.Quantity.Equal(remQty) {
		t.Fatalf("LE-01 VIOLATION: QueuedCorrection has quantity %s (expected RemainingQty %s)",
			tracked.QueuedCorrection.Quantity.String(), remQty.String())
	}

	// Confirm cancellation in Order Service and invoke CheckCancellingTimeouts
	h.osSvc.orders[clientOrderID] = &orderservice.OrderState{
		OrderID:       orderID,
		ClientOrderID: clientOrderID,
		Status:        "CANCELLED",
		OriginalQty:   origQty,
		RemainingQty:  remQty,
	}
	tracked.CancellingSince = time.Now().Add(-100 * time.Millisecond)

	h.rec.CheckCancellingTimeouts(ctx, h.marketID)

	// Replacement order published to Order Service must be 0.35!
	if len(h.osSvc.createdOrders) != 1 {
		t.Fatalf("expected 1 replacement order in Order Service, got %d", len(h.osSvc.createdOrders))
	}
	if h.osSvc.createdOrders[0].Quantity != remQty.String() {
		t.Fatalf("LE-01 VIOLATION: created order quantity was %s (expected %s)",
			h.osSvc.createdOrders[0].Quantity, remQty.String())
	}
}

func TestLERefPrice_Rebase_CancelFailure_NoFalseProgress(t *testing.T) {
	h := newLERefPriceHarness(t, "90000.00", 500*time.Millisecond, 1*time.Second)
	ctx := context.Background()

	// Initial reconcile with Version 1
	_, err := h.rec.ReconcileMarket(ctx, h.marketID, 12, 12)
	if err != nil {
		t.Fatalf("initial reconcile failed: %v", err)
	}

	// Confirm orders resting with RefVersion 1
	for _, o := range h.tracker.All(h.marketID) {
		h.tracker.SetResting(o.LevelID, o.OrderID, o.OriginalQty, o.OriginalQty)
		h.osSvc.orders[o.ClientOrderID] = &orderservice.OrderState{
			OrderID:       o.OrderID,
			ClientOrderID: o.ClientOrderID,
			Status:        "OPEN",
			RemainingQty:  o.OriginalQty,
		}
	}

	// Large price move triggers rebase: 90,000 -> 95,000 (Version 2)
	h.fetcher.SetPrice(h.marketID, decimal.RequireFromString("95000.00"))

	// Wait for Provider Version 2
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if entry, ok := h.prov.Get(h.marketID); ok && entry.Version == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Simulate Kafka/producer cancel failure during rebase
	h.producer.cancelErr = errors.New("simulated Kafka cancel failure")

	mutations, err := h.rec.ReconcileMarket(ctx, h.marketID, 12, 12)
	if err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}
	if mutations != 0 {
		t.Fatalf("expected 0 mutations when cancel fails, got %d", mutations)
	}

	// LE-02 Invariant: RebaseActive must REMAIN TRUE and candidates must NOT be falsely counted as completed!
	refState := h.rec.GetRefState(h.marketID)
	if refState == nil {
		t.Fatalf("expected refState to exist")
	}
	if !refState.RebaseActive {
		t.Fatalf("LE-02 VIOLATION: RebaseActive was set to false even though cancellations failed!")
	}

	// Remove cancel error: subsequent reconcile cycle must retry and succeed
	h.producer.cancelErr = nil
	mutations, err = h.rec.ReconcileMarket(ctx, h.marketID, 12, 12)
	if err != nil {
		t.Fatalf("reconcile retry failed: %v", err)
	}
	if mutations == 0 || len(h.producer.cancelledOrders) == 0 {
		t.Fatalf("expected cancellations on retry cycle after cancel error cleared")
	}
}
