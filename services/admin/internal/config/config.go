package config

import (
	"fmt"
	"strings"
	"time"

	platformconfig "tradedrift/platform/config"
)

// Config holds all configuration values for the Admin Service.
type Config struct {
	Port           string
	LogLevel       string
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

// Load reads and validates configuration from environment variables.
// Crucial variables (JWTSecret, PostgresDSN, KafkaBrokers) fail fast if missing.
// Operational intervals are bounded to prevent misconfiguration.
func Load() (*Config, error) {
	logLevel := platformconfig.GetEnv("LOG_LEVEL", "info")
	if err := platformconfig.ValidateLogLevel(logLevel); err != nil {
		return nil, err
	}

	postgresDSN := platformconfig.GetEnv("ADMIN_POSTGRES_DSN", "")
	if postgresDSN == "" {
		var err error
		postgresDSN, err = platformconfig.GetEnvOrError("POSTGRES_DSN")
		if err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
	}

	jwtSecret, err := platformconfig.GetEnvOrError("JWT_SECRET")
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	kafkaBrokers, err := platformconfig.GetEnvOrError("KAFKA_BROKERS")
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	outboxInterval, err := parseBoundedDuration("ADMIN_OUTBOX_INTERVAL", "OUTBOX_INTERVAL", 1*time.Second, 100*time.Millisecond, 1*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	sagaInterval, err := parseBoundedDuration("ADMIN_SAGA_INTERVAL", "SAGA_INTERVAL", 5*time.Second, 500*time.Millisecond, 5*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	healthInterval, err := parseBoundedDuration("ADMIN_HEALTH_INTERVAL", "HEALTH_INTERVAL", 15*time.Second, 1*time.Second, 5*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	return &Config{
		Port:           platformconfig.GetEnv("PORT", "8085"),
		LogLevel:       logLevel,
		PostgresDSN:    postgresDSN,
		AuthGRPCAddr:   platformconfig.GetEnv("AUTH_GRPC_ADDR", "localhost:50051"),
		WalletGRPCAddr: platformconfig.GetEnv("WALLET_GRPC_ADDR", "localhost:50052"),
		JWTSecret:      jwtSecret,
		KafkaBrokers:   kafkaBrokers,

		TradeHealthURL: platformconfig.GetEnv("TRADE_HEALTH_URL", "http://localhost:9090/ready"),
		PortHealthURL:  platformconfig.GetEnv("PORTFOLIO_HEALTH_URL", "http://localhost:9091/ready"),
		LiqHealthURL:   platformconfig.GetEnv("LIQUIDITY_HEALTH_URL", "http://localhost:8080/readyz"),
		NotifHealthURL: platformconfig.GetEnv("NOTIFICATION_HEALTH_URL", "http://localhost:9095/ready"),

		OutboxInterval: outboxInterval,
		SagaInterval:   sagaInterval,
		HealthInterval: healthInterval,
	}, nil
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

func parseBoundedDuration(primaryKey, fallbackKey string, defaultVal, min, max time.Duration) (time.Duration, error) {
	valStr := platformconfig.GetEnv(primaryKey, "")
	if valStr == "" && fallbackKey != "" {
		valStr = platformconfig.GetEnv(fallbackKey, "")
	}
	if valStr == "" {
		return defaultVal, nil
	}

	d, err := time.ParseDuration(valStr)
	if err != nil {
		return 0, fmt.Errorf("invalid duration value for %s: %q: %w", primaryKey, valStr, err)
	}
	if d < min {
		return 0, fmt.Errorf("%s value %v is below minimum allowed duration %v", primaryKey, d, min)
	}
	if d > max {
		return 0, fmt.Errorf("%s value %v exceeds maximum allowed duration %v", primaryKey, d, max)
	}
	return d, nil
}
