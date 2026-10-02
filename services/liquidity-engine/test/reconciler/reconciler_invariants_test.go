package reconciler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/meclient"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/pricing"
)

// ─── Phase C: Integration Invariant Tests ────────────────────────────────────

func TestPhaseC_StableConvergence(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, osClient := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)

	desired, err := pricing.GenerateZonedDesired(mc, mc.ReferencePrice, tracker, mc.BidZones, mc.AskZones, 1, 12, 12, nil)
	if err != nil {
		t.Fatalf("failed to generate desired: %v", err)
	}

	var meOrders []meclient.MMOrderSummary
	for i, l := range desired {
		oid := fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
		coid := fmt.Sprintf("%s-G001", l.LevelID)
		tracker.SetPending(l.LevelID, oid, coid, 1, l)
		tracker.SetResting(l.LevelID, oid, l.Quantity, l.Quantity)
		meOrders = append(meOrders, meclient.MMOrderSummary{
			OrderID:           oid,
			ClientOrderID:     coid,
			LevelID:           l.LevelID,
			Side:              l.Side,
			Price:             l.Price.String(),
			RemainingQuantity: l.Quantity.String(),
		})
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(meclient.MarketSnapshot{
			MarketID: marketID,
			State:    "LIVE",
			Orders:   meOrders,
		})
	}))
	defer srv.Close()

	rec.SetMEClient(meclient.New(srv.URL, zap.NewNop()))

	// Run 1: ReconcileMarket -> desired == actual -> 0 commands published
	cmds1, err := rec.ReconcileMarket(context.Background(), marketID, 12, 12)
	if err != nil {
		t.Fatalf("run 1 failed: %v", err)
	}
	if cmds1 != 0 {
		t.Errorf("expected 0 commands on run 1, got %d", cmds1)
	}
	if len(prod.createdOrders) != 0 || len(prod.cancelledOrders) != 0 {
		t.Errorf("expected 0 Kafka commands, got %d created, %d cancelled",
			len(prod.createdOrders), len(prod.cancelledOrders))
	}

	// Run 2: ReconcileMarket -> stable convergence -> still 0 commands published
	cmds2, err := rec.ReconcileMarket(context.Background(), marketID, 12, 12)
	if err != nil {
		t.Fatalf("run 2 failed: %v", err)
	}
	if cmds2 != 0 {
		t.Errorf("expected 0 commands on run 2, got %d", cmds2)
	}
	if len(prod.createdOrders) != 0 || len(prod.cancelledOrders) != 0 {
		t.Errorf("expected still 0 Kafka commands on run 2, got %d created, %d cancelled",
			len(prod.createdOrders), len(prod.cancelledOrders))
	}
	_ = osClient
}

func TestPhaseC_SingleFill_SingleReplacement(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, _ := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)

	desired, err := pricing.GenerateZonedDesired(mc, mc.ReferencePrice, tracker, mc.BidZones, mc.AskZones, 1, 12, 12, nil)
	if err != nil {
		t.Fatalf("failed to generate desired: %v", err)
	}

	var meOrders []meclient.MMOrderSummary
	for i, l := range desired {
		oid := fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
		coid := fmt.Sprintf("%s-G001", l.LevelID)
		tracker.SetPending(l.LevelID, oid, coid, 1, l)
		tracker.SetResting(l.LevelID, oid, l.Quantity, l.Quantity)
		meOrders = append(meOrders, meclient.MMOrderSummary{
			OrderID:           oid,
			ClientOrderID:     coid,
			LevelID:           l.LevelID,
			Side:              l.Side,
			Price:             l.Price.String(),
			RemainingQuantity: l.Quantity.String(),
		})
	}

	// Simulate complete fill on BID-01: removed from tracker and absent from ME snapshot
	filledLevelID := "MM-BTC-USDT-BID-01"
	tracker.Remove(filledLevelID)

	var meOrdersAfterFill []meclient.MMOrderSummary
	for _, o := range meOrders {
		if o.LevelID != filledLevelID {
			meOrdersAfterFill = append(meOrdersAfterFill, o)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(meclient.MarketSnapshot{
			MarketID: marketID,
			State:    "LIVE",
			Orders:   meOrdersAfterFill,
		})
	}))
	defer srv.Close()

	rec.SetMEClient(meclient.New(srv.URL, zap.NewNop()))

	cmds, err := rec.ReconcileMarket(context.Background(), marketID, 12, 12)
	if err != nil {
		t.Fatalf("ReconcileMarket failed: %v", err)
	}

	if cmds != 1 {
		t.Errorf("expected exactly 1 command published, got %d", cmds)
	}
	if len(prod.createdOrders) != 1 {
		t.Errorf("expected 1 created order, got %d", len(prod.createdOrders))
	}
	if len(prod.cancelledOrders) != 0 {
		t.Errorf("expected 0 cancelled orders, got %d", len(prod.cancelledOrders))
	}
}

func TestPhaseC_PartialFill_NoReplacement(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, cfg, prod, _ := newInvTestRec(t, marketID)
	mc := cfg.ForMarket(marketID)

	desired, err := pricing.GenerateZonedDesired(mc, mc.ReferencePrice, tracker, mc.BidZones, mc.AskZones, 1, 12, 12, nil)
	if err != nil {
		t.Fatalf("failed to generate desired: %v", err)
	}

	var meOrders []meclient.MMOrderSummary
	for i, l := range desired {
		oid := fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
		coid := fmt.Sprintf("%s-G001", l.LevelID)
		tracker.SetPending(l.LevelID, oid, coid, 1, l)
		remaining := l.Quantity
		if l.LevelID == "MM-BTC-USDT-BID-01" {
			remaining = decimal.RequireFromString("0.5")
		}
		tracker.SetResting(l.LevelID, oid, l.Quantity, remaining)
		meOrders = append(meOrders, meclient.MMOrderSummary{
			OrderID:           oid,
			ClientOrderID:     coid,
			LevelID:           l.LevelID,
			Side:              l.Side,
			Price:             l.Price.String(),
			RemainingQuantity: remaining.String(),
		})
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(meclient.MarketSnapshot{
			MarketID: marketID,
			State:    "LIVE",
			Orders:   meOrders,
		})
	}))
	defer srv.Close()

	rec.SetMEClient(meclient.New(srv.URL, zap.NewNop()))

	cmds, err := rec.ReconcileMarket(context.Background(), marketID, 12, 12)
	if err != nil {
		t.Fatalf("ReconcileMarket failed: %v", err)
	}

	if cmds != 0 {
		t.Errorf("expected 0 commands for healthy partial fill, got %d", cmds)
	}
	if len(prod.createdOrders) != 0 || len(prod.cancelledOrders) != 0 {
		t.Errorf("expected 0 Kafka commands, got %d created, %d cancelled",
			len(prod.createdOrders), len(prod.cancelledOrders))
	}
}

func TestPhaseC_InventorySkew_Counts(t *testing.T) {
	marketID := "BTC-USDT"
	rec, tracker, _, prod, _ := newInvTestRec(t, marketID)

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

	cmds, err := rec.ReconcileMarket(context.Background(), marketID, 6, 12)
	if err != nil {
		t.Fatalf("ReconcileMarket failed: %v", err)
	}

	if cmds != 18 {
		t.Errorf("expected 18 commands (6 bids + 12 asks), got %d", cmds)
	}
	if len(prod.createdOrders) != 18 {
		t.Errorf("expected 18 created orders, got %d", len(prod.createdOrders))
	}

	bids := 0
	asks := 0
	for _, o := range tracker.All(marketID) {
		if o.Side == "BUY" {
			bids++
		} else if o.Side == "SELL" {
			asks++
		}
	}
	if bids != 6 {
		t.Errorf("expected 6 tracked bids, got %d", bids)
	}
	if asks != 12 {
		t.Errorf("expected 12 tracked asks, got %d", asks)
	}
}

func TestPhaseC_Restart_NoDuplicateLogicalOrders(t *testing.T) {
	marketID := "BTC-USDT"
	_, tracker, _, _, _ := newInvTestRec(t, marketID)

	osOrders := []order.OSOrder{
		{
			LevelID:       "MM-BTC-USDT-BID-01",
			Generation:    1,
			ClientOrderID: "MM-BTC-USDT-BID-01-G001",
			OrderID:       "oid-1",
			Side:          "BUY",
			Price:         decimal.RequireFromString("90000.00"),
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("1.0"),
		},
		{
			LevelID:       "MM-BTC-USDT-BID-01",
			Generation:    2,
			ClientOrderID: "MM-BTC-USDT-BID-01-G002",
			OrderID:       "oid-2",
			Side:          "BUY",
			Price:         decimal.RequireFromString("90500.00"),
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("1.0"),
		},
		{
			LevelID:       "MM-BTC-USDT-ASK-01",
			Generation:    1,
			ClientOrderID: "MM-BTC-USDT-ASK-01-G001",
			OrderID:       "oid-3",
			Side:          "SELL",
			Price:         decimal.RequireFromString("92000.00"),
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("1.0"),
		},
	}

	added, duplicates := tracker.SyncFromOrders(marketID, osOrders)
	if added != 2 {
		t.Errorf("expected 2 unique levels added, got %d", added)
	}
	if duplicates != 1 {
		t.Errorf("expected 1 duplicate detected, got %d", duplicates)
	}

	bid01 := tracker.Get("MM-BTC-USDT-BID-01")
	if bid01 == nil {
		t.Fatal("expected MM-BTC-USDT-BID-01 to be in tracker")
	}
	if bid01.Generation != 2 {
		t.Errorf("expected generation 2 to win, got %d", bid01.Generation)
	}
	if bid01.OrderID != "oid-2" {
		t.Errorf("expected orderID oid-2, got %s", bid01.OrderID)
	}

	all := tracker.All(marketID)
	if len(all) != 2 {
		t.Errorf("expected exactly 2 orders in tracker, got %d", len(all))
	}
}
