// Package config loads and validates the Controlled Taker Service configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	platformconfig "tradedrift/platform/config"
)

// V1MaxAllowedSlippageBps defines the CTS V1 safety policy upper bound on crossing slippage (500 bps = 5.00%).
// While structurally any non-negative slippage (>= 0) is parseable, CTS V1 strictly enforces this
// policy ceiling to prevent uncontrolled crossing into illiquid order-book tails.
const V1MaxAllowedSlippageBps = 500

// ProfileType represents an activity profile (LOW, MID, HIGH).
type ProfileType string

const (
	ProfileLow  ProfileType = "LOW"
	ProfileMid  ProfileType = "MID"
	ProfileHigh ProfileType = "HIGH"
)

// IntervalConfig defines explicit triple bounds for execution scheduling.
type IntervalConfig struct {
	MinInterval  time.Duration
	BaseInterval time.Duration
	MaxInterval  time.Duration
}

// MarketConfig holds static configuration for a trading pair.
type MarketConfig struct {
	MarketID    string
	BaseAsset   string
	QuoteAsset  string
	TickSize    decimal.Decimal
	LotSize     decimal.Decimal
	MinQuantity decimal.Decimal
	Partition   int
}

// Config is the top-level configuration for CTS.
type Config struct {
	// External services
	OrderGRPCAddr string
	RedisAddr     string

	// Operational
	Enabled bool
	Markets []MarketConfig

	// Safety Boundaries
	MaxOrderNotionalUSDT   decimal.Decimal
	MaxSlippageBps         int
	MaxHourlyVolumeUSDT    decimal.Decimal
	MaxDailyVolumeUSDT     decimal.Decimal
	MaxTradesPerHour       int
	MaxSpreadPercent       decimal.Decimal
	CircuitBreakerFailures int
	WarmupDelay            time.Duration
	HighCooldown           time.Duration
	CrossingDelay          time.Duration
	ResidualGracePeriod    time.Duration
	ResidualPollInterval   time.Duration
	ResidualMaxReadRetries int

	// Inventory Bias Boundaries (USDT notional exposure)
	InventoryModerateBiasUSDT decimal.Decimal
	InventoryHeavyBiasUSDT    decimal.Decimal

	// Profile Timing
	LowInterval  IntervalConfig
	MidInterval  IntervalConfig
	HighInterval IntervalConfig

	// Ports
	HealthPort  string
	MetricsPort string
	LogLevel    string
}

// Load reads all configuration from environment variables with safe defaults.
func Load() (Config, error) {
	enabled := getEnvBool("CTS_ENABLED", true)

	orderGRPC := platformconfig.GetEnv("ORDER_GRPC_ADDR", "localhost:50053")
	redisAddr := platformconfig.GetEnv("REDIS_ADDR", "localhost:6379")
	healthPort := platformconfig.GetEnv("HEALTH_PORT", "8080")
	metricsPort := platformconfig.GetEnv("METRICS_PORT", "9090")
	logLevel := platformconfig.GetEnv("LOG_LEVEL", "info")

	maxNotional, err := getEnvDecimal("MAX_ORDER_NOTIONAL_USDT", "10000.00")
	if err != nil {
		return Config{}, fmt.Errorf("MAX_ORDER_NOTIONAL_USDT: %w", err)
	}

	maxHourly, err := getEnvDecimal("MAX_HOURLY_VOLUME_USDT", "100000.00")
	if err != nil {
		return Config{}, fmt.Errorf("MAX_HOURLY_VOLUME_USDT: %w", err)
	}

	maxDaily, err := getEnvDecimal("MAX_DAILY_VOLUME_USDT", "1500000.00")
	if err != nil {
		return Config{}, fmt.Errorf("MAX_DAILY_VOLUME_USDT: %w", err)
	}

	maxSpread, err := getEnvDecimal("MAX_SPREAD_PERCENT", "0.015") // 1.5%
	if err != nil {
		return Config{}, fmt.Errorf("MAX_SPREAD_PERCENT: %w", err)
	}

	maxSlippage, err := platformconfig.GetEnvAsInt("MAX_SLIPPAGE_BPS", 15) // 15 bps = 0.15%
	if err != nil {
		return Config{}, fmt.Errorf("MAX_SLIPPAGE_BPS: %w", err)
	}

	maxTradesHour, err := platformconfig.GetEnvAsInt("MAX_TRADES_PER_HOUR", 60)
	if err != nil {
		return Config{}, fmt.Errorf("MAX_TRADES_PER_HOUR: %w", err)
	}

	cbFailures, err := platformconfig.GetEnvAsInt("CIRCUIT_BREAKER_FAILURES", 5)
	if err != nil {
		return Config{}, fmt.Errorf("CIRCUIT_BREAKER_FAILURES: %w", err)
	}

	warmupDelay, err := platformconfig.GetEnvAsDuration("WARMUP_DELAY", 30*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("WARMUP_DELAY: %w", err)
	}

	highCooldown, err := platformconfig.GetEnvAsDuration("HIGH_COOLDOWN", 300*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("HIGH_COOLDOWN: %w", err)
	}

	crossingDelay, err := platformconfig.GetEnvAsDuration("CROSSING_DELAY", 300*time.Millisecond)
	if err != nil {
		return Config{}, fmt.Errorf("CROSSING_DELAY: %w", err)
	}

	residualGracePeriod, err := platformconfig.GetEnvAsDuration("CTS_RESIDUAL_GRACE_PERIOD", 500*time.Millisecond)
	if err != nil {
		return Config{}, fmt.Errorf("CTS_RESIDUAL_GRACE_PERIOD: %w", err)
	}

	residualPollInterval, err := platformconfig.GetEnvAsDuration("CTS_RESIDUAL_POLL_INTERVAL", 100*time.Millisecond)
	if err != nil {
		return Config{}, fmt.Errorf("CTS_RESIDUAL_POLL_INTERVAL: %w", err)
	}

	residualMaxReadRetries, err := platformconfig.GetEnvAsInt("CTS_RESIDUAL_MAX_READ_RETRIES", 3)
	if err != nil {
		return Config{}, fmt.Errorf("CTS_RESIDUAL_MAX_READ_RETRIES: %w", err)
	}

	modBias, err := getEnvDecimal("CTS_INVENTORY_MODERATE_BIAS_USDT", "5000.00")
	if err != nil {
		return Config{}, fmt.Errorf("CTS_INVENTORY_MODERATE_BIAS_USDT: %w", err)
	}
	heavyBias, err := getEnvDecimal("CTS_INVENTORY_HEAVY_BIAS_USDT", "25000.00")
	if err != nil {
		return Config{}, fmt.Errorf("CTS_INVENTORY_HEAVY_BIAS_USDT: %w", err)
	}

	// Standard default markets mirroring platform constants
	markets := []MarketConfig{
		{
			MarketID:    "BTC-USDT",
			BaseAsset:   "BTC",
			QuoteAsset:  "USDT",
			TickSize:    decimal.NewFromFloat(0.01),
			LotSize:     decimal.NewFromFloat(0.0001),
			MinQuantity: decimal.NewFromFloat(0.0001),
			Partition:   0,
		},
		{
			MarketID:    "ETH-USDT",
			BaseAsset:   "ETH",
			QuoteAsset:  "USDT",
			TickSize:    decimal.NewFromFloat(0.01),
			LotSize:     decimal.NewFromFloat(0.001),
			MinQuantity: decimal.NewFromFloat(0.001),
			Partition:   1,
		},
		{
			MarketID:    "SOL-USDT",
			BaseAsset:   "SOL",
			QuoteAsset:  "USDT",
			TickSize:    decimal.NewFromFloat(0.001),
			LotSize:     decimal.NewFromFloat(0.01),
			MinQuantity: decimal.NewFromFloat(0.01),
			Partition:   2,
		},
	}

	cfg := Config{
		OrderGRPCAddr:             orderGRPC,
		RedisAddr:                 redisAddr,
		Enabled:                   enabled,
		Markets:                   markets,
		MaxOrderNotionalUSDT:      maxNotional,
		MaxSlippageBps:            maxSlippage,
		MaxHourlyVolumeUSDT:       maxHourly,
		MaxDailyVolumeUSDT:        maxDaily,
		MaxTradesPerHour:          maxTradesHour,
		MaxSpreadPercent:          maxSpread,
		CircuitBreakerFailures:    cbFailures,
		WarmupDelay:               warmupDelay,
		HighCooldown:              highCooldown,
		CrossingDelay:             crossingDelay,
		ResidualGracePeriod:       residualGracePeriod,
		ResidualPollInterval:      residualPollInterval,
		ResidualMaxReadRetries:    residualMaxReadRetries,
		InventoryModerateBiasUSDT: modBias,
		InventoryHeavyBiasUSDT:    heavyBias,
		LowInterval: IntervalConfig{
			MinInterval:  45 * time.Second,
			BaseInterval: 75 * time.Second,
			MaxInterval:  120 * time.Second,
		},
		MidInterval: IntervalConfig{
			MinInterval:  120 * time.Second,
			BaseInterval: 240 * time.Second,
			MaxInterval:  360 * time.Second,
		},
		HighInterval: IntervalConfig{
			MinInterval:  900 * time.Second,
			BaseInterval: 1800 * time.Second,
			MaxInterval:  2700 * time.Second,
		},
		HealthPort:  healthPort,
		MetricsPort: metricsPort,
		LogLevel:    logLevel,
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// Validate checks all configuration constraints and invariants at service initialization.
func (c Config) Validate() error {
	if c.MaxOrderNotionalUSDT.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("MAX_ORDER_NOTIONAL_USDT must be > 0, got %s", c.MaxOrderNotionalUSDT)
	}
	if c.MaxHourlyVolumeUSDT.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("MAX_HOURLY_VOLUME_USDT must be > 0, got %s", c.MaxHourlyVolumeUSDT)
	}
	if c.MaxDailyVolumeUSDT.LessThan(c.MaxHourlyVolumeUSDT) {
		return fmt.Errorf("MAX_DAILY_VOLUME_USDT (%s) cannot be less than MAX_HOURLY_VOLUME_USDT (%s)", c.MaxDailyVolumeUSDT, c.MaxHourlyVolumeUSDT)
	}
	if c.MaxSlippageBps < 0 {
		return fmt.Errorf("MAX_SLIPPAGE_BPS must be >= 0, got %d", c.MaxSlippageBps)
	}
	if c.MaxSlippageBps > V1MaxAllowedSlippageBps {
		return fmt.Errorf("MAX_SLIPPAGE_BPS exceeds CTS V1 safety policy ceiling of %d bps (got %d)", V1MaxAllowedSlippageBps, c.MaxSlippageBps)
	}
	if c.MaxTradesPerHour <= 0 {
		return fmt.Errorf("MAX_TRADES_PER_HOUR must be > 0, got %d", c.MaxTradesPerHour)
	}
	if c.CircuitBreakerFailures <= 0 {
		return fmt.Errorf("CIRCUIT_BREAKER_FAILURES must be > 0, got %d", c.CircuitBreakerFailures)
	}
	if c.MaxSpreadPercent.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("MAX_SPREAD_PERCENT must be > 0, got %s", c.MaxSpreadPercent)
	}
	if c.InventoryModerateBiasUSDT.LessThanOrEqual(decimal.Zero) {
		return fmt.Errorf("CTS_INVENTORY_MODERATE_BIAS_USDT must be > 0, got %s", c.InventoryModerateBiasUSDT)
	}
	if c.InventoryHeavyBiasUSDT.LessThanOrEqual(c.InventoryModerateBiasUSDT) {
		return fmt.Errorf("CTS_INVENTORY_HEAVY_BIAS_USDT (%s) must be greater than ModerateBias (%s)", c.InventoryHeavyBiasUSDT, c.InventoryModerateBiasUSDT)
	}
	if c.WarmupDelay < 0 {
		return fmt.Errorf("WARMUP_DELAY must be >= 0, got %s", c.WarmupDelay)
	}
	if c.HighCooldown < 0 {
		return fmt.Errorf("HIGH_COOLDOWN must be >= 0, got %s", c.HighCooldown)
	}
	if c.ResidualGracePeriod <= 0 {
		return fmt.Errorf("CTS_RESIDUAL_GRACE_PERIOD must be > 0, got %s", c.ResidualGracePeriod)
	}
	if c.ResidualPollInterval <= 0 {
		return fmt.Errorf("CTS_RESIDUAL_POLL_INTERVAL must be > 0, got %s", c.ResidualPollInterval)
	}
	if c.ResidualPollInterval > c.ResidualGracePeriod {
		return fmt.Errorf("CTS_RESIDUAL_POLL_INTERVAL (%s) cannot be greater than CTS_RESIDUAL_GRACE_PERIOD (%s)", c.ResidualPollInterval, c.ResidualGracePeriod)
	}
	if c.ResidualMaxReadRetries < 0 {
		return fmt.Errorf("CTS_RESIDUAL_MAX_READ_RETRIES must be >= 0, got %d", c.ResidualMaxReadRetries)
	}
	if c.HealthPort == "" {
		return errors.New("HEALTH_PORT must not be empty")
	}
	if c.MetricsPort == "" {
		return errors.New("METRICS_PORT must not be empty")
	}
	if len(c.Markets) == 0 {
		return errors.New("markets list must not be empty")
	}

	seenMarkets := make(map[string]struct{}, len(c.Markets))
	for _, m := range c.Markets {
		if m.MarketID == "" {
			return errors.New("market MarketID must not be empty")
		}
		if _, exists := seenMarkets[m.MarketID]; exists {
			return fmt.Errorf("duplicate market ID configured: %s", m.MarketID)
		}
		seenMarkets[m.MarketID] = struct{}{}

		if m.BaseAsset == "" {
			return fmt.Errorf("market %s: BaseAsset must not be empty", m.MarketID)
		}
		if m.QuoteAsset == "" {
			return fmt.Errorf("market %s: QuoteAsset must not be empty", m.MarketID)
		}
		if m.TickSize.LessThanOrEqual(decimal.Zero) {
			return fmt.Errorf("market %s: TickSize must be > 0, got %s", m.MarketID, m.TickSize)
		}
		if m.LotSize.LessThanOrEqual(decimal.Zero) {
			return fmt.Errorf("market %s: LotSize must be > 0, got %s", m.MarketID, m.LotSize)
		}
		if m.MinQuantity.LessThanOrEqual(decimal.Zero) {
			return fmt.Errorf("market %s: MinQuantity must be > 0, got %s", m.MarketID, m.MinQuantity)
		}
		if m.Partition < 0 {
			return fmt.Errorf("market %s: Partition must be >= 0, got %d", m.MarketID, m.Partition)
		}
	}

	for name, interval := range map[string]IntervalConfig{
		"LOW":  c.LowInterval,
		"MID":  c.MidInterval,
		"HIGH": c.HighInterval,
	} {
		if interval.MinInterval <= 0 {
			return fmt.Errorf("%s profile MinInterval must be > 0, got %s", name, interval.MinInterval)
		}
		if interval.MinInterval > interval.BaseInterval {
			return fmt.Errorf("%s profile MinInterval (%s) cannot exceed BaseInterval (%s)", name, interval.MinInterval, interval.BaseInterval)
		}
		if interval.BaseInterval > interval.MaxInterval {
			return fmt.Errorf("%s profile BaseInterval (%s) cannot exceed MaxInterval (%s)", name, interval.BaseInterval, interval.MaxInterval)
		}
	}

	if err := platformconfig.ValidateLogLevel(c.LogLevel); err != nil {
		return err
	}

	return nil
}

func getEnvBool(key string, def bool) bool {
	val := os.Getenv(key)
	if val == "" {
		return def
	}
	b, err := strconv.ParseBool(val)
	if err != nil {
		return def
	}
	return b
}

func getEnvDecimal(key, def string) (decimal.Decimal, error) {
	val := os.Getenv(key)
	if val == "" {
		val = def
	}
	d, err := decimal.NewFromString(strings.TrimSpace(val))
	if err != nil {
		return decimal.Zero, fmt.Errorf("invalid decimal %q for %s: %w", val, key, err)
	}
	return d, nil
}
