package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// MarketGuard defines the interface for checking market halt status and enforcement readiness.
type MarketGuard interface {
	IsHalted(ctx context.Context, marketID string) (bool, error)
}

// RedisMarketGuard checks market halt state via Redis with fail-closed enforcement readiness.
type RedisMarketGuard struct {
	rdb redis.Cmdable
}

// NewRedisMarketGuard constructs a RedisMarketGuard.
func NewRedisMarketGuard(rdb redis.Cmdable) *RedisMarketGuard {
	return &RedisMarketGuard{rdb: rdb}
}

// IsHalted verifies if the market is halted for trading.
// Invariant 1: If market:enforcement:ready is not "1" (e.g. after Redis restart), returns ErrMarketEnforcementNotReady.
// Invariant 2: If market:halted:{marketID} is "1", returns (true, nil).
// Invariant 3: If market:halted:{marketID} is absent, returns (false, nil).
// Invariant 4: If Redis returns an error, returns (false, err) -> fail-closed.
func (g *RedisMarketGuard) IsHalted(ctx context.Context, marketID string) (bool, error) {
	if g.rdb == nil {
		return false, errors.New("redis client is uninitialized")
	}

	// 1. Cold-start / restart readiness check
	ready, err := g.rdb.Get(ctx, "market:enforcement:ready").Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return false, ErrMarketEnforcementNotReady
		}
		return false, fmt.Errorf("check enforcement readiness: %w", err)
	}
	if ready != "1" {
		return false, ErrMarketEnforcementNotReady
	}

	// 2. Market halt status check
	val, err := g.rdb.Get(ctx, "market:halted:"+marketID).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return false, nil // Open
		}
		return false, fmt.Errorf("check market halt key: %w", err)
	}

	return val == "1", nil
}
