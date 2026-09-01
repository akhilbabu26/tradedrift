package order

import (
	"testing"

	"github.com/shopspring/decimal"

	"tradedrift/services/liquidity-engine/internal/pricing"
)

func TestTracker_GenerationRecovery(t *testing.T) {
	tr := NewTracker()

	// Simulate restart recovery where Order Service returns an order at generation 7
	osOrders := []OSOrder{
		{
			LevelID:       "MM-BTC-USDT-ASK-01",
			Generation:    7,
			ClientOrderID: "MM-BTC-USDT-ASK-01-G007",
			OrderID:       "11111111-1111-1111-1111-111111111111",
			Side:          "SELL",
			Price:         decimal.RequireFromString("96500.00"),
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("1.0"),
		},
	}

	added, _ := tr.SyncFromOrders("BTC-USDT", osOrders)
	if added != 1 {
		t.Fatalf("expected 1 order added, got %d", added)
	}

	// Verify the tracker recorded the highest generation (7)
	if tr.generations["MM-BTC-USDT-ASK-01"] != 7 {
		t.Errorf("expected generation 7, got %d", tr.generations["MM-BTC-USDT-ASK-01"])
	}

	// Verify the order was recovered into StatusOSRegistered (not directly RESTING)
	recovered := tr.Get("MM-BTC-USDT-ASK-01")
	if recovered == nil {
		t.Fatal("expected recovered order to be present in tracker")
	}
	if recovered.Status != StatusOSRegistered {
		t.Errorf("expected StatusOSRegistered, got %s", recovered.Status)
	}
	if !recovered.KafkaPublished {
		t.Errorf("expected KafkaPublished to be true for recovered OS order")
	}

	// Next generation must be 8 -> clientOrderID MM-BTC-USDT-ASK-01-G008
	nextGen := tr.NextGeneration("MM-BTC-USDT-ASK-01")
	if nextGen != 8 {
		t.Errorf("expected NextGeneration to be 8, got %d", nextGen)
	}
	newCOID := ClientOrderID("MM-BTC-USDT-ASK-01", nextGen)
	if newCOID != "MM-BTC-USDT-ASK-01-G008" {
		t.Errorf("expected MM-BTC-USDT-ASK-01-G008, got %s", newCOID)
	}
}

func TestTracker_IncrementalSyncPreservesGenerations(t *testing.T) {
	tr := NewTracker()

	// Initial sync at G003
	tr.SyncFromOrders("BTC-USDT", []OSOrder{
		{
			LevelID:       "MM-BTC-USDT-BID-01",
			Generation:    3,
			ClientOrderID: "MM-BTC-USDT-BID-01-G003",
			OrderID:       "22222222-2222-2222-2222-222222222222",
			Side:          "BUY",
			Price:         decimal.RequireFromString("96400.00"),
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("0.5"),
		},
	})

	// Tracker advances generation to G004 locally
	nextGen := tr.NextGeneration("MM-BTC-USDT-BID-01")
	if nextGen != 4 {
		t.Fatalf("expected next generation 4, got %d", nextGen)
	}

	// Order Service periodic sync returns G003 again
	tr.SyncFromOrders("BTC-USDT", []OSOrder{
		{
			LevelID:       "MM-BTC-USDT-BID-01",
			Generation:    3,
			ClientOrderID: "MM-BTC-USDT-BID-01-G003",
			OrderID:       "22222222-2222-2222-2222-222222222222",
			Side:          "BUY",
			Price:         decimal.RequireFromString("96400.00"),
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("0.5"),
		},
	})

	// Generation must NOT regress to 3
	if tr.generations["MM-BTC-USDT-BID-01"] < 4 {
		t.Errorf("generation regressed to %d, expected >= 4", tr.generations["MM-BTC-USDT-BID-01"])
	}
}

func TestTracker_DuplicateLevelIDResolution(t *testing.T) {
	tr := NewTracker()

	// Simulate anomalous OS response returning two orders for the same LevelID (G006 and G007)
	osOrders := []OSOrder{
		{
			LevelID:       "MM-BTC-USDT-ASK-01",
			Generation:    6,
			ClientOrderID: "MM-BTC-USDT-ASK-01-G006",
			OrderID:       "66666666-6666-6666-6666-666666666666",
			Side:          "SELL",
			Price:         decimal.RequireFromString("96550.00"),
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("1.0"),
		},
		{
			LevelID:       "MM-BTC-USDT-ASK-01",
			Generation:    7,
			ClientOrderID: "MM-BTC-USDT-ASK-01-G007",
			OrderID:       "77777777-7777-7777-7777-777777777777",
			Side:          "SELL",
			Price:         decimal.RequireFromString("96500.00"),
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("1.0"),
		},
	}

	added, duplicates := tr.SyncFromOrders("BTC-USDT", osOrders)
	if added != 1 {
		t.Fatalf("expected 1 unique order added, got %d", added)
	}
	if duplicates != 1 {
		t.Fatalf("expected 1 duplicate detected, got %d", duplicates)
	}

	// Must select the order with the highest generation (7)
	recovered := tr.Get("MM-BTC-USDT-ASK-01")
	if recovered == nil {
		t.Fatal("expected order to be present")
	}
	if recovered.Generation != 7 {
		t.Errorf("expected Generation 7 to be chosen, got %d", recovered.Generation)
	}
	if recovered.OrderID != "77777777-7777-7777-7777-777777777777" {
		t.Errorf("expected OrderID 77777777..., got %s", recovered.OrderID)
	}
}

func TestTracker_RestingCountVsActiveCount(t *testing.T) {
	tr := NewTracker()

	// Add a RESTING bid and an OS_REGISTERED bid
	tr.SetPending("MM-BTC-USDT-BID-01", "ord-1", "MM-BTC-USDT-BID-01-G001", 1, pricing.PriceLevel{
		LevelID:  "MM-BTC-USDT-BID-01",
		MarketID: "BTC-USDT",
		Side:     "BUY",
		Price:    decimal.RequireFromString("95000"),
		Quantity: decimal.RequireFromString("1.0"),
	})
	tr.SetResting("MM-BTC-USDT-BID-01", "ord-1", decimal.NewFromInt(1), decimal.NewFromInt(1))

	tr.SetPending("MM-BTC-USDT-BID-02", "ord-2", "MM-BTC-USDT-BID-02-G001", 1, pricing.PriceLevel{
		LevelID:  "MM-BTC-USDT-BID-02",
		MarketID: "BTC-USDT",
		Side:     "BUY",
		Price:    decimal.RequireFromString("94900"),
		Quantity: decimal.RequireFromString("1.0"),
	})
	tr.SetOSRegistered("MM-BTC-USDT-BID-02", "ord-2", decimal.NewFromInt(1), decimal.NewFromInt(1))

	// ActiveCount includes both RESTING and OS_REGISTERED
	if tr.ActiveCount("BTC-USDT", "BUY") != 2 {
		t.Errorf("expected ActiveCount to be 2, got %d", tr.ActiveCount("BTC-USDT", "BUY"))
	}

	// RestingCount includes strictly RESTING
	if tr.RestingCount("BTC-USDT", "BUY") != 1 {
		t.Errorf("expected RestingCount to be 1, got %d", tr.RestingCount("BTC-USDT", "BUY"))
	}
}
