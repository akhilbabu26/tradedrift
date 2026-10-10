// Package engine implements the dynamic sizing, policy, and worker loops for CTS.
package engine

import (
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/shopspring/decimal"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
	"tradedrift/services/controlled-taker/internal/config"
)

var (
	ErrNilDepth              = errors.New("live depth snapshot is nil")
	ErrStaleDepth            = errors.New("live depth snapshot is stale (>5s)")
	ErrEmptyOppositeSide     = errors.New("opposite side of order book is empty")
	ErrInsufficientLiquidity = errors.New("insufficient reachable liquidity in slippage band")
	ErrBelowMinQuantity      = errors.New("calculated quantity is below market min_quantity")
)

// ExecutionMode defines whether an order targets only L1 or sweeps into deeper levels.
type ExecutionMode int

const (
	ExecutionNormal ExecutionMode = iota // Targets L1 only; priceCap = L1
	ExecutionSweep                       // Sweeps through L1 into L2/L3 within slippage bounds
)

func (m ExecutionMode) String() string {
	switch m {
	case ExecutionNormal:
		return "NORMAL"
	case ExecutionSweep:
		return "SWEEP"
	default:
		return "UNKNOWN"
	}
}

// SizingParameters encapsulates the output of the dynamic sizing engine.
type SizingParameters struct {
	Quantity          decimal.Decimal
	ProtectionPrice   decimal.Decimal // Worst acceptable execution price sent to exchange (slippage boundary)
	PriceCap          decimal.Decimal // Backwards-compatible alias for ProtectionPrice
	ConservativePrice decimal.Decimal // Upper-bound conservative price for notional calculations
	Profile           config.ProfileType
	L1Depth           decimal.Decimal // Available quantity at top of book (L1)
	ReachableDepth    decimal.Decimal // Total cumulative quantity across all reachable levels within slippage
	TargetDepth       decimal.Decimal // Backwards-compatible alias for L1Depth
	ReachableLevels   int
	Mode              ExecutionMode
}

// SizingOptions allows callers and tests to inject deterministic overrides.
type SizingOptions struct {
	RatioOverride              *decimal.Decimal // Overrides the profile's random depth ratio
	SweepRollOverride          *float64         // Overrides random sweep decision [0.0, 1.0]
	HighSweepProbability       float64          // Probability threshold for HIGH profile sweeps [0.0, 1.0]
	HasHighSweepProbability    bool             // If true, HighSweepProbability is explicitly configured (including 0.0)
	MaxSweepLevels             int              // Maximum depth levels to reach in a sweep (default 3)
	MaxVisibleDepthConsumption *decimal.Decimal // Maximum fraction of visible depth to consume (default 0.90)
	MaxDepthAge                time.Duration    // Maximum allowable age of depth snapshot (default 5s)
	RNG                        *rand.Rand       // Worker-local or deterministic test RNG
}

// CalculateOrderParameters computes lot-aligned order quantity and price cap dynamically from current live depth.
func CalculateOrderParameters(
	profile config.ProfileType,
	depth *redisdepth.DepthSnapshot,
	side string,
	market config.MarketConfig,
	maxNotional decimal.Decimal,
	maxSlippageBps int,
) (*SizingParameters, error) {
	return CalculateOrderParametersWithOptions(profile, depth, side, market, maxNotional, maxSlippageBps, SizingOptions{})
}

// CalculateOrderParametersWithRatio preserves backward compatibility for existing tests.
func CalculateOrderParametersWithRatio(
	profile config.ProfileType,
	depth *redisdepth.DepthSnapshot,
	side string,
	market config.MarketConfig,
	maxNotional decimal.Decimal,
	maxSlippageBps int,
	ratioOverride *decimal.Decimal,
) (*SizingParameters, error) {
	return CalculateOrderParametersWithOptions(profile, depth, side, market, maxNotional, maxSlippageBps, SizingOptions{
		RatioOverride: ratioOverride,
	})
}

// CalculateOrderParametersWithOptions performs the full dynamic sizing and sweep calculation.
func CalculateOrderParametersWithOptions(
	profile config.ProfileType,
	depth *redisdepth.DepthSnapshot,
	side string,
	market config.MarketConfig,
	maxNotional decimal.Decimal,
	maxSlippageBps int,
	opts SizingOptions,
) (*SizingParameters, error) {
	if depth == nil {
		return nil, ErrNilDepth
	}

	// 1. Validate Depth Freshness (Fail Closed)
	if depth.SnapshotAt.IsZero() {
		return nil, fmt.Errorf("%w: missing snapshot_at timestamp", ErrStaleDepth)
	}
	maxDepthAge := 5 * time.Second
	if opts.MaxDepthAge > 0 {
		maxDepthAge = opts.MaxDepthAge
	}
	if time.Since(depth.SnapshotAt) > maxDepthAge {
		return nil, fmt.Errorf("%w: snapshot_at %s is %s old (max allowed: %s)", ErrStaleDepth, depth.SnapshotAt.Format(time.RFC3339), time.Since(depth.SnapshotAt), maxDepthAge)
	}
	if time.Until(depth.SnapshotAt) > 1*time.Second {
		return nil, fmt.Errorf("%w: snapshot_at %s is %s in future", ErrStaleDepth, depth.SnapshotAt.Format(time.RFC3339), time.Until(depth.SnapshotAt))
	}

	// 2. Extract Opposite Side Levels
	var levels []redisdepth.DepthLevel
	switch side {
	case "BUY":
		levels = depth.Asks
	case "SELL":
		levels = depth.Bids
	default:
		return nil, fmt.Errorf("invalid side %q", side)
	}

	if len(levels) == 0 {
		return nil, ErrEmptyOppositeSide
	}

	l1 := levels[0]
	if !l1.Price.GreaterThan(decimal.Zero) || !l1.Quantity.GreaterThan(decimal.Zero) {
		return nil, errors.New("invalid L1 price or quantity in depth snapshot")
	}

	maxLevels := opts.MaxSweepLevels
	if maxLevels <= 0 {
		maxLevels = 3 // P1: Default to 3 levels (L1, L2, L3)
	}

	// Calculate maximum slippage boundary price
	slippageFraction := decimal.NewFromInt(int64(maxSlippageBps)).Div(decimal.NewFromInt(10000))
	var slippagePriceLimit decimal.Decimal
	if side == "BUY" {
		slippagePriceLimit = l1.Price.Mul(decimal.NewFromInt(1).Add(slippageFraction))
	} else {
		slippagePriceLimit = l1.Price.Mul(decimal.NewFromInt(1).Sub(slippageFraction))
	}

	// Collect levels within slippage boundary up to maxLevels
	reachableInSlippage := make([]redisdepth.DepthLevel, 0, maxLevels)
	for _, lvl := range levels {
		if len(reachableInSlippage) >= maxLevels {
			break
		}
		if side == "BUY" && lvl.Price.LessThanOrEqual(slippagePriceLimit) {
			reachableInSlippage = append(reachableInSlippage, lvl)
		} else if side == "SELL" && lvl.Price.GreaterThanOrEqual(slippagePriceLimit) {
			reachableInSlippage = append(reachableInSlippage, lvl)
		}
	}

	rng := opts.RNG

	// Single RNG roll per sizing operation
	var sweepRoll float64
	if opts.SweepRollOverride != nil {
		sweepRoll = *opts.SweepRollOverride
	} else if rng != nil {
		sweepRoll = rng.Float64()
	} else {
		sweepRoll = rand.Float64()
	}

	// Configurable depth consumption ceiling (defaults to 0.90 = 90% of visible reachable depth)
	maxDepthConsumption := decimal.NewFromFloat(0.90)
	if opts.MaxVisibleDepthConsumption != nil && opts.MaxVisibleDepthConsumption.GreaterThan(decimal.Zero) {
		maxDepthConsumption = *opts.MaxVisibleDepthConsumption
	}

	requiredMinDepth := market.MinQuantity.Div(maxDepthConsumption)
	isL1Dust := l1.Quantity.LessThan(requiredMinDepth)

	// Resolve dust sweep depth if L1 is dust
	var dustSweepLevels int = 1
	var dustTotalDepth decimal.Decimal = l1.Quantity
	var dustProtectionPrice decimal.Decimal = l1.Price
	if isL1Dust && len(reachableInSlippage) >= 2 {
		dustTotalDepth = decimal.Zero
		for i, lvl := range reachableInSlippage {
			dustTotalDepth = dustTotalDepth.Add(lvl.Quantity)
			dustProtectionPrice = lvl.Price
			dustSweepLevels = i + 1
			if dustTotalDepth.GreaterThanOrEqual(requiredMinDepth) {
				break
			}
		}
	}

	var rawQty decimal.Decimal
	var protectionPrice decimal.Decimal
	mode := ExecutionNormal
	reachableLevels := 1
	var totalReachableDepth decimal.Decimal = l1.Quantity

	switch profile {
	case config.ProfileLow:
		// LOW: 10% - 25% of current L1 quantity; strictly normal heartbeat
		// Phase 2 Dust Handling: if L1 is dust and deeper liquidity exists, sweep dust
		if isL1Dust && dustSweepLevels >= 2 && dustTotalDepth.GreaterThanOrEqual(requiredMinDepth) {
			mode = ExecutionSweep
			protectionPrice = dustProtectionPrice
			reachableLevels = dustSweepLevels
			totalReachableDepth = dustTotalDepth

			// Sizing: consume 100% of L1 dust plus enough of next level to form an executable lot
			desiredQty := l1.Quantity.Add(market.MinQuantity)
			maxSafeQty := totalReachableDepth.Mul(maxDepthConsumption)
			rawQty = decimal.Min(desiredQty, maxSafeQty)
		} else {
			ratio := randomDecimal(rng, 0.10, 0.25)
			if opts.RatioOverride != nil {
				ratio = *opts.RatioOverride
			}
			rawQty = l1.Quantity.Mul(ratio)
			protectionPrice = l1.Price
			mode = ExecutionNormal
			reachableLevels = 1
			totalReachableDepth = l1.Quantity
		}

	case config.ProfileMid:
		// MID: Standard size is 35% - 65% of L1
		ratio := randomDecimal(rng, 0.35, 0.65)
		if opts.RatioOverride != nil {
			ratio = *opts.RatioOverride
		}
		normalTargetQty := l1.Quantity.Mul(ratio)

		// Determine if sweep should trigger:
		// 1. L1 is dust (Phase 2)
		// 2. OR Sweep probability = 35%
		// 3. OR L1 is smaller than target quantity
		l1Insufficient := isL1Dust || l1.Quantity.LessThan(normalTargetQty)
		shouldSweep := (sweepRoll < 0.35 || l1Insufficient) && len(reachableInSlippage) >= 2

		if shouldSweep {
			mode = ExecutionSweep
			sweepTargetLevels := 2
			if isL1Dust && dustSweepLevels > 2 {
				sweepTargetLevels = dustSweepLevels
			}
			if sweepTargetLevels > len(reachableInSlippage) {
				sweepTargetLevels = len(reachableInSlippage)
			}

			targetLevel := reachableInSlippage[sweepTargetLevels-1]
			protectionPrice = targetLevel.Price
			reachableLevels = sweepTargetLevels

			totalReachableDepth = decimal.Zero
			for i := 0; i < sweepTargetLevels; i++ {
				totalReachableDepth = totalReachableDepth.Add(reachableInSlippage[i].Quantity)
			}

			// Consume 100% of L1 + 25%-50% of subsequent depth
			l2 := reachableInSlippage[1]
			l2Ratio := randomDecimal(rng, 0.25, 0.50)
			desiredQty := l1.Quantity.Add(l2.Quantity.Mul(l2Ratio))

			maxSafeQty := totalReachableDepth.Mul(maxDepthConsumption)
			rawQty = decimal.Min(desiredQty, maxSafeQty)
		} else {
			mode = ExecutionNormal
			rawQty = normalTargetQty
			protectionPrice = l1.Price
			reachableLevels = 1
			totalReachableDepth = l1.Quantity
		}

	case config.ProfileHigh:
		// HIGH: Macro wave / trend movement with bounded sweep probability (default 70%).
		// Semantics: Bounded Multi-Level Sweep.
		// It executes an aggressive multi-level sweep through L1 and into subsequent reachable levels (L2/L3)
		// within slippage bounds. It takes 100% of L1 plus 30%-60% of subsequent depth, strictly bounded
		// by MaxVisibleDepthConsumption (default 90% of total reachable depth) to preserve order book structure
		// and prevent absolute book depletion.
		highSweepProb := 0.70
		if opts.HasHighSweepProbability || opts.HighSweepProbability > 0 {
			highSweepProb = opts.HighSweepProbability
		}
		if highSweepProb > 1.0 {
			highSweepProb = 1.0
		} else if highSweepProb < 0.0 {
			highSweepProb = 0.0
		}

		shouldSweep := (sweepRoll < highSweepProb) && len(reachableInSlippage) >= 2

		if shouldSweep {
			mode = ExecutionSweep
			reachableLevels = len(reachableInSlippage)
			deepestLevel := reachableInSlippage[len(reachableInSlippage)-1]
			protectionPrice = deepestLevel.Price

			var subsequentDepth decimal.Decimal
			totalReachableDepth = l1.Quantity
			for i := 1; i < len(reachableInSlippage); i++ {
				subsequentDepth = subsequentDepth.Add(reachableInSlippage[i].Quantity)
				totalReachableDepth = totalReachableDepth.Add(reachableInSlippage[i].Quantity)
			}

			// Bounded multi-level sweep: 100% of L1 + 30%-60% of subsequent depth, capped at 90%
			sweepRatio := randomDecimal(rng, 0.30, 0.60)
			if opts.RatioOverride != nil {
				sweepRatio = *opts.RatioOverride
			}
			desiredQty := l1.Quantity.Add(subsequentDepth.Mul(sweepRatio))

			maxSafeQty := totalReachableDepth.Mul(maxDepthConsumption)
			rawQty = decimal.Min(desiredQty, maxSafeQty)
		} else {
			// Normal single-level take: 50% - 80% of L1
			mode = ExecutionNormal
			ratio := randomDecimal(rng, 0.50, 0.80)
			if opts.RatioOverride != nil {
				ratio = *opts.RatioOverride
			}
			protectionPrice = l1.Price
			reachableLevels = 1
			totalReachableDepth = l1.Quantity
			maxSafeQty := totalReachableDepth.Mul(maxDepthConsumption)
			rawQty = decimal.Min(l1.Quantity.Mul(ratio), maxSafeQty)
		}

	default:
		return nil, fmt.Errorf("unknown profile %q", profile)
	}

	// 3. Minimum Quantity Constraint
	if rawQty.LessThan(market.MinQuantity) {
		if totalReachableDepth.Mul(maxDepthConsumption).GreaterThanOrEqual(market.MinQuantity) {
			rawQty = market.MinQuantity
		} else if l1.Quantity.GreaterThanOrEqual(market.MinQuantity) {
			rawQty = market.MinQuantity
		} else {
			return nil, fmt.Errorf("%w: calculated %s < min %s and reachable depth has only %s",
				ErrBelowMinQuantity, rawQty, market.MinQuantity, totalReachableDepth)
		}
	}

	// 4. Lot-Align Quantity
	qty := lotAlign(rawQty, market.LotSize)
	if qty.LessThan(market.MinQuantity) {
		if totalReachableDepth.Mul(maxDepthConsumption).GreaterThanOrEqual(market.MinQuantity) {
			qty = market.MinQuantity
		} else {
			return nil, fmt.Errorf("%w: lot-aligned %s < min %s", ErrBelowMinQuantity, qty, market.MinQuantity)
		}
	}

	// 5. Invariant: Enforce configurable depth consumption ceiling
	if qty.GreaterThan(totalReachableDepth.Mul(maxDepthConsumption)) {
		qty = lotAlign(totalReachableDepth.Mul(maxDepthConsumption), market.LotSize)
		if qty.LessThan(market.MinQuantity) {
			return nil, fmt.Errorf("%w: lot-aligned qty %s requires depth exceeding reachable limit %s",
				ErrInsufficientLiquidity, qty, totalReachableDepth)
		}
	}

	// 6. Cap by Max Order Notional using Direction-Aware Conservative Price
	var conservativePrice decimal.Decimal
	if side == "BUY" {
		conservativePrice = protectionPrice
	} else {
		conservativePrice = l1.Price
		if protectionPrice.GreaterThan(conservativePrice) {
			conservativePrice = protectionPrice
		}
	}

	notional := qty.Mul(conservativePrice)
	if notional.GreaterThan(maxNotional) && conservativePrice.GreaterThan(decimal.Zero) {
		allowedQty := maxNotional.Div(conservativePrice)
		qty = lotAlign(allowedQty, market.LotSize)
		if qty.LessThan(market.MinQuantity) {
			return nil, fmt.Errorf("%w: capped notional produces qty %s < min %s",
				ErrBelowMinQuantity, qty, market.MinQuantity)
		}
	}

	// 7. Strictly Positive Final Verification
	if !qty.GreaterThan(decimal.Zero) {
		return nil, errors.New("resulting order quantity must be strictly positive")
	}

	return &SizingParameters{
		Quantity:          qty,
		ProtectionPrice:   protectionPrice,
		PriceCap:          protectionPrice,
		ConservativePrice: conservativePrice,
		Profile:           profile,
		L1Depth:           l1.Quantity,
		ReachableDepth:    totalReachableDepth,
		TargetDepth:       l1.Quantity,
		ReachableLevels:   reachableLevels,
		Mode:              mode,
	}, nil
}

func lotAlign(val, lotSize decimal.Decimal) decimal.Decimal {
	if !lotSize.GreaterThan(decimal.Zero) {
		return val
	}
	steps := val.Div(lotSize).Floor()
	return steps.Mul(lotSize)
}

func randomDecimal(rng *rand.Rand, min, max float64) decimal.Decimal {
	var f float64
	if rng != nil {
		f = min + rng.Float64()*(max-min)
	} else {
		f = min + rand.Float64()*(max-min)
	}
	return decimal.NewFromFloat(f).Round(4)
}

