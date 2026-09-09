package config

import (
	"fmt"
	"strings"

	"tradedrift/platform/config"
)

type Config struct {
	PostgresDSN         string
	MigrationsDir       string
	RedisAddr           string
	RedisSentinelMaster string
	RedisPassword       string
	RedisDB             int
	KafkaBrokers        []string
	KafkaGroupID        string
	KafkaDLQTopic       string
	GRPCPort            string
	MetricsPort         string
	LogLevel            string
}

func Load() (Config, error) {
	dsn := config.GetEnv("NOTIFICATION_POSTGRES_DSN", "postgres://postgres:postgres@localhost:5432/tradedrift_notification?sslmode=disable")
	if dsn == "" {
		return Config{}, fmt.Errorf("NOTIFICATION_POSTGRES_DSN is required")
	}

	rawBrokers := config.GetEnv("KAFKA_BROKERS", "localhost:9092")
	brokers := parseBrokers(rawBrokers)
	if len(brokers) == 0 {
		return Config{}, fmt.Errorf("KAFKA_BROKERS must contain at least one valid broker address")
	}

	return Config{
		PostgresDSN:         dsn,
		MigrationsDir:       config.GetEnv("NOTIFICATION_MIGRATIONS_DIR", "services/notification/migration"),
		RedisAddr:           config.GetEnv("REDIS_ADDR", "localhost:6379"),
		RedisSentinelMaster: config.GetEnv("REDIS_SENTINEL_MASTER", ""),
		RedisPassword:       config.GetEnv("REDIS_PASSWORD", ""),
		RedisDB:             0,
		KafkaBrokers:        brokers,
		KafkaGroupID:        config.GetEnv("NOTIFICATION_KAFKA_GROUP_ID", "notification-service-group"),
		KafkaDLQTopic:       config.GetEnv("NOTIFICATION_DLQ_TOPIC", "notifications.dlq"),
		GRPCPort:            config.GetEnv("NOTIFICATION_GRPC_PORT", ":50059"),
		MetricsPort:         config.GetEnv("NOTIFICATION_METRICS_PORT", ":9092"),
		LogLevel:            config.GetEnv("LOG_LEVEL", "info"),
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
