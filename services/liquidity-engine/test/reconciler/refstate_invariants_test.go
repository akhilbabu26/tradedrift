package reconciler_test

import (
	"context"
	"encoding/json"
	"fmt"
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
	"tradedrift/services/liquidity-engine/internal/orderservice"
	"tradedrift/services/liquidity-engine/internal/pricing"
	"tradedrift/services/liquidity-engine/internal/reconciler"
)

// Test 1: Multi-cycle controlled rebase convergence across cycles
func TestControlledRebase_MultiCycleConvergence(t *testing.T) {
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

	// Mock ME server that reflects current resting orders
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var meOrders []meclient.MMOrderSummary
		for _, o := range tracker.All(marketID) {
			if o.Status == order.StatusResting {
				meOrders = append(meOrders, meclient.MMOrderSummary{
					LevelID:           o.LevelID,
					OrderID:           o.OrderID,
					ClientOrderID:     o.ClientOrderID,
					Side:              o.Side,
					Price:             o.Price.String(),
					RemainingQuantity: o.RemainingQty.String(),
				})
			}
		}
		_ = json.NewEncoder(w).Encode(meclient.MarketSnapshot{
			MarketID: marketID,
			State:    "LIVE",
			Orders:   meOrders,
		})
	}))
	defer srv.Close()
	rec.SetMEClient(meclient.New(srv.URL, zap.NewNop()))

	// Populate tracker with 8 eligible RESTING orders: 4 HIGH and 4 MID
	levels := []struct {
		id   string
		zone string
		side string
	}{
		{"MM-BTC-USDT-BID-12", "HIGH", "BUY"},
		{"MM-BTC-USDT-BID-11", "HIGH", "BUY"},
		{"MM-BTC-USDT-ASK-12", "HIGH", "SELL"},
		{"MM-BTC-USDT-ASK-11", "HIGH", "SELL"},
		{"MM-BTC-USDT-BID-08", "MID", "BUY"},
		{"MM-BTC-USDT-BID-07", "MID", "BUY"},
		{"MM-BTC-USDT-ASK-08", "MID", "SELL"},
		{"MM-BTC-USDT-ASK-07", "MID", "SELL"},
	}

	for i, l := range levels {
		p := decimal.RequireFromString("95.00")
		if l.side == "SELL" {
			p = decimal.RequireFromString("105.00")
		}
		coid := fmt.Sprintf("%s-G001", l.id)
		oid := fmt.Sprintf("order-%d", i+1)
		tracker.SetPending(l.id, oid, coid, 1, pricing.PriceLevel{
			LevelID:    l.id,
			MarketID:   marketID,
			Side:       l.side,
			Price:      p,
			Quantity:   decimal.RequireFromString("1.0"),
			Zone:       l.zone,
			RefVersion: 1,
		})
		tracker.SetResting(l.id, oid, decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	}

	// Establish baseline reference at 100.00
	rec.SetRefState(marketID, &reconciler.RefState{
		LastRef:     decimal.RequireFromString("100.00"),
		LastVersion: 1,
		LastUpdated: time.Now(),
		Freshness:   reconciler.FreshnessFresh,
	})

	// Large reference move: 100.00 -> 103.00 (300 bps) at version 2
	mockProv.entries[marketID] = refprice.Entry{
		MarketID:  marketID,
		Price:     decimal.RequireFromString("103.00"),
		Version:   2,
		FetchedAt: time.Now(),
		State:     refprice.StateFresh,
	}

	ctx := context.Background()

	// Cycle 1: Reprices 2 HIGH orders
	prod.cancelledOrders = nil
	_, err := rec.ReconcileMarket(ctx, marketID, 12, 12)
	if err != nil {
		t.Fatalf("cycle 1 failed: %v", err)
	}
	if len(prod.cancelledOrders) != 2 {
		t.Fatalf("cycle 1: expected 2 cancelled orders, got %d", len(prod.cancelledOrders))
	}
	refState := rec.GetRefState(marketID)
	if !refState.RebaseActive {
		t.Fatal("cycle 1: expected RebaseActive to be true")
	}
	if refState.RebaseTargetVer != 2 {
		t.Fatalf("cycle 1: expected RebaseTargetVer=2, got %d", refState.RebaseTargetVer)
	}

	// Cycle 2: Reference stays at 103.00 (0 bps move relative to previous cycle!)
	// Persistent rebase MUST continue and reprice the next 2 orders
	prod.cancelledOrders = nil
	_, err = rec.ReconcileMarket(ctx, marketID, 12, 12)
	if err != nil {
		t.Fatalf("cycle 2 failed: %v", err)
	}
	if len(prod.cancelledOrders) != 2 {
		t.Fatalf("cycle 2: expected 2 more cancelled orders under persistent rebase, got %d", len(prod.cancelledOrders))
	}
	if !refState.RebaseActive {
		t.Fatal("cycle 2: expected RebaseActive to remain true")
	}

	// Cycle 3: Reprices next 2 orders (MID)
	prod.cancelledOrders = nil
	_, err = rec.ReconcileMarket(ctx, marketID, 12, 12)
	if err != nil {
		t.Fatalf("cycle 3 failed: %v", err)
	}
	if len(prod.cancelledOrders) != 2 {
		t.Fatalf("cycle 3: expected 2 more cancelled orders, got %d", len(prod.cancelledOrders))
	}
	if !refState.RebaseActive {
		t.Fatal("cycle 3: expected RebaseActive to remain true")
	}

	// Cycle 4: Reprices final 2 orders
	prod.cancelledOrders = nil
	_, err = rec.ReconcileMarket(ctx, marketID, 12, 12)
	if err != nil {
		t.Fatalf("cycle 4 failed: %v", err)
	}
	if len(prod.cancelledOrders) != 2 {
		t.Fatalf("cycle 4: expected 2 final cancelled orders, got %d", len(prod.cancelledOrders))
	}
	if refState.RebaseActive {
		t.Fatal("cycle 4: expected RebaseActive to be false after all eligible levels rebased")
	}

	// Cycle 5: Rebase finished — 0 orders cancelled
	prod.cancelledOrders = nil
	_, err = rec.ReconcileMarket(ctx, marketID, 12, 12)
	if err != nil {
		t.Fatalf("cycle 5 failed: %v", err)
	}
	if len(prod.cancelledOrders) != 0 {
		t.Fatalf("cycle 5: expected 0 cancels on completed rebase, got %d", len(prod.cancelledOrders))
	}

	// Invariant: Mid-rebase new version update
	refState.RebaseActive = true
	refState.RebaseTargetVer = 2
	refState.RebaseTargetRef = decimal.RequireFromString("103.00")

	// Version 3 arrives
	mockProv.entries[marketID] = refprice.Entry{
		MarketID:  marketID,
		Price:     decimal.RequireFromString("105.00"),
		Version:   3,
		FetchedAt: time.Now(),
		State:     refprice.StateFresh,
	}
	_, err = rec.ReconcileMarket(ctx, marketID, 12, 12)
	if err != nil {
		t.Fatalf("mid-rebase update cycle failed: %v", err)
	}
	if refState.RebaseTargetVer != 3 {
		t.Fatalf("expected RebaseTargetVer to update to newest authoritative version 3, got %d", refState.RebaseTargetVer)
	}
	if !refState.RebaseTargetRef.Equal(decimal.RequireFromString("105.00")) {
		t.Fatalf("expected RebaseTargetRef to update to 105.00, got %s", refState.RebaseTargetRef)
	}
}

// Test 2: STALE reference strictly blocks new order creation
func TestStaleReference_BlocksCreate(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, _, prod, _ := newInvTestRec(t, marketID)

	// Populate tracker with 11 resting BID levels (1 missing level vs 12 desired)
	for i := 1; i <= 11; i++ {
		levelID := fmt.Sprintf("MM-BTC-USDT-BID-%02d", i)
		coid := fmt.Sprintf("%s-G001", levelID)
		oid := fmt.Sprintf("order-bid-%d", i)
		tracker.SetPending(levelID, oid, coid, 1, pricing.PriceLevel{
			LevelID:    levelID,
			MarketID:   marketID,
			Side:       "BUY",
			Price:      decimal.RequireFromString("95000.00").Sub(decimal.NewFromInt(int64(i * 10))),
			Quantity:   decimal.RequireFromString("1.0"),
			Zone:       "LOW",
			RefVersion: 1,
		})
		tracker.SetResting(levelID, oid, decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	}
	// Populate 12 resting ASK levels
	for i := 1; i <= 12; i++ {
		levelID := fmt.Sprintf("MM-BTC-USDT-ASK-%02d", i)
		coid := fmt.Sprintf("%s-G001", levelID)
		oid := fmt.Sprintf("order-ask-%d", i)
		tracker.SetPending(levelID, oid, coid, 1, pricing.PriceLevel{
			LevelID:    levelID,
			MarketID:   marketID,
			Side:       "SELL",
			Price:      decimal.RequireFromString("97000.00").Add(decimal.NewFromInt(int64(i * 10))),
			Quantity:   decimal.RequireFromString("1.0"),
			Zone:       "LOW",
			RefVersion: 1,
		})
		tracker.SetResting(levelID, oid, decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	}

	// Configure provider with STALE state
	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{
			marketID: {
				MarketID:  marketID,
				Price:     decimal.RequireFromString("96000.00"),
				Version:   1,
				FetchedAt: time.Now().Add(-6 * time.Minute),
				State:     refprice.StateStale,
			},
		},
	}
	rec.SetRefProvider(mockProv)

	// Mock ME server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var meOrders []meclient.MMOrderSummary
		for _, o := range tracker.All(marketID) {
			if o.Status == order.StatusResting {
				meOrders = append(meOrders, meclient.MMOrderSummary{
					LevelID:       o.LevelID,
					OrderID:       o.OrderID,
					ClientOrderID: o.ClientOrderID,
					Side:          o.Side,
					Price:         o.Price.String(),
					RemainingQuantity: o.RemainingQty.String(),
				})
			}
		}
		_ = json.NewEncoder(w).Encode(meclient.MarketSnapshot{
			MarketID: marketID,
			State:    "LIVE",
			Orders:   meOrders,
		})
	}))
	defer srv.Close()
	rec.SetMEClient(meclient.New(srv.URL, zap.NewNop()))

	prod.createdOrders = nil
	cmds, err := rec.ReconcileMarket(context.Background(), marketID, 12, 12)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	// Assert: STALE reference must NOT create any new orders
	if len(prod.createdOrders) != 0 {
		t.Fatalf("expected 0 order creations under STALE reference, got %d", len(prod.createdOrders))
	}
	if cmds != 0 {
		t.Fatalf("expected 0 commands published under STALE with no modifications, got %d", cmds)
	}

	// Missing 12th level must NOT exist in tracker
	if tracker.Get("MM-BTC-USDT-BID-12") != nil {
		t.Fatal("expected MM-BTC-USDT-BID-12 to NOT be created in tracker under STALE")
	}
}

// Test 3: Reprice batch generates strictly unique replacement prices
func TestRepriceBatch_GuaranteedPriceUniqueness(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, _, _ := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.Repricing = config.RepricingConfig{
		SmallBps: 10,
		LargeBps: 100,
		MaxBatch: 4,
	}

	// Setup 4 resting HIGH orders
	for i := 1; i <= 4; i++ {
		levelID := fmt.Sprintf("MM-BTC-USDT-BID-%02d", i)
		coid := fmt.Sprintf("%s-G001", levelID)
		oid := fmt.Sprintf("order-%d", i)
		tracker.SetPending(levelID, oid, coid, 1, pricing.PriceLevel{
			LevelID:    levelID,
			MarketID:   marketID,
			Side:       "BUY",
			Price:      decimal.RequireFromString("90000.00").Add(decimal.NewFromInt(int64(i * 10))),
			Quantity:   decimal.RequireFromString("1.0"),
			Zone:       "HIGH",
			RefVersion: 1,
		})
		tracker.SetResting(levelID, oid, decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	}

	targetRef := decimal.RequireFromString("96000.00")
	rec.ApplyRefMovementReprice(context.Background(), mc, targetRef, 2, reconciler.ActionControlledRebase)

	// Collect generated replacement prices
	seen := make(map[string]bool)
	count := 0
	for i := 1; i <= 4; i++ {
		levelID := fmt.Sprintf("MM-BTC-USDT-BID-%02d", i)
		o := tracker.Get(levelID)
		if o.QueuedCorrection != nil {
			priceStr := o.QueuedCorrection.Price.String()
			if seen[priceStr] {
				t.Fatalf("duplicate replacement price generated in batch: %s", priceStr)
			}
			seen[priceStr] = true
			count++
		}
	}

	if count != 4 {
		t.Fatalf("expected 4 queued corrections, got %d", count)
	}
}

// Test 4: Expiry uses live reference price and current RefVersion
func TestExpiry_UsesLiveReferenceAndVersion(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, _, _ := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.ReferencePrice = decimal.RequireFromString("96450.00") // Old static seed
	mc.Repricing = config.RepricingConfig{
		OrderLifetime: 10 * time.Minute,
		MaxBatch:      2,
	}

	// Live provider returns 101500.00 with version 42
	livePrice := decimal.RequireFromString("101500.00")
	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{
			marketID: {
				MarketID:  marketID,
				Price:     livePrice,
				Version:   42,
				FetchedAt: time.Now(),
				State:     refprice.StateFresh,
			},
		},
	}
	rec.SetRefProvider(mockProv)

	// Seed an expired order
	levelID := "MM-BTC-USDT-BID-01"
	tracker.SetPending(levelID, "order-exp-1", "MM-BTC-USDT-BID-01-G001", 1, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("96000.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 10,
	})
	tracker.SetResting(levelID, "order-exp-1", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	o := tracker.Get(levelID)
	o.CreatedAt = time.Now().Add(-20 * time.Minute) // Expired

	expiredCount := rec.CheckExpiredOrders(context.Background(), marketID)
	if expiredCount != 1 {
		t.Fatalf("expected 1 expired order, got %d", expiredCount)
	}

	updated := tracker.Get(levelID)
	if updated.QueuedCorrection == nil {
		t.Fatal("expected QueuedCorrection on expired order")
	}

	// Must use live RefVersion 42 (not old 10)
	if updated.QueuedCorrection.RefVersion != 42 {
		t.Fatalf("expected queued correction RefVersion=42, got %d", updated.QueuedCorrection.RefVersion)
	}

	// Must be priced relative to live ref 101500 (LOW band is within 10-40 bps: ~101000 - 101400)
	// It must NOT be priced near 96450!
	price := updated.QueuedCorrection.Price
	if price.LessThan(decimal.RequireFromString("100000.00")) {
		t.Fatalf("expected replacement price near live ref 101500.00, but got %s (appears anchored to static 96450)", price)
	}
}

// Test 5: Provider unreachable fails closed to STALE
func TestProviderUnreachable_TransitionsToStale(t *testing.T) {
	marketID := "BTC-USDT"
	rec, _, cfg, _, _ := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]

	// Provider wired, but returns ok == false
	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{},
	}
	rec.SetRefProvider(mockProv)

	ref, ver, freshness := rec.CurrentReference(mc)
	if freshness != reconciler.FreshnessStale {
		t.Fatalf("expected FreshnessStale when provider is unreachable, got %s", freshness)
	}
	if ref.IsZero() {
		t.Fatal("expected non-zero fallback reference price")
	}
	if ver != 0 {
		t.Fatalf("expected version 0 on startup fallback, got %d", ver)
	}
}

// Test 6: Complete STALE -> PAUSED -> FRESH recovery lifecycle
func TestStaleToPausedTransition(t *testing.T) {
	marketID := "BTC-USDT"
	rec, _, cfg, _, _ := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]

	cfg.RefPrice = config.RefPriceConfig{
		StaleThreshold: 5 * time.Minute,
		PauseThreshold: 10 * time.Minute,
	}

	mockProv := &mockRefProvider{
		entries: make(map[string]refprice.Entry),
	}
	rec.SetRefProvider(mockProv)

	// Step 1: Valid reference fetch -> FRESH
	now := time.Now()
	mockProv.entries[marketID] = refprice.Entry{
		MarketID:  marketID,
		Price:     decimal.RequireFromString("100.00"),
		Version:   10,
		FetchedAt: now,
		State:     refprice.StateFresh,
	}

	_, _, freshness := rec.CurrentReference(mc)
	if freshness != reconciler.FreshnessFresh {
		t.Fatalf("step 1: expected FRESH, got %s", freshness)
	}

	// Step 2: Provider becomes unreachable -> STALE
	delete(mockProv.entries, marketID)

	_, _, freshness = rec.CurrentReference(mc)
	if freshness != reconciler.FreshnessStale {
		t.Fatalf("step 2: expected STALE immediately after provider failure, got %s", freshness)
	}

	// Step 3: 7 minutes elapsed (past StaleThreshold, but before PauseThreshold 10m) -> still STALE
	refState := rec.GetRefState(marketID)
	refState.LastUpdated = time.Now().Add(-7 * time.Minute)
	_, _, freshness = rec.CurrentReference(mc)
	if freshness != reconciler.FreshnessStale {
		t.Fatalf("step 3: expected still STALE at 7 minutes, got %s", freshness)
	}

	// Step 4: 12 minutes elapsed (past PauseThreshold 10m) -> PAUSED
	refState.LastUpdated = time.Now().Add(-12 * time.Minute)
	_, _, freshness = rec.CurrentReference(mc)
	if freshness != reconciler.FreshnessPaused {
		t.Fatalf("step 4: expected PAUSED at 12 minutes, got %s", freshness)
	}

	// Step 5: Provider recovers with fresh reference -> FRESH
	mockProv.entries[marketID] = refprice.Entry{
		MarketID:  marketID,
		Price:     decimal.RequireFromString("102.00"),
		Version:   11,
		FetchedAt: time.Now(),
		State:     refprice.StateFresh,
	}

	ref, ver, freshness := rec.CurrentReference(mc)
	if freshness != reconciler.FreshnessFresh {
		t.Fatalf("step 5: expected recovery to FRESH, got %s", freshness)
	}
	if !ref.Equal(decimal.RequireFromString("102.00")) {
		t.Fatalf("step 5: expected recovered price 102.00, got %s", ref)
	}
	if ver != 11 {
		t.Fatalf("step 5: expected recovered version 11, got %d", ver)
	}
}

// Test 7: PAUSED invariant: Zero mutations even with orphan orders in ME and missing orders in tracker
func TestPAUSED_ZeroMutationsWithOrphansAndMissingOrders(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, _, prod, osSvc := newInvTestRec(t, marketID)

	// Seed 1 resting order in tracker that will be MISSING from ME snapshot
	missingLevelID := "MM-BTC-USDT-BID-01"
	tracker.SetPending(missingLevelID, "ord-1", "MM-BTC-USDT-BID-01-G001", 1, pricing.PriceLevel{
		LevelID:    missingLevelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("99.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetResting(missingLevelID, "ord-1", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	rec.SetMissingCycles(missingLevelID, 5) // already exceeded 2-cycle hysteresis

	// Mock ME server returning an orphan order (not tracked by LE) and NOT returning ord-1
	meSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		snap := meclient.MarketSnapshot{
			MarketID: marketID,
			Sequence: 50,
			State:    "LIVE",
			Orders: []meclient.MMOrderSummary{
				{
					LevelID:           "MM-BTC-USDT-ASK-99",
					OrderID:           "orphan-me-ord-999",
					ClientOrderID:     "MM-BTC-USDT-ASK-99-G001",
					Side:              "SELL",
					Price:             "105.00",
					RemainingQuantity: "2.0",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(snap)
	}))
	defer meSrv.Close()

	rec.SetMEClient(meclient.New(meSrv.URL, zap.NewNop()))

	// Provider returns PAUSED
	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{
			marketID: {
				MarketID:  marketID,
				Price:     decimal.RequireFromString("100.00"),
				Version:   1,
				FetchedAt: time.Now().Add(-15 * time.Minute),
				State:     refprice.StatePaused,
			},
		},
	}
	rec.SetRefProvider(mockProv)

	// Execute ReconcileMarket
	published, err := rec.ReconcileMarket(context.Background(), marketID, 12, 12)
	if err != nil {
		t.Fatalf("unexpected error during reconcile: %v", err)
	}

	// Invariant: PAUSED MUST publish exactly 0 commands
	if published != 0 {
		t.Fatalf("expected 0 published commands under PAUSED, got %d", published)
	}
	if len(prod.cancelledOrders) != 0 {
		t.Fatalf("expected 0 cancel commands published to Kafka under PAUSED, got %d: %v", len(prod.cancelledOrders), prod.cancelledOrders)
	}
	if len(prod.createdOrders) != 0 {
		t.Fatalf("expected 0 create commands published to Kafka under PAUSED, got %d: %v", len(prod.createdOrders), prod.createdOrders)
	}
	if len(osSvc.cancelCalled) != 0 {
		t.Fatalf("expected 0 cancel calls to Order Service under PAUSED, got %d", len(osSvc.cancelCalled))
	}

	// Invariant: Missing order in tracker must NOT be mutated to CANCELLING or removed
	tracked := tracker.Get(missingLevelID)
	if tracked == nil || tracked.Status != order.StatusResting {
		t.Fatalf("expected missing order to remain untouched in RESTING under PAUSED, got %v", tracked)
	}
}

// Test 8: Correction replacement strictly enforces capital exposure caps
func TestCorrectionReplacement_RespectsCapitalBudget(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, osSvc := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.MaxBidExposureUSDT = decimal.RequireFromString("900.00")

	// Order 1 already consumes 850 USDT
	level1 := "MM-BTC-USDT-BID-01"
	tracker.SetPending(level1, "ord-1", "MM-BTC-USDT-BID-01-G001", 1, pricing.PriceLevel{
		LevelID:    level1,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("100.00"),
		Quantity:   decimal.RequireFromString("8.5"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetResting(level1, "ord-1", decimal.RequireFromString("8.5"), decimal.RequireFromString("8.5"))

	// Order 2 is CANCELLING with QueuedCorrection for 3.0 BTC @ 100.00 = 300 USDT (850 + 300 = 1150 > 1000)
	level2 := "MM-BTC-USDT-BID-02"
	coid2 := "MM-BTC-USDT-BID-02-G001"
	tracker.SetPending(level2, "ord-2", coid2, 1, pricing.PriceLevel{
		LevelID:    level2,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("98.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetCancelling(level2)
	tracker.QueueCorrection(level2, pricing.PriceLevel{
		LevelID:    level2,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("100.00"),
		Quantity:   decimal.RequireFromString("3.0"),
		Zone:       "LOW",
		RefVersion: 2,
	})

	// Order Service reports ord-2 is CANCELLED
	osSvc.orders[coid2] = &orderservice.OrderState{
		OrderID:       "ord-2",
		ClientOrderID: coid2,
		Status:        "CANCELLED",
		OriginalQty:   decimal.RequireFromString("1.0"),
		RemainingQty:  decimal.RequireFromString("1.0"),
	}

	o2 := tracker.Get(level2)
	rec.HandleCancellingTimeout(context.Background(), o2, mc)

	// Invariant: replacement MUST NOT be created because projected quote (1150) exceeds MaxBidExposureUSDT (1000)
	if len(osSvc.createdOrders) != 0 {
		t.Fatalf("expected replacement to be blocked by capital budget, but OS create was called: %v", osSvc.createdOrders)
	}
	if len(prod.createdOrders) != 0 {
		t.Fatalf("expected 0 Kafka creates, got %d", len(prod.createdOrders))
	}
}

// Test 9: Correction replacement uses min(desiredQty, authoritativeRemainingQty) on partial fill
func TestCorrectionReplacement_PartialFillQuantity(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, osSvc := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.MinOrderSize = decimal.RequireFromString("0.001")
	mc.LotSize = decimal.RequireFromString("0.001")

	levelID := "MM-BTC-USDT-BID-01"
	coid := "MM-BTC-USDT-BID-01-G001"

	// Old order had OriginalQty = 1.0, was CANCELLING with QueuedCorrection desired = 1.0
	tracker.SetPending(levelID, "ord-1", coid, 1, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("100.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetCancelling(levelID)
	tracker.QueueCorrection(levelID, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("101.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 2,
	})

	// Order Service confirms CANCELLED, but 0.6 was filled before cancel took effect -> RemainingQty = 0.4
	osSvc.orders[coid] = &orderservice.OrderState{
		OrderID:       "ord-1",
		ClientOrderID: coid,
		Status:        "CANCELLED",
		OriginalQty:   decimal.RequireFromString("1.0"),
		RemainingQty:  decimal.RequireFromString("0.4"),
	}

	o := tracker.Get(levelID)
	rec.HandleCancellingTimeout(context.Background(), o, mc)

	// Invariant: replacement MUST be created with quantity 0.4, NOT original 1.0
	if len(osSvc.createdOrders) != 1 {
		t.Fatalf("expected 1 replacement order created, got %d", len(osSvc.createdOrders))
	}
	created := osSvc.createdOrders[0]
	expectedQty := decimal.RequireFromString("0.4")
	actualQty, err := decimal.NewFromString(created.Quantity)
	if err != nil || !actualQty.Equal(expectedQty) {
		t.Fatalf("expected replacement quantity %s, got %s", expectedQty, created.Quantity)
	}

	if len(prod.createdOrders) != 1 {
		t.Fatalf("expected 1 Kafka create published, got %d", len(prod.createdOrders))
	}
}

// Test 10: Correction replacement skips dust when RemainingQty < MinOrderSize
func TestCorrectionReplacement_DustValidation(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, osSvc := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.MinOrderSize = decimal.RequireFromString("0.01")
	mc.LotSize = decimal.RequireFromString("0.001")

	levelID := "MM-BTC-USDT-BID-01"
	coid := "MM-BTC-USDT-BID-01-G001"

	tracker.SetPending(levelID, "ord-1", coid, 1, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("100.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetCancelling(levelID)
	tracker.QueueCorrection(levelID, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("101.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 2,
	})

	// Order Service confirms CANCELLED, remaining is 0.005 BTC (below MinOrderSize 0.01)
	osSvc.orders[coid] = &orderservice.OrderState{
		OrderID:       "ord-1",
		ClientOrderID: coid,
		Status:        "CANCELLED",
		OriginalQty:   decimal.RequireFromString("1.0"),
		RemainingQty:  decimal.RequireFromString("0.005"),
	}

	o := tracker.Get(levelID)
	rec.HandleCancellingTimeout(context.Background(), o, mc)

	// Invariant: replacement MUST be skipped because quantity is below MinOrderSize
	if len(osSvc.createdOrders) != 0 {
		t.Fatalf("expected dust replacement to be skipped, got %v", osSvc.createdOrders)
	}
	if len(prod.createdOrders) != 0 {
		t.Fatalf("expected 0 Kafka creates, got %d", len(prod.createdOrders))
	}
}

// Test 11: Controlled rebase keeps RebaseActive=true while old-version PENDING/OS_REGISTERED orders exist
func TestControlledRebase_AwaitsPendingAndOSRegisteredOldVersions(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, _, _ := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.Repricing = config.RepricingConfig{
		SmallBps: 10,
		LargeBps: 100,
		MaxBatch: 4,
	}

	// 2 orders RESTING at v1
	tracker.SetPending("MM-BTC-USDT-BID-01", "ord-1", "COID-1", 1, pricing.PriceLevel{
		LevelID: "MM-BTC-USDT-BID-01", MarketID: marketID, Side: "BUY", Price: decimal.RequireFromString("98.00"), Quantity: decimal.RequireFromString("1.0"), Zone: "HIGH", RefVersion: 1,
	})
	tracker.SetResting("MM-BTC-USDT-BID-01", "ord-1", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))

	tracker.SetPending("MM-BTC-USDT-BID-02", "ord-2", "COID-2", 1, pricing.PriceLevel{
		LevelID: "MM-BTC-USDT-BID-02", MarketID: marketID, Side: "BUY", Price: decimal.RequireFromString("97.00"), Quantity: decimal.RequireFromString("1.0"), Zone: "MID", RefVersion: 1,
	})
	tracker.SetResting("MM-BTC-USDT-BID-02", "ord-2", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))

	// 1 order PENDING at v1
	tracker.SetPending("MM-BTC-USDT-BID-03", "ord-3", "COID-3", 1, pricing.PriceLevel{
		LevelID: "MM-BTC-USDT-BID-03", MarketID: marketID, Side: "BUY", Price: decimal.RequireFromString("96.00"), Quantity: decimal.RequireFromString("1.0"), Zone: "LOW", RefVersion: 1,
	})

	// 1 order OS_REGISTERED at v1
	tracker.SetPending("MM-BTC-USDT-BID-04", "ord-4", "COID-4", 1, pricing.PriceLevel{
		LevelID: "MM-BTC-USDT-BID-04", MarketID: marketID, Side: "BUY", Price: decimal.RequireFromString("95.00"), Quantity: decimal.RequireFromString("1.0"), Zone: "LOW", RefVersion: 1,
	})
	tracker.SetOSRegistered("MM-BTC-USDT-BID-04", "ord-4", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))

	// Set initial refState with target version 2
	refState := &reconciler.RefState{
		LastRef:         decimal.RequireFromString("105.00"),
		LastVersion:     2,
		RebaseActive:    true,
		RebaseTargetVer: 2,
		RebaseTargetRef: decimal.RequireFromString("105.00"),
	}
	rec.SetRefState(marketID, refState)

	// Cycle 1: Apply rebase for targetVersion 2
	rec.ApplyRefMovementReprice(context.Background(), mc, decimal.RequireFromString("105.00"), 2, reconciler.ActionControlledRebase)

	// Invariant: The 2 RESTING orders were reordered and processed (remaining RESTING = 0).
	// But because BID-03 (PENDING) and BID-04 (OS_REGISTERED) still have RefVersion 1 < 2,
	// RebaseActive MUST remain true!
	state := rec.GetRefState(marketID)
	if !state.RebaseActive {
		t.Fatalf("expected RebaseActive=true while PENDING/OS_REGISTERED orders have RefVersion < targetVersion")
	}

	// Now promote BID-03 and BID-04 to RESTING
	tracker.SetResting("MM-BTC-USDT-BID-03", "ord-3", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	tracker.SetResting("MM-BTC-USDT-BID-04", "ord-4", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))

	// Cycle 2: Apply rebase again
	rec.ApplyRefMovementReprice(context.Background(), mc, decimal.RequireFromString("105.00"), 2, reconciler.ActionControlledRebase)

	// Now all orders have been rebased and zero orders have RefVersion < 2 -> RebaseActive MUST become false
	state = rec.GetRefState(marketID)
	if state.RebaseActive {
		t.Fatalf("expected RebaseActive=false after all orders converged to targetVersion 2")
	}
}

// Test 12: Full provider recovery after PAUSED resumes normal reconciliation
func TestProviderRecovery_AfterPaused(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, _ := newInvTestRec(t, marketID)

	cfg.RefPrice = config.RefPriceConfig{
		StaleThreshold: 5 * time.Minute,
		PauseThreshold: 10 * time.Minute,
	}

	mockProv := &mockRefProvider{
		entries: make(map[string]refprice.Entry),
	}
	rec.SetRefProvider(mockProv)

	// Mock ME server
	meSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		snap := meclient.MarketSnapshot{
			MarketID: marketID,
			Sequence: 100,
			State:    "LIVE",
		}
		_ = json.NewEncoder(w).Encode(snap)
	}))
	defer meSrv.Close()
	rec.SetMEClient(meclient.New(meSrv.URL, zap.NewNop()))

	// Step 1: Provider returns PAUSED
	mockProv.entries[marketID] = refprice.Entry{
		MarketID:  marketID,
		Price:     decimal.RequireFromString("100.00"),
		Version:   1,
		FetchedAt: time.Now().Add(-15 * time.Minute),
		State:     refprice.StatePaused,
	}

	published, err := rec.ReconcileMarket(context.Background(), marketID, 12, 12)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if published != 0 {
		t.Fatalf("expected 0 published commands under PAUSED, got %d", published)
	}
	if len(tracker.All(marketID)) != 0 {
		t.Fatalf("expected 0 tracked orders created under PAUSED, got %d", len(tracker.All(marketID)))
	}

	// Step 2: Provider recovers with newer version (FRESH)
	mockProv.entries[marketID] = refprice.Entry{
		MarketID:  marketID,
		Price:     decimal.RequireFromString("102.00"),
		Version:   2,
		FetchedAt: time.Now(),
		State:     refprice.StateFresh,
	}

	published, err = rec.ReconcileMarket(context.Background(), marketID, 2, 2)
	if err != nil {
		t.Fatalf("unexpected error after recovery: %v", err)
	}
	if published == 0 {
		t.Fatalf("expected commands published after recovery to FRESH, got 0")
	}
	if len(prod.createdOrders) == 0 {
		t.Fatalf("expected Kafka creates dispatched after recovery to FRESH")
	}
}

// Test 13: Fully filled CANCELLING order (RemainingQty == 0) never spawns a replacement
func TestCorrectionReplacement_FullyFilledBeforeCancel(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, osSvc := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]

	levelID := "MM-BTC-USDT-BID-01"
	coid := "MM-BTC-USDT-BID-01-G001"

	tracker.SetPending(levelID, "ord-1", coid, 1, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("100.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetCancelling(levelID)
	tracker.QueueCorrection(levelID, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("101.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 2,
	})

	// Order Service confirms CANCELLED, but RemainingQty is ZERO (order was completely filled before cancel took effect)
	osSvc.orders[coid] = &orderservice.OrderState{
		OrderID:       "ord-1",
		ClientOrderID: coid,
		Status:        "CANCELLED",
		OriginalQty:   decimal.RequireFromString("1.0"),
		RemainingQty:  decimal.Zero,
	}

	o := tracker.Get(levelID)
	rec.HandleCancellingTimeout(context.Background(), o, mc)

	// Invariant: replacement MUST NOT be created because order was 100% filled
	if len(osSvc.createdOrders) != 0 {
		t.Fatalf("expected 0 replacement creates for fully filled order, got %d: %v", len(osSvc.createdOrders), osSvc.createdOrders)
	}
	if len(prod.createdOrders) != 0 {
		t.Fatalf("expected 0 Kafka creates, got %d", len(prod.createdOrders))
	}
}

// Test 14: Queued correction replacement is strictly dropped when reference is PAUSED
func TestCorrectionReplacement_DroppedWhenPaused(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, osSvc := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]

	levelID := "MM-BTC-USDT-BID-01"
	coid := "MM-BTC-USDT-BID-01-G001"

	tracker.SetPending(levelID, "ord-1", coid, 1, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("100.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetCancelling(levelID)
	tracker.QueueCorrection(levelID, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("101.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 2,
	})

	// Reference price is PAUSED
	rec.SetRefState(marketID, &reconciler.RefState{
		LastRef:     decimal.RequireFromString("100.00"),
		LastVersion: 1,
		Freshness:   reconciler.FreshnessPaused,
	})

	// Order Service confirms CANCELLED with remaining quantity = 1.0
	osSvc.orders[coid] = &orderservice.OrderState{
		OrderID:       "ord-1",
		ClientOrderID: coid,
		Status:        "CANCELLED",
		OriginalQty:   decimal.RequireFromString("1.0"),
		RemainingQty:  decimal.RequireFromString("1.0"),
	}

	o := tracker.Get(levelID)
	rec.HandleCancellingTimeout(context.Background(), o, mc)

	// Invariant: replacement MUST NOT be created because reference is PAUSED
	if len(osSvc.createdOrders) != 0 {
		t.Fatalf("expected 0 replacements created while reference is PAUSED, got %d: %v", len(osSvc.createdOrders), osSvc.createdOrders)
	}
	if len(prod.createdOrders) != 0 {
		t.Fatalf("expected 0 Kafka creates while PAUSED, got %d", len(prod.createdOrders))
	}
}

// Test 15: Expiry replacement sizes strictly to remaining quantity (not original quantity)
func TestExpiryReplacement_UsesRemainingQuantity(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, _ := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.Repricing = config.RepricingConfig{
		OrderLifetime: 10 * time.Millisecond,
		MaxBatch:      4,
	}

	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{
			marketID: {
				MarketID:  marketID,
				Price:     decimal.RequireFromString("100.00"),
				Version:   10,
				FetchedAt: time.Now(),
				State:     refprice.StateFresh,
			},
		},
	}
	rec.SetRefProvider(mockProv)

	// Order is RESTING with OriginalQty = 1.0, but partially filled to RemainingQty = 0.3
	levelID := "MM-BTC-USDT-BID-01"
	coid := "MM-BTC-USDT-BID-01-G001"
	tracker.SetPending(levelID, "ord-1", coid, 1, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("99.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetResting(levelID, "ord-1", decimal.RequireFromString("1.0"), decimal.RequireFromString("0.3"))

	// Backdate CreatedAt to trigger expiry
	tracked := tracker.Get(levelID)
	tracked.CreatedAt = time.Now().Add(-1 * time.Hour)

	expired := rec.CheckExpiredOrders(context.Background(), marketID)
	if expired != 1 {
		t.Fatalf("expected 1 expired order, got %d", expired)
	}

	// Invariant: The queued replacement must have Quantity == 0.3, NOT 1.0
	if tracked.QueuedCorrection == nil {
		t.Fatalf("expected QueuedCorrection to be set")
	}
	expectedQty := decimal.RequireFromString("0.3")
	if !tracked.QueuedCorrection.Quantity.Equal(expectedQty) {
		t.Fatalf("expected replacement quantity %s, got %s", expectedQty, tracked.QueuedCorrection.Quantity)
	}

	if len(prod.cancelledOrders) != 1 {
		t.Fatalf("expected 1 cancel published for expired order")
	}
}

// Test 16: Expiry with RemainingQty == 0 cancels old order without queueing a replacement
func TestExpiryReplacement_FullyFilledDoesNotRecreate(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, _ := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.Repricing = config.RepricingConfig{
		OrderLifetime: 10 * time.Millisecond,
		MaxBatch:      4,
	}

	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{
			marketID: {
				MarketID:  marketID,
				Price:     decimal.RequireFromString("100.00"),
				Version:   10,
				FetchedAt: time.Now(),
				State:     refprice.StateFresh,
			},
		},
	}
	rec.SetRefProvider(mockProv)

	// Order is RESTING with OriginalQty = 1.0, but RemainingQty is ZERO
	levelID := "MM-BTC-USDT-BID-01"
	coid := "MM-BTC-USDT-BID-01-G001"
	tracker.SetPending(levelID, "ord-1", coid, 1, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("99.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetResting(levelID, "ord-1", decimal.RequireFromString("1.0"), decimal.Zero)

	tracked := tracker.Get(levelID)
	tracked.CreatedAt = time.Now().Add(-1 * time.Hour)

	_ = rec.CheckExpiredOrders(context.Background(), marketID)

	// Invariant: The order is cancelled, but NO correction is queued
	if tracked.QueuedCorrection != nil {
		t.Fatalf("expected QueuedCorrection to be nil for fully filled expired order, got %v", tracked.QueuedCorrection)
	}
	if len(prod.cancelledOrders) != 1 {
		t.Fatalf("expected cancel command published for fully filled expired order")
	}
}

// Test 17: Rebase replacement sizes strictly to remaining quantity (not original quantity)
func TestRebaseReplacement_UsesRemainingQuantity(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, _ := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.Repricing = config.RepricingConfig{
		SmallBps: 10,
		LargeBps: 100,
		MaxBatch: 4,
	}

	// Order is RESTING at v1 with OriginalQty = 1.0, but partially filled to RemainingQty = 0.3
	levelID := "MM-BTC-USDT-BID-01"
	tracker.SetPending(levelID, "ord-1", "COID-1", 1, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("98.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "HIGH",
		RefVersion: 1,
	})
	tracker.SetResting(levelID, "ord-1", decimal.RequireFromString("1.0"), decimal.RequireFromString("0.3"))

	refState := &reconciler.RefState{
		LastRef:         decimal.RequireFromString("105.00"),
		LastVersion:     2,
		RebaseActive:    true,
		RebaseTargetVer: 2,
		RebaseTargetRef: decimal.RequireFromString("105.00"),
	}
	rec.SetRefState(marketID, refState)

	rec.ApplyRefMovementReprice(context.Background(), mc, decimal.RequireFromString("105.00"), 2, reconciler.ActionControlledRebase)

	tracked := tracker.Get(levelID)
	if tracked.QueuedCorrection == nil {
		t.Fatalf("expected QueuedCorrection to be set on rebase")
	}
	expectedQty := decimal.RequireFromString("0.3")
	if !tracked.QueuedCorrection.Quantity.Equal(expectedQty) {
		t.Fatalf("expected rebase replacement quantity %s, got %s", expectedQty, tracked.QueuedCorrection.Quantity)
	}

	if len(prod.cancelledOrders) != 1 {
		t.Fatalf("expected 1 cancel published for rebase order")
	}
}

// Test 18: Rebase candidate with RemainingQty == 0 cancels old order without queueing a replacement
func TestRebaseReplacement_FullyFilledDoesNotRecreate(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, _ := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.Repricing = config.RepricingConfig{
		SmallBps: 10,
		LargeBps: 100,
		MaxBatch: 4,
	}

	// Order is RESTING at v1 with OriginalQty = 1.0, but RemainingQty is ZERO
	levelID := "MM-BTC-USDT-BID-01"
	tracker.SetPending(levelID, "ord-1", "COID-1", 1, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("98.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "HIGH",
		RefVersion: 1,
	})
	tracker.SetResting(levelID, "ord-1", decimal.RequireFromString("1.0"), decimal.Zero)

	refState := &reconciler.RefState{
		LastRef:         decimal.RequireFromString("105.00"),
		LastVersion:     2,
		RebaseActive:    true,
		RebaseTargetVer: 2,
		RebaseTargetRef: decimal.RequireFromString("105.00"),
	}
	rec.SetRefState(marketID, refState)

	rec.ApplyRefMovementReprice(context.Background(), mc, decimal.RequireFromString("105.00"), 2, reconciler.ActionControlledRebase)

	tracked := tracker.Get(levelID)
	// Invariant: The order is cancelled, but NO correction is queued
	if tracked.QueuedCorrection != nil {
		t.Fatalf("expected QueuedCorrection to be nil for fully filled rebase candidate, got %v", tracked.QueuedCorrection)
	}
	if len(prod.cancelledOrders) != 1 {
		t.Fatalf("expected 1 cancel published for fully filled rebase candidate")
	}
}

// Test 21: Pending retry preserves exact original create command (OriginalQty, same IDs)
func TestPendingRetry_PreservesOriginalCreateCommand(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, _ := newInvTestRec(t, marketID)
	cfg.PendingTimeout = 1 * time.Millisecond

	levelID := "MM-BTC-USDT-BID-01"
	origQty := decimal.RequireFromString("2.50")
	price := decimal.RequireFromString("95000.00")

	// PENDING order that failed its initial Kafka publish
	tracker.SetPending(levelID, "ord-original-999", "COID-original-999", 1, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      price,
		Quantity:   origQty,
		Zone:       "LOW",
		RefVersion: 1,
	})
	// Simulate remaining quantity tracked (e.g. 0.75), but PENDING create retry MUST use OriginalQty
	tracked := tracker.Get(levelID)
	tracked.RemainingQty = decimal.RequireFromString("0.75")
	tracked.KafkaPublished = false
	tracked.PendingSince = time.Now().Add(-50 * time.Millisecond)

	rec.CheckPendingTimeouts(context.Background(), marketID)

	if len(prod.publishedCreates) != 1 {
		t.Fatalf("expected 1 Kafka publish retry, got %d", len(prod.publishedCreates))
	}

	pub := prod.publishedCreates[0]
	// Invariant: PENDING retry must preserve EXACT original create command: orderID, clientOrderID, OriginalQty
	if pub.OrderID != "ord-original-999" {
		t.Fatalf("expected original order ID ord-original-999, got %s", pub.OrderID)
	}
	if pub.ClientOrderID != "COID-original-999" {
		t.Fatalf("expected original client order ID COID-original-999, got %s", pub.ClientOrderID)
	}
	if pub.Quantity != origQty.String() {
		t.Fatalf("expected retry to use OriginalQty %s, got %s", origQty.String(), pub.Quantity)
	}
	if !tracker.Get(levelID).KafkaPublished {
		t.Fatalf("expected KafkaPublished to be set to true after retry")
	}
}

// Test 22: Rebase price generation failure records metrics, logs context, and does NOT auto-complete rebase
func TestRebase_GenerationFailure_MetricsAndLogging(t *testing.T) {
	marketID := "BTC-USDT"
	metrics := &mockMetrics{}
	rec, tracker, cfg, _, _ := newInvTestRecWithMetrics(t, marketID, metrics)
	mc := &cfg.Markets[0]
	mc.BidZones = config.DefaultZoneConfig()
	mc.BidZones.High = config.ZoneBand{Count: 4, MinBps: 0, MaxBps: 0}
	mc.Repricing = config.RepricingConfig{
		SmallBps: 10,
		LargeBps: 100,
		MaxBatch: 4,
	}

	levelID := "MM-BTC-USDT-BID-01"
	tracker.SetPending(levelID, "ord-1", "COID-1", 1, pricing.PriceLevel{
		LevelID:    levelID,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("98.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "HIGH",
		RefVersion: 1,
	})
	tracker.SetResting(levelID, "ord-1", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))

	refState := &reconciler.RefState{
		LastRef:         decimal.RequireFromString("105.00"),
		LastVersion:     2,
		RebaseActive:    true,
		RebaseTargetVer: 2,
		RebaseTargetRef: decimal.RequireFromString("105.00"),
	}
	rec.SetRefState(marketID, refState)

	rec.ApplyRefMovementReprice(context.Background(), mc, decimal.RequireFromString("105.00"), 2, reconciler.ActionControlledRebase)

	// Invariant: generation failure MUST be instrumented via metrics
	if metrics.rebaseGenFailures == 0 {
		t.Fatalf("expected rebaseGenFailures metric to be incremented on generation failure")
	}

	// Invariant: RebaseActive MUST NOT be falsely marked completed when generation fails
	if !refState.RebaseActive {
		t.Fatalf("expected RebaseActive to remain true when level failed price generation")
	}

	// Order must still remain at old RefVersion 1
	tracked := tracker.Get(levelID)
	if tracked.RefVersion != 1 {
		t.Fatalf("expected order to remain at RefVersion 1, got %d", tracked.RefVersion)
	}
}

// Test 23: PAUSED reference prevents new creates and replacements across all independent paths
func TestPAUSED_AllIndependentPaths_ZeroMutations(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, osSvc := newInvTestRec(t, marketID)
	cfg.CancellingTimeout = 1 * time.Millisecond
	mc := &cfg.Markets[0]
	mc.ReferencePrice = decimal.RequireFromString("100.00")

	// Set PAUSED reference state
	refState := &reconciler.RefState{
		LastRef:     decimal.RequireFromString("100.00"),
		LastVersion: 5,
		Freshness:   reconciler.FreshnessPaused,
	}
	rec.SetRefState(marketID, refState)

	// 1. Expiry path under PAUSED: order is past OrderLifetime
	levelExpiry := "MM-BTC-USDT-BID-01"
	tracker.SetPending(levelExpiry, "ord-exp", "COID-exp", 1, pricing.PriceLevel{
		LevelID:    levelExpiry,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("98.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetResting(levelExpiry, "ord-exp", decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	tracker.Get(levelExpiry).CreatedAt = time.Now().Add(-2 * time.Hour)

	expired := rec.CheckExpiredOrders(context.Background(), marketID)
	if expired != 0 {
		t.Fatalf("expected 0 expired orders under PAUSED, got %d", expired)
	}
	if len(prod.cancelledOrders) != 0 {
		t.Fatalf("expected 0 cancels published under PAUSED expiry check")
	}

	// 2. Cancellation replacement path under PAUSED: queued replacement is dropped
	levelCancel := "MM-BTC-USDT-BID-02"
	tracker.SetPending(levelCancel, "ord-cxl", "COID-cxl", 1, pricing.PriceLevel{
		LevelID:    levelCancel,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("97.00"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 1,
	})
	tracker.SetCancelling(levelCancel)
	tracker.QueueCorrection(levelCancel, pricing.PriceLevel{
		LevelID:    levelCancel,
		MarketID:   marketID,
		Side:       "BUY",
		Price:      decimal.RequireFromString("96.50"),
		Quantity:   decimal.RequireFromString("1.0"),
		Zone:       "LOW",
		RefVersion: 6,
	})
	tracker.Get(levelCancel).CancellingSince = time.Now().Add(-50 * time.Millisecond)
	osSvc.orders["COID-cxl"] = &orderservice.OrderState{
		ClientOrderID: "COID-cxl",
		Status:        "CANCELLED",
		RemainingQty:  decimal.RequireFromString("1.0"),
	}

	rec.CheckCancellingTimeouts(context.Background(), marketID)

	// Order was removed from tracker because cancel was confirmed, but NO new replacement create was registered or published!
	if tracker.Get(levelCancel) != nil {
		t.Fatalf("expected cancelled order to be removed from tracker")
	}
	if len(prod.publishedCreates) != 0 {
		t.Fatalf("expected 0 creates published under PAUSED cancellation timeout, got %d", len(prod.publishedCreates))
	}
}


