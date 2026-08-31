package pricing

import (
	"testing"

	"github.com/shopspring/decimal"

	"tradedrift/services/liquidity-engine/internal/config"
)

func btcMarket() *config.MarketConfig {
	return &config.MarketConfig{
		MarketID:        "BTC-USDT",
		BaseAsset:       "BTC",
		QuoteAsset:      "USDT",
		TickSize:        decimal.RequireFromString("0.01"),
		LotSize:         decimal.RequireFromString("0.00001"),
		LevelCount:      12,
		MinOrderSize:    decimal.RequireFromString("0.00001"),
		SpreadBps:      4,
		ReferencePrice: decimal.RequireFromString("96450.00"),
	}
}

func TestGenerateLadder_Count(t *testing.T) {
	mc := btcMarket()
	levels := GenerateLadder(mc, 12, 12)
	if len(levels) != 24 {
		t.Fatalf("expected 24 levels, got %d", len(levels))
	}

	bids, asks := 0, 0
	for _, l := range levels {
		if l.Side == "BUY" {
			bids++
		} else {
			asks++
		}
	}
	if bids != 12 {
		t.Errorf("expected 12 bids, got %d", bids)
	}
	if asks != 12 {
		t.Errorf("expected 12 asks, got %d", asks)
	}
}

func TestGenerateLadder_BidsBelowReference(t *testing.T) {
	mc := btcMarket()
	levels := GenerateLadder(mc, 12, 12)

	ref := mc.ReferencePrice
	for _, l := range levels {
		if l.Side == "BUY" {
			if !l.Price.LessThan(ref) {
				t.Errorf("bid %s price %s must be < reference %s", l.LevelID, l.Price, ref)
			}
		}
	}
}

func TestGenerateLadder_AsksAboveReference(t *testing.T) {
	mc := btcMarket()
	levels := GenerateLadder(mc, 12, 12)

	ref := mc.ReferencePrice
	for _, l := range levels {
		if l.Side == "SELL" {
			if !l.Price.GreaterThan(ref) {
				t.Errorf("ask %s price %s must be > reference %s", l.LevelID, l.Price, ref)
			}
		}
	}
}

func TestGenerateLadder_PricesTickRounded(t *testing.T) {
	mc := btcMarket()
	levels := GenerateLadder(mc, 12, 12)
	tick := mc.TickSize

	for _, l := range levels {
		remainder := l.Price.Mod(tick)
		if !remainder.IsZero() {
			t.Errorf("level %s price %s is not tick-rounded (tick=%s, remainder=%s)",
				l.LevelID, l.Price, tick, remainder)
		}
	}
}

func TestGenerateLadder_QuantitiesLotRounded(t *testing.T) {
	mc := btcMarket()
	levels := GenerateLadder(mc, 12, 12)
	lot := mc.LotSize

	for _, l := range levels {
		remainder := l.Quantity.Mod(lot)
		if !remainder.IsZero() {
			t.Errorf("level %s quantity %s is not lot-rounded (lot=%s, remainder=%s)",
				l.LevelID, l.Price, lot, remainder)
		}
	}
}

func TestGenerateLadder_LevelIDFormat(t *testing.T) {
	mc := btcMarket()
	levels := GenerateLadder(mc, 12, 12)

	if levels[0].LevelID != "MM-BTC-USDT-BID-01" {
		t.Errorf("expected MM-BTC-USDT-BID-01, got %s", levels[0].LevelID)
	}
	if levels[23].LevelID != "MM-BTC-USDT-ASK-12" {
		t.Errorf("expected MM-BTC-USDT-ASK-12, got %s", levels[23].LevelID)
	}
}

func TestRoundToTick(t *testing.T) {
	tick := decimal.RequireFromString("0.01")

	cases := []struct {
		input    string
		expected string
	}{
		{"96450.00", "96450.00"},
		{"96450.005", "96450.00"},
		{"96450.019", "96450.01"},
		{"96450.999", "96450.99"},
	}

	for _, c := range cases {
		in := decimal.RequireFromString(c.input)
		got := roundToTick(in, tick)
		exp := decimal.RequireFromString(c.expected)
		if !got.Equal(exp) {
			t.Errorf("roundToTick(%s) = %s, want %s", c.input, got, c.expected)
		}
	}
}

func TestRoundToLot(t *testing.T) {
	lot := decimal.RequireFromString("0.00001")

	cases := []struct {
		input    string
		expected string
	}{
		{"0.85000", "0.85000"},
		{"0.85001", "0.85001"},
		{"0.850009", "0.85000"},
		{"0.850019", "0.85001"},
	}

	for _, c := range cases {
		in := decimal.RequireFromString(c.input)
		got := roundToLot(in, lot)
		exp := decimal.RequireFromString(c.expected)
		if !got.Equal(exp) {
			t.Errorf("roundToLot(%s) = %s, want %s", c.input, got, c.expected)
		}
	}
}
