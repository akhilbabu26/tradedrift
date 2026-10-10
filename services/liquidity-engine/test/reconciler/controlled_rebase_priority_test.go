package reconciler_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/pricing"
	"tradedrift/services/liquidity-engine/internal/reconciler"
)

// TestControlledRebase_PrioritizesLowZoneLevelsFirst verifies that ActionControlledRebase
// prioritizes top-of-book LOW levels (L1, L2) before MID and HIGH levels across batches,
// preventing stale deep-generation liquidity from being exposed ahead of fresh top-of-book.
func TestControlledRebase_PrioritizesLowZoneLevelsFirst(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, _, _ := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.BidZones = config.DefaultZoneConfig()
	mc.AskZones = config.DefaultZoneConfig()
	mc.Repricing = config.RepricingConfig{
		SmallBps: 10,
		LargeBps: 100,
		MaxBatch: 2, // Process exactly 2 levels per cycle
	}

	// Setup 6 resting orders at RefVersion 1:
	// - 2 LOW zone levels (MM-BTC-USDT-BID-01, MM-BTC-USDT-ASK-01)
	// - 2 MID zone levels (MM-BTC-USDT-BID-05, MM-BTC-USDT-ASK-05)
	// - 2 HIGH zone levels (MM-BTC-USDT-BID-09, MM-BTC-USDT-ASK-09)
	levels := []struct {
		id    string
		side  string
		price string
		zone  string
	}{
		{"MM-BTC-USDT-BID-01", "BUY", "99.90", "LOW"},
		{"MM-BTC-USDT-ASK-01", "SELL", "100.10", "LOW"},
		{"MM-BTC-USDT-BID-05", "BUY", "98.00", "MID"},
		{"MM-BTC-USDT-ASK-05", "SELL", "102.00", "MID"},
		{"MM-BTC-USDT-BID-09", "BUY", "95.00", "HIGH"},
		{"MM-BTC-USDT-ASK-09", "SELL", "105.00", "HIGH"},
	}

	for _, l := range levels {
		tracker.SetPending(l.id, "ord-"+l.id, "coid-"+l.id, 1, pricing.PriceLevel{
			LevelID:    l.id,
			MarketID:   marketID,
			Side:       l.side,
			Price:      decimal.RequireFromString(l.price),
			Quantity:   decimal.RequireFromString("1.0"),
			Zone:       l.zone,
			RefVersion: 1,
		})
		tracker.SetResting(l.id, "ord-"+l.id, decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	}

	targetRef := decimal.RequireFromString("110.00")
	targetVersion := int64(2)
	refState := &reconciler.RefState{
		LastRef:         targetRef,
		LastVersion:     targetVersion,
		RebaseActive:    true,
		RebaseTargetVer: targetVersion,
		RebaseTargetRef: targetRef,
		RebaseAction:    reconciler.ActionControlledRebase,
	}
	rec.SetRefState(marketID, refState)

	// ── Batch 1: Apply controlled rebase (MaxBatch = 2) ──
	repriced := rec.ApplyRefMovementReprice(context.Background(), mc, targetRef, targetVersion, reconciler.ActionControlledRebase)
	if repriced != 2 {
		t.Fatalf("expected 2 orders repriced in batch 1, got %d", repriced)
	}

	// INVARIANT: Batch 1 MUST reprice the LOW zone levels (top of book)
	bid01 := tracker.Get("MM-BTC-USDT-BID-01")
	ask01 := tracker.Get("MM-BTC-USDT-ASK-01")
	if bid01.QueuedCorrection == nil {
		t.Fatalf("expected MM-BTC-USDT-BID-01 (LOW zone) to have queued correction in Batch 1")
	}
	if ask01.QueuedCorrection == nil {
		t.Fatalf("expected MM-BTC-USDT-ASK-01 (LOW zone) to have queued correction in Batch 1")
	}
	if bid01.QueuedCorrection.RefVersion != targetVersion || ask01.QueuedCorrection.RefVersion != targetVersion {
		t.Fatalf("expected Batch 1 corrections to target RefVersion %d", targetVersion)
	}

	// INVARIANT: MID and HIGH zone levels MUST NOT be repriced in Batch 1
	bid05 := tracker.Get("MM-BTC-USDT-BID-05")
	ask05 := tracker.Get("MM-BTC-USDT-ASK-05")
	bid09 := tracker.Get("MM-BTC-USDT-BID-09")
	ask09 := tracker.Get("MM-BTC-USDT-ASK-09")
	if bid05.QueuedCorrection != nil || ask05.QueuedCorrection != nil {
		t.Fatalf("MID zone levels must not be repriced before LOW zone levels in controlled rebase")
	}
	if bid09.QueuedCorrection != nil || ask09.QueuedCorrection != nil {
		t.Fatalf("HIGH zone levels must not be repriced before LOW zone levels in controlled rebase")
	}

	// Rebase must still be active since 4 levels remain
	if !refState.RebaseActive {
		t.Fatalf("expected RebaseActive=true after Batch 1")
	}

	// Simulate Batch 1 orders transitioning to resting at RefVersion 2
	tracker.SetResting("MM-BTC-USDT-BID-01", "ord-new-b01", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	tracker.Get("MM-BTC-USDT-BID-01").RefVersion = targetVersion
	tracker.Get("MM-BTC-USDT-BID-01").QueuedCorrection = nil

	tracker.SetResting("MM-BTC-USDT-ASK-01", "ord-new-a01", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	tracker.Get("MM-BTC-USDT-ASK-01").RefVersion = targetVersion
	tracker.Get("MM-BTC-USDT-ASK-01").QueuedCorrection = nil

	// ── Batch 2: Apply controlled rebase (MaxBatch = 2) ──
	repriced = rec.ApplyRefMovementReprice(context.Background(), mc, targetRef, targetVersion, reconciler.ActionControlledRebase)
	if repriced != 2 {
		t.Fatalf("expected 2 orders repriced in batch 2, got %d", repriced)
	}

	// INVARIANT: Batch 2 MUST reprice the MID zone levels
	if bid05.QueuedCorrection == nil || ask05.QueuedCorrection == nil {
		t.Fatalf("expected MID zone levels to have queued corrections in Batch 2")
	}
	if bid09.QueuedCorrection != nil || ask09.QueuedCorrection != nil {
		t.Fatalf("HIGH zone levels must not be repriced before MID zone levels")
	}

	// Simulate Batch 2 orders transitioning to resting at RefVersion 2
	tracker.SetResting("MM-BTC-USDT-BID-05", "ord-new-b05", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	tracker.Get("MM-BTC-USDT-BID-05").RefVersion = targetVersion
	tracker.Get("MM-BTC-USDT-BID-05").QueuedCorrection = nil

	tracker.SetResting("MM-BTC-USDT-ASK-05", "ord-new-a05", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	tracker.Get("MM-BTC-USDT-ASK-05").RefVersion = targetVersion
	tracker.Get("MM-BTC-USDT-ASK-05").QueuedCorrection = nil

	// ── Batch 3: Apply controlled rebase (MaxBatch = 2) ──
	repriced = rec.ApplyRefMovementReprice(context.Background(), mc, targetRef, targetVersion, reconciler.ActionControlledRebase)
	if repriced != 2 {
		t.Fatalf("expected 2 orders repriced in batch 3, got %d", repriced)
	}

	// INVARIANT: Batch 3 repriced the HIGH zone levels
	if bid09.QueuedCorrection == nil || ask09.QueuedCorrection == nil {
		t.Fatalf("expected HIGH zone levels to have queued corrections in Batch 3")
	}

	// Simulate Batch 3 orders transitioning to resting at RefVersion 2
	tracker.SetResting("MM-BTC-USDT-BID-09", "ord-new-b09", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	tracker.Get("MM-BTC-USDT-BID-09").RefVersion = targetVersion
	tracker.Get("MM-BTC-USDT-BID-09").QueuedCorrection = nil

	tracker.SetResting("MM-BTC-USDT-ASK-09", "ord-new-a09", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	tracker.Get("MM-BTC-USDT-ASK-09").RefVersion = targetVersion
	tracker.Get("MM-BTC-USDT-ASK-09").QueuedCorrection = nil

	// All levels converged -> RebaseActive becomes false
	repriced = rec.ApplyRefMovementReprice(context.Background(), mc, targetRef, targetVersion, reconciler.ActionControlledRebase)
	if repriced != 0 {
		t.Fatalf("expected 0 orders repriced after full convergence, got %d", repriced)
	}
	if refState.RebaseActive {
		t.Fatalf("expected RebaseActive=false after all 6 levels converged")
	}
}

// TestSelectiveReprice_RetainsHighZoneFirstPriority verifies that ActionSelectiveReprice
// preserves its existing intended priority: HIGH first, then MID, leaving LOW anchored.
func TestSelectiveReprice_RetainsHighZoneFirstPriority(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, _, _ := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.BidZones = config.DefaultZoneConfig()
	mc.Repricing = config.RepricingConfig{
		SmallBps: 10,
		LargeBps: 100,
		MaxBatch: 1, // Process exactly 1 level
	}

	// Setup 3 orders: 1 LOW (anchored), 1 MID, 1 HIGH
	tracker.SetPending("MM-BTC-USDT-BID-01", "ord-1", "coid-1", 1, pricing.PriceLevel{
		LevelID: "MM-BTC-USDT-BID-01", MarketID: marketID, Side: "BUY", Price: decimal.RequireFromString("99.90"), Quantity: decimal.RequireFromString("1.0"), Zone: "LOW", RefVersion: 1,
	})
	tracker.SetResting("MM-BTC-USDT-BID-01", "ord-1", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))

	tracker.SetPending("MM-BTC-USDT-BID-05", "ord-5", "coid-5", 1, pricing.PriceLevel{
		LevelID: "MM-BTC-USDT-BID-05", MarketID: marketID, Side: "BUY", Price: decimal.RequireFromString("98.00"), Quantity: decimal.RequireFromString("1.0"), Zone: "MID", RefVersion: 1,
	})
	tracker.SetResting("MM-BTC-USDT-BID-05", "ord-5", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))

	tracker.SetPending("MM-BTC-USDT-BID-09", "ord-9", "coid-9", 1, pricing.PriceLevel{
		LevelID: "MM-BTC-USDT-BID-09", MarketID: marketID, Side: "BUY", Price: decimal.RequireFromString("95.00"), Quantity: decimal.RequireFromString("1.0"), Zone: "HIGH", RefVersion: 1,
	})
	tracker.SetResting("MM-BTC-USDT-BID-09", "ord-9", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))

	targetRef := decimal.RequireFromString("102.00")
	targetVersion := int64(2)
	refState := &reconciler.RefState{
		LastRef:         targetRef,
		LastVersion:     targetVersion,
		RebaseActive:    true,
		RebaseTargetVer: targetVersion,
		RebaseTargetRef: targetRef,
		RebaseAction:    reconciler.ActionSelectiveReprice,
	}
	rec.SetRefState(marketID, refState)

	// Apply Selective Reprice with MaxBatch = 1
	repriced := rec.ApplyRefMovementReprice(context.Background(), mc, targetRef, targetVersion, reconciler.ActionSelectiveReprice)
	if repriced != 1 {
		t.Fatalf("expected 1 order repriced, got %d", repriced)
	}

	// INVARIANT: HIGH zone order must be prioritized first in selective repricing
	bid09 := tracker.Get("MM-BTC-USDT-BID-09")
	if bid09.QueuedCorrection == nil {
		t.Fatalf("expected HIGH zone level BID-09 to be repriced first in selective repricing")
	}

	// MID must NOT be repriced yet
	bid05 := tracker.Get("MM-BTC-USDT-BID-05")
	if bid05.QueuedCorrection != nil {
		t.Fatalf("MID zone level must not be repriced before HIGH in selective repricing")
	}

	// LOW must NEVER be touched in selective repricing
	bid01 := tracker.Get("MM-BTC-USDT-BID-01")
	if bid01.QueuedCorrection != nil {
		t.Fatalf("LOW zone level must remain anchored and never touched in selective repricing")
	}
}

// TestTracker_InsideSpread_Calculation verifies that Tracker.InsideSpread calculates
// the exact top-of-book best bid and best ask correctly and handles depleted sides.
func TestTracker_InsideSpread_Calculation(t *testing.T) {
	tracker := order.NewTracker()
	marketID := "BTC-USDT"

	// Empty book: ok must be false
	_, _, ok := tracker.InsideSpread(marketID)
	if ok {
		t.Fatalf("expected InsideSpread to return ok=false for empty book")
	}

	// Only bids resting: ok must be false
	tracker.SetPending("BID-01", "o1", "c1", 1, pricing.PriceLevel{MarketID: marketID, Side: "BUY", Price: decimal.RequireFromString("90000.00"), Quantity: decimal.NewFromInt(1)})
	tracker.SetResting("BID-01", "o1", decimal.NewFromInt(1), decimal.NewFromInt(1))
	_, _, ok = tracker.InsideSpread(marketID)
	if ok {
		t.Fatalf("expected InsideSpread to return ok=false when ask side is depleted")
	}

	// Add ask resting: ok must be true
	tracker.SetPending("ASK-01", "o2", "c2", 1, pricing.PriceLevel{MarketID: marketID, Side: "SELL", Price: decimal.RequireFromString("90100.00"), Quantity: decimal.NewFromInt(1)})
	tracker.SetResting("ASK-01", "o2", decimal.NewFromInt(1), decimal.NewFromInt(1))

	bestBid, bestAsk, ok := tracker.InsideSpread(marketID)
	if !ok {
		t.Fatalf("expected InsideSpread to return ok=true when both sides exist")
	}
	if !bestBid.Equal(decimal.RequireFromString("90000.00")) {
		t.Fatalf("expected bestBid 90000.00, got %s", bestBid)
	}
	if !bestAsk.Equal(decimal.RequireFromString("90100.00")) {
		t.Fatalf("expected bestAsk 90100.00, got %s", bestAsk)
	}

	// Add a better bid and ask
	tracker.SetPending("BID-02", "o3", "c3", 1, pricing.PriceLevel{MarketID: marketID, Side: "BUY", Price: decimal.RequireFromString("90050.00"), Quantity: decimal.NewFromInt(1)})
	tracker.SetResting("BID-02", "o3", decimal.NewFromInt(1), decimal.NewFromInt(1))

	tracker.SetPending("ASK-02", "o4", "c4", 1, pricing.PriceLevel{MarketID: marketID, Side: "SELL", Price: decimal.RequireFromString("90070.00"), Quantity: decimal.NewFromInt(1)})
	tracker.SetResting("ASK-02", "o4", decimal.NewFromInt(1), decimal.NewFromInt(1))

	bestBid, bestAsk, ok = tracker.InsideSpread(marketID)
	if !bestBid.Equal(decimal.RequireFromString("90050.00")) {
		t.Fatalf("expected bestBid 90050.00, got %s", bestBid)
	}
	if !bestAsk.Equal(decimal.RequireFromString("90070.00")) {
		t.Fatalf("expected bestAsk 90070.00, got %s", bestAsk)
	}
}
