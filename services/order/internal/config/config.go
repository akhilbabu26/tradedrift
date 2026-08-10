package config

import (
	"os"

	"tradedrift/platform/config"
)

type Config struct {
	PostgresDSN    string
	GRPCPort       string
	MigrationsDir  string
	WalletGRPCAddr string
	LogLevel       string
}

func Load() Config {
	dir := config.GetEnv("ORDER_MIGRATIONS_DIR", "migration")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		if _, err2 := os.Stat("migration"); err2 == nil {
			dir = "migration"
		}
	}

	return Config{
		PostgresDSN:    config.GetEnv("ORDER_POSTGRES_DSN", "postgres://postgres:123@localhost:5432/tradedrift_order?sslmode=disable"),
		GRPCPort:       config.GetEnv("ORDER_GRPC_PORT", ":50053"),
		MigrationsDir:  dir,
		WalletGRPCAddr: config.GetEnv("WALLET_GRPC_ADDR", "localhost:50052"),
		LogLevel:       config.GetEnv("LOG_LEVEL", "info"),
	}
}