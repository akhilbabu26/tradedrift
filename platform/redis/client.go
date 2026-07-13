package redis

import (
	"context"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

// Config defines connection details for Redis.
// It supports both standalone Redis and Sentinel architectures.
// Redis Sentinel is a high-availability (HA) architecture for Redis. 
// Its purpose is to ensure that your application continues working even if the primary Redis server crashes.
type Config struct {
	Addr           string // Single-node host:port OR comma-separated sentinel hosts
	SentinelMaster string // Name of Sentinel master (if empty, standalone is used)
	Password       string // Optional Redis password
	DB             int    // Redis database index
}

// NewClient initializes a redis.Client using the provided startup context.
func NewClient(ctx context.Context, cfg Config) (*redis.Client, error) {
	addr := strings.TrimSpace(cfg.Addr)
	if addr == "" {
		return nil, fmt.Errorf("redis address is required")
	}

	var rdb *redis.Client

	if cfg.SentinelMaster != "" {
		sentinelAddrs := strings.Split(addr, ",")
		for i := range sentinelAddrs {
			sentinelAddrs[i] = strings.TrimSpace(sentinelAddrs[i])
		}

		rdb = redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:    cfg.SentinelMaster,
			SentinelAddrs: sentinelAddrs,
			Password:      cfg.Password,
			DB:            cfg.DB,
		})
	} else {
		rdb = redis.NewClient(&redis.Options{
			Addr:     addr,
			Password: cfg.Password,
			DB:       cfg.DB,
		})
	}

	// Ping the database using the provided parent context
	if err := rdb.Ping(ctx).Err(); err != nil {
		rdb.Close()
		return nil, fmt.Errorf("failed to ping redis: %w", err)
	}

	return rdb, nil
}
