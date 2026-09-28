package config

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func validTestConfig() Config {
	return Config{
		HealthPort:                "8080",
		MetricsPort:               "9090",
		LogLevel:                  "info",
		MaxOrderNotionalUSDT:      decimal.NewFromInt(10000),
		MaxHourlyVolumeUSDT:       decimal.NewFromInt(100000),
		MaxDailyVolumeUSDT:        decimal.NewFromInt(500000),
		MaxTradesPerHour:          100,
		MaxSpreadPercent:          decimal.NewFromFloat(0.01),
		MaxSlippageBps:            15,
		CircuitBreakerFailures:    3,
		InventoryModerateBiasUSDT: decimal.NewFromInt(5000),
		InventoryHeavyBiasUSDT:    decimal.NewFromInt(25000),
		WarmupDelay:               1 * time.Second,
		HighCooldown:              60 * time.Second,
		Markets: []MarketConfig{
			{
				MarketID:    "BTC-USDT",
				BaseAsset:   "BTC",
				QuoteAsset:  "USDT",
				TickSize:    decimal.NewFromFloat(0.01),
				LotSize:     decimal.NewFromFloat(0.0001),
				MinQuantity: decimal.NewFromFloat(0.0001),
				Partition:   0,
			},
		},
		LowInterval:  IntervalConfig{MinInterval: 10 * time.Second, BaseInterval: 20 * time.Second, MaxInterval: 30 * time.Second},
		MidInterval:  IntervalConfig{MinInterval: 20 * time.Second, BaseInterval: 40 * time.Second, MaxInterval: 60 * time.Second},
		HighInterval: IntervalConfig{MinInterval: 30 * time.Second, BaseInterval: 60 * time.Second, MaxInterval: 90 * time.Second},
	}
}

func TestConfig_Validate(t *testing.T) {
	// 1. Valid config passes
	cfg := validTestConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid config to pass, got: %v", err)
	}

	// 2. Negative notional rejected
	cfg = validTestConfig()
	cfg.MaxOrderNotionalUSDT = decimal.NewFromInt(-100)
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error on negative MaxOrderNotionalUSDT, got nil")
	}

	// 3. Zero circuit breaker failures rejected
	cfg = validTestConfig()
	cfg.CircuitBreakerFailures = 0
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error on 0 CircuitBreakerFailures, got nil")
	}

	// 4. Inverted intervals rejected (MinInterval > BaseInterval)
	cfg = validTestConfig()
	cfg.LowInterval = IntervalConfig{MinInterval: 50 * time.Second, BaseInterval: 20 * time.Second, MaxInterval: 60 * time.Second}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error when MinInterval > BaseInterval, got nil")
	}

	// 5. Inverted intervals rejected (BaseInterval > MaxInterval)
	cfg = validTestConfig()
	cfg.LowInterval = IntervalConfig{MinInterval: 10 * time.Second, BaseInterval: 80 * time.Second, MaxInterval: 60 * time.Second}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error when BaseInterval > MaxInterval, got nil")
	}

	// 6. Non-positive moderate bias rejected
	cfg = validTestConfig()
	cfg.InventoryModerateBiasUSDT = decimal.Zero
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error when InventoryModerateBiasUSDT <= 0, got nil")
	}

	// 7. Heavy bias <= Moderate bias rejected
	cfg = validTestConfig()
	cfg.InventoryHeavyBiasUSDT = cfg.InventoryModerateBiasUSDT
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error when InventoryHeavyBiasUSDT <= ModerateBias, got nil")
	}

	// 8a. Negative MaxSlippageBps structurally rejected
	cfg = validTestConfig()
	cfg.MaxSlippageBps = -1
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error when MaxSlippageBps < 0, got nil")
	}

	// 8b. MaxSlippageBps > V1MaxAllowedSlippageBps (500) rejected by safety policy
	cfg = validTestConfig()
	cfg.MaxSlippageBps = 501
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error when MaxSlippageBps > V1MaxAllowedSlippageBps, got nil")
	}

	// 9. Empty ports rejected
	cfg = validTestConfig()
	cfg.HealthPort = ""
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error on empty HealthPort, got nil")
	}

	// 10. Empty markets list rejected
	cfg = validTestConfig()
	cfg.Markets = nil
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error on empty Markets, got nil")
	}

	// 11. Duplicate market ID rejected
	cfg = validTestConfig()
	cfg.Markets = append(cfg.Markets, cfg.Markets[0])
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error on duplicate market ID, got nil")
	}

	// 12. Invalid market parameters rejected (non-positive tick/lot/min qty)
	cfg = validTestConfig()
	cfg.Markets = []MarketConfig{
		{
			MarketID:    "BTC-USDT",
			BaseAsset:   "BTC",
			QuoteAsset:  "USDT",
			TickSize:    decimal.Zero,
			LotSize:     decimal.NewFromFloat(0.001),
			MinQuantity: decimal.NewFromFloat(0.001),
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error on non-positive TickSize, got nil")
	}

	// 13. Invalid LogLevel rejected
	cfg = validTestConfig()
	cfg.LogLevel = "verbose"
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error on invalid LogLevel, got nil")
	}
}

func TestConfig_Load(t *testing.T) {
	// 1. Default Load succeeds
	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected default config to load successfully, got: %v", err)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected default LogLevel 'info', got %s", cfg.LogLevel)
	}
	if cfg.MaxSlippageBps != 15 {
		t.Errorf("expected default MaxSlippageBps 15, got %d", cfg.MaxSlippageBps)
	}

	// 2. Malformed integer env returns error
	t.Setenv("MAX_SLIPPAGE_BPS", "not-a-number")
	if _, err := Load(); err == nil {
		t.Fatalf("expected error when MAX_SLIPPAGE_BPS is malformed, got nil")
	}

	// 3. Malformed duration env returns error
	t.Setenv("MAX_SLIPPAGE_BPS", "20")
	t.Setenv("WARMUP_DELAY", "invalid-duration")
	if _, err := Load(); err == nil {
		t.Fatalf("expected error when WARMUP_DELAY is malformed, got nil")
	}
}
