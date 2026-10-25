package postgres_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	postgresrepo "tradedrift/services/market/internal/repository/postgres"
)

func TestCalculatePriceChangePercent(t *testing.T) {
	tests := []struct {
		name       string
		lastPrice  decimal.Decimal
		firstPrice decimal.Decimal
		expected   decimal.Decimal
	}{
		{
			name:       "Standard positive price change",
			lastPrice:  decimal.NewFromFloat(105.0),
			firstPrice: decimal.NewFromFloat(100.0),
			expected:   decimal.NewFromFloat(5.0),
		},
		{
			name:       "Standard negative price change",
			lastPrice:  decimal.NewFromFloat(95.0),
			firstPrice: decimal.NewFromFloat(100.0),
			expected:   decimal.NewFromFloat(-5.0),
		},
		{
			name:       "Zero first price (no trades in reference window)",
			lastPrice:  decimal.NewFromFloat(100.0),
			firstPrice: decimal.Zero,
			expected:   decimal.Zero,
		},
		{
			name:       "Zero last price (corrupted or uninitialized)",
			lastPrice:  decimal.Zero,
			firstPrice: decimal.NewFromFloat(100.0),
			expected:   decimal.Zero,
		},
		{
			name:       "Both zero (no trades at all)",
			lastPrice:  decimal.Zero,
			firstPrice: decimal.Zero,
			expected:   decimal.Zero,
		},
		{
			name:       "Equal prices (1 trade in history or no price movement)",
			lastPrice:  decimal.NewFromFloat(50000.0),
			firstPrice: decimal.NewFromFloat(50000.0),
			expected:   decimal.Zero,
		},
		{
			name:       "Inactive market (last trade is also reference trade from before 24h)",
			lastPrice:  decimal.NewFromFloat(2450.50),
			firstPrice: decimal.NewFromFloat(2450.50),
			expected:   decimal.Zero,
		},
		{
			name:       "100% surge",
			lastPrice:  decimal.NewFromFloat(200.0),
			firstPrice: decimal.NewFromFloat(100.0),
			expected:   decimal.NewFromFloat(100.0),
		},
		{
			name:       "50% drop",
			lastPrice:  decimal.NewFromFloat(50.0),
			firstPrice: decimal.NewFromFloat(100.0),
			expected:   decimal.NewFromFloat(-50.0),
		},
		{
			name:       "Small tick move (e.g. 2500.00 to 2500.25)",
			lastPrice:  decimal.NewFromFloat(2500.25),
			firstPrice: decimal.NewFromFloat(2500.00),
			expected:   decimal.NewFromFloat(0.01),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := postgresrepo.CalculatePriceChangePercent(tt.lastPrice, tt.firstPrice)
			if !result.Equal(tt.expected) {
				t.Fatalf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

// Inactive market metrics simulator matching Postgres query logic
type SimulatedTrade struct {
	Price      decimal.Decimal
	Quantity   decimal.Decimal
	ExecutedAt time.Time
}

type SimulatedCandle struct {
	StartTime  time.Time
	ClosePrice decimal.Decimal
}

// ComputeTicker24hSimulated mirrors the SQL logic in GetTicker24h and GetMarketsOverview
func ComputeTicker24hSimulated(now time.Time, trades []SimulatedTrade) (lastPrice, high24h, low24h, vol24h, quoteVol24h, priceChangePct decimal.Decimal) {
	if len(trades) == 0 {
		return decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero
	}

	cutoff24h := now.Add(-24 * time.Hour)
	var latest *SimulatedTrade
	var refTrade *SimulatedTrade
	var earliest *SimulatedTrade

	var trades24h []SimulatedTrade

	for i := range trades {
		t := &trades[i]
		if latest == nil || t.ExecutedAt.After(latest.ExecutedAt) {
			latest = t
		}
		if earliest == nil || t.ExecutedAt.Before(earliest.ExecutedAt) {
			earliest = t
		}
		if !t.ExecutedAt.After(cutoff24h) {
			if refTrade == nil || t.ExecutedAt.After(refTrade.ExecutedAt) {
				refTrade = t
			}
		}
		if !t.ExecutedAt.Before(cutoff24h) {
			trades24h = append(trades24h, *t)
		}
	}

	lastPrice = latest.Price

	if len(trades24h) > 0 {
		high24h = trades24h[0].Price
		low24h = trades24h[0].Price
		vol24h = decimal.Zero
		quoteVol24h = decimal.Zero
		for _, t := range trades24h {
			if t.Price.GreaterThan(high24h) {
				high24h = t.Price
			}
			if t.Price.LessThan(low24h) {
				low24h = t.Price
			}
			vol24h = vol24h.Add(t.Quantity)
			quoteVol24h = quoteVol24h.Add(t.Price.Mul(t.Quantity))
		}
	} else {
		// Inactive market: fall back to last_price for high and low, 0 volume
		high24h = lastPrice
		low24h = lastPrice
		vol24h = decimal.Zero
		quoteVol24h = decimal.Zero
	}

	var firstPrice decimal.Decimal
	if refTrade != nil {
		firstPrice = refTrade.Price
	} else if earliest != nil {
		firstPrice = earliest.Price
	}

	priceChangePct = postgresrepo.CalculatePriceChangePercent(lastPrice, firstPrice)
	return
}

func TestInactiveMarket_MetricsSemantics(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

	t.Run("Inactive market: trade exists 36 hours ago, none in 24h", func(t *testing.T) {
		trades := []SimulatedTrade{
			{
				Price:      decimal.NewFromFloat(2429.50),
				Quantity:   decimal.NewFromFloat(1.5),
				ExecutedAt: now.Add(-36 * time.Hour),
			},
		}

		lastPrice, high24h, low24h, vol24h, quoteVol24h, pct := ComputeTicker24hSimulated(now, trades)

		// 1. Last price must be preserved
		if !lastPrice.Equal(decimal.NewFromFloat(2429.50)) {
			t.Errorf("expected lastPrice 2429.50, got %s", lastPrice)
		}
		// 2. High and Low must NOT be zero; they must equal lastPrice
		if !high24h.Equal(decimal.NewFromFloat(2429.50)) {
			t.Errorf("expected high24h to equal lastPrice 2429.50, got %s", high24h)
		}
		if !low24h.Equal(decimal.NewFromFloat(2429.50)) {
			t.Errorf("expected low24h to equal lastPrice 2429.50, got %s", low24h)
		}
		// 3. 24h volume must be strictly zero
		if !vol24h.IsZero() {
			t.Errorf("expected vol24h to be zero, got %s", vol24h)
		}
		if !quoteVol24h.IsZero() {
			t.Errorf("expected quoteVol24h to be zero, got %s", quoteVol24h)
		}
		// 4. Change must be zero percent
		if !pct.IsZero() {
			t.Errorf("expected price change to be 0%%, got %s", pct)
		}
	})

	t.Run("Active market: multiple trades in 24h window", func(t *testing.T) {
		trades := []SimulatedTrade{
			{
				Price:      decimal.NewFromFloat(2400.00),
				Quantity:   decimal.NewFromFloat(1.0),
				ExecutedAt: now.Add(-25 * time.Hour), // ref trade
			},
			{
				Price:      decimal.NewFromFloat(2450.00),
				Quantity:   decimal.NewFromFloat(2.0),
				ExecutedAt: now.Add(-10 * time.Hour),
			},
			{
				Price:      decimal.NewFromFloat(2380.00),
				Quantity:   decimal.NewFromFloat(3.0),
				ExecutedAt: now.Add(-5 * time.Hour),
			},
			{
				Price:      decimal.NewFromFloat(2520.00),
				Quantity:   decimal.NewFromFloat(1.0),
				ExecutedAt: now.Add(-1 * time.Hour), // latest trade
			},
		}

		lastPrice, high24h, low24h, vol24h, quoteVol24h, pct := ComputeTicker24hSimulated(now, trades)

		if !lastPrice.Equal(decimal.NewFromFloat(2520.00)) {
			t.Errorf("expected lastPrice 2520.00, got %s", lastPrice)
		}
		if !high24h.Equal(decimal.NewFromFloat(2520.00)) {
			t.Errorf("expected high24h 2520.00, got %s", high24h)
		}
		if !low24h.Equal(decimal.NewFromFloat(2380.00)) {
			t.Errorf("expected low24h 2380.00, got %s", low24h)
		}
		if !vol24h.Equal(decimal.NewFromFloat(6.0)) { // 2 + 3 + 1
			t.Errorf("expected vol24h 6.0, got %s", vol24h)
		}
		expectedQuote := decimal.NewFromFloat(2450.0*2.0 + 2380.0*3.0 + 2520.0*1.0)
		if !quoteVol24h.Equal(expectedQuote) {
			t.Errorf("expected quoteVol24h %s, got %s", expectedQuote, quoteVol24h)
		}
		// (2520 - 2400) / 2400 * 100 = 120 / 2400 * 100 = 5.0%
		if !pct.Equal(decimal.NewFromFloat(5.0)) {
			t.Errorf("expected price change 5.0%%, got %s", pct)
		}
	})

	t.Run("Market with no trade history ever", func(t *testing.T) {
		lastPrice, high24h, low24h, vol24h, quoteVol24h, pct := ComputeTicker24hSimulated(now, nil)

		if !lastPrice.IsZero() || !high24h.IsZero() || !low24h.IsZero() || !vol24h.IsZero() || !quoteVol24h.IsZero() || !pct.IsZero() {
			t.Errorf("all fields must be zero for never-traded market: %s, %s, %s, %s, %s, %s",
				lastPrice, high24h, low24h, vol24h, quoteVol24h, pct)
		}
	})
}

// ComputeTrendSimulated mirrors the SQL logic in GetMarketsOverview trend CTE
func ComputeTrendSimulated(now time.Time, candles []SimulatedCandle, limit int) []string {
	cutoff7d := now.Add(-7 * 24 * time.Hour)
	var eligible []SimulatedCandle
	for _, c := range candles {
		// Enforce: start_time >= NOW() - INTERVAL '7 days'
		if !c.StartTime.Before(cutoff7d) {
			eligible = append(eligible, c)
		}
	}

	if len(eligible) == 0 {
		return []string{}
	}

	// Sort desc to get most recent `limit`
	for i := 0; i < len(eligible)-1; i++ {
		for j := i + 1; j < len(eligible); j++ {
			if eligible[i].StartTime.Before(eligible[j].StartTime) {
				eligible[i], eligible[j] = eligible[j], eligible[i]
			}
		}
	}

	if len(eligible) > limit {
		eligible = eligible[:limit]
	}

	// Order chronologically (ASC)
	for i := 0; i < len(eligible)-1; i++ {
		for j := i + 1; j < len(eligible); j++ {
			if eligible[i].StartTime.After(eligible[j].StartTime) {
				eligible[i], eligible[j] = eligible[j], eligible[i]
			}
		}
	}

	res := make([]string, len(eligible))
	for i, c := range eligible {
		res[i] = c.ClosePrice.String()
	}
	return res
}

func TestTrendFreshness_Enforcement(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

	t.Run("Fresh candles within 7 days", func(t *testing.T) {
		candles := []SimulatedCandle{
			{StartTime: now.Add(-3 * 24 * time.Hour), ClosePrice: decimal.NewFromFloat(2400.0)},
			{StartTime: now.Add(-2 * 24 * time.Hour), ClosePrice: decimal.NewFromFloat(2450.0)},
			{StartTime: now.Add(-1 * 24 * time.Hour), ClosePrice: decimal.NewFromFloat(2500.0)},
		}

		trend := ComputeTrendSimulated(now, candles, 168)
		if len(trend) != 3 {
			t.Fatalf("expected 3 fresh trend points, got %d", len(trend))
		}
		if trend[0] != "2400" || trend[1] != "2450" || trend[2] != "2500" {
			t.Errorf("unexpected trend sequence: %v", trend)
		}
	})

	t.Run("Stale candles (> 7 days old) are excluded", func(t *testing.T) {
		candles := []SimulatedCandle{
			{StartTime: now.Add(-14 * 24 * time.Hour), ClosePrice: decimal.NewFromFloat(2100.0)}, // stale
			{StartTime: now.Add(-8 * 24 * time.Hour), ClosePrice: decimal.NewFromFloat(2200.0)},  // stale
			{StartTime: now.Add(-2 * 24 * time.Hour), ClosePrice: decimal.NewFromFloat(2450.0)},  // fresh
		}

		trend := ComputeTrendSimulated(now, candles, 168)
		if len(trend) != 1 {
			t.Fatalf("expected 1 fresh point, got %d: %v", len(trend), trend)
		}
		if trend[0] != "2450" {
			t.Errorf("expected 2450, got %s", trend[0])
		}
	})

	t.Run("Completely stale history yields empty trend array", func(t *testing.T) {
		candles := []SimulatedCandle{
			{StartTime: now.Add(-30 * 24 * time.Hour), ClosePrice: decimal.NewFromFloat(2000.0)},
			{StartTime: now.Add(-10 * 24 * time.Hour), ClosePrice: decimal.NewFromFloat(2100.0)},
		}

		trend := ComputeTrendSimulated(now, candles, 168)
		if len(trend) != 0 {
			t.Fatalf("expected empty trend for all-stale history, got %d: %v", len(trend), trend)
		}
	})

	t.Run("Boundary time history: exact 7 days cutoff", func(t *testing.T) {
		candles := []SimulatedCandle{
			// Exactly 7 days + 1 second ago (stale)
			{StartTime: now.Add(-7*24*time.Hour - 1*time.Second), ClosePrice: decimal.NewFromFloat(2000.0)},
			// Exactly 7 days - 1 second ago (eligible)
			{StartTime: now.Add(-7*24*time.Hour + 1*time.Second), ClosePrice: decimal.NewFromFloat(2100.0)},
		}

		trend := ComputeTrendSimulated(now, candles, 168)
		if len(trend) != 1 {
			t.Fatalf("expected 1 boundary-eligible point, got %d", len(trend))
		}
		if trend[0] != "2100" {
			t.Errorf("expected 2100, got %s", trend[0])
		}
	})

	t.Run("Sparse candles preserved without synthetic generation", func(t *testing.T) {
		candles := []SimulatedCandle{
			{StartTime: now.Add(-5 * 24 * time.Hour), ClosePrice: decimal.NewFromFloat(2400.0)},
			{StartTime: now.Add(-1 * 24 * time.Hour), ClosePrice: decimal.NewFromFloat(2500.0)},
		}

		trend := ComputeTrendSimulated(now, candles, 168)
		// Must NOT fabricate missing days/hours!
		if len(trend) != 2 {
			t.Fatalf("expected exactly 2 points without synthetic points, got %d", len(trend))
		}
		if trend[0] != "2400" || trend[1] != "2500" {
			t.Errorf("unexpected trend values: %v", trend)
		}
	})

	t.Run("Empty history returns empty slice", func(t *testing.T) {
		trend := ComputeTrendSimulated(now, nil, 168)
		if len(trend) != 0 {
			t.Fatalf("expected empty trend, got %d", len(trend))
		}
	})
}
