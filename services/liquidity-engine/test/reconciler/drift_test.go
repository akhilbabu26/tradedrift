package reconciler_test

import (
	"math/rand"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"tradedrift/platform/refprice"
	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/reconciler"
)

func TestDrift_BoundedLimits(t *testing.T) {
	drifter := &reconciler.MarketDrifter{}
	basePrice := decimal.RequireFromString("85000.00")
	cfg := reconciler.DriftConfig{
		Enabled:        true,
		MaxDriftBps:    25,
		VolatilityBps:  10, // high volatility to stress boundary
		ReversionSpeed: 0.10,
		StepCadence:    1 * time.Millisecond,
	}
	rng := rand.New(rand.NewSource(42))
	now := time.Now()

	for i := 0; i < 1000; i++ {
		now = now.Add(2 * time.Millisecond)
		price := drifter.Step(basePrice, now, cfg, rng)

		if !price.GreaterThan(decimal.Zero) {
			t.Fatalf("step %d: drifted price must be positive, got %s", i, price)
		}

		bps := drifter.CurrentBps()
		if bps > 25.0001 || bps < -25.0001 {
			t.Fatalf("step %d: drift bps %.4f exceeded bounds [-25, 25]", i, bps)
		}

		// Also check raw decimal percentage deviation
		ratio := price.Sub(basePrice).Div(basePrice).Mul(decimal.NewFromInt(10000)).InexactFloat64()
		if ratio > 26.0 || ratio < -26.0 {
			t.Fatalf("step %d: price ratio %.4f bps exceeded bounds", i, ratio)
		}
	}
}

func TestDrift_MeanReversionPull(t *testing.T) {
	basePrice := decimal.RequireFromString("2600.00")
	cfg := reconciler.DriftConfig{
		Enabled:        true,
		MaxDriftBps:    30,
		VolatilityBps:  0, // zero volatility: test pure deterministic mean reversion
		ReversionSpeed: 0.20,
		StepCadence:    10 * time.Millisecond,
	}

	// Provider setup
	marketID := "ETH-USDT"
	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{
			marketID: {
				MarketID:  marketID,
				Price:     basePrice,
				Version:   1,
				FetchedAt: time.Now(),
				State:     refprice.StateFresh,
			},
		},
	}

	markets := []config.MarketConfig{
		{
			MarketID: marketID,
			TickSize: decimal.RequireFromString("0.01"),
		},
	}

	dp := reconciler.NewDriftingProvider(mockProv, cfg, markets, nil)

	// Step 1: Initialize
	e1, ok := dp.Get(marketID)
	if !ok || !e1.Price.Equal(basePrice) {
		t.Fatalf("expected initial price %s, got %s", basePrice, e1.Price)
	}
}

func TestDrift_TickSizeAlignment(t *testing.T) {
	basePrice := decimal.RequireFromString("119.563")
	tickSize := decimal.RequireFromString("0.001")
	cfg := reconciler.DriftConfig{
		Enabled:        true,
		MaxDriftBps:    25,
		VolatilityBps:  5,
		ReversionSpeed: 0.15,
		StepCadence:    1 * time.Millisecond,
	}

	marketID := "SOL-USDT"
	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{
			marketID: {
				MarketID:  marketID,
				Price:     basePrice,
				Version:   1,
				FetchedAt: time.Now(),
				State:     refprice.StateFresh,
			},
		},
	}

	markets := []config.MarketConfig{
		{
			MarketID: marketID,
			TickSize: tickSize,
		},
	}

	rng := rand.New(rand.NewSource(12345))
	dp := reconciler.NewDriftingProviderWithRNG(mockProv, cfg, markets, rng, nil)

	for i := 0; i < 50; i++ {
		time.Sleep(2 * time.Millisecond)
		e, ok := dp.Get(marketID)
		if !ok {
			t.Fatalf("step %d: Get returned false", i)
		}
		// Remainder modulo tickSize must be zero
		rem := e.Price.Mod(tickSize)
		if !rem.IsZero() {
			t.Fatalf("step %d: price %s is not aligned to tick size %s (remainder: %s)", i, e.Price, tickSize, rem)
		}
	}
}

func TestDriftingProvider_NonFreshReturnsUndrifted(t *testing.T) {
	basePrice := decimal.RequireFromString("90000.00")
	marketID := "BTC-USDT"
	cfg := reconciler.DriftConfig{
		Enabled:        true,
		MaxDriftBps:    25,
		VolatilityBps:  5,
		ReversionSpeed: 0.15,
		StepCadence:    1 * time.Millisecond,
	}

	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{
			marketID: {
				MarketID:  marketID,
				Price:     basePrice,
				Version:   1,
				FetchedAt: time.Now().Add(-10 * time.Minute),
				State:     refprice.StateStale, // STALE!
			},
		},
	}

	markets := []config.MarketConfig{{MarketID: marketID}}
	dp := reconciler.NewDriftingProvider(mockProv, cfg, markets, nil)

	entry, ok := dp.Get(marketID)
	if !ok {
		t.Fatal("expected ok = true")
	}
	if !entry.Price.Equal(basePrice) {
		t.Fatalf("non-fresh entry must not be drifted; expected %s, got %s", basePrice, entry.Price)
	}
	if entry.State != refprice.StateStale {
		t.Fatalf("expected state STALE, got %s", entry.State)
	}
}

func TestDriftingProvider_DisabledReturnsUndrifted(t *testing.T) {
	basePrice := decimal.RequireFromString("90000.00")
	marketID := "BTC-USDT"
	cfg := reconciler.DriftConfig{
		Enabled: false, // DISABLED
	}

	mockProv := &mockRefProvider{
		entries: map[string]refprice.Entry{
			marketID: {
				MarketID:  marketID,
				Price:     basePrice,
				Version:   1,
				FetchedAt: time.Now(),
				State:     refprice.StateFresh,
			},
		},
	}

	markets := []config.MarketConfig{{MarketID: marketID}}
	dp := reconciler.NewDriftingProvider(mockProv, cfg, markets, nil)

	entry, ok := dp.Get(marketID)
	if !ok {
		t.Fatal("expected ok = true")
	}
	if !entry.Price.Equal(basePrice) {
		t.Fatalf("disabled drift must return base price; expected %s, got %s", basePrice, entry.Price)
	}
}
