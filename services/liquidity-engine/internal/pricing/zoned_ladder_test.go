package pricing

import (
	"math/rand"
	"testing"

	"github.com/shopspring/decimal"

	"tradedrift/services/liquidity-engine/internal/config"
)

// ── Mock TrackerReader ────────────────────────────────────────────────────────

// mockTracker implements TrackerReader for tests.
// Set activeLevels to simulate existing tracker state.
type mockTracker struct {
	activeLevels map[string]*ActiveLevel
}

func (m *mockTracker) GetActive(levelID string) *ActiveLevel {
	return m.activeLevels[levelID]
}

// emptyTracker returns a TrackerReader with no existing levels (all slots are new).
func emptyTracker() TrackerReader {
	return &mockTracker{activeLevels: map[string]*ActiveLevel{}}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func btcMarketConfig() *config.MarketConfig {
	return &config.MarketConfig{
		MarketID:    "BTC-USDT",
		TickSize:    decimal.RequireFromString("0.01"),
		LotSize:     decimal.RequireFromString("0.00001"),
		QtyPerLevel: decimal.RequireFromString("0.85"),
		BidZones:    config.DefaultZoneConfig(),
		AskZones:    config.DefaultZoneConfig(),
	}
}

func solMarketConfig() *config.MarketConfig {
	return &config.MarketConfig{
		MarketID:    "SOL-USDT",
		TickSize:    decimal.RequireFromString("0.001"),
		LotSize:     decimal.RequireFromString("0.01"),
		QtyPerLevel: decimal.RequireFromString("20.0"),
		BidZones:    config.DefaultZoneConfig(),
		AskZones:    config.DefaultZoneConfig(),
	}
}

func btcRef() decimal.Decimal { return decimal.NewFromFloat(96450.00) }
func solRef() decimal.Decimal { return decimal.NewFromFloat(188.20) }

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestGenerateZonedDesired_LevelCount(t *testing.T) {
	mc := btcMarketConfig()
	ref := btcRef()

	levels, err := GenerateZonedDesired(mc, ref, emptyTracker(), mc.BidZones, mc.AskZones, 1, 12, 12, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 7+4+1=12 bids + 12 asks = 24 total
	if len(levels) != 24 {
		t.Errorf("expected 24 levels, got %d", len(levels))
	}

	bids, asks := 0, 0
	for _, l := range levels {
		switch l.Side {
		case "BUY":
			bids++
		case "SELL":
			asks++
		}
	}
	if bids != 12 {
		t.Errorf("expected 12 BID levels, got %d", bids)
	}
	if asks != 12 {
		t.Errorf("expected 12 ASK levels, got %d", asks)
	}
}

func TestGenerateZonedDesired_BidsBelowRef(t *testing.T) {
	mc := btcMarketConfig()
	ref := btcRef()

	levels, err := GenerateZonedDesired(mc, ref, emptyTracker(), mc.BidZones, mc.AskZones, 1, 12, 12, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, l := range levels {
		if l.Side == "BUY" && !l.Price.LessThan(ref) {
			t.Errorf("BID price %s is not below ref %s (level %s)", l.Price, ref, l.LevelID)
		}
	}
}

func TestGenerateZonedDesired_AsksAboveRef(t *testing.T) {
	mc := btcMarketConfig()
	ref := btcRef()

	levels, err := GenerateZonedDesired(mc, ref, emptyTracker(), mc.BidZones, mc.AskZones, 1, 12, 12, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, l := range levels {
		if l.Side == "SELL" && !l.Price.GreaterThan(ref) {
			t.Errorf("ASK price %s is not above ref %s (level %s)", l.Price, ref, l.LevelID)
		}
	}
}

func TestGenerateZonedDesired_BidPricesUnique(t *testing.T) {
	mc := btcMarketConfig()
	ref := btcRef()

	levels, err := GenerateZonedDesired(mc, ref, emptyTracker(), mc.BidZones, mc.AskZones, 1, 12, 12, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	seen := map[string]bool{}
	for _, l := range levels {
		if l.Side != "BUY" {
			continue
		}
		key := l.Price.String()
		if seen[key] {
			t.Errorf("duplicate BID price %s", key)
		}
		seen[key] = true
	}
}

func TestGenerateZonedDesired_AskPricesUnique(t *testing.T) {
	mc := btcMarketConfig()
	ref := btcRef()

	levels, err := GenerateZonedDesired(mc, ref, emptyTracker(), mc.BidZones, mc.AskZones, 1, 12, 12, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	seen := map[string]bool{}
	for _, l := range levels {
		if l.Side != "SELL" {
			continue
		}
		key := l.Price.String()
		if seen[key] {
			t.Errorf("duplicate ASK price %s", key)
		}
		seen[key] = true
	}
}

func TestGenerateZonedDesired_ZoneLabels(t *testing.T) {
	mc := btcMarketConfig()
	ref := btcRef()

	levels, err := GenerateZonedDesired(mc, ref, emptyTracker(), mc.BidZones, mc.AskZones, 1, 12, 12, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Level index 1-7 → LOW, 8-11 → MID, 12 → HIGH (per side)
	var bidLevels []PriceLevel
	for _, l := range levels {
		if l.Side == "BUY" {
			bidLevels = append(bidLevels, l)
		}
	}

	for i, l := range bidLevels {
		expectedZone := ""
		switch {
		case i < 7:
			expectedZone = "LOW"
		case i < 11:
			expectedZone = "MID"
		default:
			expectedZone = "HIGH"
		}
		if l.Zone != expectedZone {
			t.Errorf("BID level %d (%s): expected zone %s, got %s", i+1, l.LevelID, expectedZone, l.Zone)
		}
	}
}

func TestGenerateZonedDesired_LocksExistingActivePrice(t *testing.T) {
	mc := btcMarketConfig()
	ref := btcRef()
	lockedPrice := decimal.NewFromFloat(96200.00)

	// Simulate an existing RESTING order at BID-01 with a locked price.
	tracker := &mockTracker{
		activeLevels: map[string]*ActiveLevel{
			"MM-BTC-USDT-BID-01": {
				Status:     "RESTING",
				Price:      lockedPrice,
				RefVersion: 5,
			},
		},
	}

	levels, err := GenerateZonedDesired(mc, ref, tracker, mc.BidZones, mc.AskZones, 10, 12, 12, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, l := range levels {
		if l.LevelID == "MM-BTC-USDT-BID-01" {
			if !l.Price.Equal(lockedPrice) {
				t.Errorf("expected RESTING price %s to be locked, got %s", lockedPrice, l.Price)
			}
			if l.RefVersion != 5 {
				t.Errorf("expected RefVersion=5 preserved, got %d", l.RefVersion)
			}
			return
		}
	}
	t.Fatal("did not find MM-BTC-USDT-BID-01 in output")
}

func TestGenerateZonedDesired_RegeneratesCancellingSlot(t *testing.T) {
	mc := btcMarketConfig()
	ref := btcRef()
	cancellingPrice := decimal.NewFromFloat(96200.00)

	// CANCELLING should be treated as absent — new price generated.
	tracker := &mockTracker{
		activeLevels: map[string]*ActiveLevel{
			"MM-BTC-USDT-BID-01": {
				Status: "CANCELLING",
				Price:  cancellingPrice,
			},
		},
	}

	levels, err := GenerateZonedDesired(mc, ref, tracker, mc.BidZones, mc.AskZones, 99, 12, 12, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, l := range levels {
		if l.LevelID == "MM-BTC-USDT-BID-01" {
			// New price must be different from the old cancelling price
			// (with overwhelming probability — zone has thousands of ticks).
			// We just verify the RefVersion is from the new call (99).
			if l.RefVersion != 99 {
				t.Errorf("expected new RefVersion=99 for regenerated slot, got %d", l.RefVersion)
			}
			return
		}
	}
	t.Fatal("did not find MM-BTC-USDT-BID-01 in output")
}

func TestGenerateZonedDesired_RefVersionStampedOnNew(t *testing.T) {
	mc := btcMarketConfig()
	ref := btcRef()

	levels, err := GenerateZonedDesired(mc, ref, emptyTracker(), mc.BidZones, mc.AskZones, 42, 12, 12, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, l := range levels {
		if l.RefVersion != 42 {
			t.Errorf("level %s: expected RefVersion=42, got %d", l.LevelID, l.RefVersion)
		}
	}
}

func TestGenerateZonedDesired_TickNormalised(t *testing.T) {
	mc := btcMarketConfig() // tick=0.01
	ref := btcRef()

	levels, err := GenerateZonedDesired(mc, ref, emptyTracker(), mc.BidZones, mc.AskZones, 1, 12, 12, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tick := mc.TickSize
	for _, l := range levels {
		rem := l.Price.Mod(tick)
		if !rem.IsZero() {
			t.Errorf("level %s price %s is not tick-aligned (tick=%s, remainder=%s)",
				l.LevelID, l.Price, tick, rem)
		}
	}
}

func TestGenerateZonedDesired_SOLMarket(t *testing.T) {
	// SOL has a tighter tick (0.001) — verify the generator still produces 24 valid levels.
	mc := solMarketConfig()
	ref := solRef()

	levels, err := GenerateZonedDesired(mc, ref, emptyTracker(), mc.BidZones, mc.AskZones, 1, 12, 12, nil)
	if err != nil {
		t.Fatalf("SOL: unexpected error: %v", err)
	}
	if len(levels) != 24 {
		t.Errorf("SOL: expected 24 levels, got %d", len(levels))
	}
	for _, l := range levels {
		if l.Side == "BUY" && !l.Price.LessThan(ref) {
			t.Errorf("SOL BID %s price %s not below ref %s", l.LevelID, l.Price, ref)
		}
		if l.Side == "SELL" && !l.Price.GreaterThan(ref) {
			t.Errorf("SOL ASK %s price %s not above ref %s", l.LevelID, l.Price, ref)
		}
	}
}

func TestGenerateZonedDesired_InvalidRef(t *testing.T) {
	mc := btcMarketConfig()
	_, err := GenerateZonedDesired(mc, decimal.Zero, emptyTracker(), mc.BidZones, mc.AskZones, 1, 12, 12, nil)
	if err == nil {
		t.Fatal("expected error for zero reference price")
	}
}

func TestGenerateZonedDesired_DefaultZones(t *testing.T) {
	// If BidZones/AskZones are zero-value, DefaultZoneConfig() should kick in.
	mc := &config.MarketConfig{
		MarketID:    "BTC-USDT",
		TickSize:    decimal.RequireFromString("0.01"),
		LotSize:     decimal.RequireFromString("0.00001"),
		QtyPerLevel: decimal.RequireFromString("0.85"),
		// BidZones and AskZones are zero-value
	}
	ref := btcRef()
	levels, err := GenerateZonedDesired(mc, ref, emptyTracker(), config.ZoneConfig{}, config.ZoneConfig{}, 1, 12, 12, nil)
	if err != nil {
		t.Fatalf("unexpected error with zero-value ZoneConfig: %v", err)
	}
	if len(levels) != 24 {
		t.Errorf("expected 24 levels with default zones, got %d", len(levels))
	}
}

// Compile-time check: mockTracker implements TrackerReader.
var _ TrackerReader = (*mockTracker)(nil)

func TestAllocateZones_LowFirst(t *testing.T) {
	defaultCfg := config.DefaultZoneConfig() // Low: 7, Mid: 4, High: 1

	tests := []struct {
		target   int
		wantLow  int
		wantMid  int
		wantHigh int
	}{
		{target: 12, wantLow: 7, wantMid: 4, wantHigh: 1},
		{target: 11, wantLow: 7, wantMid: 4, wantHigh: 0},
		{target: 8, wantLow: 7, wantMid: 1, wantHigh: 0},
		{target: 7, wantLow: 7, wantMid: 0, wantHigh: 0},
		{target: 6, wantLow: 6, wantMid: 0, wantHigh: 0},
		{target: 4, wantLow: 4, wantMid: 0, wantHigh: 0},
		{target: 2, wantLow: 2, wantMid: 0, wantHigh: 0},
		{target: 1, wantLow: 1, wantMid: 0, wantHigh: 0},
		{target: 0, wantLow: 0, wantMid: 0, wantHigh: 0},
	}

	for _, tt := range tests {
		alloc := AllocateZones(tt.target, defaultCfg)
		if alloc.Low.Count != tt.wantLow || alloc.Mid.Count != tt.wantMid || alloc.High.Count != tt.wantHigh {
			t.Errorf("AllocateZones(%d): got LOW=%d, MID=%d, HIGH=%d; want LOW=%d, MID=%d, HIGH=%d",
				tt.target, alloc.Low.Count, alloc.Mid.Count, alloc.High.Count,
				tt.wantLow, tt.wantMid, tt.wantHigh)
		}
	}
}

func TestGenerateZonedDesired_InventorySkewCounts(t *testing.T) {
	mc := btcMarketConfig()
	ref := btcRef()

	// 4 bids (should be 4 LOW, 0 MID, 0 HIGH), 12 asks (7 LOW, 4 MID, 1 HIGH)
	levels, err := GenerateZonedDesired(mc, ref, emptyTracker(), mc.BidZones, mc.AskZones, 1, 4, 12, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bids := 0
	asks := 0
	for _, l := range levels {
		if l.Side == "BUY" {
			bids++
			if l.Zone != "LOW" {
				t.Errorf("expected 4 bids to all be in LOW zone, got %s for level %s", l.Zone, l.LevelID)
			}
		} else if l.Side == "SELL" {
			asks++
		}
	}

	if bids != 4 {
		t.Errorf("expected 4 bids, got %d", bids)
	}
	if asks != 12 {
		t.Errorf("expected 12 asks, got %d", asks)
	}
}

func TestGenerateLevelPrice_ZoneBoundaryGuard(t *testing.T) {
	mc := btcMarketConfig() // tick = 0.01
	ref := decimal.RequireFromString("10000.00")
	rng := rand.New(rand.NewSource(12345))

	bands := []config.ZoneBand{
		{MinBps: 5, MaxBps: 50, Count: 7},
		{MinBps: 50, MaxBps: 150, Count: 4},
		{MinBps: 150, MaxBps: 300, Count: 1},
	}

	for _, band := range bands {
		taken := make(map[string]bool)
		for i := 0; i < 50; i++ {
			price, err := GenerateLevelPrice(mc, ref, "BUY", band, taken, rng)
			if err != nil {
				t.Fatalf("GenerateLevelPrice BUY failed: %v", err)
			}
			taken[price.String()] = true

			ten4 := decimal.NewFromInt(10000)
			minMult := decimal.NewFromInt(int64(band.MinBps)).Div(ten4)
			maxMult := decimal.NewFromInt(int64(band.MaxBps)).Div(ten4)
			one := decimal.NewFromInt(1)

			maxPrice := ref.Div(one.Add(minMult))
			minPrice := ref.Div(one.Add(maxMult))

			if price.LessThan(minPrice) || price.GreaterThan(maxPrice) {
				t.Errorf("BUY price %s out of bounds [%s, %s]", price, minPrice, maxPrice)
			}
			if !price.LessThan(ref) {
				t.Errorf("BUY price %s not less than ref %s", price, ref)
			}
		}
	}

	for _, band := range bands {
		taken := make(map[string]bool)
		for i := 0; i < 50; i++ {
			price, err := GenerateLevelPrice(mc, ref, "SELL", band, taken, rng)
			if err != nil {
				t.Fatalf("GenerateLevelPrice SELL failed: %v", err)
			}
			taken[price.String()] = true

			ten4 := decimal.NewFromInt(10000)
			minMult := decimal.NewFromInt(int64(band.MinBps)).Div(ten4)
			maxMult := decimal.NewFromInt(int64(band.MaxBps)).Div(ten4)
			one := decimal.NewFromInt(1)

			minPrice := ref.Mul(one.Add(minMult))
			maxPrice := ref.Mul(one.Add(maxMult))

			if price.LessThan(minPrice) || price.GreaterThan(maxPrice) {
				t.Errorf("SELL price %s out of bounds [%s, %s]", price, minPrice, maxPrice)
			}
			if !price.GreaterThan(ref) {
				t.Errorf("SELL price %s not greater than ref %s", price, ref)
			}
		}
	}
}

func TestGenerateZonedDesired_DuplicatePriceImpossible_1000Runs(t *testing.T) {
	mc := btcMarketConfig()
	ref := btcRef()
	rng := rand.New(rand.NewSource(42))

	for iter := 0; iter < 1000; iter++ {
		levels, err := GenerateZonedDesired(mc, ref, emptyTracker(), mc.BidZones, mc.AskZones, int64(iter), 12, 12, rng)
		if err != nil {
			t.Fatalf("iter %d: unexpected error: %v", iter, err)
		}

		bids := make(map[string]bool)
		asks := make(map[string]bool)
		for _, l := range levels {
			priceStr := l.Price.String()
			if l.Side == "BUY" {
				if bids[priceStr] {
					t.Fatalf("iter %d: duplicate BID price: %s", iter, priceStr)
				}
				bids[priceStr] = true
			} else {
				if asks[priceStr] {
					t.Fatalf("iter %d: duplicate ASK price: %s", iter, priceStr)
				}
				asks[priceStr] = true
			}
		}
	}
}

func TestGenerateLevelPrice_NarrowZoneBoundaryCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	taken := make(map[string]bool)

	// Case 1: Rounded below minPrice
	// ref = 10000, tickSize = 1.00
	// Band MinBps = 10, MaxBps = 10 (both yield minPrice = 10010, maxPrice = 10010)
	// But let's construct a market config where tick size is large enough that mid rounds below minPrice:
	// For BUY: minMult = 1 - 0.0010 = 0.9990, maxMult = 1 - 0.0005 = 0.9995
	// ref = 1000.00, tickSize = 1.00
	// minPrice = 999.00, maxPrice = 999.50. rangeTicks = (999.50 - 999.00)/1.00 = 0.
	// mid = 999.25. roundToTick(999.25, 1.00) = 999.00 (in bounds)
	// Now with minPrice = 999.20, maxPrice = 999.40:
	// mid = 999.30, roundToTick(999.30, 1.00) = 999.00.
	// 999.00 < minPrice (999.20) -> out of bounds!
	mc := &config.MarketConfig{
		MarketID: "BTC-USDT",
		TickSize: decimal.RequireFromString("1.00"),
	}
	ref := decimal.RequireFromString("1000.00")

	// Test 1: Band where midpoint rounds below minPrice
	// minPrice = 1000 * (1 - 0.0008) = 999.20
	// maxPrice = 1000 * (1 - 0.0006) = 999.40
	bandBelow := config.ZoneBand{
		MinBps: 6, // maxPrice for BUY
		MaxBps: 8, // minPrice for BUY
		Count:  1,
	}
	_, err := GenerateLevelPrice(mc, ref, "BUY", bandBelow, taken, rng)
	if err == nil {
		t.Fatalf("expected error when narrow zone price rounds below minPrice, got nil")
	}

	// Test 2: Valid narrow zone where tick rounded price is within [minPrice, maxPrice]
	// ref = 1000.00, tickSize = 0.10
	// minPrice = 999.00, maxPrice = 999.05 -> mid = 999.025, rounded = 999.00 (== minPrice, valid!)
	mcValid := &config.MarketConfig{
		MarketID: "BTC-USDT",
		TickSize: decimal.RequireFromString("0.10"),
	}
	bandValid := config.ZoneBand{
		MinBps: 95,
		MaxBps: 100, // minPrice = 990.00, maxPrice = 990.50
		Count:  1,
	}
	price, err := GenerateLevelPrice(mcValid, ref, "BUY", bandValid, taken, rng)
	if err != nil {
		t.Fatalf("expected success for valid in-bounds narrow zone, got: %v", err)
	}
	if price.LessThan(decimal.RequireFromString("990.00")) || price.GreaterThan(decimal.RequireFromString("990.50")) {
		t.Fatalf("expected price within [990.00, 990.50], got %s", price)
	}
}

