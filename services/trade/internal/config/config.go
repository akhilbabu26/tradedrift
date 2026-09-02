package config

import (
	"fmt"
	"strings"

	"tradedrift/platform/config"
)

// Config holds all runtime configuration for the Trade Service.
type Config struct {
	PostgresDSN   string
	MigrationsDir string
	KafkaBrokers  []string
	KafkaGroupID  string
	KafkaTopic    string   // trades.settled.v1
	KafkaDLQTopic string   // trades.settled.dlq
	GRPCPort      string
	LogLevel      string
}

// Load reads environment variables with sensible defaults.
// Call config.LoadEnv() before Load() to auto-load a .env file.
func Load() (Config, error) {
	dsn := config.GetEnv("TRADE_POSTGRES_DSN", "")
	if dsn == "" {
		return Config{}, fmt.Errorf("TRADE_POSTGRES_DSN is required")
	}

	rawBrokers := config.GetEnv("KAFKA_BROKERS", "localhost:9092")
	brokers := parseBrokers(rawBrokers)
	if len(brokers) == 0 {
		return Config{}, fmt.Errorf("KAFKA_BROKERS is required")
	}

	return Config{
		PostgresDSN:   dsn,
		MigrationsDir: config.GetEnv("TRADE_MIGRATIONS_DIR", "migration"),
		KafkaBrokers:  brokers,
		KafkaGroupID:  config.GetEnv("KAFKA_GROUP_ID", "trade-service"),
		KafkaTopic:    config.GetEnv("KAFKA_TOPIC_TRADE_SETTLED", "trades.settled.v1"),
		KafkaDLQTopic: config.GetEnv("KAFKA_TOPIC_TRADE_DLQ", "trades.settled.dlq"),
		GRPCPort:      config.GetEnv("TRADE_GRPC_PORT", ":50057"),
		LogLevel:      config.GetEnv("LOG_LEVEL", "info"),
	}, nil
}

func parseBrokers(raw string) []string {
	parts := strings.Split(raw, ",")
	brokers := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			brokers = append(brokers, trimmed)
		}
	}
	return brokers
}
