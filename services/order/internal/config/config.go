package config

import (
	"os"
	"strings"

	"tradedrift/platform/config"
)

type Config struct {
	PostgresDSN         string
	GRPCPort            string
	MigrationsDir       string
	WalletGRPCAddr      string
	KafkaBrokers        []string
	KafkaGroupID        string
	TopicTradesSettled  string
	RedisAddr           string
	MaxPriceDeviation   string
	LogLevel            string
}

func Load() Config {
	dir := config.GetEnv("ORDER_MIGRATIONS_DIR", "migration")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if _, err2 := os.Stat("migration"); err2 == nil {
			dir = "migration"
		}
	}

	brokers := config.GetEnv("KAFKA_BROKERS", "localhost:9092")

	return Config{
		PostgresDSN:         config.GetEnv("ORDER_POSTGRES_DSN", "postgres://postgres:123@localhost:5432/tradedrift_order?sslmode=disable"),
		GRPCPort:            config.GetEnv("ORDER_GRPC_PORT", ":50053"),
		MigrationsDir:       dir,
		WalletGRPCAddr:      config.GetEnv("WALLET_GRPC_ADDR", "localhost:50052"),
		KafkaBrokers:        strings.Split(brokers, ","),
		KafkaGroupID:        config.GetEnv("ORDER_KAFKA_GROUP_ID", "order-service-group"),
		TopicTradesSettled:  config.GetEnv("KAFKA_TOPIC_TRADE_SETTLED", "trades.settled.v1"),
		RedisAddr:           config.GetEnv("REDIS_ADDR", "localhost:6379"),
		MaxPriceDeviation:   config.GetEnv("MAX_PRICE_DEVIATION_PERCENT", "0.05"),
		LogLevel:            config.GetEnv("LOG_LEVEL", "info"),
	}
}