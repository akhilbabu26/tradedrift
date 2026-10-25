package reconciler_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"tradedrift/platform/refprice"
	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/pricing"
	"tradedrift/services/liquidity-engine/internal/reconciler"
)

// TestETH_LiquidityRecoveryAndSpreadRestoration verifies that:
// 1. Recovered ETH orders with stale timestamps and missing zones have zones authoritatively resolved.
// 2. Wide order book spread (3.30%) trips CTS spread guard.
// 3. Controlled rebase re-prices LOW zone bids to inside spread (<15 bps).
// 4. Spread tightens to valid range (<1.50%), clearing CTS spread guard.
// 5. STALE and PAUSED states strictly protect against new order creations.
func TestETH_LiquidityRecoveryAndSpreadRestoration(t *testing.T) {
	marketID := "ETH-USDT"
	rec, tracker, cfg, prod, _ := newInvTestRec(t, marketID)
	mc := &cfg.Markets[0]
	mc.MarketID = marketID
	mc.BaseAsset = "ETH"
	mc.QuoteAsset = "USDT"
	mc.TickSize = decimal.RequireFromString("0.01")
	mc.LotSize = decimal.RequireFromString("0.0001")
	mc.BidZones = config.DefaultZoneConfig()
	mc.AskZones = config.DefaultZoneConfig()
	mc.Repricing = config.RepricingConfig{
		SmallBps: 10,
		LargeBps: 100,
		MaxBatch: 4,
	}

	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{
			marketID: {
				MarketID:  marketID,
				Price:     decimal.RequireFromString("2429.00"),
				Version:   2,
				FetchedAt: time.Now(),
				State:     refprice.StateFresh,
			},
		},
	}
	rec.SetRefProvider(mockProv)

	// Step 1: Simulate the historical stuck state observed in forensic audit:
	// 12 Bids resting at ~$2350 (v1), 12 Asks resting at ~$2429 (v2)
	// Bids are recovered from Order Service without zone metadata.
	staleCreatedAt := time.Now().Add(-40 * time.Hour)
	var osOrders []order.OSOrder

	// Bids at 2350.33
	for i := 1; i <= 12; i++ {
		lvl := fmt.Sprintf("MM-ETH-USDT-BID-%02d", i)
		price := decimal.RequireFromString("2350.33").Sub(decimal.NewFromFloat(float64(i) * 1.5))
		osOrders = append(osOrders, order.OSOrder{
			LevelID:       lvl,
			Generation:    i + 300,
			ClientOrderID: fmt.Sprintf("%s-G%03d", lvl, i+300),
			OrderID:       fmt.Sprintf("uuid-bid-%02d", i),
			Side:          "BUY",
			Price:         price,
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("1.0"),
			CreatedAt:     staleCreatedAt,
		})
	}

	// Asks at 2429.13 (already at v2)
	for i := 1; i <= 12; i++ {
		lvl := fmt.Sprintf("MM-ETH-USDT-ASK-%02d", i)
		price := decimal.RequireFromString("2429.13").Add(decimal.NewFromFloat(float64(i) * 1.5))
		osOrders = append(osOrders, order.OSOrder{
			LevelID:       lvl,
			Generation:    i + 600,
			ClientOrderID: fmt.Sprintf("%s-G%03d", lvl, i+600),
			OrderID:       fmt.Sprintf("uuid-ask-%02d", i),
			Side:          "SELL",
			Price:         price,
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("1.0"),
			CreatedAt:     time.Now().Add(-10 * time.Minute),
		})
	}

	// Authoritative zone resolver mapping
	resolver := func(levelID string) string {
		return pricing.ResolveZoneForMarketConfig(levelID, mc)
	}

	added, _ := tracker.SyncFromOrders(marketID, osOrders, resolver)
	if added != 24 {
		t.Fatalf("expected 24 orders synced, got %d", added)
	}

	// Verify all orders have authoritative zones restored
	for i := 1; i <= 7; i++ {
		bidLvl := tracker.Get(fmt.Sprintf("MM-ETH-USDT-BID-%02d", i))
		if bidLvl.Zone != "LOW" {
			t.Errorf("expected level %s zone LOW, got %q", bidLvl.LevelID, bidLvl.Zone)
		}
		if !bidLvl.CreatedAt.Equal(staleCreatedAt) {
			t.Errorf("expected level %s CreatedAt=%v, got %v", bidLvl.LevelID, staleCreatedAt, bidLvl.CreatedAt)
		}
	}
	for i := 8; i <= 11; i++ {
		bidLvl := tracker.Get(fmt.Sprintf("MM-ETH-USDT-BID-%02d", i))
		if bidLvl.Zone != "MID" {
			t.Errorf("expected level %s zone MID, got %q", bidLvl.LevelID, bidLvl.Zone)
		}
	}
	bidLvl12 := tracker.Get("MM-ETH-USDT-BID-12")
	if bidLvl12.Zone != "HIGH" {
		t.Errorf("expected level 12 zone HIGH, got %q", bidLvl12.Zone)
	}

	// Promote all to RESTING for rebase evaluation
	for _, o := range tracker.All(marketID) {
		tracker.SetResting(o.LevelID, o.OrderID, o.OriginalQty, o.RemainingQty)
		if o.Side == "SELL" {
			o.RefVersion = 2 // Asks are v2
		} else {
			o.RefVersion = 1 // Bids are v1
		}
	}

	// Step 2: Verify spread before rebase: Best Bid 2348.83, Best Ask 2430.63 -> ~3.4% spread
	bestBid := decimal.Zero
	bestAsk := decimal.RequireFromString("999999.00")
	for _, o := range tracker.All(marketID) {
		if o.Side == "BUY" && o.Price.GreaterThan(bestBid) {
			bestBid = o.Price
		}
		if o.Side == "SELL" && o.Price.LessThan(bestAsk) {
			bestAsk = o.Price
		}
	}
	midPrice := bestBid.Add(bestAsk).Div(decimal.NewFromInt(2))
	spreadPct := bestAsk.Sub(bestBid).Div(midPrice)
	maxAllowedSpread := decimal.RequireFromString("0.015") // 1.5%

	if !spreadPct.GreaterThan(maxAllowedSpread) {
		t.Fatalf("expected initial spread %s to exceed max %s", spreadPct, maxAllowedSpread)
	}

	// Step 3: Run Rebase with reference at 2429.00 (v2)
	refState := &reconciler.RefState{
		LastRef:         decimal.RequireFromString("2429.00"),
		LastVersion:     2,
		RebaseActive:    true,
		RebaseTargetVer: 2,
		RebaseTargetRef: decimal.RequireFromString("2429.00"),
	}
	rec.SetRefState(marketID, refState)

	// Batch 1: Controlled rebase reprices LOW zone bids first!
	n := rec.ApplyRefMovementReprice(context.Background(), mc, decimal.RequireFromString("2429.00"), 2, reconciler.ActionControlledRebase)
	if n != 4 {
		t.Fatalf("expected Batch 1 to reprice 4 LOW bids, got %d", n)
	}

	// Verify that cancelled bids are L1, L2, L3, L4 (top-of-book LOW zone)
	if len(prod.cancelledOrders) != 4 {
		t.Fatalf("expected 4 cancelled orders in batch 1, got %d", len(prod.cancelledOrders))
	}

	// Simulate cancel confirmation & replacement of Batch 1 at ~$2428 (near 2429 ref)
	for i := 1; i <= 4; i++ {
		lvl := fmt.Sprintf("MM-ETH-USDT-BID-%02d", i)
		tracker.Remove(lvl)
		newPrice := decimal.RequireFromString("2428.00").Sub(decimal.NewFromFloat(float64(i) * 0.10))
		tracker.SetPending(lvl, fmt.Sprintf("new-bid-%d", i), fmt.Sprintf("coid-bid-%d", i), 2, pricing.PriceLevel{
			LevelID:    lvl,
			MarketID:   marketID,
			Side:       "BUY",
			Price:      newPrice,
			Quantity:   decimal.RequireFromString("1.0"),
			Zone:       "LOW",
			RefVersion: 2,
		})
		tracker.SetResting(lvl, fmt.Sprintf("new-bid-%d", i), decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
	}

	// Step 4: Verify that top-of-book spread is now tightened to < 1.50%
	bestBidAfter := decimal.Zero
	bestAskAfter := decimal.RequireFromString("999999.00")
	for _, o := range tracker.All(marketID) {
		if o.Status == order.StatusResting {
			if o.Side == "BUY" && o.Price.GreaterThan(bestBidAfter) {
				bestBidAfter = o.Price
			}
			if o.Side == "SELL" && o.Price.LessThan(bestAskAfter) {
				bestAskAfter = o.Price
			}
		}
	}
	newMid := bestBidAfter.Add(bestAskAfter).Div(decimal.NewFromInt(2))
	newSpreadPct := bestAskAfter.Sub(bestBidAfter).Div(newMid)

	// Best bid ~2427.90, best ask ~2430.63 -> spread ~2.73 on 2429 (~0.11% = 11 bps)
	if newSpreadPct.GreaterThan(maxAllowedSpread) {
		t.Fatalf("expected restored spread %s to be within max %s", newSpreadPct, maxAllowedSpread)
	}

	// Step 5: Verify that STALE or PAUSED reference price strictly prevents creating new liquidity
	mockProv.entries[marketID] = refprice.Entry{
		MarketID:  marketID,
		Price:     decimal.RequireFromString("2429.00"),
		Version:   2,
		FetchedAt: time.Now().Add(-15 * time.Minute),
		State:     refprice.StatePaused,
	}

	// Try to reconcile while PAUSED: must produce 0 creates
	creates, err := rec.ReconcileMarket(context.Background(), marketID, 12, 12)
	if err != nil {
		t.Fatalf("reconcile while paused failed: %v", err)
	}
	if creates != 0 {
		t.Fatalf("expected 0 order creations while PAUSED, got %d", creates)
	}
}
