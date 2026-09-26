package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"tradedrift/platform/config"
	"tradedrift/services/matching-engine/internal/market"
)

type appConfig struct {
	KafkaBrokers []string
	KafkaGroupID string
	PostgresDSN  string
	RedisAddr    string
	HTTPPort     string
}

func loadConfig() (appConfig, error) {
	postgresDSN := os.Getenv("POSTGRES_DSN")
	if postgresDSN == "" {
		return appConfig{}, fmt.Errorf("POSTGRES_DSN env var is required")
	}

	kafkaBrokers := config.GetEnv("KAFKA_BROKERS", "localhost:9092")
	brokers := strings.Split(kafkaBrokers, ",")
	for i := range brokers {
		brokers[i] = strings.TrimSpace(brokers[i])
	}

	httpPort := config.GetEnv("HTTP_PORT", "8082")
	if !strings.HasPrefix(httpPort, ":") {
		httpPort = ":" + httpPort
	}

	return appConfig{
		KafkaBrokers: brokers,
		KafkaGroupID: config.GetEnv("KAFKA_GROUP_ID", "matching-engine-group"),
		PostgresDSN:  postgresDSN,
		RedisAddr:    config.GetEnv("REDIS_ADDR", "localhost:6379"),
		HTTPPort:     httpPort,
	}, nil
}

func marketConfigs() ([]market.MarketConfig, error) {
	btcPart, err := config.GetEnvAsInt("BTC_PARTITION", 0)
	if err != nil {
		return nil, fmt.Errorf("BTC_PARTITION: %w", err)
	}

	ethPart, err := config.GetEnvAsInt("ETH_PARTITION", 1)
	if err != nil {
		return nil, fmt.Errorf("ETH_PARTITION: %w", err)
	}

	solPart, err := config.GetEnvAsInt("SOL_PARTITION", 2)
	if err != nil {
		return nil, fmt.Errorf("SOL_PARTITION: %w", err)
	}

	return []market.MarketConfig{
		{
			MarketID:         "BTC-USDT",
			TickSize:         decimal.RequireFromString("0.01"),
			LotSize:          decimal.RequireFromString("0.00001"),
			Partition:        btcPart,
			SnapshotInterval: 10000,
			SnapshotDuration: 60 * time.Second,
		},
		{
			MarketID:         "ETH-USDT",
			TickSize:         decimal.RequireFromString("0.01"),
			LotSize:          decimal.RequireFromString("0.0001"),
			Partition:        ethPart,
			SnapshotInterval: 10000,
			SnapshotDuration: 60 * time.Second,
		},
		{
			MarketID:         "SOL-USDT",
			TickSize:         decimal.RequireFromString("0.001"),
			LotSize:          decimal.RequireFromString("0.01"),
			Partition:        solPart,
			SnapshotInterval: 10000,
			SnapshotDuration: 60 * time.Second,
		},
	}, nil
}

func validateMarketConfigs(configs []market.MarketConfig) error {
	seenPartitions := make(map[int]string)
	for _, mc := range configs {
		if mc.TickSize.LessThanOrEqual(decimal.Zero) {
			return fmt.Errorf("market %s: TickSize must be > 0 (got %s)", mc.MarketID, mc.TickSize)
		}
		if mc.LotSize.LessThanOrEqual(decimal.Zero) {
			return fmt.Errorf("market %s: LotSize must be > 0 (got %s)", mc.MarketID, mc.LotSize)
		}
		if existing, ok := seenPartitions[mc.Partition]; ok {
			return fmt.Errorf("partition %d assigned to both %s and %s", mc.Partition, existing, mc.MarketID)
		}
		seenPartitions[mc.Partition] = mc.MarketID
	}
	return nil
}
