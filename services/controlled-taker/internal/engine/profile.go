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

// SizingParameters encapsulates the output of the dynamic sizing engine.
type SizingParameters struct {
	Quantity          decimal.Decimal
	PriceCap          decimal.Decimal // Limit price sent to exchange
	ConservativePrice decimal.Decimal // Upper-bound conservative price for notional calculations (PriceCap for BUY, L1 for SELL)
	Profile           config.ProfileType
	TargetDepth       decimal.Decimal
	ReachableLevels   int
}

// CalculateOrderParameters computes lot-aligned order quantity and price cap dynamically from current live depth.
// Invariant: The calculation MUST receive the live depth snapshot as an input.
// The sizing functions DO NOT have access to or depend on fixed quantities.
func CalculateOrderParameters(
	profile config.ProfileType,
	depth *redisdepth.DepthSnapshot,
	side string,
	market config.MarketConfig,
	maxNotional decimal.Decimal,
	maxSlippageBps int,
) (*SizingParameters, error) {
	return CalculateOrderParametersWithRatio(profile, depth, side, market, maxNotional, maxSlippageBps, nil)
}

// CalculateOrderParametersWithRatio is an internal sizing calculator that accepts an optional ratio override (used for deterministic testing).
func CalculateOrderParametersWithRatio(
	profile config.ProfileType,
	depth *redisdepth.DepthSnapshot,
	side string,
	market config.MarketConfig,
	maxNotional decimal.Decimal,
	maxSlippageBps int,
	ratioOverride *decimal.Decimal,
) (*SizingParameters, error) {
	if depth == nil {
		return nil, ErrNilDepth
	}

	// 1. Validate Depth Freshness (Fail Closed)
	if depth.SnapshotAt.IsZero() {
		return nil, fmt.Errorf("%w: missing snapshot_at timestamp", ErrStaleDepth)
	}
	if time.Since(depth.SnapshotAt) > 5*time.Second {
		return nil, fmt.Errorf("%w: snapshot_at %s is %s old", ErrStaleDepth, depth.SnapshotAt.Format(time.RFC3339), time.Since(depth.SnapshotAt))
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

	slippageFraction := decimal.NewFromInt(int64(maxSlippageBps)).Div(decimal.NewFromInt(10000))

	var rawQty decimal.Decimal
	var priceCap decimal.Decimal
	reachableLevels := 1

	switch profile {
	case config.ProfileLow:
		// LOW: 5% - 15% of current L1 quantity
		ratio := randomDecimal(0.05, 0.15)
		if ratioOverride != nil {
			ratio = *ratioOverride
		}
		rawQty = l1.Quantity.Mul(ratio)
		priceCap = l1.Price

	case config.ProfileMid:
		// MID: 20% - 50% of current L1 quantity
		ratio := randomDecimal(0.20, 0.50)
		if ratioOverride != nil {
			ratio = *ratioOverride
		}
		rawQty = l1.Quantity.Mul(ratio)
		if side == "BUY" {
			priceCap = l1.Price.Add(market.TickSize)
		} else {
			priceCap = l1.Price.Sub(market.TickSize)
		}

	case config.ProfileHigh:
		// HIGH: Sweeps across reachable levels within slippage band
		var pLimit decimal.Decimal
		if side == "BUY" {
			pLimit = l1.Price.Mul(decimal.NewFromInt(1).Add(slippageFraction))
		} else {
			pLimit = l1.Price.Mul(decimal.NewFromInt(1).Sub(slippageFraction))
		}

		// Find reachable levels within slippage limit
		reachable := make([]redisdepth.DepthLevel, 0, len(levels))
		for _, lvl := range levels {
			if side == "BUY" && lvl.Price.LessThanOrEqual(pLimit) {
				reachable = append(reachable, lvl)
			} else if side == "SELL" && lvl.Price.GreaterThanOrEqual(pLimit) {
				reachable = append(reachable, lvl)
			}
		}

		if len(reachable) == 0 {
			return nil, ErrInsufficientLiquidity
		}

		reachableLevels = len(reachable)
		deepestLevel := reachable[len(reachable)-1]
		priceCap = deepestLevel.Price

		var desiredQty decimal.Decimal
		var totalReachableDepth decimal.Decimal
		for _, r := range reachable {
			totalReachableDepth = totalReachableDepth.Add(r.Quantity)
		}

		if reachableLevels == 1 {
			// Only 1 level reachable: consume 50%-80% of L1
			ratio := randomDecimal(0.50, 0.80)
			if ratioOverride != nil {
				ratio = *ratioOverride
			}
			desiredQty = l1.Quantity.Mul(ratio)
		} else {
			// Multi-level sweep: 100% of L1 + 30%-80% of subsequent reachable depth
			var subsequentDepth decimal.Decimal
			for i := 1; i < len(reachable); i++ {
				subsequentDepth = subsequentDepth.Add(reachable[i].Quantity)
			}
			sweepRatio := randomDecimal(0.30, 0.80)
			if ratioOverride != nil {
				sweepRatio = *ratioOverride
			}
			desiredQty = l1.Quantity.Add(subsequentDepth.Mul(sweepRatio))
		}

		// Calculate maximum safe quantity to satisfy the 1.5x depth guard (requiredDepth = targetQty * 1.5 <= totalReachableDepth)
		requiredMinDepth := market.MinQuantity.Mul(decimal.NewFromFloat(1.5))
		if totalReachableDepth.LessThan(requiredMinDepth) {
			return nil, fmt.Errorf("%w: reachable depth %s cannot satisfy 1.5x of min_quantity %s",
				ErrInsufficientLiquidity, totalReachableDepth, market.MinQuantity)
		}

		maxSafeQty := totalReachableDepth.Div(decimal.NewFromFloat(1.5))
		rawQty = decimal.Min(desiredQty, maxSafeQty)

	default:
		return nil, fmt.Errorf("unknown profile %q", profile)
	}

	// 3. Apply Minimum Quantity Constraint
	if rawQty.LessThan(market.MinQuantity) {
		if l1.Quantity.GreaterThanOrEqual(market.MinQuantity) {
			rawQty = market.MinQuantity
		} else {
			return nil, fmt.Errorf("%w: calculated %s < min %s and L1 has only %s",
				ErrBelowMinQuantity, rawQty, market.MinQuantity, l1.Quantity)
		}
	}

	// 4. Lot-Align Quantity
	qty := lotAlign(rawQty, market.LotSize)

	// If lot alignment rounded below min_quantity, bump to min_quantity if L1 permits
	if qty.LessThan(market.MinQuantity) {
		if l1.Quantity.GreaterThanOrEqual(market.MinQuantity) {
			qty = market.MinQuantity
		} else {
			return nil, fmt.Errorf("%w: lot-aligned %s < min %s", ErrBelowMinQuantity, qty, market.MinQuantity)
		}
	}

	// Re-verify that the final lot-aligned quantity strictly satisfies the 1.5x depth guard for HIGH sweeps
	if profile == config.ProfileHigh {
		var totalReachable decimal.Decimal
		for _, r := range levels {
			if (side == "BUY" && r.Price.LessThanOrEqual(priceCap)) || (side == "SELL" && r.Price.GreaterThanOrEqual(priceCap)) {
				totalReachable = totalReachable.Add(r.Quantity)
			}
		}
		if qty.Mul(decimal.NewFromFloat(1.5)).GreaterThan(totalReachable) {
			return nil, fmt.Errorf("%w: lot-aligned qty %s requires 1.5x depth (%s) exceeding reachable depth %s",
				ErrInsufficientLiquidity, qty, qty.Mul(decimal.NewFromFloat(1.5)), totalReachable)
		}
	}

	// 5. Cap by Max Order Notional using Conservative Ceiling Price
	// Invariant: ConservativePrice must always be the highest execution price that the order
	// could legally achieve, given its side and price cap.
	// For BUY: CTS executes against Asks up to PriceCap, so PriceCap is the conservative ceiling price.
	// For SELL: CTS crosses against Bids starting from L1 down to PriceCap, so L1.Price (highest bid)
	// is the conservative ceiling price.
	var conservativePrice decimal.Decimal
	if side == "BUY" {
		conservativePrice = priceCap
	} else {
		conservativePrice = l1.Price
		if priceCap.GreaterThan(conservativePrice) {
			conservativePrice = priceCap
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

	// 6. Re-check that quantity > 0
	if !qty.GreaterThan(decimal.Zero) {
		return nil, errors.New("resulting order quantity must be strictly positive")
	}

	return &SizingParameters{
		Quantity:          qty,
		PriceCap:          priceCap,
		ConservativePrice: conservativePrice,
		Profile:           profile,
		TargetDepth:       l1.Quantity,
		ReachableLevels:   reachableLevels,
	}, nil
}

func lotAlign(val, lotSize decimal.Decimal) decimal.Decimal {
	if !lotSize.GreaterThan(decimal.Zero) {
		return val
	}
	steps := val.Div(lotSize).Floor()
	return steps.Mul(lotSize)
}

func randomDecimal(min, max float64) decimal.Decimal {
	f := min + rand.Float64()*(max-min)
	return decimal.NewFromFloat(f).Round(4)
}
