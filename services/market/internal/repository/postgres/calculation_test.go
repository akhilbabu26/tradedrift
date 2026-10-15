package postgres_test

import (
	"testing"

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
