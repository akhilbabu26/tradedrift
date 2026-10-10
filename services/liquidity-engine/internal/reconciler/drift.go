package reconciler

import (
	"context"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	platformconfig "tradedrift/platform/config"
	"tradedrift/platform/refprice"
	"tradedrift/services/liquidity-engine/internal/config"
)

// DriftConfig governs the Bounded Mean-Reverting Reference Price Drift (Phase 4).
type DriftConfig struct {
	Enabled        bool
	MaxDriftBps    int           // Maximum excursion from external anchor, e.g. 25 bps (±0.25%)
	VolatilityBps  int           // Volatility shock per step, e.g. 4 bps
	ReversionSpeed float64       // Mean-reversion speed pulling back to anchor, e.g. 0.15
	StepCadence    time.Duration // Time between drift evaluations, e.g. 2s
}

// LoadDriftConfigFromEnv loads drift settings from environment variables with safe production defaults.
func LoadDriftConfigFromEnv() DriftConfig {
	enabledStr := strings.ToLower(platformconfig.GetEnv("LE_DRIFT_ENABLED", "false"))
	enabled := enabledStr == "true" || enabledStr == "1" || enabledStr == "yes"

	maxDriftBps, err := platformconfig.GetEnvAsInt("LE_DRIFT_MAX_BPS", 25)
	if err != nil || maxDriftBps <= 0 {
		maxDriftBps = 25
	}

	volatilityBps, err := platformconfig.GetEnvAsInt("LE_DRIFT_VOLATILITY_BPS", 4)
	if err != nil || volatilityBps <= 0 {
		volatilityBps = 4
	}

	revSpeedStr := platformconfig.GetEnv("LE_DRIFT_REVERSION_SPEED", "0.15")
	revSpeed, err := strconv.ParseFloat(revSpeedStr, 64)
	if err != nil || revSpeed <= 0 {
		revSpeed = 0.15
	}

	stepCadence, err := platformconfig.GetEnvAsDuration("LE_DRIFT_STEP_CADENCE", 2*time.Second)
	if err != nil || stepCadence <= 0 {
		stepCadence = 2 * time.Second
	}

	return DriftConfig{
		Enabled:        enabled,
		MaxDriftBps:    maxDriftBps,
		VolatilityBps:  volatilityBps,
		ReversionSpeed: revSpeed,
		StepCadence:    stepCadence,
	}
}

// MarketDrifter tracks the Ornstein-Uhlenbeck drift state for a single market.
type MarketDrifter struct {
	marketID      string
	tickSize      decimal.Decimal
	currentBps    float64
	lastStepAt    time.Time
	driftedPrice  decimal.Decimal
	lastBasePrice decimal.Decimal
}

// Step advances the mean-reverting process and returns the drifted, tick-aligned price.
func (d *MarketDrifter) Step(basePrice decimal.Decimal, now time.Time, cfg DriftConfig, rng *rand.Rand) decimal.Decimal {
	if !basePrice.GreaterThan(decimal.Zero) {
		return basePrice
	}

	// First initialization
	if d.driftedPrice.IsZero() || d.lastBasePrice.IsZero() {
		d.driftedPrice = basePrice
		d.lastBasePrice = basePrice
		d.lastStepAt = now
		d.currentBps = 0
		return basePrice
	}

	// If cadence has not elapsed, return cached drifted price for stability within cycle
	if now.Sub(d.lastStepAt) < cfg.StepCadence {
		return d.driftedPrice
	}

	// If external base price changed (CoinGecko reported a new price), adjust offset relative to new base
	if !basePrice.Equal(d.lastBasePrice) {
		diff := d.driftedPrice.Sub(basePrice)
		ratio := diff.Div(basePrice).Mul(decimal.NewFromInt(10000)).InexactFloat64()
		d.currentBps = ratio
		d.lastBasePrice = basePrice
	}

	// Ornstein-Uhlenbeck equation: dX = -theta * X + sigma * dW
	meanReversion := -cfg.ReversionSpeed * d.currentBps
	shock := (rng.Float64()*2.0 - 1.0) * float64(cfg.VolatilityBps)
	newBps := d.currentBps + meanReversion + shock

	// Hard clamp to [-MaxDriftBps, +MaxDriftBps]
	maxBps := float64(cfg.MaxDriftBps)
	if newBps > maxBps {
		newBps = maxBps
	} else if newBps < -maxBps {
		newBps = -maxBps
	}

	d.currentBps = newBps
	d.lastStepAt = now

	// Calculate new drifted price
	mult := decimal.NewFromFloat(1.0 + newBps/10000.0)
	rawPrice := basePrice.Mul(mult)

	// Tick alignment
	if d.tickSize.GreaterThan(decimal.Zero) {
		steps := rawPrice.Div(d.tickSize).Round(0)
		d.driftedPrice = steps.Mul(d.tickSize)
	} else {
		d.driftedPrice = rawPrice.Round(2)
	}

	return d.driftedPrice
}

// CurrentBps returns the active basis point offset of this drifter.
func (d *MarketDrifter) CurrentBps() float64 {
	return d.currentBps
}

// DriftingProvider wraps a ReferencePriceProvider and applies bounded mean-reverting drift.
type DriftingProvider struct {
	base      ReferencePriceProvider
	cfg       DriftConfig
	drifters  map[string]*MarketDrifter
	mu        sync.Mutex
	rng       *rand.Rand
	logger    *zap.Logger
	publisher refprice.AnchorPublisher
}

// NewDriftingProvider creates a new DriftingProvider.
func NewDriftingProvider(
	base ReferencePriceProvider,
	cfg DriftConfig,
	markets []config.MarketConfig,
	logger *zap.Logger,
) *DriftingProvider {
	if logger == nil {
		logger = zap.NewNop()
	}
	drifters := make(map[string]*MarketDrifter)
	for _, m := range markets {
		drifters[m.MarketID] = &MarketDrifter{
			marketID: m.MarketID,
			tickSize: m.TickSize,
		}
	}
	return &DriftingProvider{
		base:     base,
		cfg:      cfg,
		drifters: drifters,
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
		logger:   logger,
	}
}

// NewDriftingProviderWithRNG creates a DriftingProvider with a deterministic RNG source for testing.
func NewDriftingProviderWithRNG(
	base ReferencePriceProvider,
	cfg DriftConfig,
	markets []config.MarketConfig,
	rng *rand.Rand,
	logger *zap.Logger,
) *DriftingProvider {
	if logger == nil {
		logger = zap.NewNop()
	}
	drifters := make(map[string]*MarketDrifter)
	for _, m := range markets {
		drifters[m.MarketID] = &MarketDrifter{
			marketID: m.MarketID,
			tickSize: m.TickSize,
		}
	}
	return &DriftingProvider{
		base:     base,
		cfg:      cfg,
		drifters: drifters,
		rng:      rng,
		logger:   logger,
	}
}

// WithPublisher attaches an optional Redis anchor publisher to keep Order Service in sync.
func (dp *DriftingProvider) WithPublisher(pub refprice.AnchorPublisher) *DriftingProvider {
	dp.publisher = pub
	return dp
}

// Get returns the reference price entry, drifted if FRESH and enabled.
func (dp *DriftingProvider) Get(marketID string) (refprice.Entry, bool) {
	entry, ok := dp.base.Get(marketID)
	if !ok {
		return entry, false
	}

	if !dp.cfg.Enabled || entry.State != refprice.StateFresh || entry.Price.IsZero() {
		return entry, true
	}

	dp.mu.Lock()
	defer dp.mu.Unlock()

	drifter, exists := dp.drifters[marketID]
	if !exists {
		return entry, true
	}

	prevPrice := entry.Price
	driftedPrice := drifter.Step(entry.Price, time.Now(), dp.cfg, dp.rng)
	entry.Price = driftedPrice
	entry.Source = "drift:" + entry.Source

	if !driftedPrice.Equal(prevPrice) {
		dp.logger.Debug("simulated market anchor drifted",
			zap.String("market_id", marketID),
			zap.String("external_reference_price", prevPrice.String()),
			zap.String("simulated_market_anchor", driftedPrice.String()),
			zap.Float64("offset_bps", drifter.currentBps))

		if dp.publisher != nil {
			_ = dp.publisher.PublishAnchor(context.Background(), entry, 60*time.Second)
		}
	}

	return entry, true
}
