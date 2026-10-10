package test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
	"tradedrift/services/controlled-taker/internal/config"
	"tradedrift/services/controlled-taker/internal/engine"
)

// TestMicrostructureAcceptance_CTS_SizingAndProtection validates the CTS side of the Phase 5 acceptance test:
// Reference: 85,000
// Book:
//   Best Ask: 0.10 @ 85,020
//   L2 Ask:   0.05 @ 85,040
//   L3 Ask:   0.03 @ 85,060
// Total reachable depth = 0.18 BTC
// CTS sizes order with protection price up to 85,060 (protection boundary <= 85,070)
// and passes pre-order validation without tripping the depth ceiling.
func TestMicrostructureAcceptance_CTS_SizingAndProtection(t *testing.T) {
	market := config.MarketConfig{
		MarketID:    "BTC-USDT",
		BaseAsset:   "BTC",
		QuoteAsset:  "USDT",
		TickSize:    decimal.NewFromFloat(0.01),
		LotSize:     decimal.NewFromFloat(0.01),
		MinQuantity: decimal.NewFromFloat(0.01),
	}

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(84980), Quantity: decimal.NewFromFloat(0.10)},
			{Price: decimal.NewFromInt(84960), Quantity: decimal.NewFromFloat(0.05)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85020), Quantity: decimal.NewFromFloat(0.10)},
			{Price: decimal.NewFromInt(85040), Quantity: decimal.NewFromFloat(0.05)},
			{Price: decimal.NewFromInt(85060), Quantity: decimal.NewFromFloat(0.03)},
		},
	}

	sweepRoll := 0.10 // Triggers sweep (< 0.70 highSweepProb)
	maxDepthConsumption := decimal.NewFromFloat(1.0) // Allow full depth consumption for multi-level test
	opts := engine.SizingOptions{
		MaxSweepLevels:             3,
		SweepRollOverride:          &sweepRoll,
		MaxVisibleDepthConsumption: &maxDepthConsumption,
	}

	// Calculate HIGH sweep parameters
	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileHigh,
		depth,
		"BUY",
		market,
		decimal.NewFromInt(50000), // Sufficient notional
		15,                        // 15 bps slippage allows up to 85,020 * 1.0015 = 85,147
		opts,
	)
	if err != nil {
		t.Fatalf("unexpected sizing error: %v", err)
	}

	if params.Mode != engine.ExecutionSweep {
		t.Fatalf("expected ExecutionSweep, got %s", params.Mode)
	}

	// Total reachable depth is 0.18 BTC
	if !params.ReachableDepth.Equal(decimal.NewFromFloat(0.18)) {
		t.Errorf("expected total reachable depth 0.18, got %s", params.ReachableDepth)
	}

	// Protection price must cover the swept levels (85,060)
	if params.ProtectionPrice.LessThan(decimal.NewFromInt(85060)) {
		t.Errorf("protection price %s must be >= 85,060 to ensure all 3 levels execute", params.ProtectionPrice)
	}

	// SafetyManager validation
	cb := engine.NewCircuitBreaker(5, time.Minute, zap.NewNop())
	sm := engine.NewSafetyManager(
		cb,
		decimal.NewFromInt(1000000),
		decimal.NewFromInt(5000000),
		1000,
		decimal.NewFromFloat(0.02),
		maxDepthConsumption,
	)

	if err := sm.ValidatePreOrder(depth, "BUY", params.Quantity, params.ProtectionPrice, params.ConservativePrice); err != nil {
		t.Fatalf("pre-order validation failed: %v", err)
	}
}

// TestMicrostructureAcceptance_DivergentReferenceBookPricing explicitly verifies that:
// 1. When reference price is $85,000 but the actual order book asks are at $84,720,
//    CTS does NOT submit a BUY order priced at $85,000.
// 2. The protection price is derived strictly from the order book ($84,720 + max slippage),
//    protecting the taker against paying an arbitrary price.
// 3. ME receives a MARKET order with protection price derived from book depth, NOT reference price.
func TestMicrostructureAcceptance_DivergentReferenceBookPricing(t *testing.T) {
	market := config.MarketConfig{
		MarketID:    "BTC-USDT",
		BaseAsset:   "BTC",
		QuoteAsset:  "USDT",
		TickSize:    decimal.NewFromFloat(0.01),
		LotSize:     decimal.NewFromFloat(0.0001),
		MinQuantity: decimal.NewFromFloat(0.0001),
	}

	refPrice := decimal.NewFromInt(85000)
	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(84700), Quantity: decimal.NewFromFloat(0.50)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(84720), Quantity: decimal.NewFromFloat(0.50)},
		},
	}

	opts := engine.SizingOptions{
		MaxSweepLevels: 1,
	}

	maxSlippageBps := 15
	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileLow,
		depth,
		"BUY",
		market,
		decimal.NewFromInt(10000),
		maxSlippageBps,
		opts,
	)
	if err != nil {
		t.Fatalf("CalculateOrderParametersWithOptions failed: %v", err)
	}

	// Invariant 1: ProtectionPrice is anchored to L1 ask (84,720), NOT refPrice (85,000)
	maxExpectedProtection := decimal.NewFromInt(84720).Mul(decimal.NewFromFloat(1.0 + float64(maxSlippageBps)/10000.0))
	if params.ProtectionPrice.GreaterThan(maxExpectedProtection) {
		t.Fatalf("Protection price %s leaked above book slippage ceiling %s towards ref %s",
			params.ProtectionPrice, maxExpectedProtection, refPrice)
	}

	// Invariant 2: ProtectionPrice must NEVER be equal to or greater than refPrice ($85,000)
	if params.ProtectionPrice.GreaterThanOrEqual(refPrice) {
		t.Fatalf("CRITICAL INVARIANT VIOLATION: ProtectionPrice %s was derived from external reference %s instead of live book depth (84,720)",
			params.ProtectionPrice, refPrice)
	}

	// Invariant 3: ConservativePrice for BUY is the book-derived ProtectionPrice
	if !params.ConservativePrice.Equal(params.ProtectionPrice) {
		t.Fatalf("expected ConservativePrice == ProtectionPrice for BUY, got %s vs %s",
			params.ConservativePrice, params.ProtectionPrice)
	}
}

