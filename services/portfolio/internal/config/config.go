package config

import (
	"fmt"
	"strings"

	"tradedrift/platform/config"
)

type Config struct {
	PostgresDSN           string
	MigrationsDir         string
	KafkaBrokers          []string
	KafkaGroupID          string
	KafkaTopicTradeSettled string
	KafkaTopicPortfolioUpdated string
	KafkaTopicTradeDLQ    string
	WalletGRPCAddr        string
	MarketGRPCAddr        string
	GRPCPort              string
	MetricsPort           string
	LogLevel              string
}

func Load() (Config, error) {
	dsn := config.GetEnv("PORTFOLIO_POSTGRES_DSN", "")
	if dsn == "" {
		return Config{}, fmt.Errorf("PORTFOLIO_POSTGRES_DSN is required")
	}

	rawBrokers := config.GetEnv("KAFKA_BROKERS", "localhost:9092")
	brokers := parseBrokers(rawBrokers)
	if len(brokers) == 0 {
		return Config{}, fmt.Errorf("KAFKA_BROKERS must contain at least one valid broker address")
	}

	walletAddr := config.GetEnv("WALLET_GRPC_ADDR", "localhost:50052")
	marketAddr := config.GetEnv("MARKET_GRPC_ADDR", "localhost:50054")

	return Config{
		PostgresDSN:                dsn,
		MigrationsDir:              config.GetEnv("PORTFOLIO_MIGRATIONS_DIR", "services/portfolio/migration"),
		KafkaBrokers:               brokers,
		KafkaGroupID:               config.GetEnv("KAFKA_GROUP_ID", "portfolio-service-group"),
		KafkaTopicTradeSettled:     config.GetEnv("KAFKA_TOPIC_TRADE_SETTLED", "trades.settled.v1"),
		KafkaTopicPortfolioUpdated: config.GetEnv("KAFKA_TOPIC_PORTFOLIO_UPDATED", "portfolios.updated.v1"),
		KafkaTopicTradeDLQ:         config.GetEnv("KAFKA_TOPIC_TRADE_DLQ", "trades.settled.dlq"),
		WalletGRPCAddr:             walletAddr,
		MarketGRPCAddr:             marketAddr,
		GRPCPort:                   config.GetEnv("PORTFOLIO_GRPC_PORT", ":50058"),
		MetricsPort:                config.GetEnv("PORTFOLIO_METRICS_PORT", ":9091"),
		LogLevel:                   config.GetEnv("LOG_LEVEL", "info"),
	}, nil
}

func parseBrokers(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
