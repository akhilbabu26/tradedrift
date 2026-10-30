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
	RedisAddr     string
	LogLevel      string
}

func Load() Config {
	dir := config.GetEnv("MARKET_MIGRATIONS_DIR", "migration")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if _, err2 := os.Stat("migration"); err2 == nil {
			dir = "migration"
		}
	}

	dsn, err := config.GetDSN("MARKET_POSTGRES_DSN", "postgres://postgres:123@localhost:5432/tradedrift_market?sslmode=disable")
	if err != nil {
		panic(err)
	}

	brokers, err := config.GetAddress("KAFKA_BROKERS", "localhost:9092")
	if err != nil {
		panic(err)
	}

	redisAddr, err := config.GetAddress("REDIS_ADDR", "localhost:6379")
	if err != nil {
		panic(err)
	}

	return Config{
		PostgresDSN:   dsn,
		GRPCPort:      config.GetEnv("MARKET_GRPC_PORT", ":50054"),
		MigrationsDir: dir,
		KafkaBrokers:  brokers,
		KafkaGroupID:  config.GetEnv("KAFKA_GROUP_ID", "market-service-group"),
		KafkaTopic:    config.GetEnv("KAFKA_TOPIC_TRADE_EXECUTED", "trades.executed"),
		RedisAddr:     redisAddr,
		LogLevel:      config.GetEnv("LOG_LEVEL", "info"),
	}
}
