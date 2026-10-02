package refprice

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// State represents the freshness of a cached reference price entry.
//
//	Fresh  → price was fetched within StaleThreshold; safe to use for new liquidity
//	Stale  → price is older than StaleThreshold but newer than PauseThreshold;
//	          use last known good — do not destroy existing healthy orders
//	Paused → price is older than PauseThreshold; stop creating new liquidity;
//	          existing RESTING orders must NOT be cancelled
type State int

const (
	StateFresh  State = iota // 0–StaleThreshold
	StateStale               // StaleThreshold–PauseThreshold
	StatePaused              // >PauseThreshold
)

func (s State) String() string {
	switch s {
	case StateFresh:
		return "FRESH"
	case StateStale:
		return "STALE"
	case StatePaused:
		return "PAUSED"
	default:
		return "UNKNOWN"
	}
}

// Entry is a single cached reference price for one market.
//
// Version is a monotonic counter: incremented on every successful fetch for this market.
// It lets downstream components record which reference version they used,
// enabling post-hoc debugging: "why was BID-03 created at this price?"
//
// FetchedAt is the local timestamp at which RefPrice successfully received the external snapshot.
//
// Source identifies where the price came from ("coingecko", "seed", "test").
type Entry struct {
	MarketID  string
	Price     decimal.Decimal
	Version   int64
	FetchedAt time.Time
	Source    string
	State     State
}

// Config controls the Provider's polling and staleness behaviour.
// All thresholds must be set via environment variables; use LoadConfig() for that.
type Config struct {
	// RefreshInterval is how often the background goroutine fetches fresh prices.
	// Default: 30s
	RefreshInterval time.Duration

	// StaleThreshold is the age beyond which an entry transitions FRESH → STALE.
	// LE continues using last-known-good price but logs a warning.
	// Default: 5m
	StaleThreshold time.Duration

	// PauseThreshold is the age beyond which an entry transitions STALE → PAUSED.
	// LE stops creating new liquidity orders.
	// Existing RESTING orders are preserved.
	// Default: 10m
	PauseThreshold time.Duration

	// FetchTimeout is the per-request HTTP timeout passed to the Fetcher.
	// Default: 5s
	FetchTimeout time.Duration

	// AnchorTTL is the expiration set on published Redis anchors (refprice:anchor:{marketID}).
	// Default: 60s
	AnchorTTL time.Duration
}

// Validate ensures provider configuration timing invariants hold.
// Uses a pointer receiver so that the AnchorTTL default backstop assignment
// is not silently discarded (RP-05 fix: value-receiver mutation is a no-op).
func (c *Config) Validate() error {
	if c.RefreshInterval <= 0 {
		return fmt.Errorf("RefreshInterval must be positive, got %v", c.RefreshInterval)
	}
	if c.FetchTimeout <= 0 {
		return fmt.Errorf("FetchTimeout must be positive, got %v", c.FetchTimeout)
	}
	if c.StaleThreshold <= 0 {
		return fmt.Errorf("StaleThreshold must be positive, got %v", c.StaleThreshold)
	}
	if c.PauseThreshold <= c.StaleThreshold {
		return fmt.Errorf("PauseThreshold (%v) must be strictly greater than StaleThreshold (%v)", c.PauseThreshold, c.StaleThreshold)
	}
	// Backstop: AnchorTTL defaults to 60s if caller built Config{} directly without DefaultConfig().
	// DefaultConfig() already sets this; this line is a safety net only.
	if c.AnchorTTL <= 0 {
		c.AnchorTTL = 60 * time.Second
	}
	return nil
}

// DefaultConfig returns production-safe defaults.
func DefaultConfig() Config {
	return Config{
		RefreshInterval: 30 * time.Second,
		StaleThreshold:  5 * time.Minute,
		PauseThreshold:  10 * time.Minute,
		FetchTimeout:    5 * time.Second,
		AnchorTTL:       60 * time.Second,
	}
}

// Provider is the thread-safe reference-price cache.
//
// Single writer: the background Run() goroutine calls Fetcher and updates the cache.
// Multiple readers: Get() is protected by an RWMutex.
//
// INVARIANT: Provider never writes to market_trades, candles, Redis depth,
// or the ME order book. It is read-only data for LE zone calculation.
type Provider struct {
	cfg       Config
	fetcher   Fetcher
	publisher AnchorPublisher
	marketIDs []string
	marketSet map[string]struct{}
	logger    *zap.Logger
	clock     func() time.Time // injectable clock; defaults to time.Now

	mu             sync.RWMutex
	entries        map[string]entry // marketID → internal entry
	marketVersions map[string]int64 // marketID → monotonic version counter (market-scoped)
	lastStates     map[string]State // marketID → last recorded State for transition metrics
	metrics        *Metrics
}

// entry is the internal mutable record; exported Entry is the read-only view.
type entry struct {
	price     decimal.Decimal
	version   int64
	fetchedAt time.Time
	source    string
}

// NewProvider creates a validated, ready-to-use Provider.
// Call Seed() or SeedIfAbsent() to provide startup fallbacks before Run() has had time to fetch.
// Call Run(ctx) to start the background poller.
func NewProvider(cfg Config, fetcher Fetcher, marketIDs []string, logger *zap.Logger) (*Provider, error) {
	// Validate() uses a pointer receiver (RP-05) — pass address of the local copy so
	// the AnchorTTL backstop default is preserved in cfg before it is stored.
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid refprice config: %w", err)
	}
	if fetcher == nil {
		return nil, fmt.Errorf("fetcher cannot be nil")
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	// Deduplicate and validate market IDs into an immutable normalized list (RP-4, RP-05)
	seen := make(map[string]bool, len(marketIDs))
	mIDs := make([]string, 0, len(marketIDs))
	mSet := make(map[string]struct{}, len(marketIDs))
	for _, m := range marketIDs {
		m = strings.TrimSpace(m)
		if m != "" && !seen[m] {
			seen[m] = true
			mIDs = append(mIDs, m)
			mSet[m] = struct{}{}
		}
	}
	if len(mIDs) == 0 {
		return nil, fmt.Errorf("refprice provider requires at least one valid market ID")
	}

	return &Provider{
		cfg:            cfg,
		fetcher:        fetcher,
		marketIDs:      mIDs,
		marketSet:      mSet,
		logger:         logger,
		entries:        make(map[string]entry, len(mIDs)),
		marketVersions: make(map[string]int64, len(mIDs)),
		lastStates:     make(map[string]State, len(mIDs)),
		metrics:        DefaultMetrics(),
	}, nil
}

// WithClock overrides the clock function for deterministic testing (RP-07).
func (p *Provider) WithClock(fn func() time.Time) *Provider {
	if fn != nil {
		p.clock = fn
	}
	return p
}

func (p *Provider) now() time.Time {
	if p.clock != nil {
		return p.clock()
	}
	return time.Now()
}

// stateForAge computes the reference price State from its age (RP-02).
func (p *Provider) stateForAge(age time.Duration) State {
	switch {
	case age >= p.cfg.PauseThreshold:
		return StatePaused
	case age >= p.cfg.StaleThreshold:
		return StateStale
	default:
		return StateFresh
	}
}

// WithMetrics overrides the metrics collector used by the Provider (e.g. for testing).
func (p *Provider) WithMetrics(m *Metrics) *Provider {
	if m != nil {
		p.metrics = m
	}
	return p
}

// WithPublisher attaches an external AnchorPublisher (e.g. RedisAnchorPublisher).
func (p *Provider) WithPublisher(pub AnchorPublisher) *Provider {
	p.publisher = pub
	return p
}

// recordTransition increments Prometheus state metrics only on actual state changes.
func (p *Provider) recordTransition(marketID string, oldState, newState State) {
	if p.metrics == nil || oldState == newState {
		return
	}
	if oldState == StateFresh && newState == StateStale {
		p.metrics.marketStale.WithLabelValues(marketID).Inc()
	} else if (oldState == StateFresh || oldState == StateStale) && newState == StatePaused {
		p.metrics.marketPaused.WithLabelValues(marketID).Inc()
	} else if (oldState == StateStale || oldState == StatePaused) && newState == StateFresh {
		p.metrics.marketRecovery.WithLabelValues(marketID).Inc()
	}
}

// isConfiguredMarket reports whether the given market ID was registered at construction (RP-01, RP-02, RP-06).
func (p *Provider) isConfiguredMarket(marketID string) bool {
	_, ok := p.marketSet[marketID]
	return ok
}

// SeedIfAbsent stores a startup fallback price for a market only if no entry exists yet.
// If a live price (or previous seed) has already been recorded, SeedIfAbsent is a safe no-op.
// Returns true if the market was seeded, false if ignored.
//
// Seed lifecycle contract:
//   Seed → bootstrap fallback
//         ↓
//   temporarily FRESH
//         ↓
//   ages naturally
//         ↓
//   STALE
//         ↓
//   PAUSED
//
// Invariant (RP-02, RP-06): Rejects unconfigured markets.
func (p *Provider) SeedIfAbsent(marketID string, price decimal.Decimal) bool {
	if !p.isConfiguredMarket(marketID) {
		p.logger.Warn("rejecting seed for unconfigured market (RP-02)",
			zap.String("market_id", marketID))
		if p.metrics != nil {
			p.metrics.unknownMarket.WithLabelValues(marketID).Inc()
		}
		return false
	}

	if !price.GreaterThan(decimal.Zero) {
		p.logger.Warn("ignoring zero/negative seed price",
			zap.String("market_id", marketID),
			zap.String("price", price.String()))
		return false
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if existing, exists := p.entries[marketID]; exists {
		p.logger.Info("seed skipped — reference price already exists",
			zap.String("market_id", marketID),
			zap.String("existing_source", existing.source),
			zap.String("existing_price", existing.price.String()))
		return false
	}

	p.marketVersions[marketID]++
	ver := p.marketVersions[marketID]
	p.entries[marketID] = entry{
		price:     price,
		version:   ver,
		fetchedAt: p.now(),
		source:    "seed",
	}
	p.lastStates[marketID] = StateFresh

	p.logger.Info("reference price seeded from startup config",
		zap.String("market_id", marketID),
		zap.String("price", price.String()),
		zap.Int64("version", ver))
	return true
}

// Seed stores a startup fallback price for a market using SeedIfAbsent semantics.
//
// Seed lifecycle contract:
//   Seed → bootstrap fallback
//         ↓
//   temporarily FRESH
//         ↓
//   ages naturally
//         ↓
//   STALE
//         ↓
//   PAUSED
//
// INVARIANT: Seed may initialize an absent market, but must never overwrite a live/external reference.
func (p *Provider) Seed(marketID string, price decimal.Decimal) {
	p.SeedIfAbsent(marketID, price)
}

// Get returns the current Entry for the given market.
// The returned State reflects how fresh the price is at the moment of the call.
// Returns (Entry{}, false) if no price (not even a seed) has been stored yet.
func (p *Provider) Get(marketID string) (Entry, bool) {
	if !p.isConfiguredMarket(marketID) {
		if p.metrics != nil {
			p.metrics.unknownMarket.WithLabelValues(marketID).Inc()
		}
		return Entry{}, false
	}

	p.mu.RLock()
	e, ok := p.entries[marketID]
	if !ok {
		p.mu.RUnlock()
		return Entry{}, false
	}

	age := p.now().Sub(e.fetchedAt)
	state := p.stateForAge(age)
	lastState, hasLast := p.lastStates[marketID]

	// INVARIANT (RP-01): Construct the snapshot completely under RLock.
	// Price, Version, FetchedAt, and State describe one atomic, coherent snapshot.
	snap := Entry{
		MarketID:  marketID,
		Price:     e.price,
		Version:   e.version,
		FetchedAt: e.fetchedAt,
		Source:    e.source,
		State:     state,
	}
	p.mu.RUnlock()

	// Update transition observability metric separately if state changed
	if !hasLast || state != lastState {
		p.mu.Lock()
		prev, exists := p.lastStates[marketID]
		if !exists || prev != state {
			p.recordTransition(marketID, prev, state)
			p.lastStates[marketID] = state
		}
		p.mu.Unlock()
	}

	return snap, true
}

// GetAll returns current entries for all configured markets.
// Useful for logging/health snapshots.
func (p *Provider) GetAll() []Entry {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]Entry, 0, len(p.entries))
	now := p.now()
	for marketID, e := range p.entries {
		age := now.Sub(e.fetchedAt)
		state := p.stateForAge(age)
		result = append(result, Entry{
			MarketID:  marketID,
			Price:     e.price,
			Version:   e.version,
			FetchedAt: e.fetchedAt,
			Source:    e.source,
			State:     state,
		})
	}
	return result
}

// Run starts the background polling loop.
// It blocks until ctx is cancelled.
// Designed to run as a separate goroutine: go p.Run(ctx).
func (p *Provider) Run(ctx context.Context) {
	p.logger.Info("reference price provider started",
		zap.Duration("refresh_interval", p.cfg.RefreshInterval),
		zap.Duration("stale_threshold", p.cfg.StaleThreshold),
		zap.Duration("pause_threshold", p.cfg.PauseThreshold))

	// Attempt an immediate fetch on startup (before the first ticker fires).
	_ = p.fetchAndUpdate(ctx)

	ticker := time.NewTicker(p.cfg.RefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("reference price provider stopped")
			return
		case <-ticker.C:
			_ = p.fetchAndUpdate(ctx)
		}
	}
}

// FetchInitial performs a synchronous, bounded fetch on startup to populate the
// provider cache and Redis anchor prior to the market maker or reconciler quoting.
// It executes the exact same fetchAndUpdate() machinery used by the background polling loop.
func (p *Provider) FetchInitial(ctx context.Context, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = p.cfg.FetchTimeout
	}
	fetchCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return p.fetchAndUpdate(fetchCtx)
}

// fetchAndUpdate calls the Fetcher with a bounded timeout and updates
// the cache for each successfully fetched market.
// Markets not returned by the Fetcher retain their last-known-good entry
// (age continues to increase → eventual STALE/PAUSED transition).
func (p *Provider) fetchAndUpdate(ctx context.Context) error {
	fetchCtx, cancel := context.WithTimeout(ctx, p.cfg.FetchTimeout)
	defer cancel()

	source := "coingecko"
	if s, ok := p.fetcher.(interface{ Source() string }); ok {
		source = s.Source()
	}

	start := p.now()
	prices, err := p.fetcher.Fetch(fetchCtx, p.marketIDs)
	dur := p.now().Sub(start).Seconds()
	if p.metrics != nil {
		p.metrics.fetchDuration.WithLabelValues(source).Observe(dur)
	}

	if err != nil {
		if p.metrics != nil {
			p.metrics.fetchFailure.WithLabelValues(source).Inc()
		}
		p.logger.Warn("reference price fetch failed — retaining last known good",
			zap.Error(err))

		// Check for aging transitions during fetch failure
		now := p.now()
		p.mu.Lock()
		for _, mID := range p.marketIDs {
			if e, ok := p.entries[mID]; ok {
				age := now.Sub(e.fetchedAt)
				s := p.stateForAge(age)
				prev := p.lastStates[mID]
				if prev != s {
					p.recordTransition(mID, prev, s)
					p.lastStates[mID] = s
				}
			}
		}
		p.mu.Unlock()

		// Log current states so operators can see degradation in progress.
		for _, e := range p.GetAll() {
			p.logger.Warn("reference price state after fetch failure",
				zap.String("market_id", e.MarketID),
				zap.String("state", e.State.String()),
				zap.String("price", e.Price.String()),
				zap.Time("fetched_at", e.FetchedAt))
		}
		return err
	}

	if p.metrics != nil {
		p.metrics.fetchSuccess.WithLabelValues(source).Inc()
	}

	now := p.now()
	var updatedEntries []Entry

	p.mu.Lock()
	for marketID, price := range prices {
		// Invariant (RP-01): Provider must reject unconfigured markets returned by Fetcher.
		if !p.isConfiguredMarket(marketID) {
			if p.metrics != nil {
				p.metrics.unknownMarket.WithLabelValues(marketID).Inc()
			}
			p.logger.Warn("fetcher returned price for unconfigured market — ignoring (RP-01)",
				zap.String("market_id", marketID))
			continue
		}

		// INVARIANT: A reference price must always be a finite, strictly positive decimal.
		if !price.GreaterThan(decimal.Zero) {
			p.logger.Warn("fetcher returned invalid price — ignoring",
				zap.String("market_id", marketID),
				zap.String("price", price.String()))
			continue
		}
		p.marketVersions[marketID]++
		ver := p.marketVersions[marketID]
		p.entries[marketID] = entry{
			price:     price,
			version:   ver,
			fetchedAt: now,
			source:    source,
		}

		// Track state transition (e.g. recovery from STALE/PAUSED -> FRESH)
		prev := p.lastStates[marketID]
		if prev != StateFresh {
			p.recordTransition(marketID, prev, StateFresh)
			p.lastStates[marketID] = StateFresh
		}

		updatedEntries = append(updatedEntries, Entry{
			MarketID:  marketID,
			Price:     price,
			Version:   ver,
			FetchedAt: now,
			Source:    source,
			State:     StateFresh,
		})

		p.logger.Info("reference price updated",
			zap.String("market_id", marketID),
			zap.String("price", price.String()),
			zap.Int64("version", ver))
	}

	// For configured markets not updated in this fetch, evaluate aging transitions
	for _, marketID := range p.marketIDs {
		if _, fetched := prices[marketID]; !fetched {
			if e, ok := p.entries[marketID]; ok {
				age := now.Sub(e.fetchedAt)
				s := p.stateForAge(age)
				prev := p.lastStates[marketID]
				if prev != s {
					p.recordTransition(marketID, prev, s)
					p.lastStates[marketID] = s
				}
			}
		}
	}
	p.mu.Unlock()

	// INVARIANT: Publish fresh anchors to Redis so Order Service has an independent,
	// monotonic reference for validating MM orders without coupling to the LE.
	if p.publisher != nil {
		for _, e := range updatedEntries {
			if pubErr := p.publisher.PublishAnchor(ctx, e, p.cfg.AnchorTTL); pubErr != nil {
				p.logger.Warn("failed to publish refprice anchor",
					zap.String("market_id", e.MarketID),
					zap.Error(pubErr))
			}
		}
	}

	// Warn about any markets the fetcher did not return.
	for _, marketID := range p.marketIDs {
		if _, fetched := prices[marketID]; !fetched {
			p.logger.Warn("fetcher did not return price for market — retaining last known good",
				zap.String("market_id", marketID))
		}
	}

	return nil
}

