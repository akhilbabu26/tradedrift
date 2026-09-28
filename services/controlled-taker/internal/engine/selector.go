package engine

import (
	"math/rand"
	"sync"

	"github.com/shopspring/decimal"
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
// Uses an inventory-balancing mean-reversion algorithm normalized to USDT notional exposure
// so that BTC, ETH, and SOL behave with consistent economic risk weighting.
func (s *DirectionSelector) SelectSide(marketID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	delta := s.netNotionalDelta[marketID]

	// Determine BUY probability:
	// If netNotionalDelta > 0 (CTS accumulated base asset in USDT terms), bias shifts toward SELL.
	// If netNotionalDelta < 0 (CTS depleted base asset in USDT terms), bias shifts toward BUY.
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

	if rand.Float64() < buyProb {
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
