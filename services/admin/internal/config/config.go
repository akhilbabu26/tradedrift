package config

import (
	"os"
	"strings"
	"time"
)

// Config holds all configuration values for the Admin Service.
type Config struct {
	Port           string
	PostgresDSN    string
	AuthGRPCAddr   string
	WalletGRPCAddr string
	JWTSecret      string
	KafkaBrokers   string

	TradeHealthURL string
	PortHealthURL  string
	LiqHealthURL   string
	NotifHealthURL string

	OutboxInterval time.Duration
	SagaInterval   time.Duration
	HealthInterval time.Duration
}

// Load reads configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		Port:           getEnv("PORT", "8085"),
		PostgresDSN:    getEnv("POSTGRES_DSN", "postgres://postgres:postgres@localhost:5432/tradedrift_admin?sslmode=disable"),
		AuthGRPCAddr:   getEnv("AUTH_GRPC_ADDR", "localhost:50051"),
		WalletGRPCAddr: getEnv("WALLET_GRPC_ADDR", "localhost:50052"),
		JWTSecret:      getEnv("JWT_SECRET", "super-secret-jwt-key-tradedrift-dev-32bytes"),
		KafkaBrokers:   getEnv("KAFKA_BROKERS", "localhost:9092"),

		TradeHealthURL: getEnv("TRADE_HEALTH_URL", "http://localhost:9090/ready"),
		PortHealthURL:  getEnv("PORTFOLIO_HEALTH_URL", "http://localhost:9091/ready"),
		LiqHealthURL:   getEnv("LIQUIDITY_HEALTH_URL", "http://localhost:8080/readyz"),
		NotifHealthURL: getEnv("NOTIFICATION_HEALTH_URL", "http://localhost:9095/ready"),

		OutboxInterval: 1 * time.Second,
		SagaInterval:   5 * time.Second,
		HealthInterval: 15 * time.Second,
	}
}

// SplitKafkaBrokers returns the brokers as a string slice.
func (c *Config) SplitKafkaBrokers() []string {
	parts := strings.Split(c.KafkaBrokers, ",")
	var res []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			res = append(res, trimmed)
		}
	}
	return res
}

func getEnv(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}
