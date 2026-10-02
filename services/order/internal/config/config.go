package config

import (
	"os"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"tradedrift/platform/config"
)

type Config struct {
	PostgresDSN              string
	GRPCPort                 string
	MigrationsDir            string
	WalletGRPCAddr           string
	KafkaBrokers             []string
	KafkaGroupID              string
	TopicTradesSettled        string
	TopicOrdersCancelled      string
	RedisAddr                 string
	MaxPriceDeviation         string
	MMUserID                 string
	MMReferenceMaxDeviation  string
	RefPriceAnchorMaxAge     time.Duration
	LogLevel                  string
}

func Load() Config {
	dir := config.GetEnv("ORDER_MIGRATIONS_DIR", "migration")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if _, err2 := os.Stat("migration"); err2 == nil {
			dir = "migration"
		}
	}

	brokers := config.GetEnv("KAFKA_BROKERS", "localhost:9092")
	anchorMaxAge, _ := config.GetEnvAsDuration("REFPRICE_ANCHOR_MAX_AGE", 60*time.Second)

	mmDev := config.GetEnv("MM_REFERENCE_MAX_DEVIATION_PERCENT", "0.10")
	// Startup validation (Directive 4): MM deviation must strictly exceed the maximum LE ladder
	// distance (300 bps / 0.03) to prevent rejection loops on outer HIGH zone sentinel levels.
	if dev, err := decimal.NewFromString(mmDev); err == nil {
		if dev.LessThanOrEqual(decimal.NewFromFloat(0.03)) {
			panic("MM_REFERENCE_MAX_DEVIATION_PERCENT must strictly exceed 0.03 (300 bps) to accommodate LE ladder HIGH zone")
		}
	}

	return Config{
		PostgresDSN:             config.GetEnv("ORDER_POSTGRES_DSN", "postgres://postgres:123@localhost:5432/tradedrift_order?sslmode=disable"),
		GRPCPort:                config.GetEnv("ORDER_GRPC_PORT", ":50053"),
		MigrationsDir:           dir,
		WalletGRPCAddr:          config.GetEnv("WALLET_GRPC_ADDR", "localhost:50052"),
		KafkaBrokers:            strings.Split(brokers, ","),
		KafkaGroupID:            config.GetEnv("ORDER_KAFKA_GROUP_ID", "order-service-group"),
		TopicTradesSettled:      config.GetEnv("KAFKA_TOPIC_TRADE_SETTLED", "trades.settled.v1"),
		TopicOrdersCancelled:    config.GetEnv("KAFKA_TOPIC_ORDERS_CANCELLED", "orders.cancelled.v1"),
		RedisAddr:               config.GetEnv("REDIS_ADDR", "localhost:6379"),
		MaxPriceDeviation:       config.GetEnv("MAX_PRICE_DEVIATION_PERCENT", "0.05"),
		MMUserID:                config.GetEnv("MM_USER_ID", "00000000-0000-0000-0000-000000000001"),
		MMReferenceMaxDeviation: mmDev,
		RefPriceAnchorMaxAge:    anchorMaxAge,
		LogLevel:                config.GetEnv("LOG_LEVEL", "info"),
	}
}