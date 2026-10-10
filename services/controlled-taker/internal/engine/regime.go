package engine

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	"tradedrift/services/controlled-taker/internal/config"
)

// MarketRegime represents a macro activity state for a market.
type MarketRegime string

const (
	RegimeQuiet  MarketRegime = "QUIET"
	RegimeNormal MarketRegime = "NORMAL"
	RegimeActive MarketRegime = "ACTIVE"
	RegimeBurst  MarketRegime = "BURST"
)

// RegimeConfig defines the behavioral parameters for a specific regime.
type RegimeConfig struct {
	DwellMin   time.Duration
	DwellMax   time.Duration
	Interval   config.IntervalConfig
	WeightLow  float64
	WeightMid  float64
	WeightHigh float64
}

// DefaultRegimeConfigs returns the authoritative configuration for all regimes.
func DefaultRegimeConfigs() map[MarketRegime]RegimeConfig {
	return map[MarketRegime]RegimeConfig{
		RegimeQuiet: {
			DwellMin: 2 * time.Minute,
			DwellMax: 5 * time.Minute,
			Interval: config.IntervalConfig{
				MinInterval:  10 * time.Second,
				BaseInterval: 20 * time.Second,
				MaxInterval:  45 * time.Second,
			},
			WeightLow:  0.85,
			WeightMid:  0.15,
			WeightHigh: 0.00,
		},
		RegimeNormal: {
			DwellMin: 5 * time.Minute,
			DwellMax: 15 * time.Minute,
			Interval: config.IntervalConfig{
				MinInterval:  2 * time.Second,
				BaseInterval: 5 * time.Second,
				MaxInterval:  10 * time.Second,
			},
			WeightLow:  0.65,
			WeightMid:  0.30,
			WeightHigh: 0.05,
		},
		RegimeActive: {
			DwellMin: 1 * time.Minute,
			DwellMax: 3 * time.Minute,
			Interval: config.IntervalConfig{
				MinInterval:  2 * time.Second,
				BaseInterval: 4 * time.Second,
				MaxInterval:  8 * time.Second,
			},
			WeightLow:  0.40,
			WeightMid:  0.45,
			WeightHigh: 0.15,
		},
		RegimeBurst: {
			DwellMin: 20 * time.Second,
			DwellMax: 45 * time.Second,
			Interval: config.IntervalConfig{
				MinInterval:  800 * time.Millisecond,
				BaseInterval: 1800 * time.Millisecond,
				MaxInterval:  3500 * time.Millisecond,
			},
			WeightLow:  0.20,
			WeightMid:  0.50,
			WeightHigh: 0.30,
		},
	}
}

// RegimeManager tracks and manages the macro activity state for one market.
// It is purely responsible for regime state, dwell timing, and profile weights.
type RegimeManager struct {
	currentRegime MarketRegime
	dwellUntil    time.Time
	configs       map[MarketRegime]RegimeConfig
}

// RequiredRegimes defines the complete set of supported macro activity regimes.
var RequiredRegimes = []MarketRegime{RegimeQuiet, RegimeNormal, RegimeActive, RegimeBurst}

// ValidateRegimeConfigs strictly validates that all required macro regimes exist,
// have non-zero bounded dwell timers, valid positive interval bounds, and non-negative weights with positive sum.
func ValidateRegimeConfigs(configs map[MarketRegime]RegimeConfig) error {
	if configs == nil {
		return fmt.Errorf("regime configurations cannot be nil")
	}

	if len(configs) != len(RequiredRegimes) {
		return fmt.Errorf("expected exactly %d regime configurations, got %d", len(RequiredRegimes), len(configs))
	}

	for _, reg := range RequiredRegimes {
		cfg, exists := configs[reg]
		if !exists {
			return fmt.Errorf("missing required regime configuration for %s", reg)
		}

		if cfg.DwellMin <= 0 {
			return fmt.Errorf("regime %s DwellMin must be > 0, got %s", reg, cfg.DwellMin)
		}
		if cfg.DwellMin > cfg.DwellMax {
			return fmt.Errorf("regime %s DwellMin (%s) cannot exceed DwellMax (%s)", reg, cfg.DwellMin, cfg.DwellMax)
		}

		if cfg.Interval.MinInterval <= 0 {
			return fmt.Errorf("regime %s MinInterval must be > 0, got %s", reg, cfg.Interval.MinInterval)
		}
		if cfg.Interval.MinInterval > cfg.Interval.BaseInterval {
			return fmt.Errorf("regime %s MinInterval (%s) cannot exceed BaseInterval (%s)", reg, cfg.Interval.MinInterval, cfg.Interval.BaseInterval)
		}
		if cfg.Interval.BaseInterval > cfg.Interval.MaxInterval {
			return fmt.Errorf("regime %s BaseInterval (%s) cannot exceed MaxInterval (%s)", reg, cfg.Interval.BaseInterval, cfg.Interval.MaxInterval)
		}

		if math.IsNaN(cfg.WeightLow) || math.IsInf(cfg.WeightLow, 0) || cfg.WeightLow < 0 {
			return fmt.Errorf("regime %s WeightLow must be finite and non-negative, got %f", reg, cfg.WeightLow)
		}
		if math.IsNaN(cfg.WeightMid) || math.IsInf(cfg.WeightMid, 0) || cfg.WeightMid < 0 {
			return fmt.Errorf("regime %s WeightMid must be finite and non-negative, got %f", reg, cfg.WeightMid)
		}
		if math.IsNaN(cfg.WeightHigh) || math.IsInf(cfg.WeightHigh, 0) || cfg.WeightHigh < 0 {
			return fmt.Errorf("regime %s WeightHigh must be finite and non-negative, got %f", reg, cfg.WeightHigh)
		}

		sum := cfg.WeightLow + cfg.WeightMid + cfg.WeightHigh
		if sum <= 0 {
			return fmt.Errorf("regime %s total profile weight must be > 0, got %f", reg, sum)
		}
	}

	return nil
}

// NewRegimeManager initializes a RegimeManager starting in RegimeNormal.
func NewRegimeManager(now time.Time, rng *rand.Rand, configs map[MarketRegime]RegimeConfig) *RegimeManager {
	if configs == nil {
		configs = DefaultRegimeConfigs()
	}
	if err := ValidateRegimeConfigs(configs); err != nil {
		panic(fmt.Sprintf("invalid regime configuration: %v", err))
	}
	rm := &RegimeManager{
		currentRegime: RegimeNormal,
		configs:       configs,
	}
	rm.dwellUntil = now.Add(rm.sampleDwell(RegimeNormal, rng))
	return rm
}

// CurrentRegime returns the currently active regime.
func (rm *RegimeManager) CurrentRegime() MarketRegime {
	return rm.currentRegime
}

// ActiveConfig returns the RegimeConfig for the current regime.
func (rm *RegimeManager) ActiveConfig() RegimeConfig {
	return rm.configs[rm.currentRegime]
}

// ActiveIntervalConfig returns the interval configuration for the current regime.
func (rm *RegimeManager) ActiveIntervalConfig() config.IntervalConfig {
	return rm.configs[rm.currentRegime].Interval
}

// DwellUntil returns the timestamp when the current regime dwell timer expires.
func (rm *RegimeManager) DwellUntil() time.Time {
	return rm.dwellUntil
}

// SetRegime forces a specific regime and dwell duration (primarily for deterministic testing).
func (rm *RegimeManager) SetRegime(regime MarketRegime, dwellUntil time.Time) {
	rm.currentRegime = regime
	rm.dwellUntil = dwellUntil
}

// SetConfigs overrides the regime configurations (primarily for deterministic testing).
func (rm *RegimeManager) SetConfigs(configs map[MarketRegime]RegimeConfig) {
	rm.configs = configs
}

// Tick evaluates whether the dwell timer has expired and triggers a Markov transition if so.
// Returns the active regime and a boolean indicating if the dwell timer expired and a transition was evaluated.
func (rm *RegimeManager) Tick(now time.Time, rng *rand.Rand) (MarketRegime, bool) {
	if now.Before(rm.dwellUntil) {
		return rm.currentRegime, false
	}

	prev := rm.currentRegime
	next := rm.transition(prev, rng)
	rm.currentRegime = next
	rm.dwellUntil = now.Add(rm.sampleDwell(next, rng))
	return next, true
}

// transition implements the approved Markov transition matrix:
//   QUIET   -> QUIET (40%) / NORMAL (60%)
//   NORMAL  -> QUIET (25%) / NORMAL (55%) / ACTIVE (20%)
//   ACTIVE  -> NORMAL (40%) / ACTIVE (40%) / BURST (20%)
//   BURST   -> ACTIVE (65%) / NORMAL (35%)
func (rm *RegimeManager) transition(current MarketRegime, rng *rand.Rand) MarketRegime {
	var r float64
	if rng != nil {
		r = rng.Float64()
	} else {
		r = rand.Float64()
	}

	switch current {
	case RegimeQuiet:
		if r < 0.40 {
			return RegimeQuiet
		}
		return RegimeNormal

	case RegimeNormal:
		if r < 0.25 {
			return RegimeQuiet
		}
		if r < 0.80 {
			return RegimeNormal
		}
		return RegimeActive

	case RegimeActive:
		if r < 0.40 {
			return RegimeNormal
		}
		if r < 0.80 {
			return RegimeActive
		}
		return RegimeBurst

	case RegimeBurst:
		if r < 0.65 {
			return RegimeActive
		}
		return RegimeNormal

	default:
		return RegimeNormal
	}
}

func (rm *RegimeManager) sampleDwell(regime MarketRegime, rng *rand.Rand) time.Duration {
	cfg := rm.configs[regime]
	diff := cfg.DwellMax - cfg.DwellMin
	if diff <= 0 {
		return cfg.DwellMin
	}
	var f float64
	if rng != nil {
		f = rng.Float64()
	} else {
		f = rand.Float64()
	}
	return cfg.DwellMin + time.Duration(f*float64(diff))
}

// ChooseProfile samples a profile using the current regime's probability distribution,
// renormalizing probabilities if MID or HIGH are currently ineligible (e.g. in cooldown).
func (rm *RegimeManager) ChooseProfile(rng *rand.Rand, canMid, canHigh bool) config.ProfileType {
	cfg := rm.ActiveConfig()
	wLow, wMid, wHigh := cfg.WeightLow, cfg.WeightMid, cfg.WeightHigh

	if !canHigh {
		wHigh = 0.0
	}
	if !canMid {
		wMid = 0.0
	}

	total := wLow + wMid + wHigh
	if total <= 0.0 {
		return config.ProfileLow
	}

	var roll float64
	if rng != nil {
		roll = rng.Float64() * total
	} else {
		roll = rand.Float64() * total
	}

	if roll < wLow {
		return config.ProfileLow
	}
	if roll < wLow+wMid {
		return config.ProfileMid
	}
	return config.ProfileHigh
}
