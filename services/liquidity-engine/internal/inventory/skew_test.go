package inventory

import (
	"testing"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/config"
)

func testMarketConfig() *config.MarketConfig {
	return &config.MarketConfig{
		MarketID:      "BTC-USDT",
		LevelCount:    12,
		MinBase:       decimal.RequireFromString("30"),      // 30 BTC
		CriticalBase:  decimal.RequireFromString("5"),       // 5 BTC
		MinQuote:      decimal.RequireFromString("1000000"), // $1,000,000 USDT
		CriticalQuote: decimal.RequireFromString("100000"),  // $100,000 USDT
	}
}

func TestComputeSkew_Normal(t *testing.T) {
	mc := testMarketConfig()
	logger := zap.NewNop()

	// High balances on both sides
	effectiveBase := decimal.RequireFromString("100")
	effectiveQuote := decimal.RequireFromString("5000000")

	skew := ComputeSkew(mc, effectiveBase, effectiveQuote, logger)

	if skew.BaseTier != TierNormal || skew.QuoteTier != TierNormal {
		t.Fatalf("expected TierNormal on both sides, got Base: %v, Quote: %v", skew.BaseTier, skew.QuoteTier)
	}
	if skew.BidCount != 12 || skew.AskCount != 12 {
		t.Errorf("expected 12 bids and 12 asks, got Bids: %d, Asks: %d", skew.BidCount, skew.AskCount)
	}
}

func TestComputeSkew_LowBase(t *testing.T) {
	mc := testMarketConfig()
	logger := zap.NewNop()

	// Base dropped to 20 BTC (<= 30 MinBase, but > 5 CriticalBase)
	effectiveBase := decimal.RequireFromString("20")
	effectiveQuote := decimal.RequireFromString("5000000")

	skew := ComputeSkew(mc, effectiveBase, effectiveQuote, logger)

	if skew.BaseTier != TierLow {
		t.Errorf("expected BaseTier Low, got %v", skew.BaseTier)
	}
	if skew.AskCount != 6 {
		t.Errorf("expected 6 asks (halved), got %d", skew.AskCount)
	}
	if skew.BidCount != 12 {
		t.Errorf("expected 12 bids (normal), got %d", skew.BidCount)
	}
}

func TestComputeSkew_CriticalBase(t *testing.T) {
	mc := testMarketConfig()
	logger := zap.NewNop()

	// Base dropped to 3 BTC (<= 5 CriticalBase)
	effectiveBase := decimal.RequireFromString("3")
	effectiveQuote := decimal.RequireFromString("5000000")

	skew := ComputeSkew(mc, effectiveBase, effectiveQuote, logger)

	if skew.BaseTier != TierCritical {
		t.Errorf("expected BaseTier Critical, got %v", skew.BaseTier)
	}
	if skew.AskCount != 0 {
		t.Errorf("expected 0 asks (halted selling base), got %d", skew.AskCount)
	}
	if skew.BidCount != 12 {
		t.Errorf("expected 12 bids, got %d", skew.BidCount)
	}
}

func TestComputeSkew_CriticalQuote(t *testing.T) {
	mc := testMarketConfig()
	logger := zap.NewNop()

	// USDT dropped to $50,000 (<= $100,000 CriticalQuote)
	effectiveBase := decimal.RequireFromString("100")
	effectiveQuote := decimal.RequireFromString("50000")

	skew := ComputeSkew(mc, effectiveBase, effectiveQuote, logger)

	if skew.QuoteTier != TierCritical {
		t.Errorf("expected QuoteTier Critical, got %v", skew.QuoteTier)
	}
	if skew.BidCount != 0 {
		t.Errorf("expected 0 bids (halted buying base with USDT), got %d", skew.BidCount)
	}
	if skew.AskCount != 12 {
		t.Errorf("expected 12 asks, got %d", skew.AskCount)
	}
}
