package engine

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"testing"
	"time"

	"tradedrift/services/controlled-taker/internal/config"
)

type DistributionSampler func(rng *rand.Rand, cfg config.IntervalConfig) time.Duration

// BoundedJitterSampler implements the current production baseline.
func BoundedJitterSampler(rng *rand.Rand, cfg config.IntervalConfig) time.Duration {
	f := rng.Float64()
	jitterPercent := (f * 0.50) - 0.25 // -25% to +25%
	interval := time.Duration(float64(cfg.BaseInterval) * (1.0 + jitterPercent))
	if interval < cfg.MinInterval {
		return cfg.MinInterval
	}
	if interval > cfg.MaxInterval {
		return cfg.MaxInterval
	}
	return interval
}

// ExponentialSampler implements standard memoryless Poisson inter-arrival sampling.
func ExponentialSampler(rng *rand.Rand, cfg config.IntervalConfig) time.Duration {
	u := rng.Float64()
	for u == 0.0 {
		u = rng.Float64()
	}
	// t = -BaseInterval * ln(u)
	raw := -float64(cfg.BaseInterval) * math.Log(u)
	interval := time.Duration(raw)
	if interval < cfg.MinInterval {
		return cfg.MinInterval
	}
	if interval > cfg.MaxInterval {
		return cfg.MaxInterval
	}
	return interval
}

// WeibullSampler implements Weibull inter-arrival sampling with shape k=1.5.
func WeibullSampler(rng *rand.Rand, cfg config.IntervalConfig) time.Duration {
	const k = 1.5
	// Mean of Weibull is lambda * Gamma(1 + 1/k).
	// For k = 1.5, Gamma(5/3) = math.Gamma(1.0 + 1.0/1.5) approx 0.9027452929509337
	gammaVal := math.Gamma(1.0 + 1.0/k)
	lambda := float64(cfg.BaseInterval) / gammaVal

	u := rng.Float64()
	for u == 0.0 || u == 1.0 {
		u = rng.Float64()
	}
	// t = lambda * (-ln(1 - u))^(1/k)
	raw := lambda * math.Pow(-math.Log(1.0-u), 1.0/k)
	interval := time.Duration(raw)
	if interval < cfg.MinInterval {
		return cfg.MinInterval
	}
	if interval > cfg.MaxInterval {
		return cfg.MaxInterval
	}
	return interval
}

// LogNormalSampler implements Log-Normal sampling with sigma=0.4.
func LogNormalSampler(rng *rand.Rand, cfg config.IntervalConfig) time.Duration {
	const sigma = 0.4
	// Mean is exp(mu + sigma^2 / 2) -> mu = ln(BaseInterval) - sigma^2 / 2
	baseSeconds := cfg.BaseInterval.Seconds()
	mu := math.Log(baseSeconds) - (sigma*sigma)/2.0

	z := rng.NormFloat64()
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

type DistributionStats struct {
	Name            string
	Regime          MarketRegime
	Samples         int
	P1              time.Duration
	P5              time.Duration
	P25             time.Duration
	Median          time.Duration
	Mean            time.Duration
	P75             time.Duration
	P95             time.Duration
	P99             time.Duration
	Max             time.Duration
	BoundaryClamped float64 // Fraction of samples at MinInterval or MaxInterval
	CV              float64 // Coefficient of variation = std_dev / mean
	FracGt2xMedian  float64 // Fraction > 2 * median
	FracGt3xMedian  float64 // Fraction > 3 * median
}

func computeStats(name string, regime MarketRegime, durations []time.Duration, cfg config.IntervalConfig) DistributionStats {
	n := len(durations)
	sorted := make([]time.Duration, n)
	copy(sorted, durations)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var sum float64
	boundaryCount := 0
	for _, d := range sorted {
		sum += float64(d)
		if d <= cfg.MinInterval || d >= cfg.MaxInterval {
			boundaryCount++
		}
	}
	meanFloat := sum / float64(n)
	mean := time.Duration(meanFloat)

	var varianceSum float64
	for _, d := range sorted {
		diff := float64(d) - meanFloat
		varianceSum += diff * diff
	}
	stdDev := math.Sqrt(varianceSum / float64(n))
	cv := 0.0
	if meanFloat > 0 {
		cv = stdDev / meanFloat
	}

	percentile := func(p float64) time.Duration {
		idx := int(math.Round(p * float64(n-1)))
		if idx < 0 {
			idx = 0
		}
		if idx >= n {
			idx = n - 1
		}
		return sorted[idx]
	}

	median := percentile(0.50)
	doubleMedian := median * 2
	tripleMedian := median * 3

	gt2xCount := 0
	gt3xCount := 0
	for _, d := range sorted {
		if d > doubleMedian {
			gt2xCount++
		}
		if d > tripleMedian {
			gt3xCount++
		}
	}

	return DistributionStats{
		Name:            name,
		Regime:          regime,
		Samples:         n,
		P1:              percentile(0.01),
		P5:              percentile(0.05),
		P25:             percentile(0.25),
		Median:          median,
		Mean:            mean,
		P75:             percentile(0.75),
		P95:             percentile(0.95),
		P99:             percentile(0.99),
		Max:             sorted[n-1],
		BoundaryClamped: float64(boundaryCount) / float64(n),
		CV:              cv,
		FracGt2xMedian:  float64(gt2xCount) / float64(n),
		FracGt3xMedian:  float64(gt3xCount) / float64(n),
	}
}

// TestBenchmark_ArrivalDistributions evaluates the 4 candidate distributions across regimes.
func TestBenchmark_ArrivalDistributions(t *testing.T) {
	const sampleCount = 100000
	seed := int64(987654321)

	candidates := []struct {
		name    string
		sampler DistributionSampler
	}{
		{"A. Bounded Jitter (Baseline)", BoundedJitterSampler},
		{"B. Exponential", ExponentialSampler},
		{"C. Weibull (k=1.5)", WeibullSampler},
		{"D. Log-Normal (sigma=0.4)", LogNormalSampler},
	}

	regimesToTest := []struct {
		regime MarketRegime
		cfg    config.IntervalConfig
	}{
		{
			regime: RegimeNormal,
			cfg: config.IntervalConfig{
				MinInterval:  4 * time.Second,
				BaseInterval: 8 * time.Second,
				MaxInterval:  16 * time.Second,
			},
		},
		{
			regime: RegimeQuiet,
			cfg: config.IntervalConfig{
				MinInterval:  10 * time.Second,
				BaseInterval: 20 * time.Second,
				MaxInterval:  45 * time.Second,
			},
		},
		{
			regime: RegimeActive,
			cfg: config.IntervalConfig{
				MinInterval:  2 * time.Second,
				BaseInterval: 4 * time.Second,
				MaxInterval:  8 * time.Second,
			},
		},
	}

	for _, reg := range regimesToTest {
		fmt.Printf("\n========================================================================================\n")
		fmt.Printf("REGIME: %s [Min: %v, Base: %v, Max: %v]\n", reg.regime, reg.cfg.MinInterval, reg.cfg.BaseInterval, reg.cfg.MaxInterval)
		fmt.Printf("========================================================================================\n")
		fmt.Printf("%-28s | %-6s | %-6s | %-6s | %-6s | %-6s | %-6s | %-6s | %-6s | %-6s | %-5s | %-7s | %-7s | %-7s\n",
			"Candidate", "p1", "p5", "p25", "med", "mean", "p75", "p95", "p99", "max", "CV", "clamp%", ">2xMed", ">3xMed")
		fmt.Printf("------------------------------------------------------------------------------------------------------------------------------------\n")

		for _, cand := range candidates {
			rng := rand.New(rand.NewSource(seed))
			samples := make([]time.Duration, sampleCount)
			for i := 0; i < sampleCount; i++ {
				samples[i] = cand.sampler(rng, reg.cfg)
			}

			stats := computeStats(cand.name, reg.regime, samples, reg.cfg)
			fmt.Printf("%-28s | %-6v | %-6v | %-6v | %-6v | %-6v | %-6v | %-6v | %-6v | %-6v | %-5.2f | %-6.1f%% | %-6.2f%% | %-6.2f%%\n",
				stats.Name,
				stats.P1.Round(time.Millisecond),
				stats.P5.Round(time.Millisecond),
				stats.P25.Round(time.Millisecond),
				stats.Median.Round(time.Millisecond),
				stats.Mean.Round(time.Millisecond),
				stats.P75.Round(time.Millisecond),
				stats.P95.Round(time.Millisecond),
				stats.P99.Round(time.Millisecond),
				stats.Max.Round(time.Millisecond),
				stats.CV,
				stats.BoundaryClamped*100.0,
				stats.FracGt2xMedian*100.0,
				stats.FracGt3xMedian*100.0,
			)
		}
	}
}
