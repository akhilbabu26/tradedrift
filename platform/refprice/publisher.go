package refprice

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// AnchorPublisher defines the contract for publishing authoritative reference price anchors.
type AnchorPublisher interface {
	PublishAnchor(ctx context.Context, entry Entry, ttl time.Duration) error
}

// AnchorPayload is the canonical JSON representation stored in Redis.
// Order Service reads and unmarshals this DTO to validate MM orders.
type AnchorPayload struct {
	MarketID  string `json:"market_id"`
	Price     string `json:"price"`
	Version   int64  `json:"version"`
	FetchedAt string `json:"fetched_at"`
	Source    string `json:"source"`
	State     string `json:"state"`
}

// monotonicSetScript ensures we only update the anchor if the new version is strictly greater
// than any existing version in Redis, preventing race conditions or delayed writes.
var monotonicSetScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current then
    local ok, data = pcall(cjson.decode, current)
    if ok and data and data.version and tonumber(data.version) >= tonumber(ARGV[2]) then
        return 0
    end
end
redis.call('SET', KEYS[1], ARGV[1], 'EX', tonumber(ARGV[3]))
return 1
`)

type redisAnchorPublisher struct {
	client redis.Cmdable
	logger *zap.Logger
}

// NewRedisAnchorPublisher returns an AnchorPublisher that writes anchors to Redis with monotonic version safety.
func NewRedisAnchorPublisher(client redis.Cmdable, logger *zap.Logger) AnchorPublisher {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &redisAnchorPublisher{
		client: client,
		logger: logger,
	}
}

// NewRedisAnchorPublisherFromAddr connects to Redis at addr and returns an AnchorPublisher and cleanup closer.
func NewRedisAnchorPublisherFromAddr(addr string, logger *zap.Logger) (AnchorPublisher, func() error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	if addr == "" {
		return &redisAnchorPublisher{logger: logger}, func() error { return nil }
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	return NewRedisAnchorPublisher(client, logger), client.Close
}

func (p *redisAnchorPublisher) PublishAnchor(ctx context.Context, entry Entry, ttl time.Duration) error {
	if p.client == nil {
		return nil
	}
	// INVARIANT: Only FRESH external reference prices may be published as an MM validation anchor.
	if entry.State != StateFresh {
		p.logger.Debug("skipping redis anchor publish for non-fresh entry",
			zap.String("market_id", entry.MarketID),
			zap.String("state", entry.State.String()))
		return nil
	}

	ttlSec := int(ttl.Seconds())
	if ttlSec <= 0 {
		ttlSec = 60
	}

	payload := AnchorPayload{
		MarketID:  entry.MarketID,
		Price:     entry.Price.String(),
		Version:   entry.Version,
		FetchedAt: entry.FetchedAt.UTC().Format(time.RFC3339Nano),
		Source:    entry.Source,
		State:     entry.State.String(),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal anchor payload: %w", err)
	}

	key := "refprice:anchor:" + entry.MarketID
	res, err := monotonicSetScript.Run(ctx, p.client, []string{key}, string(payloadBytes), entry.Version, ttlSec).Result()
	if err != nil {
		return fmt.Errorf("redis monotonicSetScript error for %s: %w", key, err)
	}

	if val, ok := res.(int64); ok && val == 0 {
		p.logger.Debug("skipped anchor update — redis contains equal or higher version",
			zap.String("market_id", entry.MarketID),
			zap.Int64("attempted_version", entry.Version))
	} else {
		p.logger.Debug("published refprice anchor to redis",
			zap.String("market_id", entry.MarketID),
			zap.Int64("version", entry.Version),
			zap.String("price", payload.Price),
			zap.Int("ttl_sec", ttlSec))
	}

	return nil
}
