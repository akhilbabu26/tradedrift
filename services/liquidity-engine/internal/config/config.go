// Package config loads and validates the Liquidity Engine configuration from environment variables.
// Market configs (tick size, lot size, partition) mirror the Matching Engine's market configuration.
// Both LE and ME read the same BTC_PARTITION / ETH_PARTITION / SOL_PARTITION env vars —
// this shared source of truth prevents routing mismatches.
package config

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// ── Zone configuration ───────────────────────────────────────────────────────

// ZoneBand defines the price distance range for one liquidity zone.
// Distances are expressed in basis points (bps) relative to the current reference price.
//
//	BID zone: price = ref / (1 + bps/10000)  →  further below ref at higher bps
//	ASK zone: price = ref * (1 + bps/10000)  →  further above ref at higher bps
//
// Count is how many levels occupy this zone (LOW=7, MID=4, HIGH=1 per the 7/4/1 design).
type ZoneBand struct {
	MinBps int // minimum distance from ref (inclusive)
	MaxBps int // maximum distance from ref (exclusive)
	Count  int // number of levels in this zone
}

// ZoneConfig groups the three zones for one side of the order book.
type ZoneConfig struct {
	Low  ZoneBand
	Mid  ZoneBand
	High ZoneBand
}

// TotalLevels returns the sum of levels across all zones.
func (z ZoneConfig) TotalLevels() int {
	return z.Low.Count + z.Mid.Count + z.High.Count
}

// DefaultZoneConfig returns the 7/4/1 zone split with production-safe bps defaults.
// LOW : 5–50 bps  (7 levels)   — tightly around the reference price
// MID : 50–150 bps (4 levels)  — moderate distance
// HIGH: 150–300 bps (1 level)  — outer sentinel
func DefaultZoneConfig() ZoneConfig {
	return ZoneConfig{
		Low:  ZoneBand{MinBps: 5, MaxBps: 50, Count: 7},
		Mid:  ZoneBand{MinBps: 50, MaxBps: 150, Count: 4},
		High: ZoneBand{MinBps: 150, MaxBps: 300, Count: 1},
	}
}

// ── Repricing policy ─────────────────────────────────────────────────────────

// RepricingConfig controls how aggressively the LE responds to reference-price movement.
//
// Three-tier policy:
//
//	Movement < SmallBps  → KEEP: no repricing, existing ladder is fine
//	Movement < LargeBps  → MEDIUM: reprice outermost stale levels, up to MaxBatch per cycle
//	Movement ≥ LargeBps  → LARGE: controlled full-ladder regeneration over multiple cycles
//
// OrderLifetime is how long a RESTING level is kept before being expired and replaced
// with a fresh generation at a newly randomised price within its zone.
type RepricingConfig struct {
	SmallBps      int           // movement below this → no action (default: 10 bps)
	LargeBps      int           // movement at or above this → full regeneration (default: 100 bps)
	MaxBatch      int           // max levels repriced per 60-second evaluation cycle (default: 4)
	OrderLifetime time.Duration // max age of a resting level before expiry-replace (default: 30m)
}

// ── RefPrice provider config ──────────────────────────────────────────────────

// RefPriceConfig controls the shared platform/refprice.Provider that is embedded in the LE.
// Seeds are startup fallbacks used before the first live API response arrives.
type RefPriceConfig struct {
	APIURL          string          // CoinGecko base URL (default: https://api.coingecko.com/api/v3)
	APIKey          string          // Optional CoinGecko API key
	Plan            string          // Optional CoinGecko API plan: "demo" or "pro" (default: "demo")
	RefreshInterval time.Duration   // polling cadence (default: 30s)
	StaleThreshold  time.Duration   // FRESH→STALE transition (default: 5m)
	PauseThreshold  time.Duration   // STALE→PAUSED transition (default: 10m)
	FetchTimeout    time.Duration   // per-request HTTP timeout (default: 5s)
	BTCSeed         decimal.Decimal // startup seed for BTC-USDT (from BTC_REFERENCE_PRICE)
	ETHSeed         decimal.Decimal // startup seed for ETH-USDT (from ETH_REFERENCE_PRICE)
	SOLSeed         decimal.Decimal // startup seed for SOL-USDT (from SOL_REFERENCE_PRICE)
}

// SeedForMarket returns the configured startup seed price for marketID, or decimal.Zero if unconfigured.
func (c *RefPriceConfig) SeedForMarket(marketID string) decimal.Decimal {
	if c == nil {
		return decimal.Zero
	}
	switch marketID {
	case "BTC-USDT":
		return c.BTCSeed
	case "ETH-USDT":
		return c.ETHSeed
	case "SOL-USDT":
		return c.SOLSeed
	default:
		return decimal.Zero
	}
}

// ── MarketConfig ──────────────────────────────────────────────────────────────

// MarketConfig holds per-market configuration that mirrors the ME's market.MarketConfig.
// Tick/lot sizes must exactly match the ME's values — the ME rejects orders that violate them.
//
// MIGRATION NOTE: SpreadBps and ReferencePrice are V1 static fields, kept for backward
// compatibility. They are superseded by BidZones/AskZones and the platform/refprice
// Provider respectively. Do not add new code that reads SpreadBps or ReferencePrice.
type MarketConfig struct {
	MarketID        string
	BaseAsset       string
	QuoteAsset      string
	TickSize        decimal.Decimal // minimum price increment (ME enforces this)
	LotSize         decimal.Decimal // minimum quantity increment (ME enforces this)
	Partition       int             // Kafka partition — must equal ME's partition for this market
	LevelCount      int             // number of bid + ask levels to maintain (default 12 each)
	MinOrderSize    decimal.Decimal // minimum resting quantity before treating as consumed
	MinBase         decimal.Decimal // effective available base below which skew to LOW
	CriticalBase    decimal.Decimal // effective available base below which skew to CRITICAL
	MinQuote        decimal.Decimal // effective available quote below which bid-side skew
	CriticalQuote   decimal.Decimal

	// Deprecated V1 fields — superseded by dynamic pricing. Kept for backward compat.
	SpreadBps      int             // V1: flat bps spacing. Unused when BidZones/AskZones are set.
	ReferencePrice decimal.Decimal // Seed/fallback reference price used during startup bootstrap or if refprice Provider is offline.

	// ── Dynamic pricing (Phase 3+) ──────────────────────────────────────────

	// BidZones / AskZones define the LOW/MID/HIGH price bands for each side.
	// Zero value → DefaultZoneConfig() is used by GenerateZonedDesired().
	BidZones ZoneConfig
	AskZones ZoneConfig

	// QtyPerLevel is the quantity placed at each level for this market.
	// Replaces the hardcoded switch statement in pricing/ladder.go levelQuantity().
	// Zero value → falls back to the hardcoded defaults for backward compat.
	QtyPerLevel decimal.Decimal

	// Capital budget: maximum total notional the LE is allowed to deploy per side.
	// These caps are checked in applyCreate() before each order is submitted.
	// Zero value → no budget cap applied (default for backward compat).
	MaxBidExposureUSDT decimal.Decimal // max total USDT committed across all bid levels
	MaxAskExposureBase decimal.Decimal // max total base committed across all ask levels

	// Repricing policy for this market.
	// Zero value → RepricingConfig with SmallBps=10, LargeBps=100, MaxBatch=4, OrderLifetime=30m.
	Repricing RepricingConfig
}

// Config is the full LE configuration.
type Config struct {
	// Kafka
	KafkaBrokers []string
	KafkaGroupID string

	// External services
	WalletGRPCAddr string
	OrderGRPCAddr  string
	MEHTTPAddr     string
	RedisAddr      string

	// Markets
	Markets []MarketConfig

	// Timing
	WalletRefreshInterval     time.Duration
	MaxBalanceStaleness       time.Duration
	ReconcileInterval         time.Duration
	MaxOrderStateStaleness    time.Duration
	PendingTimeout            time.Duration
	CancellingTimeout         time.Duration
	CancelRetryLimit          int
	MELivenessThreshold       int
	TargetedReconcileDebounce time.Duration
	OrphanCancelCooldown      time.Duration

	// Health
	HealthPort  string
	MetricsPort string

	// Readiness thresholds
	MinReadyBids int
	MinReadyAsks int

	// Dynamic pricing (Phase 3+)
	RefPrice RefPriceConfig
}


// ForMarket returns the MarketConfig for the given market ID, or nil if not found.
func (c *Config) ForMarket(marketID string) *MarketConfig {
	for i := range c.Markets {
		if c.Markets[i].MarketID == marketID {
			return &c.Markets[i]
		}
	}
	return nil
}

// ValidatePartitions verifies that LE partition assignments are valid (no duplicates).
func (c *Config) ValidatePartitions() error {
	seen := map[int]string{}
	for _, m := range c.Markets {
		if prev, ok := seen[m.Partition]; ok {
			return fmt.Errorf("partition %d assigned to both %s and %s — check BTC/ETH/SOL_PARTITION env vars", m.Partition, prev, m.MarketID)
		}
		seen[m.Partition] = m.MarketID
	}
	return nil
}

// PartitionFor returns the Kafka partition for the given market ID.
func (c *Config) PartitionFor(marketID string) int {
	mc := c.ForMarket(marketID)
	if mc == nil {
		return -1
	}
	return mc.Partition
}
