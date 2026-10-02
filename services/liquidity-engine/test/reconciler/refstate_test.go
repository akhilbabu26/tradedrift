package reconciler_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/reconciler"
)

func TestClassifyMovement(t *testing.T) {
	cfg := config.RepricingConfig{
		SmallBps: 10,
		LargeBps: 100,
		MaxBatch: 4,
	}

	tests := []struct {
		name       string
		prev       decimal.Decimal
		next       decimal.Decimal
		freshness  reconciler.Freshness
		wantAction reconciler.RepricingAction
	}{
		{
			name:       "Small movement (5 bps) -> KEEP",
			prev:       decimal.RequireFromString("100.00"),
			next:       decimal.RequireFromString("100.05"),
			freshness:  reconciler.FreshnessFresh,
			wantAction: reconciler.ActionKeep,
		},
		{
			name:       "Small downward movement (5 bps) -> KEEP",
			prev:       decimal.RequireFromString("100.00"),
			next:       decimal.RequireFromString("99.95"),
			freshness:  reconciler.FreshnessFresh,
			wantAction: reconciler.ActionKeep,
		},
		{
			name:       "Selective reprice (50 bps) -> SELECTIVE_REPRICE",
			prev:       decimal.RequireFromString("100.00"),
			next:       decimal.RequireFromString("100.50"),
			freshness:  reconciler.FreshnessFresh,
			wantAction: reconciler.ActionSelectiveReprice,
		},
		{
			name:       "Exact SmallBps boundary (10 bps) -> SELECTIVE_REPRICE",
			prev:       decimal.RequireFromString("100.00"),
			next:       decimal.RequireFromString("100.10"),
			freshness:  reconciler.FreshnessFresh,
			wantAction: reconciler.ActionSelectiveReprice,
		},
		{
			name:       "Large movement (200 bps) -> CONTROLLED_REBASE",
			prev:       decimal.RequireFromString("100.00"),
			next:       decimal.RequireFromString("102.00"),
			freshness:  reconciler.FreshnessFresh,
			wantAction: reconciler.ActionControlledRebase,
		},
		{
			name:       "Exact LargeBps boundary (100 bps) -> CONTROLLED_REBASE",
			prev:       decimal.RequireFromString("100.00"),
			next:       decimal.RequireFromString("101.00"),
			freshness:  reconciler.FreshnessFresh,
			wantAction: reconciler.ActionControlledRebase,
		},
		{
			name:       "Stale reference -> HOLD",
			prev:       decimal.RequireFromString("100.00"),
			next:       decimal.RequireFromString("105.00"),
			freshness:  reconciler.FreshnessStale,
			wantAction: reconciler.ActionHold,
		},
		{
			name:       "Paused reference -> PAUSE",
			prev:       decimal.RequireFromString("100.00"),
			next:       decimal.RequireFromString("105.00"),
			freshness:  reconciler.FreshnessPaused,
			wantAction: reconciler.ActionPause,
		},
		{
			name:       "Zero prev -> KEEP",
			prev:       decimal.Zero,
			next:       decimal.RequireFromString("100.00"),
			freshness:  reconciler.FreshnessFresh,
			wantAction: reconciler.ActionKeep,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action := reconciler.ClassifyMovement(tc.prev, tc.next, tc.freshness, cfg)
			if action != tc.wantAction {
				t.Errorf("got %s, want %s", action, tc.wantAction)
			}
		})
	}
}
