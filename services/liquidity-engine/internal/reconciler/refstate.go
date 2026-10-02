package reconciler

import (
	"time"

	"github.com/shopspring/decimal"

	"tradedrift/services/liquidity-engine/internal/config"
)

// Freshness indicates the validity and timeliness of the reference price.
type Freshness int

const (
	FreshnessFresh Freshness = iota
	FreshnessStale
	FreshnessPaused
)

func (f Freshness) String() string {
	switch f {
	case FreshnessFresh:
		return "FRESH"
	case FreshnessStale:
		return "STALE"
	case FreshnessPaused:
		return "PAUSED"
	default:
		return "UNKNOWN"
	}
}

// ReferenceSnapshot captures an immutable snapshot of the authoritative reference price state.
type ReferenceSnapshot struct {
	Price     decimal.Decimal
	Version   int64
	FetchedAt time.Time
	Freshness Freshness
}

// RefState tracks reference price history and freshness for a market.
type RefState struct {
	LastRef         decimal.Decimal
	LastVersion     int64
	LastUpdated     time.Time
	Freshness       Freshness

	RebaseActive    bool
	RebaseTargetRef decimal.Decimal
	RebaseTargetVer int64
	RebaseAction    RepricingAction
}

// RepricingAction defines how the LE should adapt its order ladder to reference movement.
type RepricingAction int

const (
	ActionKeep             RepricingAction = iota // movement < SmallBps
	ActionSelectiveReprice                        // SmallBps ≤ movement < LargeBps
	ActionControlledRebase                        // movement ≥ LargeBps
	ActionHold                                    // reference STALE — keep existing, no new creation
	ActionPause                                   // reference PAUSED — no new creation at all
)

func (a RepricingAction) String() string {
	switch a {
	case ActionKeep:
		return "KEEP"
	case ActionSelectiveReprice:
		return "SELECTIVE_REPRICE"
	case ActionControlledRebase:
		return "CONTROLLED_REBASE"
	case ActionHold:
		return "HOLD"
	case ActionPause:
		return "PAUSE"
	default:
		return "UNKNOWN"
	}
}

// ClassifyMovement evaluates reference price movement and determines the appropriate repricing action.
//
// Invariants:
//  1. Reference failure (STALE/PAUSED) NEVER triggers aggressive cancellations.
//     Existing resting liquidity remains authoritative.
//  2. When Fresh:
//     - movement < SmallBps (e.g. 10 bps)  → KEEP (no-op, price locked)
//     - SmallBps ≤ movement < LargeBps     → SELECTIVE_REPRICE (targeted batched updates)
//     - movement ≥ LargeBps (e.g. 100 bps) → CONTROLLED_REBASE (gradual outer-in batched rebase)
func ClassifyMovement(prev, next decimal.Decimal, freshness Freshness, cfg config.RepricingConfig) RepricingAction {
	switch freshness {
	case FreshnessStale:
		return ActionHold
	case FreshnessPaused:
		return ActionPause
	}

	if prev.IsZero() || prev.IsNegative() || next.IsZero() || next.IsNegative() {
		return ActionKeep
	}

	smallBps := cfg.SmallBps
	if smallBps <= 0 {
		smallBps = 10
	}
	largeBps := cfg.LargeBps
	if largeBps <= 0 {
		largeBps = 100
	}

	// movementBps = (|next - prev| / prev) * 10000
	diff := next.Sub(prev).Abs()
	movementBps := diff.Div(prev).Mul(decimal.NewFromInt(10000))

	smallDec := decimal.NewFromInt(int64(smallBps))
	largeDec := decimal.NewFromInt(int64(largeBps))

	if movementBps.LessThan(smallDec) {
		return ActionKeep
	}
	if movementBps.LessThan(largeDec) {
		return ActionSelectiveReprice
	}
	return ActionControlledRebase
}
