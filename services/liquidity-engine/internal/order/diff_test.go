package order

import (
	"testing"

	"github.com/shopspring/decimal"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/pricing"
)

const testMarket = "BTC-USDT"

func testCfg() *config.MarketConfig {
	return &config.MarketConfig{
		MarketID:     testMarket,
		MinOrderSize: decimal.RequireFromString("0.00001"),
		TickSize:     decimal.RequireFromString("0.01"),
		LotSize:      decimal.RequireFromString("0.00001"),
	}
}

func price(s string) decimal.Decimal { return decimal.RequireFromString(s) }
func qty(s string) decimal.Decimal   { return decimal.RequireFromString(s) }

func desiredLevel(id, side, p, q string) pricing.PriceLevel {
	return pricing.PriceLevel{
		LevelID:  id,
		MarketID: testMarket,
		Side:     side,
		Price:    price(p),
		Quantity: qty(q),
	}
}

func addResting(t *Tracker, id, side, p, remaining string) *Tracker {
	t.orders[id] = &LiveOrder{
		LevelID:       id,
		ClientOrderID: id + "-G001",
		OrderID:       "order-uuid-" + id,
		MarketID:      testMarket,
		Side:          side,
		Price:         price(p),
		OriginalQty:   qty(remaining),
		RemainingQty:  qty(remaining),
		FilledQty:     decimal.Zero,
		Status:        StatusResting,
	}
	return t
}

func TestDiff_EmptyBothSides(t *testing.T) {
	tracker := NewTracker()
	entries := Diff(nil, tracker, testMarket, testCfg())
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestDiff_AllDesiredMissing(t *testing.T) {
	tracker := NewTracker()
	cfg := &config.MarketConfig{
		MarketID:        testMarket,
		TickSize:        decimal.RequireFromString("0.01"),
		LotSize:         decimal.RequireFromString("0.00001"),
		SpreadBps:       4,
		ReferencePrice: price("96450.00"),
		LevelCount:     12,
		MinOrderSize:   decimal.RequireFromString("0.00001"),
	}
	desired := pricing.GenerateLadder(cfg, 12, 12)

	entries := Diff(desired, tracker, testMarket, cfg)
	if len(entries) != 24 {
		t.Fatalf("expected 24 CREATE entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Action != DiffCreate {
			t.Errorf("expected DiffCreate, got %d for %s", e.Action, e.LevelID)
		}
	}
}

func TestDiff_ExactMatch(t *testing.T) {
	tracker := NewTracker()
	desired := []pricing.PriceLevel{
		desiredLevel("MM-BTC-USDT-ASK-01", "SELL", "96487.00", "0.85000"),
		desiredLevel("MM-BTC-USDT-BID-01", "BUY", "96411.00", "0.85000"),
	}
	addResting(tracker, "MM-BTC-USDT-ASK-01", "SELL", "96487.00", "0.85000")
	addResting(tracker, "MM-BTC-USDT-BID-01", "BUY", "96411.00", "0.85000")

	entries := Diff(desired, tracker, testMarket, testCfg())
	if len(entries) != 0 {
		t.Errorf("exact match should produce 0 entries, got %d: %+v", len(entries), entries)
	}
}

func TestDiff_PriceMismatch(t *testing.T) {
	tracker := NewTracker()
	desired := []pricing.PriceLevel{
		desiredLevel("MM-BTC-USDT-ASK-01", "SELL", "96500.00", "0.85000"),
	}
	addResting(tracker, "MM-BTC-USDT-ASK-01", "SELL", "96450.00", "0.85000")

	entries := Diff(desired, tracker, testMarket, testCfg())
	if len(entries) != 1 {
		t.Fatalf("expected 1 CORRECT, got %d: %+v", len(entries), entries)
	}
	if entries[0].Action != DiffCorrect {
		t.Errorf("expected DiffCorrect, got %d", entries[0].Action)
	}
}

func TestDiff_QuantityExhausted(t *testing.T) {
	tracker := NewTracker()
	desired := []pricing.PriceLevel{
		desiredLevel("MM-BTC-USDT-ASK-01", "SELL", "96500.00", "0.85000"),
	}
	tracker.orders["MM-BTC-USDT-ASK-01"] = &LiveOrder{
		LevelID:       "MM-BTC-USDT-ASK-01",
		ClientOrderID: "MM-BTC-USDT-ASK-01-G001",
		OrderID:       "order-uuid-1",
		MarketID:      testMarket,
		Side:          "SELL",
		Price:         price("96500.00"),
		OriginalQty:   qty("0.85000"),
		RemainingQty:  qty("0.000005"), // below MinOrderSize (0.00001)
		Status:        StatusResting,
	}

	entries := Diff(desired, tracker, testMarket, testCfg())
	if len(entries) != 1 {
		t.Fatalf("expected 1 CORRECT for exhausted qty, got %d: %+v", len(entries), entries)
	}
	if entries[0].Action != DiffCorrect {
		t.Errorf("expected DiffCorrect, got %d", entries[0].Action)
	}
}

func TestDiff_ExtraResting(t *testing.T) {
	tracker := NewTracker()
	desired := []pricing.PriceLevel{
		desiredLevel("MM-BTC-USDT-ASK-01", "SELL", "96500.00", "0.85000"),
	}
	addResting(tracker, "MM-BTC-USDT-ASK-01", "SELL", "96500.00", "0.85000")
	addResting(tracker, "MM-BTC-USDT-ASK-02", "SELL", "96650.00", "0.85000") // extra

	entries := Diff(desired, tracker, testMarket, testCfg())
	if len(entries) != 1 {
		t.Fatalf("expected 1 CANCEL for extra RESTING, got %d", len(entries))
	}
	if entries[0].Action != DiffCancel {
		t.Errorf("expected DiffCancel, got %d", entries[0].Action)
	}
	if entries[0].LevelID != "MM-BTC-USDT-ASK-02" {
		t.Errorf("wrong level cancelled: %s", entries[0].LevelID)
	}
}

func TestDiff_PendingBlocksCreate(t *testing.T) {
	tracker := NewTracker()
	desired := []pricing.PriceLevel{
		desiredLevel("MM-BTC-USDT-ASK-01", "SELL", "96500.00", "0.85000"),
	}
	tracker.orders["MM-BTC-USDT-ASK-01"] = &LiveOrder{
		LevelID:  "MM-BTC-USDT-ASK-01",
		MarketID: testMarket,
		Status:   StatusPending,
	}

	entries := Diff(desired, tracker, testMarket, testCfg())
	if len(entries) != 0 {
		t.Errorf("PENDING should block CREATE, got %d entries: %+v", len(entries), entries)
	}
}

func TestDiff_CancellingBlocksCreate(t *testing.T) {
	tracker := NewTracker()
	desired := []pricing.PriceLevel{
		desiredLevel("MM-BTC-USDT-ASK-01", "SELL", "96500.00", "0.85000"),
	}
	tracker.orders["MM-BTC-USDT-ASK-01"] = &LiveOrder{
		LevelID:  "MM-BTC-USDT-ASK-01",
		MarketID: testMarket,
		Status:   StatusCancelling,
	}

	entries := Diff(desired, tracker, testMarket, testCfg())
	if len(entries) != 0 {
		t.Errorf("CANCELLING should produce 0 entries, got %d: %+v", len(entries), entries)
	}
}

func TestDiff_StaleBlocksCreate(t *testing.T) {
	tracker := NewTracker()
	desired := []pricing.PriceLevel{
		desiredLevel("MM-BTC-USDT-ASK-01", "SELL", "96500.00", "0.85000"),
	}
	tracker.orders["MM-BTC-USDT-ASK-01"] = &LiveOrder{
		LevelID:  "MM-BTC-USDT-ASK-01",
		MarketID: testMarket,
		Status:   StatusStale,
	}

	entries := Diff(desired, tracker, testMarket, testCfg())
	if len(entries) != 0 {
		t.Errorf("STALE should block CREATE, got %d entries: %+v", len(entries), entries)
	}
}

func TestTracker_CommittedBase(t *testing.T) {
	tracker := NewTracker()
	tracker.orders["MM-BTC-USDT-ASK-01"] = &LiveOrder{
		LevelID:      "MM-BTC-USDT-ASK-01",
		MarketID:     testMarket,
		Side:         "SELL",
		OriginalQty:  qty("1.00000"),
		RemainingQty: qty("0.60000"), // partially filled
		Status:       StatusResting,
	}
	tracker.orders["MM-BTC-USDT-ASK-02"] = &LiveOrder{
		LevelID:      "MM-BTC-USDT-ASK-02",
		MarketID:     testMarket,
		Side:         "SELL",
		OriginalQty:  qty("0.85000"),
		RemainingQty: qty("0.85000"),
		Status:       StatusResting,
	}

	committed := tracker.CommittedBase(testMarket)
	expected := qty("1.45000") // 0.60 + 0.85 (not 1.00 + 0.85)
	if !committed.Equal(expected) {
		t.Errorf("CommittedBase = %s, want %s (must use RemainingQty, not OriginalQty)", committed, expected)
	}
}

func TestTracker_CommittedQuote(t *testing.T) {
	tracker := NewTracker()
	tracker.orders["MM-BTC-USDT-BID-01"] = &LiveOrder{
		LevelID:      "MM-BTC-USDT-BID-01",
		MarketID:     testMarket,
		Side:         "BUY",
		Price:        price("96411.00"),
		RemainingQty: qty("0.85000"),
		Status:       StatusResting,
	}
	tracker.orders["MM-BTC-USDT-BID-02"] = &LiveOrder{
		LevelID:      "MM-BTC-USDT-BID-02",
		MarketID:     testMarket,
		Side:         "BUY",
		Price:        price("96372.00"),
		RemainingQty: qty("0.85000"),
		Status:       StatusPending,
	}

	committed := tracker.CommittedQuote(testMarket)
	expected := qty("0.85").Mul(price("96411.00")).Add(qty("0.85").Mul(price("96372.00")))
	if !committed.Equal(expected) {
		t.Errorf("CommittedQuote = %s, want %s", committed, expected)
	}
}

func TestClientOrderID(t *testing.T) {
	got := ClientOrderID("MM-BTC-USDT-ASK-01", 3)
	want := "MM-BTC-USDT-ASK-01-G003"
	if got != want {
		t.Errorf("ClientOrderID = %q, want %q", got, want)
	}
}
