package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	platformconfig "tradedrift/platform/config"
)

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

	// Startup seeds: fallback reference prices used until platform/refprice Provider
	// has completed its first live API fetch. Do NOT use these as execution prices.
	btcRef, err := getEnvDecimal("BTC_REFERENCE_PRICE", "84000.00")
	if err != nil {
		return Config{}, fmt.Errorf("BTC_REFERENCE_PRICE: %w", err)
	}
	ethRef, err := getEnvDecimal("ETH_REFERENCE_PRICE", "2675.00")
	if err != nil {
		return Config{}, fmt.Errorf("ETH_REFERENCE_PRICE: %w", err)
	}
	solRef, err := getEnvDecimal("SOL_REFERENCE_PRICE", "117.00")
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
	orphanCooldown, err := platformconfig.GetEnvAsDuration("LE_ORPHAN_CANCEL_COOLDOWN", 5*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("LE_ORPHAN_CANCEL_COOLDOWN: %w", err)
	}

	// ── Dynamic pricing config ────────────────────────────────────────────────

	refPriceURL := platformconfig.GetEnv("LE_REFPRICE_API_URL", "https://api.coingecko.com/api/v3")
	refAPIKey := platformconfig.GetEnv("LE_COINGECKO_API_KEY", "")
	refPlan := platformconfig.GetEnv("LE_COINGECKO_PLAN", "demo")

	refRefresh, err := platformconfig.GetEnvAsDuration("LE_REFPRICE_REFRESH_INTERVAL", 30*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("LE_REFPRICE_REFRESH_INTERVAL: %w", err)
	}
	refStale, err := platformconfig.GetEnvAsDuration("LE_REFPRICE_STALE_THRESHOLD", 5*time.Minute)
	if err != nil {
		return Config{}, fmt.Errorf("LE_REFPRICE_STALE_THRESHOLD: %w", err)
	}
	refPause, err := platformconfig.GetEnvAsDuration("LE_REFPRICE_PAUSE_THRESHOLD", 10*time.Minute)
	if err != nil {
		return Config{}, fmt.Errorf("LE_REFPRICE_PAUSE_THRESHOLD: %w", err)
	}
	refFetchTimeout, err := platformconfig.GetEnvAsDuration("LE_REFPRICE_FETCH_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("LE_REFPRICE_FETCH_TIMEOUT: %w", err)
	}

	// Zone config — 7/4/1 split with bps-range env overrides.
	lowMin, err := platformconfig.GetEnvAsInt("LE_LOW_MIN_BPS", 5)
	if err != nil {
		return Config{}, fmt.Errorf("LE_LOW_MIN_BPS: %w", err)
	}
	lowMax, err := platformconfig.GetEnvAsInt("LE_LOW_MAX_BPS", 50)
	if err != nil {
		return Config{}, fmt.Errorf("LE_LOW_MAX_BPS: %w", err)
	}
	midMin, err := platformconfig.GetEnvAsInt("LE_MID_MIN_BPS", 50)
	if err != nil {
		return Config{}, fmt.Errorf("LE_MID_MIN_BPS: %w", err)
	}
	midMax, err := platformconfig.GetEnvAsInt("LE_MID_MAX_BPS", 150)
	if err != nil {
		return Config{}, fmt.Errorf("LE_MID_MAX_BPS: %w", err)
	}
	highMin, err := platformconfig.GetEnvAsInt("LE_HIGH_MIN_BPS", 150)
	if err != nil {
		return Config{}, fmt.Errorf("LE_HIGH_MIN_BPS: %w", err)
	}
	highMax, err := platformconfig.GetEnvAsInt("LE_HIGH_MAX_BPS", 300)
	if err != nil {
		return Config{}, fmt.Errorf("LE_HIGH_MAX_BPS: %w", err)
	}

	zones := ZoneConfig{
		Low:  ZoneBand{MinBps: lowMin, MaxBps: lowMax, Count: 7},
		Mid:  ZoneBand{MinBps: midMin, MaxBps: midMax, Count: 4},
		High: ZoneBand{MinBps: highMin, MaxBps: highMax, Count: 1},
	}

	// Repricing policy.
	smallBps, err := platformconfig.GetEnvAsInt("LE_SMALL_REPRICE_BPS", 10)
	if err != nil {
		return Config{}, fmt.Errorf("LE_SMALL_REPRICE_BPS: %w", err)
	}
	largeBps, err := platformconfig.GetEnvAsInt("LE_LARGE_REPRICE_BPS", 100)
	if err != nil {
		return Config{}, fmt.Errorf("LE_LARGE_REPRICE_BPS: %w", err)
	}
	maxBatch, err := platformconfig.GetEnvAsInt("LE_MAX_REPRICE_BATCH", 4)
	if err != nil {
		return Config{}, fmt.Errorf("LE_MAX_REPRICE_BATCH: %w", err)
	}
	orderLifetime, err := platformconfig.GetEnvAsDuration("LE_ORDER_LIFETIME", 30*time.Minute)
	if err != nil {
		return Config{}, fmt.Errorf("LE_ORDER_LIFETIME: %w", err)
	}

	repricing := RepricingConfig{
		SmallBps:      smallBps,
		LargeBps:      largeBps,
		MaxBatch:      maxBatch,
		OrderLifetime: orderLifetime,
	}

	// Per-market quantity per level (env-configurable, falls back to V1 hardcoded values).
	btcQty, err := getEnvDecimal("LE_BTC_QTY_PER_LEVEL", "0.85")
	if err != nil {
		return Config{}, fmt.Errorf("LE_BTC_QTY_PER_LEVEL: %w", err)
	}
	ethQty, err := getEnvDecimal("LE_ETH_QTY_PER_LEVEL", "1.5")
	if err != nil {
		return Config{}, fmt.Errorf("LE_ETH_QTY_PER_LEVEL: %w", err)
	}
	solQty, err := getEnvDecimal("LE_SOL_QTY_PER_LEVEL", "20.0")
	if err != nil {
		return Config{}, fmt.Errorf("LE_SOL_QTY_PER_LEVEL: %w", err)
	}

	// Capital budget caps (zero = no cap; safe backward-compat default).
	btcMaxBidUSDT, err := getEnvDecimal("LE_BTC_MAX_BID_USDT", "2000000")
	if err != nil {
		return Config{}, fmt.Errorf("LE_BTC_MAX_BID_USDT: %w", err)
	}
	btcMaxAskBTC, err := getEnvDecimal("LE_BTC_MAX_ASK_BASE", "20")
	if err != nil {
		return Config{}, fmt.Errorf("LE_BTC_MAX_ASK_BASE: %w", err)
	}
	ethMaxBidUSDT, err := getEnvDecimal("LE_ETH_MAX_BID_USDT", "200000")
	if err != nil {
		return Config{}, fmt.Errorf("LE_ETH_MAX_BID_USDT: %w", err)
	}
	ethMaxAskETH, err := getEnvDecimal("LE_ETH_MAX_ASK_BASE", "100")
	if err != nil {
		return Config{}, fmt.Errorf("LE_ETH_MAX_ASK_BASE: %w", err)
	}
	solMaxBidUSDT, err := getEnvDecimal("LE_SOL_MAX_BID_USDT", "100000")
	if err != nil {
		return Config{}, fmt.Errorf("LE_SOL_MAX_BID_USDT: %w", err)
	}
	solMaxAskSOL, err := getEnvDecimal("LE_SOL_MAX_ASK_BASE", "1000")
	if err != nil {
		return Config{}, fmt.Errorf("LE_SOL_MAX_ASK_BASE: %w", err)
	}

	cfg := Config{
		KafkaBrokers:   brokers,
		KafkaGroupID:   platformconfig.GetEnv("KAFKA_GROUP_ID", "liquidity-engine-group"),
		WalletGRPCAddr: platformconfig.GetEnv("WALLET_GRPC_ADDR", "localhost:50052"),
		OrderGRPCAddr:  platformconfig.GetEnv("ORDER_GRPC_ADDR", "localhost:50053"),
		MEHTTPAddr:     platformconfig.GetEnv("ME_HTTP_ADDR", "http://localhost:8082"),
		RedisAddr:      platformconfig.GetEnv("REDIS_ADDR", "localhost:6379"),

		WalletRefreshInterval:     walletRefresh,
		MaxBalanceStaleness:       maxBalStaleness,
		ReconcileInterval:         reconcileInterval,
		MaxOrderStateStaleness:    maxOrderStaleness,
		PendingTimeout:            pendingTimeout,
		CancellingTimeout:         cancellingTimeout,
		CancelRetryLimit:          cancelRetry,
		MELivenessThreshold:       meThreshold,
		TargetedReconcileDebounce: debounce,
		OrphanCancelCooldown:      orphanCooldown,

		HealthPort:   platformconfig.GetEnv("HEALTH_PORT", "8080"),
		MetricsPort:  platformconfig.GetEnv("METRICS_PORT", "9090"),
		MinReadyBids: minReadyBids,
		MinReadyAsks: minReadyAsks,

		Markets: []MarketConfig{
			{
				// BTC-USDT — tick=0.01, lot=0.00001 (verified from ME main.go)
				MarketID:           "BTC-USDT",
				BaseAsset:          "BTC",
				QuoteAsset:         "USDT",
				TickSize:           decimal.RequireFromString("0.01"),
				LotSize:            decimal.RequireFromString("0.00001"),
				Partition:          btcPart,
				LevelCount:         12,
				MinOrderSize:       decimal.RequireFromString("0.00001"),
				MinBase:            decimal.RequireFromString("30"),
				CriticalBase:       decimal.RequireFromString("5"),
				MinQuote:           decimal.RequireFromString("1000000"),
				CriticalQuote:      decimal.RequireFromString("100000"),
				SpreadBps:          4,      // V1 compat — superseded by BidZones/AskZones
				ReferencePrice:     btcRef, // V1 compat — superseded by platform/refprice
				BidZones:           zones,
				AskZones:           zones,
				QtyPerLevel:        btcQty,
				MaxBidExposureUSDT: btcMaxBidUSDT,
				MaxAskExposureBase: btcMaxAskBTC,
				Repricing:          repricing,
			},
			{
				// ETH-USDT — tick=0.01, lot=0.0001 (verified from ME main.go)
				MarketID:           "ETH-USDT",
				BaseAsset:          "ETH",
				QuoteAsset:         "USDT",
				TickSize:           decimal.RequireFromString("0.01"),
				LotSize:            decimal.RequireFromString("0.0001"),
				Partition:          ethPart,
				LevelCount:         12,
				MinOrderSize:       decimal.RequireFromString("0.0001"),
				MinBase:            decimal.RequireFromString("100"),
				CriticalBase:       decimal.RequireFromString("10"),
				MinQuote:           decimal.RequireFromString("100000"),
				CriticalQuote:      decimal.RequireFromString("10000"),
				SpreadBps:          4,
				ReferencePrice:     ethRef,
				BidZones:           zones,
				AskZones:           zones,
				QtyPerLevel:        ethQty,
				MaxBidExposureUSDT: ethMaxBidUSDT,
				MaxAskExposureBase: ethMaxAskETH,
				Repricing:          repricing,
			},
			{
				// SOL-USDT — tick=0.001, lot=0.01 (verified from ME main.go)
				MarketID:           "SOL-USDT",
				BaseAsset:          "SOL",
				QuoteAsset:         "USDT",
				TickSize:           decimal.RequireFromString("0.001"),
				LotSize:            decimal.RequireFromString("0.01"),
				Partition:          solPart,
				LevelCount:         12,
				MinOrderSize:       decimal.RequireFromString("0.01"),
				MinBase:            decimal.RequireFromString("500"),
				CriticalBase:       decimal.RequireFromString("50"),
				MinQuote:           decimal.RequireFromString("20000"),
				CriticalQuote:      decimal.RequireFromString("2000"),
				SpreadBps:          4,
				ReferencePrice:     solRef,
				BidZones:           zones,
				AskZones:           zones,
				QtyPerLevel:        solQty,
				MaxBidExposureUSDT: solMaxBidUSDT,
				MaxAskExposureBase: solMaxAskSOL,
				Repricing:          repricing,
			},
		},

		RefPrice: RefPriceConfig{
			APIURL:          refPriceURL,
			APIKey:          refAPIKey,
			Plan:            refPlan,
			RefreshInterval: refRefresh,
			StaleThreshold:  refStale,
			PauseThreshold:  refPause,
			FetchTimeout:    refFetchTimeout,
			BTCSeed:         btcRef,
			ETHSeed:         ethRef,
			SOLSeed:         solRef,
		},
	}
	return cfg, nil
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
