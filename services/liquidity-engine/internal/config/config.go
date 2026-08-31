// Package config loads and validates the Liquidity Engine configuration from environment variables.
// Market configs (tick size, lot size, partition) mirror the Matching Engine's market configuration.
// Both LE and ME read the same BTC_PARTITION / ETH_PARTITION / SOL_PARTITION env vars —
// this shared source of truth prevents routing mismatches.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	platformconfig "tradedrift/platform/config"
)

// MarketConfig holds per-market configuration that mirrors the ME's market.MarketConfig.
// Tick/lot sizes must exactly match the ME's values — the ME rejects orders that violate them.
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
	MinQuote       decimal.Decimal // effective available quote below which bid-side skew
	CriticalQuote  decimal.Decimal
	SpreadBps      int             // base spread in basis points (default 4 bps per level)
	ReferencePrice decimal.Decimal // V1 static reference price
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

	// Health
	HealthPort  string
	MetricsPort string

	// Readiness thresholds
	MinReadyBids int
	MinReadyAsks int
}

// Load reads all configuration from environment variables.
// Returns an error if any required variable is missing or invalid.
func Load() (Config, error) {
	rawBrokers := platformconfig.GetEnv("KAFKA_BROKERS", "localhost:9092")
	brokers := strings.Split(rawBrokers, ",")
	for i := range brokers {
		brokers[i] = strings.TrimSpace(brokers[i])
	}

	btcPart, err := platformconfig.GetEnvAsInt("BTC_PARTITION", 0)
	if err != nil {
		return Config{}, fmt.Errorf("BTC_PARTITION: %w", err)
	}
	ethPart, err := platformconfig.GetEnvAsInt("ETH_PARTITION", 1)
	if err != nil {
		return Config{}, fmt.Errorf("ETH_PARTITION: %w", err)
	}
	solPart, err := platformconfig.GetEnvAsInt("SOL_PARTITION", 2)
	if err != nil {
		return Config{}, fmt.Errorf("SOL_PARTITION: %w", err)
	}

	btcRef, err := getEnvDecimal("BTC_REFERENCE_PRICE", "96450.00")
	if err != nil {
		return Config{}, fmt.Errorf("BTC_REFERENCE_PRICE: %w", err)
	}
	ethRef, err := getEnvDecimal("ETH_REFERENCE_PRICE", "2780.50")
	if err != nil {
		return Config{}, fmt.Errorf("ETH_REFERENCE_PRICE: %w", err)
	}
	solRef, err := getEnvDecimal("SOL_REFERENCE_PRICE", "188.20")
	if err != nil {
		return Config{}, fmt.Errorf("SOL_REFERENCE_PRICE: %w", err)
	}

	cancelRetry, err := platformconfig.GetEnvAsInt("CANCEL_RETRY_LIMIT", 3)
	if err != nil {
		return Config{}, fmt.Errorf("CANCEL_RETRY_LIMIT: %w", err)
	}
	meThreshold, err := platformconfig.GetEnvAsInt("ME_LIVENESS_THRESHOLD", 3)
	if err != nil {
		return Config{}, fmt.Errorf("ME_LIVENESS_THRESHOLD: %w", err)
	}
	minReadyBids, err := platformconfig.GetEnvAsInt("MIN_READY_BIDS", 6)
	if err != nil {
		return Config{}, fmt.Errorf("MIN_READY_BIDS: %w", err)
	}
	minReadyAsks, err := platformconfig.GetEnvAsInt("MIN_READY_ASKS", 6)
	if err != nil {
		return Config{}, fmt.Errorf("MIN_READY_ASKS: %w", err)
	}

	walletRefresh, err := platformconfig.GetEnvAsDuration("WALLET_REFRESH_INTERVAL", 15*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("WALLET_REFRESH_INTERVAL: %w", err)
	}
	maxBalStaleness, err := platformconfig.GetEnvAsDuration("MAX_BALANCE_STALENESS", 60*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("MAX_BALANCE_STALENESS: %w", err)
	}
	reconcileInterval, err := platformconfig.GetEnvAsDuration("RECONCILE_INTERVAL", 30*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("RECONCILE_INTERVAL: %w", err)
	}
	maxOrderStaleness, err := platformconfig.GetEnvAsDuration("MAX_ORDER_STATE_STALENESS", 90*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("MAX_ORDER_STATE_STALENESS: %w", err)
	}
	pendingTimeout, err := platformconfig.GetEnvAsDuration("PENDING_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("PENDING_TIMEOUT: %w", err)
	}
	cancellingTimeout, err := platformconfig.GetEnvAsDuration("CANCELLING_TIMEOUT", 30*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("CANCELLING_TIMEOUT: %w", err)
	}
	debounce, err := platformconfig.GetEnvAsDuration("TARGETED_RECONCILE_DEBOUNCE", 200*time.Millisecond)
	if err != nil {
		return Config{}, fmt.Errorf("TARGETED_RECONCILE_DEBOUNCE: %w", err)
	}

	cfg := Config{
		KafkaBrokers:   brokers,
		KafkaGroupID:   platformconfig.GetEnv("KAFKA_GROUP_ID", "liquidity-engine-group"),
		WalletGRPCAddr: platformconfig.GetEnv("WALLET_GRPC_ADDR", "localhost:50052"),
		OrderGRPCAddr:  platformconfig.GetEnv("ORDER_GRPC_ADDR", "localhost:50053"),
		MEHTTPAddr:     platformconfig.GetEnv("ME_HTTP_ADDR", "http://localhost:8082"),

		WalletRefreshInterval:     walletRefresh,
		MaxBalanceStaleness:       maxBalStaleness,
		ReconcileInterval:         reconcileInterval,
		MaxOrderStateStaleness:    maxOrderStaleness,
		PendingTimeout:            pendingTimeout,
		CancellingTimeout:         cancellingTimeout,
		CancelRetryLimit:          cancelRetry,
		MELivenessThreshold:       meThreshold,
		TargetedReconcileDebounce: debounce,

		HealthPort:   platformconfig.GetEnv("HEALTH_PORT", "8080"),
		MetricsPort:  platformconfig.GetEnv("METRICS_PORT", "9090"),
		MinReadyBids: minReadyBids,
		MinReadyAsks: minReadyAsks,

		Markets: []MarketConfig{
			{
				// BTC-USDT — tick=0.01, lot=0.00001 (verified from ME main.go)
				MarketID:       "BTC-USDT",
				BaseAsset:      "BTC",
				QuoteAsset:     "USDT",
				TickSize:       decimal.RequireFromString("0.01"),
				LotSize:        decimal.RequireFromString("0.00001"),
				Partition:      btcPart,
				LevelCount:     12,
				MinOrderSize:   decimal.RequireFromString("0.00001"),
				MinBase:        decimal.RequireFromString("30"),      // 30 BTC = normal inventory
				CriticalBase:   decimal.RequireFromString("5"),       // 5 BTC = critical
				MinQuote:       decimal.RequireFromString("1000000"), // $1M USDT = normal
				CriticalQuote:  decimal.RequireFromString("100000"),
				SpreadBps:      4,
				ReferencePrice: btcRef,
			},
			{
				// ETH-USDT — tick=0.01, lot=0.0001 (verified from ME main.go)
				MarketID:       "ETH-USDT",
				BaseAsset:      "ETH",
				QuoteAsset:     "USDT",
				TickSize:       decimal.RequireFromString("0.01"),
				LotSize:        decimal.RequireFromString("0.0001"),
				Partition:      ethPart,
				LevelCount:     12,
				MinOrderSize:   decimal.RequireFromString("0.0001"),
				MinBase:        decimal.RequireFromString("100"),
				CriticalBase:   decimal.RequireFromString("10"),
				MinQuote:       decimal.RequireFromString("100000"),
				CriticalQuote:  decimal.RequireFromString("10000"),
				SpreadBps:      4,
				ReferencePrice: ethRef,
			},
			{
				// SOL-USDT — tick=0.001, lot=0.01 (verified from ME main.go)
				MarketID:       "SOL-USDT",
				BaseAsset:      "SOL",
				QuoteAsset:     "USDT",
				TickSize:       decimal.RequireFromString("0.001"),
				LotSize:        decimal.RequireFromString("0.01"),
				Partition:      solPart,
				LevelCount:     12,
				MinOrderSize:   decimal.RequireFromString("0.01"),
				MinBase:        decimal.RequireFromString("500"),
				CriticalBase:   decimal.RequireFromString("50"),
				MinQuote:       decimal.RequireFromString("20000"),
				CriticalQuote:  decimal.RequireFromString("2000"),
				SpreadBps:      4,
				ReferencePrice: solRef,
			},
		},
	}
	return cfg, nil
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

// Helper for decimal environment variable parsing
func getEnvDecimal(key, fallback string) (decimal.Decimal, error) {
	v := platformconfig.GetEnv(key, fallback)
	d, err := decimal.NewFromString(v)
	if err != nil {
		return decimal.Zero, fmt.Errorf("must be decimal, got %q: %w", v, err)
	}
	return d, nil
}
