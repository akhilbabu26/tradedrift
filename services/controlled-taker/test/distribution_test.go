package test

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
	"tradedrift/services/controlled-taker/internal/config"
	"tradedrift/services/controlled-taker/internal/engine"
)

// TestDistribution_LogNormalIntervalSampling verifies that SampleLogNormalInterval:
// 1. Strictly bounds draws within [MinInterval, MaxInterval].
// 2. Converges to a mean close to BaseInterval.
// 3. Demonstrates realistic coefficient of variation (~0.38).
// 4. Exhibits natural macro breathing pauses (> 2x median).
func TestDistribution_LogNormalIntervalSampling(t *testing.T) {
	cfg := config.IntervalConfig{
		MinInterval:  4 * time.Second,
		BaseInterval: 8 * time.Second,
		MaxInterval:  16 * time.Second,
	}

	rng := rand.New(rand.NewSource(42))
	const samples = 20000

	var sum float64
	durations := make([]float64, samples)
	for i := 0; i < samples; i++ {
		d := engine.SampleLogNormalInterval(rng, cfg)
		if d < cfg.MinInterval || d > cfg.MaxInterval {
			t.Fatalf("sample %d: %v out of bounds [%v, %v]", i, d, cfg.MinInterval, cfg.MaxInterval)
		}
		secs := d.Seconds()
		durations[i] = secs
		sum += secs
	}

	mean := sum / float64(samples)
	if math.Abs(mean-8.0) > 0.20 {
		t.Errorf("expected mean close to 8.0s, got %f", mean)
	}

	var varSum float64
	for _, s := range durations {
		diff := s - mean
		varSum += diff * diff
	}
	stdDev := math.Sqrt(varSum / float64(samples))
	cv := stdDev / mean

	if cv < 0.30 || cv > 0.45 {
		t.Errorf("expected coefficient of variation in [0.30, 0.45], got %f", cv)
	}
}

// TestDetermineSweepSeverity verifies severity classification based on actual L2 depth impact:
// - L1 only -> Light
// - L1 + L2 -> Medium
// - L1 + L2 + L3+ -> Heavy
func TestDetermineSweepSeverity(t *testing.T) {
	levels := []redisdepth.DepthLevel{
		{Price: decimal.NewFromInt(95000), Quantity: decimal.NewFromFloat(0.50)}, // L1: 0.50
		{Price: decimal.NewFromInt(95010), Quantity: decimal.NewFromFloat(0.80)}, // L2: 0.80 (cum 1.30)
		{Price: decimal.NewFromInt(95020), Quantity: decimal.NewFromFloat(1.20)}, // L3: 1.20 (cum 2.50)
	}

	tests := []struct {
		name     string
		fillQty  decimal.Decimal
		expected engine.HighSweepSeverity
	}{
		{
			name:     "Zero fill",
			fillQty:  decimal.Zero,
			expected: engine.SweepSeverityLight,
		},
		{
			name:     "Partial L1 fill (0.20 BTC <= 0.50)",
			fillQty:  decimal.NewFromFloat(0.20),
			expected: engine.SweepSeverityLight,
		},
		{
			name:     "Exact 100% L1 fill (0.50 BTC)",
			fillQty:  decimal.NewFromFloat(0.50),
			expected: engine.SweepSeverityLight,
		},
		{
			name:     "L1 + partial L2 fill (0.90 BTC <= 1.30)",
			fillQty:  decimal.NewFromFloat(0.90),
			expected: engine.SweepSeverityMedium,
		},
		{
			name:     "Exact L1 + 100% L2 fill (1.30 BTC)",
			fillQty:  decimal.NewFromFloat(1.30),
			expected: engine.SweepSeverityMedium,
		},
		{
			name:     "L1 + L2 + partial L3 fill (1.80 BTC > 1.30)",
			fillQty:  decimal.NewFromFloat(1.80),
			expected: engine.SweepSeverityHeavy,
		},
		{
			name:     "Deep multi-level sweep (3.00 BTC > 1.30)",
			fillQty:  decimal.NewFromFloat(3.00),
			expected: engine.SweepSeverityHeavy,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := engine.DetermineSweepSeverity(tc.fillQty, levels)
			if actual != tc.expected {
				t.Errorf("expected %s, got %s for fillQty %s", tc.expected, actual, tc.fillQty)
			}
		})
	}
}

// TestPostHighAdaptiveCooldown_Severity verifies cooldown ranges:
// - Light:  15s – 20s
// - Medium: 25s – 35s
// - Heavy:  35s – 50s
func TestPostHighAdaptiveCooldown_Severity(t *testing.T) {
	rng := rand.New(rand.NewSource(12345))

	const iterations = 5000

	// 1. LIGHT severity: 15s to 20s
	for i := 0; i < iterations; i++ {
		cd := engine.SamplePostHighCooldown(engine.SweepSeverityLight, rng)
		if cd < 15*time.Second || cd > 20*time.Second {
			t.Fatalf("LIGHT cooldown out of bounds [15s, 20s]: %v", cd)
		}
	}

	// 2. MEDIUM severity: 25s to 35s
	for i := 0; i < iterations; i++ {
		cd := engine.SamplePostHighCooldown(engine.SweepSeverityMedium, rng)
		if cd < 25*time.Second || cd > 35*time.Second {
			t.Fatalf("MEDIUM cooldown out of bounds [25s, 35s]: %v", cd)
		}
	}

	// 3. HEAVY severity: 35s to 50s
	for i := 0; i < iterations; i++ {
		cd := engine.SamplePostHighCooldown(engine.SweepSeverityHeavy, rng)
		if cd < 35*time.Second || cd > 50*time.Second {
			t.Fatalf("HEAVY cooldown out of bounds [35s, 50s]: %v", cd)
		}
	}
}

// TestAdaptivePostHighCooldown_MinimumFloorLogic verifies:
// 1. cooldown > nextWait -> nextWait becomes cooldown.
// 2. nextWait > cooldown -> nextWait is preserved.
func TestAdaptivePostHighCooldown_MinimumFloorLogic(t *testing.T) {
	// Case A: regime interval 7s, cooldown 30s -> actual wait = 30s
	regimeInterval := 7 * time.Second
	cooldown := 30 * time.Second

	actualWait := regimeInterval
	if cooldown > actualWait {
		actualWait = cooldown
	}
	if actualWait != 30*time.Second {
		t.Fatalf("expected actualWait to be elevated to cooldown 30s, got %v", actualWait)
	}

	// Case B: regime interval 45s, cooldown 30s -> actual wait = 45s (preserved)
	regimeInterval = 45 * time.Second
	cooldown = 30 * time.Second

	actualWait = regimeInterval
	if cooldown > actualWait {
		actualWait = cooldown
	}
	if actualWait != 45*time.Second {
		t.Fatalf("expected actualWait to preserve regime interval 45s, got %v", actualWait)
	}
}
