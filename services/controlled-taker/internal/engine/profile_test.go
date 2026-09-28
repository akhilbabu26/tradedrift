package engine

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
	"tradedrift/services/controlled-taker/internal/config"
)

func sampleMarketConfig() config.MarketConfig {
	return config.MarketConfig{
		MarketID:    "BTC-USDT",
		BaseAsset:   "BTC",
		QuoteAsset:  "USDT",
		TickSize:    decimal.NewFromFloat(0.01),
		LotSize:     decimal.NewFromFloat(0.0001),
		MinQuantity: decimal.NewFromFloat(0.0001),
		Partition:   0,
	}
}

// TestDynamicDepthSizing_ExactRatioRequirement tests the user-specified invariant:
// L1 = 0.20 BTC -> 10% -> 0.02 BTC
// L1 = 0.50 BTC -> 10% -> 0.05 BTC
// Proves that the order size is strictly derived from live depth and not a fixed constant.
func TestDynamicDepthSizing_ExactRatioRequirement(t *testing.T) {
	market := sampleMarketConfig()
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 15
	tenPercent := decimal.NewFromFloat(0.10)

	// Case 1: L1 = 0.20 BTC -> 10% -> 0.02 BTC
	depth1 := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.20)},
		},
	}
	params1, err := CalculateOrderParametersWithRatio(config.ProfileLow, depth1, "BUY", market, maxNotional, slippageBps, &tenPercent)
	if err != nil {
		t.Fatalf("unexpected error for depth1: %v", err)
	}
	expected1 := decimal.NewFromFloat(0.02)
	if !params1.Quantity.Equal(expected1) {
		t.Errorf("expected L1=0.20 BTC @ 10%% to yield %s, got %s", expected1, params1.Quantity)
	}

	// Case 2: L1 = 0.50 BTC -> 10% -> 0.05 BTC
	depth2 := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.50)},
		},
	}
	params2, err := CalculateOrderParametersWithRatio(config.ProfileLow, depth2, "BUY", market, maxNotional, slippageBps, &tenPercent)
	if err != nil {
		t.Fatalf("unexpected error for depth2: %v", err)
	}
	expected2 := decimal.NewFromFloat(0.05)
	if !params2.Quantity.Equal(expected2) {
		t.Errorf("expected L1=0.50 BTC @ 10%% to yield %s, got %s", expected2, params2.Quantity)
	}
}

// TestDynamicDepthSizing_SameProfileDifferentDepthDifferentQuantity tests the core invariant:
// Same profile + different live depth = different order quantity.
func TestDynamicDepthSizing_SameProfileDifferentDepthDifferentQuantity(t *testing.T) {
	market := sampleMarketConfig()
	// High maxNotional so sizing is purely governed by live book depth
	maxNotional := decimal.NewFromInt(10000000)
	slippageBps := 15

	depthValues := []float64{0.10, 0.50, 1.20, 3.00, 8.50}

	profileRatios := map[config.ProfileType]decimal.Decimal{
		config.ProfileLow:  decimal.NewFromFloat(0.10),
		config.ProfileMid:  decimal.NewFromFloat(0.35),
		config.ProfileHigh: decimal.NewFromFloat(0.50),
	}

	for _, profile := range []config.ProfileType{config.ProfileLow, config.ProfileMid, config.ProfileHigh} {
		var prevQty decimal.Decimal
		ratio := profileRatios[profile]
		for _, d := range depthValues {
			depthSnap := &redisdepth.DepthSnapshot{
				MarketID:   "BTC-USDT",
				SnapshotAt: time.Now(),
				Asks: []redisdepth.DepthLevel{
					{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(d)},
				},
			}

			params, err := CalculateOrderParametersWithRatio(profile, depthSnap, "BUY", market, maxNotional, slippageBps, &ratio)
			if err != nil {
				t.Fatalf("profile %s: unexpected error for depth %f: %v", profile, d, err)
			}

			// Invariant: Quantity must strictly scale with increasing depth
			if prevQty.GreaterThan(decimal.Zero) {
				if params.Quantity.Equal(prevQty) {
					t.Fatalf("profile %s: order quantity %s was identical for different depths (violates dynamic sizing invariant)",
						profile, params.Quantity)
				}
				if !params.Quantity.GreaterThan(prevQty) {
					t.Fatalf("profile %s: order quantity %s did not increase when depth increased from prior to %f",
						profile, params.Quantity, d)
				}
			}
			prevQty = params.Quantity
		}
	}
}

// TestDynamicDepthSizing_HighProfileCumulativeSweep tests multi-level book sweeping.
func TestDynamicDepthSizing_HighProfileCumulativeSweep(t *testing.T) {
	market := sampleMarketConfig()
	maxNotional := decimal.NewFromInt(500000)
	slippageBps := 20 // 0.20% allowed

	// Book with 4 discrete levels:
	// L1: 96,500 @ 0.05 BTC
	// L2: 96,510 @ 0.05 BTC
	// L3: 96,520 @ 0.05 BTC
	// L4: 96,530 @ 0.05 BTC (all within 0.20% = ~$193 slippage)
	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromFloat(96500.00), Quantity: decimal.NewFromFloat(0.05)},
			{Price: decimal.NewFromFloat(96510.00), Quantity: decimal.NewFromFloat(0.05)},
			{Price: decimal.NewFromFloat(96520.00), Quantity: decimal.NewFromFloat(0.05)},
			{Price: decimal.NewFromFloat(96530.00), Quantity: decimal.NewFromFloat(0.05)},
		},
	}

	params, err := CalculateOrderParameters(config.ProfileHigh, depth, "BUY", market, maxNotional, slippageBps)
	if err != nil {
		t.Fatalf("unexpected error for HIGH profile: %v", err)
	}

	// Must sweep beyond Level 1 (0.05 BTC)
	if !params.Quantity.GreaterThan(decimal.NewFromFloat(0.05)) {
		t.Errorf("expected HIGH profile to sweep > 0.05 BTC, got %s", params.Quantity)
	}

	// Must not exceed cumulative reachable depth of 0.20 BTC
	if params.Quantity.GreaterThan(decimal.NewFromFloat(0.20)) {
		t.Errorf("expected HIGH profile <= 0.20 BTC, got %s", params.Quantity)
	}

	// Reachable levels must be 4
	if params.ReachableLevels != 4 {
		t.Errorf("expected 4 reachable levels, got %d", params.ReachableLevels)
	}

	// Price cap must reach deepest reachable level
	if !params.PriceCap.Equal(decimal.NewFromFloat(96530.00)) {
		t.Errorf("expected price cap 96530.00, got %s", params.PriceCap)
	}
}

// TestNilOrStaleDepth_FailsClosed tests that missing or stale depth fails closed without creating orders.
func TestNilOrStaleDepth_FailsClosed(t *testing.T) {
	market := sampleMarketConfig()
	maxNotional := decimal.NewFromInt(10000)

	// Case 1: nil depth
	_, err := CalculateOrderParameters(config.ProfileLow, nil, "BUY", market, maxNotional, 15)
	if err != ErrNilDepth {
		t.Errorf("expected ErrNilDepth, got %v", err)
	}

	// Case 2: stale depth (> 5s old)
	staleDepth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now().Add(-10 * time.Second),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.20)},
		},
	}
	_, err = CalculateOrderParameters(config.ProfileLow, staleDepth, "BUY", market, maxNotional, 15)
	if err == nil {
		t.Error("expected error for stale depth snapshot, got nil")
	}

	// Case 2b: zero timestamp (fail closed)
	zeroTimeDepth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Time{},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.20)},
		},
	}
	_, err = CalculateOrderParameters(config.ProfileLow, zeroTimeDepth, "BUY", market, maxNotional, 15)
	if err == nil {
		t.Error("expected error for zero SnapshotAt timestamp, got nil")
	}

	// Case 2c: future timestamp (fail closed)
	futureDepth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now().Add(5 * time.Minute),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.20)},
		},
	}
	_, err = CalculateOrderParameters(config.ProfileLow, futureDepth, "BUY", market, maxNotional, 15)
	if err == nil {
		t.Error("expected error for future SnapshotAt timestamp, got nil")
	}

	// Case 3: empty opposite side
	emptyDepth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks:       nil,
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96490), Quantity: decimal.NewFromFloat(0.20)},
		},
	}
	_, err = CalculateOrderParameters(config.ProfileLow, emptyDepth, "BUY", market, maxNotional, 15)
	if err != ErrEmptyOppositeSide {
		t.Errorf("expected ErrEmptyOppositeSide, got %v", err)
	}
}

// TestHighProfile_SafetyGuardAlignment proves that the HIGH profile sizing adaptively
// clamps desired volume so it NEVER conflicts with the SafetyManager's 1.5x depth guard.
func TestHighProfile_SafetyGuardAlignment(t *testing.T) {
	market := sampleMarketConfig()
	maxNotional := decimal.NewFromInt(10000000)
	slippageBps := 50 // 0.50%

	// 2 levels of 0.10 BTC each -> Total reachable = 0.20 BTC
	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96490), Quantity: decimal.NewFromFloat(0.20)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.10)},
			{Price: decimal.NewFromInt(96510), Quantity: decimal.NewFromFloat(0.10)},
		},
	}

	// Sweep ratio 80% (which without clamp would ask for 0.10 + 0.08 = 0.18 BTC, requiring 0.27 BTC > 0.20 BTC)
	eightyPercent := decimal.NewFromFloat(0.80)
	params, err := CalculateOrderParametersWithRatio(
		config.ProfileHigh,
		depth,
		"BUY",
		market,
		maxNotional,
		slippageBps,
		&eightyPercent,
	)
	if err != nil {
		t.Fatalf("unexpected sizing error: %v", err)
	}

	// Verify that SafetyManager's 1.5x cumulative depth requirement PASSES
	cb := NewCircuitBreaker(5, time.Minute, zap.NewNop())
	sm := NewSafetyManager(
		cb,
		decimal.NewFromInt(1000000),
		decimal.NewFromInt(5000000),
		1000,
		decimal.NewFromFloat(0.02),
	)

	if err := sm.ValidatePreOrder(depth, "BUY", params.Quantity, params.PriceCap); err != nil {
		t.Fatalf("HIGH profile sized quantity %s failed 1.5x depth safety check: %v", params.Quantity, err)
	}
}

// TestHighProfile_MinQuantityExceedsSafeDepth_FailsClosed proves that if total reachable depth
// cannot safely satisfy even the minimum order quantity (totalReachableDepth < minQuantity * 1.5),
// the sizing algorithm fails closed with ErrInsufficientLiquidity instead of outputting an unsafe order.
func TestHighProfile_MinQuantityExceedsSafeDepth_FailsClosed(t *testing.T) {
	market := sampleMarketConfig()
	market.MinQuantity = decimal.NewFromFloat(0.001) // 0.001 BTC requires 0.0015 BTC depth

	// Book has only 0.0012 BTC (< 0.0015 BTC)
	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.0012)},
		},
	}

	_, err := CalculateOrderParameters(config.ProfileHigh, depth, "BUY", market, decimal.NewFromInt(10000), 15)
	if err == nil {
		t.Fatalf("expected ErrInsufficientLiquidity when min_quantity * 1.5 exceeds depth, got nil")
	}
}

// TestHighProfile_MaxNotionalCap_BelowMinQuantity_FailsClosed verifies that when a HIGH sweep
// has abundant depth but max_order_notional capping forces the lot-aligned quantity below
// market.MinQuantity, sizing fails closed cleanly with ErrBelowMinQuantity.
func TestHighProfile_MaxNotionalCap_BelowMinQuantity_FailsClosed(t *testing.T) {
	market := config.MarketConfig{
		MarketID:    "BTC-USDT",
		TickSize:    decimal.NewFromFloat(0.01),
		LotSize:     decimal.NewFromFloat(0.0001),
		MinQuantity: decimal.NewFromFloat(0.001), // Min quantity 0.001 BTC
	}

	// Abundant depth: 2.0 BTC across asks at ~100,000 USDT
	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(99990), Quantity: decimal.NewFromFloat(1.0)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(100000), Quantity: decimal.NewFromFloat(1.0)},
			{Price: decimal.NewFromInt(100010), Quantity: decimal.NewFromFloat(1.0)},
		},
	}

	// Max notional is capped at only $50 USDT.
	// At price $100,010, allowedQty = $50 / 100,010 ~= 0.000499 BTC, which is < MinQuantity (0.001 BTC).
	maxNotional := decimal.NewFromInt(50)

	_, err := CalculateOrderParameters(config.ProfileHigh, depth, "BUY", market, maxNotional, 15)
	if err == nil {
		t.Fatalf("expected error when max notional caps quantity below min_quantity, got nil")
	}
	if !errors.Is(err, ErrBelowMinQuantity) {
		t.Fatalf("expected ErrBelowMinQuantity, got: %v", err)
	}
}
