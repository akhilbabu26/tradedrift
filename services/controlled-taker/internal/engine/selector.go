package engine

import (
	"math/rand"
	"sync"

	"github.com/shopspring/decimal"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
)

// DirectionSelector manages BUY/SELL side selection with inventory-balancing bias
// normalized by USDT notional exposure.
type DirectionSelector struct {
	mu               sync.Mutex
	netNotionalDelta map[string]decimal.Decimal // marketID -> net USDT notional exposure (+ = long/bought, - = short/sold)
	moderateBiasUSDT decimal.Decimal
	heavyBiasUSDT    decimal.Decimal
}

// NewDirectionSelector creates a new direction selector with default thresholds (5k / 25k USDT).
func NewDirectionSelector() *DirectionSelector {
	return NewDirectionSelectorWithThresholds(decimal.NewFromInt(5000), decimal.NewFromInt(25000))
}

// NewDirectionSelectorWithThresholds creates a new direction selector with configurable bias thresholds.
func NewDirectionSelectorWithThresholds(moderateBias, heavyBias decimal.Decimal) *DirectionSelector {
	return &DirectionSelector{
		netNotionalDelta: make(map[string]decimal.Decimal),
		moderateBiasUSDT: moderateBias,
		heavyBiasUSDT:    heavyBias,
	}
}

// SelectSide determines whether to submit a BUY or SELL order.
// Uses an inventory-balancing mean-reversion algorithm normalized to USDT notional exposure.
func (s *DirectionSelector) SelectSide(marketID string, rng ...*rand.Rand) string {
	return s.SelectSideWithContext(marketID, decimal.Zero, decimal.Zero, rng...)
}

// SelectSideWithContext determines BUY/SELL side considering both inventory-balancing bias
// and reference-price gravitational context (divergence between midPrice and refPrice).
func (s *DirectionSelector) SelectSideWithContext(marketID string, midPrice, refPrice decimal.Decimal, rng ...*rand.Rand) string {
	return s.SelectSideWithDepth(marketID, midPrice, refPrice, nil, rng...)
}

// SelectSideWithDepth determines BUY/SELL side considering:
// 1. Inventory-balancing mean-reversion bias (USDT net exposure)
// 2. Order-book microstructure depth imbalance (+/- 8% bias from L1-L3 bids vs asks)
// 3. Reference-price gentle secondary context (+/- 5% divergence nudge)
func (s *DirectionSelector) SelectSideWithDepth(marketID string, midPrice, refPrice decimal.Decimal, depth *redisdepth.DepthSnapshot, rng ...*rand.Rand) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	delta := s.netNotionalDelta[marketID]

	// Determine BUY probability from inventory exposure:
	buyProb := 0.50
	if delta.GreaterThan(s.heavyBiasUSDT) {
		buyProb = 0.25 // Heavy sell bias
	} else if delta.GreaterThan(s.moderateBiasUSDT) {
		buyProb = 0.35 // Moderate sell bias
	} else if delta.LessThan(s.heavyBiasUSDT.Neg()) {
		buyProb = 0.75 // Heavy buy bias
	} else if delta.LessThan(s.moderateBiasUSDT.Neg()) {
		buyProb = 0.65 // Moderate buy bias
	}

	// Reference price context: secondary gentle mean-reversion gravity well (if both prices valid)
	// Softened to +/- 5% so that order-book depth imbalance and inventory remain primary
	if midPrice.GreaterThan(decimal.Zero) && refPrice.GreaterThan(decimal.Zero) {
		diffBps := midPrice.Sub(refPrice).Div(refPrice).Mul(decimal.NewFromInt(10000)).InexactFloat64()
		if diffBps < -15.0 {
			// Market is below external reference -> subtle shade towards BUY (+5%)
			buyProb += 0.05
		} else if diffBps > 15.0 {
			// Market is above external reference -> subtle shade towards SELL (-5%)
			buyProb -= 0.05
		}
	}

	// Order-book microstructure depth imbalance (top-3 levels)
	if depth != nil && len(depth.Bids) > 0 && len(depth.Asks) > 0 {
		var bidDepth, askDepth decimal.Decimal
		for i := 0; i < 3 && i < len(depth.Bids); i++ {
			bidDepth = bidDepth.Add(depth.Bids[i].Quantity)
		}
		for i := 0; i < 3 && i < len(depth.Asks); i++ {
			askDepth = askDepth.Add(depth.Asks[i].Quantity)
		}

		total := bidDepth.Add(askDepth)
		if total.GreaterThan(decimal.Zero) {
			// Imbalance ratio in [-1.0, 1.0]: (bids - asks) / (bids + asks)
			// Positive means bid depth >> ask depth (buying support) -> favor BUY
			// Negative means ask depth >> bid depth (selling pressure) -> favor SELL
			imbalance := bidDepth.Sub(askDepth).Div(total).InexactFloat64()
			buyProb += imbalance * 0.08
		}
	}

	// Strict bounding: 15% <= buyProb <= 85%
	if buyProb > 0.85 {
		buyProb = 0.85
	} else if buyProb < 0.15 {
		buyProb = 0.15
	}

	var roll float64
	if len(rng) > 0 && rng[0] != nil {
		roll = rng[0].Float64()
	} else {
		roll = rand.Float64()
	}

	if roll < buyProb {
		return "BUY"
	}
	return "SELL"
}

// RecordFill updates the net notional exposure upon execution.
// notional is denominated in USDT.
func (s *DirectionSelector) RecordFill(marketID, side string, notional decimal.Decimal) {
	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.netNotionalDelta[marketID]
	if side == "BUY" {
		s.netNotionalDelta[marketID] = current.Add(notional)
	} else {
		s.netNotionalDelta[marketID] = current.Sub(notional)
	}
}

// GetNetNotionalDelta returns the current net USDT notional exposure for marketID.
func (s *DirectionSelector) GetNetNotionalDelta(marketID string) decimal.Decimal {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.netNotionalDelta[marketID]
}
