package test

import (
	"math/rand"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
	"tradedrift/services/controlled-taker/internal/engine"
)

func TestDirectionSelector_InventoryBias(t *testing.T) {
	selector := engine.NewDirectionSelector()
	marketID := "BTC-USDT"

	// 1. Initial neutral state: roughly balanced (over 1000 trials, BUY should be ~50%)
	buyCount := 0
	trials := 1000
	for i := 0; i < trials; i++ {
		if selector.SelectSide(marketID) == "BUY" {
			buyCount++
		}
	}
	buyRatio := float64(buyCount) / float64(trials)
	if buyRatio < 0.40 || buyRatio > 0.60 {
		t.Errorf("expected neutral buyRatio around 0.50, got %f", buyRatio)
	}

	// 2. Heavy accumulation of base asset (+30,000 USDT notional accumulated)
	// Exceeds heavy threshold (+25,000 USDT) -> Should heavily bias towards SELL (buyProb = 0.25)
	selector.RecordFill(marketID, "BUY", decimal.NewFromInt(30000))
	buyCount = 0
	for i := 0; i < trials; i++ {
		if selector.SelectSide(marketID) == "BUY" {
			buyCount++
		}
	}
	buyRatio = float64(buyCount) / float64(trials)
	if buyRatio > 0.35 {
		t.Errorf("expected heavy SELL bias (buyRatio < 0.35) when accumulated base, got %f", buyRatio)
	}

	// 3. Heavy depletion of base asset (-60,000 USDT) -> net -30,000 USDT
	// Exceeds heavy negative threshold (-25,000 USDT) -> Should heavily bias towards BUY (buyProb = 0.75)
	selector.RecordFill(marketID, "SELL", decimal.NewFromInt(60000))
	buyCount = 0
	for i := 0; i < trials; i++ {
		if selector.SelectSide(marketID) == "BUY" {
			buyCount++
		}
	}
	buyRatio = float64(buyCount) / float64(trials)
	if buyRatio < 0.65 {
		t.Errorf("expected heavy BUY bias (buyRatio > 0.65) when depleted base, got %f", buyRatio)
	}
}

// TestDirectionSelector_ConcurrencyStress verifies that concurrent calls to SelectSide
// and RecordFill across multiple markets and goroutines are race-free and thread-safe.
func TestDirectionSelector_ConcurrencyStress(t *testing.T) {
	selector := engine.NewDirectionSelector()
	markets := []string{"BTC-USDT", "ETH-USDT", "SOL-USDT"}

	const goroutines = 30
	const iterations = 100

	done := make(chan struct{})
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			m := markets[id%len(markets)]
			for i := 0; i < iterations; i++ {
				side := selector.SelectSide(m)
				selector.RecordFill(m, side, decimal.NewFromInt(100))
				_ = selector.GetNetNotionalDelta(m)
			}
		}(g)
	}

	for g := 0; g < goroutines; g++ {
		<-done
	}

	for _, m := range markets {
		delta := selector.GetNetNotionalDelta(m)
		if delta.IsZero() {
			// perfectly balanced or non-zero, but must not panic
		}
	}
}

// TestDirectionSelector_RNG_Determinism verifies that injecting a worker RNG produces:
// 1. Exactly identical sequences for identical seeds.
// 2. Differing sequences across different seeds.
func TestDirectionSelector_RNG_Determinism(t *testing.T) {
	selectorA1 := engine.NewDirectionSelector()
	selectorA2 := engine.NewDirectionSelector()
	selectorB := engine.NewDirectionSelector()
	marketID := "BTC-USDT"

	rngA1 := rand.New(rand.NewSource(42))
	rngA2 := rand.New(rand.NewSource(42))
	rngB := rand.New(rand.NewSource(99999))

	const trials = 50
	seqA1 := make([]string, trials)
	seqA2 := make([]string, trials)
	seqB := make([]string, trials)

	for i := 0; i < trials; i++ {
		seqA1[i] = selectorA1.SelectSide(marketID, rngA1)
		seqA2[i] = selectorA2.SelectSide(marketID, rngA2)
		seqB[i] = selectorB.SelectSide(marketID, rngB)
	}

	// 1. Identical seeds must yield 100% identical sequence
	for i := 0; i < trials; i++ {
		if seqA1[i] != seqA2[i] {
			t.Fatalf("trial %d: expected identical direction with same seed, got seqA1=%s, seqA2=%s", i, seqA1[i], seqA2[i])
		}
	}

	// 2. Different seeds must diverge over 50 trials
	diffCount := 0
	for i := 0; i < trials; i++ {
		if seqA1[i] != seqB[i] {
			diffCount++
		}
	}
	if diffCount == 0 {
		t.Fatalf("expected different RNG seeds to produce differing sequences over %d trials", trials)
	}
}

// TestDirectionSelector_ReferencePriceDoesNotDictatePrice verifies that:
// 1. Reference price has at most a small directional influence (bounded to +/-5%).
// 2. Reference price NEVER dictates an execution price or overrides book conditions.
func TestDirectionSelector_ReferencePriceDoesNotDictatePrice(t *testing.T) {
	selector := engine.NewDirectionSelector()
	marketID := "BTC-USDT"

	midPrice := decimal.NewFromInt(84710)
	refPrice := decimal.NewFromInt(85000) // Ref is significantly above book (+34 bps)

	depth := &redisdepth.DepthSnapshot{
		MarketID:   marketID,
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(84700), Quantity: decimal.NewFromFloat(1.0)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(84720), Quantity: decimal.NewFromFloat(1.0)},
		},
	}

	trials := 2000
	buyCount := 0
	for i := 0; i < trials; i++ {
		rng := rand.New(rand.NewSource(int64(i + 1000)))
		side := selector.SelectSideWithDepth(marketID, midPrice, refPrice, depth, rng)
		if side == "BUY" {
			buyCount++
		}
	}

	buyRatio := float64(buyCount) / float64(trials)
	// Neutral buy probability is 0.50. With refPrice > midPrice by 34 bps (> 15 bps threshold),
	// ref gravity adds exactly +0.05 to buyProb (target: ~0.55).
	// Under NO circumstances should buyRatio be 1.0 (forced execution) or dictate price.
	if buyRatio < 0.48 || buyRatio > 0.62 {
		t.Errorf("expected bounded buyRatio around 0.55 (+/-0.05 influence), got %f", buyRatio)
	}
}

