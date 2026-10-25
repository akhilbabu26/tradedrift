package order

import (
	"testing"
	"time"

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

// ─── T24: OS Resync Generation Upgrade ───────────────────────────────────────

func TestT24_OSResync_GenerationUpgrade(t *testing.T) {
	tr := NewTracker()
	levelID := "MM-SOL-USDT-BID-01"

	// Initial state in tracker: Generation 3
	tr.SetPending(levelID, "old-order-id", "MM-SOL-USDT-BID-01-G003", 3, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: "SOL-USDT",
		Side:     "BUY",
		Price:    decimal.RequireFromString("99.00"),
		Quantity: decimal.RequireFromString("1.0"),
	})

	// Order Service returns Generation 4
	osOrders := []OSOrder{
		{
			LevelID:       levelID,
			Generation:    4,
			ClientOrderID: "MM-SOL-USDT-BID-01-G004",
			OrderID:       "new-order-id",
			Side:          "BUY",
			Price:         decimal.RequireFromString("99.50"),
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("1.0"),
		},
	}

	tr.SyncFromOrders("SOL-USDT", osOrders)

	live := tr.Get(levelID)
	if live == nil {
		t.Fatal("T24: expected order to exist in tracker")
	}
	if live.Generation != 4 {
		t.Errorf("T24: expected Generation=4, got %d", live.Generation)
	}
	if live.ClientOrderID != "MM-SOL-USDT-BID-01-G004" {
		t.Errorf("T24: expected ClientOrderID=MM-SOL-USDT-BID-01-G004, got %s", live.ClientOrderID)
	}
	if live.OrderID != "new-order-id" {
		t.Errorf("T24: expected OrderID=new-order-id, got %s", live.OrderID)
	}
	if !live.Price.Equal(decimal.RequireFromString("99.50")) {
		t.Errorf("T24: expected Price=99.50, got %s", live.Price)
	}
	if tr.generations[levelID] != 4 {
		t.Errorf("T24: expected tr.generations=4, got %d", tr.generations[levelID])
	}

	nextGen := tr.NextGeneration(levelID)
	if nextGen != 5 {
		t.Errorf("T24: expected NextGeneration=5, got %d", nextGen)
	}
}

// ─── T25: Older OS Generation Cannot Downgrade Tracker ───────────────────────

func TestT25_OlderOSGeneration_CannotDowngradeTracker(t *testing.T) {
	tr := NewTracker()
	levelID := "MM-SOL-USDT-BID-01"

	// Tracker is on Generation 4
	tr.SetPending(levelID, "current-order-id", "MM-SOL-USDT-BID-01-G004", 4, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: "SOL-USDT",
		Side:     "BUY",
		Price:    decimal.RequireFromString("100.00"),
		Quantity: decimal.RequireFromString("1.0"),
	})

	// Order Service returns stale record with Generation 3
	osOrders := []OSOrder{
		{
			LevelID:       levelID,
			Generation:    3,
			ClientOrderID: "MM-SOL-USDT-BID-01-G003",
			OrderID:       "old-stale-order-id",
			Side:          "BUY",
			Price:         decimal.RequireFromString("98.00"),
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("0.5"),
		},
	}

	tr.SyncFromOrders("SOL-USDT", osOrders)

	live := tr.Get(levelID)
	if live == nil {
		t.Fatal("T25: expected order to exist in tracker")
	}
	if live.Generation != 4 {
		t.Errorf("T25: generation regressed! expected 4, got %d", live.Generation)
	}
	if live.ClientOrderID != "MM-SOL-USDT-BID-01-G004" {
		t.Errorf("T25: ClientOrderID regressed! expected G004, got %s", live.ClientOrderID)
	}
	if live.OrderID != "current-order-id" {
		t.Errorf("T25: OrderID overwritten by stale OS record! expected current-order-id, got %s", live.OrderID)
	}
	if !live.Price.Equal(decimal.RequireFromString("100.00")) {
		t.Errorf("T25: Price overwritten! expected 100.00, got %s", live.Price)
	}
}

// ─── T26: Same-Generation Execution State Refresh ────────────────────────────

func TestT26_SameGeneration_ExecutionRefresh(t *testing.T) {
	tr := NewTracker()
	levelID := "MM-SOL-USDT-BID-01"

	// Tracker has Generation 4 with old OrderID and full qty (10)
	tr.SetPending(levelID, "old-order-id", "MM-SOL-USDT-BID-01-G004", 4, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: "SOL-USDT",
		Side:     "BUY",
		Price:    decimal.RequireFromString("100.00"),
		Quantity: decimal.RequireFromString("10.0"),
	})

	// Order Service returns Generation 4 with confirmed new OrderID and partial fill (Remaining 7)
	osOrders := []OSOrder{
		{
			LevelID:       levelID,
			Generation:    4,
			ClientOrderID: "MM-SOL-USDT-BID-01-G004",
			OrderID:       "new-order-id",
			Side:          "BUY",
			Price:         decimal.RequireFromString("100.00"),
			OriginalQty:   decimal.RequireFromString("10.0"),
			RemainingQty:  decimal.RequireFromString("7.0"),
		},
	}

	tr.SyncFromOrders("SOL-USDT", osOrders)

	live := tr.Get(levelID)
	if live == nil {
		t.Fatal("T26: expected order to exist in tracker")
	}
	if live.Generation != 4 {
		t.Errorf("T26: expected Generation=4, got %d", live.Generation)
	}
	if live.ClientOrderID != "MM-SOL-USDT-BID-01-G004" {
		t.Errorf("T26: expected ClientOrderID=MM-SOL-USDT-BID-01-G004, got %s", live.ClientOrderID)
	}
	if live.OrderID != "new-order-id" {
		t.Errorf("T26: expected OrderID=new-order-id, got %s", live.OrderID)
	}
	expectedRem := decimal.RequireFromString("7.0")
	if !live.RemainingQty.Equal(expectedRem) {
		t.Errorf("T26: expected RemainingQty=7.0, got %s", live.RemainingQty)
	}
	expectedFilled := decimal.RequireFromString("3.0")
	if !live.FilledQty.Equal(expectedFilled) {
		t.Errorf("T26: expected FilledQty=3.0, got %s", live.FilledQty)
	}
	if live.Status != StatusOSRegistered {
		t.Errorf("T26: expected StatusOSRegistered, got %s", live.Status)
	}
}

func TestTracker_SyncFromOrders_NormalRecovery(t *testing.T) {
	tr := NewTracker()
	fixedCreated := time.Now().Add(-10 * time.Minute)

	osOrders := []OSOrder{
		{
			LevelID:       "MM-BTC-USDT-BID-01",
			Generation:    2,
			ClientOrderID: "MM-BTC-USDT-BID-01-G002",
			OrderID:       "uuid-btc-bid-01",
			Side:          "BUY",
			Price:         decimal.RequireFromString("95000.00"),
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("1.0"),
			CreatedAt:     fixedCreated,
		},
	}

	mockResolver := func(levelID string) string {
		if levelID == "MM-BTC-USDT-BID-01" {
			return "LOW"
		}
		return ""
	}

	added, duplicates := tr.SyncFromOrders("BTC-USDT", osOrders, mockResolver)
	if added != 1 || duplicates != 0 {
		t.Fatalf("expected added=1, duplicates=0, got added=%d, duplicates=%d", added, duplicates)
	}

	order := tr.Get("MM-BTC-USDT-BID-01")
	if order == nil {
		t.Fatal("expected order in tracker")
	}
	if order.Zone != "LOW" {
		t.Errorf("expected Zone=LOW, got %q", order.Zone)
	}
	if !order.CreatedAt.Equal(fixedCreated) {
		t.Errorf("expected CreatedAt=%v, got %v", fixedCreated, order.CreatedAt)
	}
	if !order.PendingSince.Equal(fixedCreated) {
		t.Errorf("expected PendingSince=%v, got %v", fixedCreated, order.PendingSince)
	}
	if order.Status != StatusOSRegistered {
		t.Errorf("expected Status=OS_REGISTERED, got %s", order.Status)
	}
	if order.FilledQty.Sign() != 0 {
		t.Errorf("expected FilledQty=0, got %s", order.FilledQty)
	}
}

func TestTracker_SyncFromOrders_MissingMetadata(t *testing.T) {
	tr := NewTracker()

	osOrders := []OSOrder{
		{
			LevelID:       "MM-ETH-USDT-BID-05",
			Generation:    1,
			ClientOrderID: "MM-ETH-USDT-BID-05-G001",
			OrderID:       "uuid-eth-bid-05",
			Side:          "BUY",
			Price:         decimal.RequireFromString("2350.00"),
			OriginalQty:   decimal.RequireFromString("2.0"),
			RemainingQty:  decimal.RequireFromString("2.0"),
			CreatedAt:     time.Time{}, // zero time / missing
		},
	}

	// No resolver provided
	added, duplicates := tr.SyncFromOrders("ETH-USDT", osOrders)
	if added != 1 || duplicates != 0 {
		t.Fatalf("expected added=1, duplicates=0, got added=%d, duplicates=%d", added, duplicates)
	}

	order := tr.Get("MM-ETH-USDT-BID-05")
	if order == nil {
		t.Fatal("expected order in tracker")
	}
	// Missing timestamps must NOT be silently set to time.Now()
	if !order.CreatedAt.IsZero() {
		t.Errorf("expected CreatedAt to remain zero, got %v", order.CreatedAt)
	}
	if !order.PendingSince.IsZero() {
		t.Errorf("expected PendingSince to remain zero, got %v", order.PendingSince)
	}
	// But OSRegisteredSince must record discovery time for expiry bounding
	if order.OSRegisteredSince.IsZero() {
		t.Error("expected OSRegisteredSince to be populated")
	}
	if order.Zone != "" {
		t.Errorf("expected Zone to be empty string, got %q", order.Zone)
	}
}

func TestTracker_SyncFromOrders_PartiallyFilled(t *testing.T) {
	tr := NewTracker()

	osOrders := []OSOrder{
		{
			LevelID:       "MM-SOL-USDT-ASK-03",
			Generation:    5,
			ClientOrderID: "MM-SOL-USDT-ASK-03-G005",
			OrderID:       "uuid-sol-ask-03",
			Side:          "SELL",
			Price:         decimal.RequireFromString("110.00"),
			OriginalQty:   decimal.RequireFromString("5.0"),
			RemainingQty:  decimal.RequireFromString("2.0"),
			CreatedAt:     time.Now().Add(-5 * time.Minute),
		},
	}

	added, _ := tr.SyncFromOrders("SOL-USDT", osOrders)
	if added != 1 {
		t.Fatalf("expected added=1, got %d", added)
	}

	order := tr.Get("MM-SOL-USDT-ASK-03")
	if order == nil {
		t.Fatal("expected order in tracker")
	}
	expectedRemaining := decimal.RequireFromString("2.0")
	expectedFilled := decimal.RequireFromString("3.0")
	if !order.RemainingQty.Equal(expectedRemaining) {
		t.Errorf("expected RemainingQty=%s, got %s", expectedRemaining, order.RemainingQty)
	}
	if !order.FilledQty.Equal(expectedFilled) {
		t.Errorf("expected FilledQty=%s, got %s", expectedFilled, order.FilledQty)
	}
}

func TestTracker_SyncFromOrders_FullyFilledAndInvalid(t *testing.T) {
	tr := NewTracker()

	osOrders := []OSOrder{
		{
			LevelID:       "MM-BTC-USDT-BID-02",
			Generation:    10,
			ClientOrderID: "MM-BTC-USDT-BID-02-G010",
			OrderID:       "uuid-btc-bid-02",
			Side:          "BUY",
			Price:         decimal.RequireFromString("94000.00"),
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.Zero, // fully filled!
		},
		{
			LevelID:       "MM-BTC-USDT-BID-03",
			Generation:    4,
			ClientOrderID: "MM-BTC-USDT-BID-03-G004",
			OrderID:       "uuid-btc-bid-03",
			Side:          "BUY",
			Price:         decimal.RequireFromString("93000.00"),
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("-0.5"), // negative remaining!
		},
		{
			LevelID:       "MM-BTC-USDT-BID-04",
			Generation:    2,
			ClientOrderID: "MM-BTC-USDT-BID-04-G002",
			OrderID:       "uuid-btc-bid-04",
			Side:          "BUY",
			Price:         decimal.RequireFromString("92000.00"),
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("1.5"), // remaining > original!
		},
	}

	added, _ := tr.SyncFromOrders("BTC-USDT", osOrders)
	if added != 0 {
		t.Fatalf("expected 0 orders added, got %d", added)
	}

	// Fully filled order must not be active
	if tr.Get("MM-BTC-USDT-BID-02") != nil {
		t.Error("fully filled order should not be added to active orders")
	}
	// But generation 10 must be recorded in generations map to prevent regression
	if tr.generations["MM-BTC-USDT-BID-02"] != 10 {
		t.Errorf("expected generation 10 recorded, got %d", tr.generations["MM-BTC-USDT-BID-02"])
	}

	// Inconsistent orders must not be added
	if tr.Get("MM-BTC-USDT-BID-03") != nil {
		t.Error("negative remaining order should not be in tracker")
	}
	if tr.Get("MM-BTC-USDT-BID-04") != nil {
		t.Error("inconsistent remaining > original order should not be in tracker")
	}
}

func TestTracker_SyncFromOrders_RepeatedSynchronization(t *testing.T) {
	tr := NewTracker()
	initialCreated := time.Now().Add(-20 * time.Minute)

	// Step 1: Initial sync with no resolver and missing zone
	tr.SyncFromOrders("ETH-USDT", []OSOrder{
		{
			LevelID:       "MM-ETH-USDT-BID-01",
			Generation:    1,
			ClientOrderID: "MM-ETH-USDT-BID-01-G001",
			OrderID:       "uuid-eth-1",
			Side:          "BUY",
			Price:         decimal.RequireFromString("2300.00"),
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("1.0"),
			CreatedAt:     time.Time{}, // unknown
		},
	})

	order1 := tr.Get("MM-ETH-USDT-BID-01")
	if order1 == nil || order1.Zone != "" || !order1.CreatedAt.IsZero() {
		t.Fatalf("initial sync state unexpected: zone=%q, createdAt=%v", order1.Zone, order1.CreatedAt)
	}

	// Step 2: Second sync provides authoritative resolver and CreatedAt
	mockResolver := func(levelID string) string {
		return "LOW"
	}

	added, duplicates := tr.SyncFromOrders("ETH-USDT", []OSOrder{
		{
			LevelID:       "MM-ETH-USDT-BID-01",
			Generation:    1,
			ClientOrderID: "MM-ETH-USDT-BID-01-G001",
			OrderID:       "uuid-eth-1",
			Side:          "BUY",
			Price:         decimal.RequireFromString("2300.00"),
			OriginalQty:   decimal.RequireFromString("1.0"),
			RemainingQty:  decimal.RequireFromString("0.8"), // updated remaining
			CreatedAt:     initialCreated,
		},
	}, mockResolver)

	if added != 0 {
		t.Errorf("expected 0 new orders added on repeated sync, got %d", added)
	}
	if duplicates != 0 {
		t.Errorf("expected 0 duplicates, got %d", duplicates)
	}

	order2 := tr.Get("MM-ETH-USDT-BID-01")
	if order2.Zone != "LOW" {
		t.Errorf("expected Zone to be updated to LOW, got %q", order2.Zone)
	}
	if !order2.CreatedAt.Equal(initialCreated) {
		t.Errorf("expected CreatedAt to be restored to %v, got %v", initialCreated, order2.CreatedAt)
	}
	expectedRem := decimal.RequireFromString("0.8")
	if !order2.RemainingQty.Equal(expectedRem) {
		t.Errorf("expected RemainingQty updated to %s, got %s", expectedRem, order2.RemainingQty)
	}
}

