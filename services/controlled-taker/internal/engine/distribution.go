package engine

import (
	"math"
	"math/rand"
	"time"

	"github.com/shopspring/decimal"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
	"tradedrift/services/controlled-taker/internal/config"
)

// HighSweepSeverity classifies the depth impact of a HIGH execution.
type HighSweepSeverity int

const (
	SweepSeverityLight  HighSweepSeverity = iota // L1 only
	SweepSeverityMedium                          // L1 + L2
	SweepSeverityHeavy                           // L1 + L2 + L3+
)

func (s HighSweepSeverity) String() string {
	switch s {
	case SweepSeverityLight:
		return "LIGHT"
	case SweepSeverityMedium:
		return "MEDIUM"
	case SweepSeverityHeavy:
		return "HEAVY"
	default:
		return "UNKNOWN"
	}
}

// SampleLogNormalInterval generates inter-arrival intervals using a Log-Normal distribution (sigma=0.4).
// This distribution provides natural macro breathing pauses (>2x median) while avoiding the severe
// boundary-pinning artifacts observed in Exponential (53% clamped) and Weibull (35% clamped).
func SampleLogNormalInterval(rng *rand.Rand, cfg config.IntervalConfig) time.Duration {
	const sigma = 0.4
	baseSeconds := cfg.BaseInterval.Seconds()
	if baseSeconds <= 0 {
		return cfg.MinInterval
	}
	mu := math.Log(baseSeconds) - (sigma*sigma)/2.0

	var z float64
	if rng != nil {
		z = rng.NormFloat64()
	} else {
		z = rand.NormFloat64()
	}

	rawSeconds := math.Exp(mu + sigma*z)
	interval := time.Duration(rawSeconds * float64(time.Second))

	if interval < cfg.MinInterval {
		return cfg.MinInterval
	}
	if interval > cfg.MaxInterval {
		return cfg.MaxInterval
	}
	return interval
}

// DetermineSweepSeverity evaluates the actual L2 depth impact of an executed order.
// - L1 only -> Light
// - L1 + L2 -> Medium
// - L1 + L2 + L3+ -> Heavy
func DetermineSweepSeverity(filledQty decimal.Decimal, levels []redisdepth.DepthLevel) HighSweepSeverity {
	if len(levels) == 0 || filledQty.LessThanOrEqual(decimal.Zero) {
		return SweepSeverityLight
	}

	l1Qty := levels[0].Quantity
	if filledQty.LessThanOrEqual(l1Qty) {
		return SweepSeverityLight
	}

	if len(levels) == 1 {
		return SweepSeverityLight
	}

	l1PlusL2 := l1Qty.Add(levels[1].Quantity)
	if filledQty.LessThanOrEqual(l1PlusL2) {
		return SweepSeverityMedium
	}

	return SweepSeverityHeavy
}

// SamplePostHighCooldown returns an adaptive pacing cooldown duration based on sweep severity:
// - Light:  15s – 20s
// - Medium: 25s – 35s
// - Heavy:  35s – 50s
func SamplePostHighCooldown(severity HighSweepSeverity, rng *rand.Rand) time.Duration {
	var minDuration, maxDuration time.Duration

	switch severity {
	case SweepSeverityLight:
		minDuration = 15 * time.Second
		maxDuration = 20 * time.Second
	case SweepSeverityMedium:
		minDuration = 25 * time.Second
		maxDuration = 35 * time.Second
	case SweepSeverityHeavy:
		minDuration = 35 * time.Second
		maxDuration = 50 * time.Second
	default:
		minDuration = 15 * time.Second
		maxDuration = 20 * time.Second
	}

	diff := maxDuration - minDuration
	var f float64
	if rng != nil {
		f = rng.Float64()
	} else {
		f = rand.Float64()
	}

	return minDuration + time.Duration(f*float64(diff))
}
