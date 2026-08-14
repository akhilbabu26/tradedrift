package config

import (
	"os"

	"tradedrift/platform/config"
)

type Config struct {
	PostgresDSN   string
	GRPCPort      string
	MigrationsDir string
	KafkaBrokers  string
	KafkaGroupID  string
	KafkaTopic    string
	LogLevel      string
}

func Load() Config {
	dir := config.GetEnv("MARKET_MIGRATIONS_DIR", "migration")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if _, err2 := os.Stat("migration"); err2 == nil {
			dir = "migration"
		}
	}

	return Config{
		PostgresDSN:   config.GetEnv("MARKET_POSTGRES_DSN", "postgres://postgres:123@localhost:5432/tradedrift_market?sslmode=disable"),
		GRPCPort:      config.GetEnv("MARKET_GRPC_PORT", ":50054"),
		MigrationsDir: dir,
		KafkaBrokers:  config.GetEnv("KAFKA_BROKERS", "localhost:9092"),
		KafkaGroupID:  config.GetEnv("KAFKA_GROUP_ID", "market-service-group"),
		KafkaTopic:    config.GetEnv("KAFKA_TOPIC_TRADE_EXECUTED", "trade.executed.v1"),
		LogLevel:      config.GetEnv("LOG_LEVEL", "info"),
	}
}
