// Package redisdepth provides a high-throughput, read-only Redis client for inspecting top-of-book depth.
package redisdepth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/controlled-taker/internal/metrics"
)

var (
	ErrDepthNotFound    = errors.New("depth snapshot not found")
	ErrEmptySnapshotAt  = errors.New("missing snapshot_at timestamp")
	ErrStaleSnapshot    = errors.New("stale depth snapshot")
	ErrFutureSnapshot   = errors.New("future depth snapshot timestamp")
	ErrMarketMismatch   = errors.New("market_id mismatch in depth snapshot")
	ErrIncompleteDepth  = errors.New("incomplete depth snapshot")
	ErrInvalidOrderBook = errors.New("invalid order book structure or level ordering")
	ErrCrossedOrderBook = errors.New("crossed or locked order book")
)

// DepthLevel is a parsed price level.
type DepthLevel struct {
	Price    decimal.Decimal
	Quantity decimal.Decimal
}

// DepthSnapshot is a parsed L2 order book depth snapshot.
type DepthSnapshot struct {
	MarketID   string
	Sequence   uint64
	Bids       []DepthLevel
	Asks       []DepthLevel
	SnapshotAt time.Time
}

type depthLevelDTO struct {
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
}

type depthSnapshotDTO struct {
	MarketID   string          `json:"market_id"`
	Sequence   uint64          `json:"sequence"`
	Bids       []depthLevelDTO `json:"bids"`
	Asks       []depthLevelDTO `json:"asks"`
	SnapshotAt string          `json:"snapshot_at"`
}

// Reader reads depth snapshots from Redis.
type Reader struct {
	client *redis.Client
	logger *zap.Logger
}

// NewReader creates a new Redis depth reader.
func NewReader(redisAddr string, logger *zap.Logger) (*Reader, error) {
	client := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping Redis at %s: %w", redisAddr, err)
	}

	return &Reader{
		client: client,
		logger: logger,
	}, nil
}

// Close closes the Redis connection.
func (r *Reader) Close() error {
	return r.client.Close()
}

// Ping checks Redis connectivity.
func (r *Reader) Ping(ctx context.Context) error {
	return r.client.Ping(ctx).Err()
}

// GetDepth reads and parses the current depth snapshot for marketID with strict fail-closed validation.
func (r *Reader) GetDepth(ctx context.Context, marketID string) (*DepthSnapshot, error) {
	key := "depth:" + marketID
	val, err := r.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			metrics.DepthReadErrors.WithLabelValues(marketID, "not_found").Inc()
			return nil, fmt.Errorf("%w for %s (book empty/unseeded)", ErrDepthNotFound, marketID)
		}
		metrics.DepthReadErrors.WithLabelValues(marketID, "redis_read_failed").Inc()
		return nil, fmt.Errorf("read %s from Redis: %w", key, err)
	}

	return ParseSnapshotJSON(marketID, []byte(val))
}

// ParseSnapshotJSON parses and strictly validates raw snapshot JSON bytes for marketID.
func ParseSnapshotJSON(marketID string, raw []byte) (*DepthSnapshot, error) {
	var dto depthSnapshotDTO
	if err := json.Unmarshal(raw, &dto); err != nil {
		metrics.DepthReadErrors.WithLabelValues(marketID, "unmarshal_error").Inc()
		return nil, fmt.Errorf("unmarshal depth for %s: %w", marketID, err)
	}

	// 1. Validate Market ID
	if dto.MarketID != marketID {
		metrics.DepthReadErrors.WithLabelValues(marketID, "market_mismatch").Inc()
		return nil, fmt.Errorf("%w: expected %s, got %s", ErrMarketMismatch, marketID, dto.MarketID)
	}

	// 2. Validate SnapshotAt Timestamp
	if dto.SnapshotAt == "" {
		metrics.DepthReadErrors.WithLabelValues(marketID, "missing_timestamp").Inc()
		return nil, fmt.Errorf("%w for %s", ErrEmptySnapshotAt, marketID)
	}

	snapshotTime, parseErr := time.Parse(time.RFC3339Nano, dto.SnapshotAt)
	if parseErr != nil || snapshotTime.IsZero() {
		snapshotTime, parseErr = time.Parse(time.RFC3339, dto.SnapshotAt)
		if parseErr != nil || snapshotTime.IsZero() {
			metrics.DepthReadErrors.WithLabelValues(marketID, "unparseable_timestamp").Inc()
			return nil, fmt.Errorf("%w for %s (%q): %v", ErrEmptySnapshotAt, marketID, dto.SnapshotAt, parseErr)
		}
	}

	now := time.Now()
	if now.Sub(snapshotTime) > 5*time.Second {
		metrics.DepthReadErrors.WithLabelValues(marketID, "stale").Inc()
		return nil, fmt.Errorf("%w for %s: %s old", ErrStaleSnapshot, marketID, now.Sub(snapshotTime))
	}
	if snapshotTime.Sub(now) > 1*time.Second {
		metrics.DepthReadErrors.WithLabelValues(marketID, "future_timestamp").Inc()
		return nil, fmt.Errorf("%w for %s: %s ahead", ErrFutureSnapshot, marketID, snapshotTime.Sub(now))
	}

	// 3. Validate Presence of Levels
	if len(dto.Bids) == 0 || len(dto.Asks) == 0 {
		metrics.DepthReadErrors.WithLabelValues(marketID, "incomplete_depth").Inc()
		return nil, fmt.Errorf("%w for %s: bids=%d, asks=%d", ErrIncompleteDepth, marketID, len(dto.Bids), len(dto.Asks))
	}

	// 4. Parse & Validate Bids (Strictly Descending, Price > 0, Qty > 0)
	bids := make([]DepthLevel, 0, len(dto.Bids))
	for i, b := range dto.Bids {
		p, errP := decimal.NewFromString(b.Price)
		q, errQ := decimal.NewFromString(b.Quantity)
		if errP != nil || errQ != nil || !p.GreaterThan(decimal.Zero) || !q.GreaterThan(decimal.Zero) {
			metrics.DepthReadErrors.WithLabelValues(marketID, "invalid_bid_level").Inc()
			return nil, fmt.Errorf("%w at bid index %d: price=%q, qty=%q", ErrInvalidOrderBook, i, b.Price, b.Quantity)
		}
		if i > 0 && !bids[i-1].Price.GreaterThan(p) {
			metrics.DepthReadErrors.WithLabelValues(marketID, "bids_not_descending").Inc()
			return nil, fmt.Errorf("%w: bid level %d price %s is not lower than previous %s", ErrInvalidOrderBook, i, p, bids[i-1].Price)
		}
		bids = append(bids, DepthLevel{Price: p, Quantity: q})
	}

	// 5. Parse & Validate Asks (Strictly Ascending, Price > 0, Qty > 0)
	asks := make([]DepthLevel, 0, len(dto.Asks))
	for i, a := range dto.Asks {
		p, errP := decimal.NewFromString(a.Price)
		q, errQ := decimal.NewFromString(a.Quantity)
		if errP != nil || errQ != nil || !p.GreaterThan(decimal.Zero) || !q.GreaterThan(decimal.Zero) {
			metrics.DepthReadErrors.WithLabelValues(marketID, "invalid_ask_level").Inc()
			return nil, fmt.Errorf("%w at ask index %d: price=%q, qty=%q", ErrInvalidOrderBook, i, a.Price, a.Quantity)
		}
		if i > 0 && !p.GreaterThan(asks[i-1].Price) {
			metrics.DepthReadErrors.WithLabelValues(marketID, "asks_not_ascending").Inc()
			return nil, fmt.Errorf("%w: ask level %d price %s is not higher than previous %s", ErrInvalidOrderBook, i, p, asks[i-1].Price)
		}
		asks = append(asks, DepthLevel{Price: p, Quantity: q})
	}

	// 6. Crossed/Locked Book Validation
	if !asks[0].Price.GreaterThan(bids[0].Price) {
		metrics.DepthReadErrors.WithLabelValues(marketID, "crossed_book").Inc()
		return nil, fmt.Errorf("%w for %s: best ask %s <= best bid %s", ErrCrossedOrderBook, marketID, asks[0].Price, bids[0].Price)
	}

	return &DepthSnapshot{
		MarketID:   dto.MarketID,
		Sequence:   dto.Sequence,
		Bids:       bids,
		Asks:       asks,
		SnapshotAt: snapshotTime,
	}, nil
}
