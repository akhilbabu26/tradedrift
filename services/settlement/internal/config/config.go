package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"tradedrift/platform/config"
)

// Config holds all runtime configuration for the Settlement Service.
type Config struct {
	PostgresDSN       string
	MigrationsDir     string
	KafkaBrokers      []string      // comma-separated in env, split on load
	KafkaGroupID      string
	KafkaTopic        string
	WalletGRPCAddr    string
	WalletGRPCTimeout time.Duration // per-RPC deadline for Wallet.SettleTrade
	LogLevel          string
}

// Load reads environment variables with sensible defaults.
// Returns an error if any value fails validation (e.g. malformed duration).
// Call config.LoadEnv() before Load() to auto-load a .env file.
func Load() (Config, error) {
	dir := config.GetEnv("SETTLEMENT_MIGRATIONS_DIR", "migration")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if _, err2 := os.Stat("migration"); err2 == nil {
			dir = "migration"
		}
	}

	rawBrokers := config.GetEnv("KAFKA_BROKERS", "localhost:9092")
	brokers := parseBrokers(rawBrokers)

	// Parse WALLET_GRPC_TIMEOUT explicitly — a misconfigured duration (e.g.
	// WALLET_GRPC_TIMEOUT=abc) must fail loudly at startup rather than silently
	// defaulting to 5s, which would mask operator mistakes.
	grpcTimeout, err := config.GetEnvAsDuration("WALLET_GRPC_TIMEOUT", 5*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("invalid WALLET_GRPC_TIMEOUT: %w", err)
	}
	if grpcTimeout <= 0 {
		return Config{}, fmt.Errorf("WALLET_GRPC_TIMEOUT must be positive, got %s", grpcTimeout)
	}

	return Config{
		PostgresDSN:       config.GetEnv("SETTLEMENT_POSTGRES_DSN", "postgres://postgres:123@localhost:5432/tradedrift_settlement?sslmode=disable"),
		MigrationsDir:     dir,
		KafkaBrokers:      brokers,
		KafkaGroupID:      config.GetEnv("KAFKA_GROUP_ID", "settlement-service-group"),
		KafkaTopic:        config.GetEnv("KAFKA_TOPIC_TRADE_EXECUTED", "trades.executed"),
		WalletGRPCAddr:    config.GetEnv("WALLET_GRPC_ADDR", "localhost:50052"),
		WalletGRPCTimeout: grpcTimeout,
		LogLevel:          config.GetEnv("LOG_LEVEL", "info"),
	}, nil
}

// parseBrokers splits a comma-separated broker string and trims whitespace.
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
