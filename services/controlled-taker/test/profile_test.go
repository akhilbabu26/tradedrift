package test

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
	"tradedrift/services/controlled-taker/internal/config"
	"tradedrift/services/controlled-taker/internal/engine"
)

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
	params1, err := engine.CalculateOrderParametersWithRatio(config.ProfileLow, depth1, "BUY", market, maxNotional, slippageBps, &tenPercent)
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
	params2, err := engine.CalculateOrderParametersWithRatio(config.ProfileLow, depth2, "BUY", market, maxNotional, slippageBps, &tenPercent)
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

			params, err := engine.CalculateOrderParametersWithRatio(profile, depthSnap, "BUY", market, maxNotional, slippageBps, &ratio)
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

	sweepRoll := 0.10 // Deterministic sweep roll (< 0.70)
	// Default behavior: maxLevels is clamped to 3 (L1, L2, L3)
	defaultParams, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileHigh, depth, "BUY", market, maxNotional, slippageBps,
		engine.SizingOptions{SweepRollOverride: &sweepRoll},
	)
	if err != nil {
		t.Fatalf("unexpected error for default HIGH profile: %v", err)
	}
	if defaultParams.ReachableLevels != 3 {
		t.Errorf("expected default to clamp to 3 reachable levels, got %d", defaultParams.ReachableLevels)
	}
	if !defaultParams.PriceCap.Equal(decimal.NewFromFloat(96520.00)) {
		t.Errorf("expected default price cap 96520.00, got %s", defaultParams.PriceCap)
	}

	// Configured behavior: explicit MaxSweepLevels = 4 sweeps all 4 levels
	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileHigh, depth, "BUY", market, maxNotional, slippageBps,
		engine.SizingOptions{SweepRollOverride: &sweepRoll, MaxSweepLevels: 4},
	)
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

func TestDynamicDepthSizing_HighProfileSweepsL1AndSubsequentDepth(t *testing.T) {
	market := sampleMarketConfig()
	maxNotional := decimal.NewFromInt(10000000)
	slippageBps := 50

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85020), Quantity: decimal.NewFromFloat(1.0)},
			{Price: decimal.NewFromInt(85040), Quantity: decimal.NewFromFloat(0.5)},
			{Price: decimal.NewFromInt(85060), Quantity: decimal.NewFromFloat(0.5)},
		},
	}

	sweepRoll := 0.10
	ceiling90 := decimal.NewFromFloat(0.90)
	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileHigh, depth, "BUY", market, maxNotional, slippageBps,
		engine.SizingOptions{
			SweepRollOverride:          &sweepRoll,
			MaxVisibleDepthConsumption: &ceiling90,
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Must fully clear L1 (1.0 BTC) and sweep into subsequent depth
	if !params.Quantity.GreaterThan(decimal.NewFromFloat(1.0)) {
		t.Errorf("expected sweep to consume > 1.0 BTC (clearing L1), got %s", params.Quantity)
	}

	// Must obey the 90% visible depth ceiling (2.0 * 0.90 = 1.80 BTC)
	if params.Quantity.GreaterThan(decimal.NewFromFloat(1.80)) {
		t.Errorf("expected sweep <= 1.80 BTC (90%% ceiling), got %s", params.Quantity)
	}

	// ProtectionPrice and PriceCap must match deepest reachable level
	if !params.ProtectionPrice.Equal(decimal.NewFromInt(85060)) {
		t.Errorf("expected ProtectionPrice=85060, got %s", params.ProtectionPrice)
	}
	if !params.PriceCap.Equal(params.ProtectionPrice) {
		t.Errorf("expected PriceCap to equal ProtectionPrice, got %s vs %s", params.PriceCap, params.ProtectionPrice)
	}
}

// TestNilOrStaleDepth_FailsClosed tests that missing or stale depth fails closed without creating orders.
func TestNilOrStaleDepth_FailsClosed(t *testing.T) {
	market := sampleMarketConfig()
	maxNotional := decimal.NewFromInt(10000)

	// Case 1: nil depth
	_, err := engine.CalculateOrderParameters(config.ProfileLow, nil, "BUY", market, maxNotional, 15)
	if !errors.Is(err, engine.ErrNilDepth) {
		t.Errorf("expected ErrNilDepth, got %v", err)
	}

	// Case 2: stale depth (> 30s old)
	staleDepth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now().Add(-35 * time.Second),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.20)},
		},
	}
	_, err = engine.CalculateOrderParameters(config.ProfileLow, staleDepth, "BUY", market, maxNotional, 15)
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
	_, err = engine.CalculateOrderParameters(config.ProfileLow, zeroTimeDepth, "BUY", market, maxNotional, 15)
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
	_, err = engine.CalculateOrderParameters(config.ProfileLow, futureDepth, "BUY", market, maxNotional, 15)
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
	_, err = engine.CalculateOrderParameters(config.ProfileLow, emptyDepth, "BUY", market, maxNotional, 15)
	if !errors.Is(err, engine.ErrEmptyOppositeSide) {
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
	params, err := engine.CalculateOrderParametersWithRatio(
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

	// Verify that SafetyManager's cumulative depth consumption ceiling requirement PASSES
	cb := engine.NewCircuitBreaker(5, time.Minute, zap.NewNop())
	sm := engine.NewSafetyManager(
		cb,
		decimal.NewFromInt(1000000),
		decimal.NewFromInt(5000000),
		1000,
		decimal.NewFromFloat(0.02),
		decimal.NewFromFloat(0.90),
	)

	if err := sm.ValidatePreOrder(depth, "BUY", params.Quantity, params.PriceCap, params.ConservativePrice); err != nil {
		t.Fatalf("HIGH profile sized quantity %s failed depth safety check: %v", params.Quantity, err)
	}
}

// TestHighProfile_MinQuantityExceedsSafeDepth_FailsClosed proves that if total reachable depth
// cannot safely satisfy even the minimum order quantity within the max depth consumption ceiling
// (totalReachableDepth * maxDepthConsumption < minQuantity), the sizing algorithm fails closed
// with ErrInsufficientLiquidity instead of outputting an unsafe order.
func TestHighProfile_MinQuantityExceedsSafeDepth_FailsClosed(t *testing.T) {
	market := sampleMarketConfig()
	market.MinQuantity = decimal.NewFromFloat(0.001) // 0.001 BTC requires at least 0.001 / 0.90 = 0.001111 BTC depth

	// Book has only 0.0010 BTC (< 0.001111 BTC safe depth)
	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.0010)},
		},
	}

	_, err := engine.CalculateOrderParameters(config.ProfileHigh, depth, "BUY", market, decimal.NewFromInt(10000), 15)
	if err == nil {
		t.Fatalf("expected ErrInsufficientLiquidity when min_quantity exceeds safe depth ceiling, got nil")
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

	_, err := engine.CalculateOrderParameters(config.ProfileHigh, depth, "BUY", market, maxNotional, 15)
	if err == nil {
		t.Fatalf("expected error when max notional caps quantity below min_quantity, got nil")
	}
	if !errors.Is(err, engine.ErrBelowMinQuantity) {
		t.Fatalf("expected ErrBelowMinQuantity, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Phase 1 Sweep & Multi-Level Sizing Test Suite
// ---------------------------------------------------------------------------

// Test 1 — Healthy L1: L1 > target, roll does not trigger sweep -> ExecutionNormal, priceCap = L1
func TestPhase1_HealthyL1_NormalExecution(t *testing.T) {
	market := sampleMarketConfig()
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 15
	sweepRollNo := 0.90 // > 0.20 -> normal

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85000), Quantity: decimal.NewFromFloat(0.85)},
			{Price: decimal.NewFromInt(85050), Quantity: decimal.NewFromFloat(0.85)},
		},
	}

	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileMid, depth, "BUY", market, maxNotional, slippageBps,
		engine.SizingOptions{SweepRollOverride: &sweepRollNo},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Mode != engine.ExecutionNormal {
		t.Errorf("expected ExecutionNormal, got %v", params.Mode)
	}
	if !params.PriceCap.Equal(decimal.NewFromInt(85000)) {
		t.Errorf("expected priceCap = 85000 (L1), got %s", params.PriceCap)
	}
	if params.ReachableLevels != 1 {
		t.Errorf("expected ReachableLevels = 1, got %d", params.ReachableLevels)
	}
}

// Test 2 — L1 Insufficient: L1 depth is small relative to target -> Auto-triggers ExecutionSweep, priceCap = L2
func TestPhase1_L1Insufficient_TriggersSweep(t *testing.T) {
	market := sampleMarketConfig()
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 15
	sweepRollNo := 0.90 // Even if roll is high, L1 is insufficient -> triggers sweep

	// L1 has only 0.0001 BTC (dust), L2 has healthy 0.85 BTC
	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85000), Quantity: decimal.NewFromFloat(0.0001)},
			{Price: decimal.NewFromInt(85050), Quantity: decimal.NewFromFloat(0.85)},
		},
	}

	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileMid, depth, "BUY", market, maxNotional, slippageBps,
		engine.SizingOptions{SweepRollOverride: &sweepRollNo},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Mode != engine.ExecutionSweep {
		t.Errorf("expected ExecutionSweep, got %v", params.Mode)
	}
	if !params.PriceCap.Equal(decimal.NewFromInt(85050)) {
		t.Errorf("expected priceCap = 85050 (L2), got %s", params.PriceCap)
	}
	if params.ReachableLevels != 2 {
		t.Errorf("expected ReachableLevels = 2, got %d", params.ReachableLevels)
	}
}

// Test 3 — MID Probability Triggers Sweep
func TestPhase1_MidProbability_TriggersSweep(t *testing.T) {
	market := sampleMarketConfig()
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 15
	sweepRollYes := 0.10 // < 0.20 -> triggers sweep

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85000), Quantity: decimal.NewFromFloat(0.85)},
			{Price: decimal.NewFromInt(85050), Quantity: decimal.NewFromFloat(0.85)},
		},
	}

	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileMid, depth, "BUY", market, maxNotional, slippageBps,
		engine.SizingOptions{SweepRollOverride: &sweepRollYes},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Mode != engine.ExecutionSweep {
		t.Errorf("expected ExecutionSweep, got %v", params.Mode)
	}
	if !params.PriceCap.Equal(decimal.NewFromInt(85050)) {
		t.Errorf("expected priceCap = 85050 (L2), got %s", params.PriceCap)
	}
	if params.ReachableLevels != 2 {
		t.Errorf("expected ReachableLevels = 2, got %d", params.ReachableLevels)
	}
}

// Test 4 — MID Probability Does Not Trigger Sweep
func TestPhase1_MidProbability_DoesNotTriggerSweep(t *testing.T) {
	market := sampleMarketConfig()
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 15
	sweepRollNo := 0.50 // > 0.20 -> no sweep

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85000), Quantity: decimal.NewFromFloat(0.85)},
			{Price: decimal.NewFromInt(85050), Quantity: decimal.NewFromFloat(0.85)},
		},
	}

	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileMid, depth, "BUY", market, maxNotional, slippageBps,
		engine.SizingOptions{SweepRollOverride: &sweepRollNo},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Mode != engine.ExecutionNormal {
		t.Errorf("expected ExecutionNormal, got %v", params.Mode)
	}
	if !params.PriceCap.Equal(decimal.NewFromInt(85000)) {
		t.Errorf("expected priceCap = 85000 (L1), got %s", params.PriceCap)
	}
}

// Test 5 — LOW Profile: Regardless of sweep roll, LOW is always normal heartbeat
func TestPhase1_LowProfile_AlwaysNormal(t *testing.T) {
	market := sampleMarketConfig()
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 15
	sweepRollYes := 0.01 // very low roll

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85000), Quantity: decimal.NewFromFloat(0.85)},
			{Price: decimal.NewFromInt(85050), Quantity: decimal.NewFromFloat(0.85)},
		},
	}

	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileLow, depth, "BUY", market, maxNotional, slippageBps,
		engine.SizingOptions{SweepRollOverride: &sweepRollYes},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Mode != engine.ExecutionNormal {
		t.Errorf("expected LOW to be ExecutionNormal, got %v", params.Mode)
	}
	if !params.PriceCap.Equal(decimal.NewFromInt(85000)) {
		t.Errorf("expected priceCap = 85000 (L1), got %s", params.PriceCap)
	}
	if params.ReachableLevels != 1 {
		t.Errorf("expected ReachableLevels = 1, got %d", params.ReachableLevels)
	}
}

// Test 6 — HIGH Profile: Force sweep probability -> multi-level sweep
func TestPhase1_HighProfile_MultiLevelSweep(t *testing.T) {
	market := sampleMarketConfig()
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 20
	sweepRollYes := 0.20 // < 0.70 -> triggers sweep

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85000), Quantity: decimal.NewFromFloat(0.20)},
			{Price: decimal.NewFromInt(85050), Quantity: decimal.NewFromFloat(0.30)},
			{Price: decimal.NewFromInt(85100), Quantity: decimal.NewFromFloat(0.50)},
		},
	}

	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileHigh, depth, "BUY", market, maxNotional, slippageBps,
		engine.SizingOptions{SweepRollOverride: &sweepRollYes},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Mode != engine.ExecutionSweep {
		t.Errorf("expected ExecutionSweep, got %v", params.Mode)
	}
	if !params.PriceCap.Equal(decimal.NewFromInt(85100)) {
		t.Errorf("expected priceCap = 85100 (L3), got %s", params.PriceCap)
	}
	if params.ReachableLevels != 3 {
		t.Errorf("expected ReachableLevels = 3, got %d", params.ReachableLevels)
	}
}

// Test 7 — Insufficient Total Depth: fails closed with ErrInsufficientLiquidity
func TestPhase1_InsufficientTotalDepth_FailsClosed(t *testing.T) {
	market := sampleMarketConfig()
	market.MinQuantity = decimal.NewFromFloat(1.0) // 1.0 BTC min requires 1.5 BTC total depth
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 15
	sweepRollYes := 0.10

	// Total reachable is only 0.50 BTC < 1.5 BTC
	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85000), Quantity: decimal.NewFromFloat(0.20)},
			{Price: decimal.NewFromInt(85050), Quantity: decimal.NewFromFloat(0.30)},
		},
	}

	_, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileMid, depth, "BUY", market, maxNotional, slippageBps,
		engine.SizingOptions{SweepRollOverride: &sweepRollYes},
	)
	if err == nil {
		t.Fatalf("expected error when total reachable depth is insufficient, got nil")
	}
}

// Test 8 — Maximum Slippage: L3 is beyond permitted slippage -> bounded to L2
func TestPhase1_MaximumSlippage_BoundsSweepLevel(t *testing.T) {
	market := sampleMarketConfig()
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 10 // 10 bps on 85,000 is 85 USDT (max allowed price is 85,085)
	sweepRollYes := 0.10

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85000), Quantity: decimal.NewFromFloat(0.20)},
			{Price: decimal.NewFromInt(85050), Quantity: decimal.NewFromFloat(0.30)}, // 85050 <= 85085 (in bounds)
			{Price: decimal.NewFromInt(85200), Quantity: decimal.NewFromFloat(0.50)}, // 85200 > 85085 (out of bounds)
		},
	}

	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileHigh, depth, "BUY", market, maxNotional, slippageBps,
		engine.SizingOptions{SweepRollOverride: &sweepRollYes},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !params.PriceCap.Equal(decimal.NewFromInt(85050)) {
		t.Errorf("expected priceCap capped at L2 (85050) due to slippage, got %s", params.PriceCap)
	}
	if params.ReachableLevels != 2 {
		t.Errorf("expected ReachableLevels = 2, got %d", params.ReachableLevels)
	}
}

// Test 9 — Dust: L1 < 1.5x MinQuantity, L2 is healthy -> automatically triggers dust sweep
func TestPhase1_DustSweep_SOLScenario(t *testing.T) {
	solMarket := config.MarketConfig{
		MarketID:    "SOL-USDT",
		TickSize:    decimal.NewFromFloat(0.01),
		LotSize:     decimal.NewFromFloat(0.01),
		MinQuantity: decimal.NewFromFloat(0.01), // 0.01 SOL min
	}
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 15
	sweepRollNo := 0.99 // roll is high, but L1 is dust (0.0100 < 0.0150)

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "SOL-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromFloat(119.29), Quantity: decimal.NewFromFloat(0.01)}, // Dust on L1
			{Price: decimal.NewFromFloat(119.20), Quantity: decimal.NewFromFloat(20.0)}, // L2 healthy
		},
	}

	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileMid, depth, "SELL", solMarket, maxNotional, slippageBps,
		engine.SizingOptions{SweepRollOverride: &sweepRollNo},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Mode != engine.ExecutionSweep {
		t.Errorf("expected dust to trigger ExecutionSweep, got %v", params.Mode)
	}
	if !params.PriceCap.Equal(decimal.NewFromFloat(119.20)) {
		t.Errorf("expected priceCap = 119.20 (L2 Bid), got %s", params.PriceCap)
	}
	// Quantity should be >= MinQuantity and satisfy 1.5x depth guard
	if params.Quantity.LessThan(solMarket.MinQuantity) {
		t.Errorf("expected quantity >= min_quantity %s, got %s", solMarket.MinQuantity, params.Quantity)
	}
}

// Test 10 — Empty L2/L3: Single level depth only -> safely executes on L1 without panic
func TestPhase1_SingleLevelOnly_NoPanic(t *testing.T) {
	market := sampleMarketConfig()
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 15
	sweepRollYes := 0.05

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85000), Quantity: decimal.NewFromFloat(0.50)},
		},
	}

	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileMid, depth, "BUY", market, maxNotional, slippageBps,
		engine.SizingOptions{SweepRollOverride: &sweepRollYes},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Mode != engine.ExecutionNormal {
		t.Errorf("expected ExecutionNormal when only L1 exists, got %v", params.Mode)
	}
	if !params.PriceCap.Equal(decimal.NewFromInt(85000)) {
		t.Errorf("expected priceCap = 85000, got %s", params.PriceCap)
	}
}

// ---------------------------------------------------------------------------
// Phase 2 Dust Handling Edge Case Test Suite
// ---------------------------------------------------------------------------

// TestPhase2_MultiDustLevels: L1 and L2 are both tiny dust, sweeps across both into L3
func TestPhase2_MultiDustLevels(t *testing.T) {
	market := sampleMarketConfig()
	market.MinQuantity = decimal.NewFromFloat(0.001) // 0.001 BTC min requires 0.0015 BTC total depth
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 25
	sweepRollNo := 0.99 // roll is normal, but dust forces sweep

	// L1 = 0.0004 BTC (dust), L2 = 0.0005 BTC (still dust, cumulative 0.0009 < 0.0015), L3 = 0.50 BTC (healthy)
	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85000), Quantity: decimal.NewFromFloat(0.0004)},
			{Price: decimal.NewFromInt(85010), Quantity: decimal.NewFromFloat(0.0005)},
			{Price: decimal.NewFromInt(85020), Quantity: decimal.NewFromFloat(0.5000)},
		},
	}

	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileLow, depth, "BUY", market, maxNotional, slippageBps,
		engine.SizingOptions{SweepRollOverride: &sweepRollNo},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Mode != engine.ExecutionSweep {
		t.Errorf("expected multi-dust to trigger ExecutionSweep, got %v", params.Mode)
	}
	if params.ReachableLevels != 3 {
		t.Errorf("expected ReachableLevels = 3 (reached L3 to satisfy 1.5x depth), got %d", params.ReachableLevels)
	}
	if !params.PriceCap.Equal(decimal.NewFromInt(85020)) {
		t.Errorf("expected priceCap = 85020 (L3), got %s", params.PriceCap)
	}
}

// TestPhase2_SubMinQuantityResidual: L1 is smaller than minQuantity (maker partial fill residual)
func TestPhase2_SubMinQuantityResidual(t *testing.T) {
	market := sampleMarketConfig()
	market.MinQuantity = decimal.NewFromFloat(0.0001)
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 15
	sweepRollNo := 0.99

	// Maker order had residual of 0.00003 BTC (< min_quantity 0.0001)
	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85000), Quantity: decimal.NewFromFloat(0.00003)},
			{Price: decimal.NewFromInt(85050), Quantity: decimal.NewFromFloat(0.50)},
		},
	}

	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileLow, depth, "BUY", market, maxNotional, slippageBps,
		engine.SizingOptions{SweepRollOverride: &sweepRollNo},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Mode != engine.ExecutionSweep {
		t.Errorf("expected sub-minQuantity residual to trigger ExecutionSweep, got %v", params.Mode)
	}
	if params.Quantity.LessThan(market.MinQuantity) {
		t.Errorf("expected order quantity >= min_quantity %s, got %s", market.MinQuantity, params.Quantity)
	}
	if !params.PriceCap.Equal(decimal.NewFromInt(85050)) {
		t.Errorf("expected priceCap = 85050 (L2), got %s", params.PriceCap)
	}
}

// TestPhase2_Dust_NoDeeperLiquidity_FailsClosed: L1 is dust, but no deeper levels exist -> fails closed safely
func TestPhase2_Dust_NoDeeperLiquidity_FailsClosed(t *testing.T) {
	market := sampleMarketConfig()
	market.MinQuantity = decimal.NewFromFloat(0.001)
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 15

	// Only 1 level exists and it has only 0.0002 BTC (< 1.5 * 0.001 = 0.0015)
	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85000), Quantity: decimal.NewFromFloat(0.0002)},
		},
	}

	_, err := engine.CalculateOrderParameters(config.ProfileLow, depth, "BUY", market, maxNotional, slippageBps)
	if err == nil {
		t.Fatalf("expected error when dust has no deeper liquidity to satisfy safety guard, got nil")
	}
}

// TestPhase2_Dust_DeeperLiquidityBeyondSlippage_FailsClosed: deeper liquidity exists but exceeds slippage
func TestPhase2_Dust_DeeperLiquidityBeyondSlippage_FailsClosed(t *testing.T) {
	market := sampleMarketConfig()
	market.MinQuantity = decimal.NewFromFloat(0.001)
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 5 // Very tight 5 bps (~$42 on 85,000)

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85000), Quantity: decimal.NewFromFloat(0.0002)}, // dust
			{Price: decimal.NewFromInt(85200), Quantity: decimal.NewFromFloat(1.0000)}, // 200 USDT away > 42 USDT limit
		},
	}

	_, err := engine.CalculateOrderParameters(config.ProfileLow, depth, "BUY", market, maxNotional, slippageBps)
	if err == nil {
		t.Fatalf("expected error when deeper liquidity is beyond slippage limit, got nil")
	}
}

// TestPhase1_HighProfile_RollBelowProbability_Sweeps proves that when sweepRoll < HighSweepProbability,
// HIGH profile triggers a multi-level sweep.
func TestPhase1_HighProfile_RollBelowProbability_Sweeps(t *testing.T) {
	market := sampleMarketConfig()
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 50
	sweepRoll := 0.65 // < 0.70 default threshold

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85000), Quantity: decimal.NewFromFloat(0.50)},
			{Price: decimal.NewFromInt(85050), Quantity: decimal.NewFromFloat(0.50)},
			{Price: decimal.NewFromInt(85100), Quantity: decimal.NewFromFloat(0.50)},
		},
	}

	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileHigh, depth, "BUY", market, maxNotional, slippageBps,
		engine.SizingOptions{SweepRollOverride: &sweepRoll, HighSweepProbability: 0.70},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Mode != engine.ExecutionSweep {
		t.Errorf("expected ExecutionSweep when roll %f < 0.70, got %v", sweepRoll, params.Mode)
	}
	if params.ReachableLevels < 2 {
		t.Errorf("expected ReachableLevels >= 2, got %d", params.ReachableLevels)
	}
}

// TestPhase1_HighProfile_RollAboveProbability_NormalExecution proves that when sweepRoll >= HighSweepProbability,
// HIGH profile stays in normal L1 execution mode.
func TestPhase1_HighProfile_RollAboveProbability_NormalExecution(t *testing.T) {
	market := sampleMarketConfig()
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 50
	sweepRoll := 0.75 // >= 0.70 default threshold

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85000), Quantity: decimal.NewFromFloat(0.50)},
			{Price: decimal.NewFromInt(85050), Quantity: decimal.NewFromFloat(0.50)},
			{Price: decimal.NewFromInt(85100), Quantity: decimal.NewFromFloat(0.50)},
		},
	}

	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileHigh, depth, "BUY", market, maxNotional, slippageBps,
		engine.SizingOptions{SweepRollOverride: &sweepRoll, HighSweepProbability: 0.70},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Mode != engine.ExecutionNormal {
		t.Errorf("expected ExecutionNormal when roll %f >= 0.70, got %v", sweepRoll, params.Mode)
	}
	if params.ReachableLevels != 1 {
		t.Errorf("expected ReachableLevels == 1 for normal execution, got %d", params.ReachableLevels)
	}
	if !params.PriceCap.Equal(decimal.NewFromInt(85000)) {
		t.Errorf("expected priceCap to be L1 (85000), got %s", params.PriceCap)
	}
}

// TestPhase1_HighProfile_ZeroProbability_NeverSweeps proves that when HighSweepProbability is explicitly 0.0,
// sweeps are never triggered even if sweepRoll is 0.01 and multiple depth levels are available.
func TestPhase1_HighProfile_ZeroProbability_NeverSweeps(t *testing.T) {
	market := sampleMarketConfig()
	maxNotional := decimal.NewFromInt(1000000)
	slippageBps := 50
	sweepRoll := 0.01 // very low roll, would sweep under default 0.70

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(85000), Quantity: decimal.NewFromFloat(0.50)},
			{Price: decimal.NewFromInt(85050), Quantity: decimal.NewFromFloat(0.50)},
			{Price: decimal.NewFromInt(85100), Quantity: decimal.NewFromFloat(0.50)},
		},
	}

	params, err := engine.CalculateOrderParametersWithOptions(
		config.ProfileHigh, depth, "BUY", market, maxNotional, slippageBps,
		engine.SizingOptions{
			SweepRollOverride:       &sweepRoll,
			HighSweepProbability:    0.0,
			HasHighSweepProbability: true,
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params.Mode != engine.ExecutionNormal {
		t.Errorf("expected ExecutionNormal when HighSweepProbability=0, got %v", params.Mode)
	}
	if params.ReachableLevels != 1 {
		t.Errorf("expected ReachableLevels == 1 for normal execution, got %d", params.ReachableLevels)
	}
	if !params.PriceCap.Equal(decimal.NewFromInt(85000)) {
		t.Errorf("expected priceCap to be L1 (85000), got %s", params.PriceCap)
	}
}


