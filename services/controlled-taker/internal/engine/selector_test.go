package engine

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestDirectionSelector_InventoryBias(t *testing.T) {
	selector := NewDirectionSelector()
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
	selector := NewDirectionSelector()
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
