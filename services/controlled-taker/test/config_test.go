package test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"tradedrift/services/controlled-taker/internal/config"
)

func validTestConfig() config.Config {
	return config.Config{
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
		ResidualGracePeriod:       1500 * time.Millisecond,
		ResidualDeadline:          3000 * time.Millisecond,
		ResidualPollInterval:      200 * time.Millisecond,
		ResidualMaxReadRetries:    3,
		Markets: []config.MarketConfig{
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
		HighSweepProbability:       0.70,
		MaxSweepLevels:             3,
		MaxVisibleDepthConsumption: decimal.NewFromFloat(0.90),
		MaxDepthAge:                5 * time.Second,
	}
}

func TestConfig_Validate(t *testing.T) {
	// 1. Valid config passes
	cfg := validTestConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid config to pass, got: %v", err)
	}

	// 1b. MaxVisibleDepthConsumption validation
	cfg = validTestConfig()
	cfg.MaxVisibleDepthConsumption = decimal.Zero
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error on 0 MaxVisibleDepthConsumption, got nil")
	}

	cfg = validTestConfig()
	cfg.MaxVisibleDepthConsumption = decimal.NewFromFloat(1.5)
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error on > 1 MaxVisibleDepthConsumption, got nil")
	}

	// 1c. MaxDepthAge validation
	cfg = validTestConfig()
	cfg.MaxDepthAge = 0
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error on 0 MaxDepthAge, got nil")
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
	cfg.Markets = []config.MarketConfig{
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

	// 14. Residual grace period <= 0 rejected
	cfg = validTestConfig()
	cfg.ResidualGracePeriod = 0
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error when ResidualGracePeriod <= 0, got nil")
	}

	// 15. Residual poll interval <= 0 rejected
	cfg = validTestConfig()
	cfg.ResidualPollInterval = 0
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error when ResidualPollInterval <= 0, got nil")
	}

	// 16. Residual poll interval > Residual grace period rejected
	cfg = validTestConfig()
	cfg.ResidualPollInterval = 2 * time.Second
	cfg.ResidualGracePeriod = 1 * time.Second
	cfg.ResidualDeadline = 3 * time.Second
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error when ResidualPollInterval > ResidualGracePeriod, got nil")
	}

	// 16b. Residual deadline <= Residual grace period rejected
	cfg = validTestConfig()
	cfg.ResidualGracePeriod = 4 * time.Second
	cfg.ResidualDeadline = 3 * time.Second
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error when ResidualDeadline <= ResidualGracePeriod, got nil")
	}

	// 17. Negative max read retries rejected
	cfg = validTestConfig()
	cfg.ResidualMaxReadRetries = -1
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error when ResidualMaxReadRetries < 0, got nil")
	}

	// 17. HighSweepProbability > 1.0 or < 0.0 rejected
	cfg = validTestConfig()
	cfg.HighSweepProbability = 1.5
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error when HighSweepProbability > 1.0, got nil")
	}
	cfg.HighSweepProbability = -0.1
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error when HighSweepProbability < 0.0, got nil")
	}

	// 18. MaxSweepLevels <= 0 rejected
	cfg = validTestConfig()
	cfg.MaxSweepLevels = 0
	if err := cfg.Validate(); err == nil {
		t.Fatalf("expected error when MaxSweepLevels <= 0, got nil")
	}
}

func TestConfig_Load(t *testing.T) {
	// 1. Default Load succeeds
	cfg, err := config.Load()
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
	if _, err := config.Load(); err == nil {
		t.Fatalf("expected error when MAX_SLIPPAGE_BPS is malformed, got nil")
	}

	// 3. Malformed duration env returns error
	t.Setenv("MAX_SLIPPAGE_BPS", "20")
	t.Setenv("WARMUP_DELAY", "invalid-duration")
	if _, err := config.Load(); err == nil {
		t.Fatalf("expected error when WARMUP_DELAY is malformed, got nil")
	}
}
