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

// BestBid returns the top-of-book bid level, if present.
func (s *DepthSnapshot) BestBid() (DepthLevel, bool) {
	if s == nil || len(s.Bids) == 0 {
		return DepthLevel{}, false
	}
	return s.Bids[0], true
}

// BestAsk returns the top-of-book ask level, if present.
func (s *DepthSnapshot) BestAsk() (DepthLevel, bool) {
	if s == nil || len(s.Asks) == 0 {
		return DepthLevel{}, false
	}
	return s.Asks[0], true
}

// Spread returns the difference between BestAsk and BestBid.
func (s *DepthSnapshot) Spread() (decimal.Decimal, bool) {
	bid, okBid := s.BestBid()
	ask, okAsk := s.BestAsk()
	if !okBid || !okAsk {
		return decimal.Zero, false
	}
	return ask.Price.Sub(bid.Price), true
}

// MidPrice returns (BestBid + BestAsk) / 2.
func (s *DepthSnapshot) MidPrice() (decimal.Decimal, bool) {
	bid, okBid := s.BestBid()
	ask, okAsk := s.BestAsk()
	if !okBid || !okAsk {
		return decimal.Zero, false
	}
	return bid.Price.Add(ask.Price).Div(decimal.NewFromInt(2)), true
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

const DefaultMaxDepthAge = 5 * time.Second

// Reader reads depth snapshots from Redis.
type Reader struct {
	client      *redis.Client
	logger      *zap.Logger
	maxDepthAge time.Duration
}

// NewReader creates a new Redis depth reader with DefaultMaxDepthAge (5s).
func NewReader(redisAddr string, logger *zap.Logger) (*Reader, error) {
	return NewReaderWithMaxAge(redisAddr, DefaultMaxDepthAge, logger)
}

// NewReaderWithMaxAge creates a new Redis depth reader with custom max depth staleness threshold.
func NewReaderWithMaxAge(redisAddr string, maxAge time.Duration, logger *zap.Logger) (*Reader, error) {
	if maxAge <= 0 {
		maxAge = DefaultMaxDepthAge
	}
	client := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping Redis at %s: %w", redisAddr, err)
	}

	return &Reader{
		client:      client,
		logger:      logger,
		maxDepthAge: maxAge,
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

	return ParseSnapshotJSONWithMaxAge(marketID, []byte(val), r.maxDepthAge)
}

// ParseSnapshotJSON parses and strictly validates raw snapshot JSON bytes using DefaultMaxDepthAge.
func ParseSnapshotJSON(marketID string, raw []byte) (*DepthSnapshot, error) {
	return ParseSnapshotJSONWithMaxAge(marketID, raw, DefaultMaxDepthAge)
}

// ParseSnapshotJSONWithMaxAge parses and strictly validates raw snapshot JSON bytes for marketID against maxAge.
func ParseSnapshotJSONWithMaxAge(marketID string, raw []byte, maxAge time.Duration) (*DepthSnapshot, error) {
	if maxAge <= 0 {
		maxAge = DefaultMaxDepthAge
	}
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
	if now.Sub(snapshotTime) > maxAge {
		metrics.DepthReadErrors.WithLabelValues(marketID, "stale").Inc()
		return nil, fmt.Errorf("%w for %s: %s old (max allowed: %s)", ErrStaleSnapshot, marketID, now.Sub(snapshotTime), maxAge)
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

// RefPriceAnchor represents external reference price context published to Redis by LE.
type RefPriceAnchor struct {
	MarketID  string          `json:"market_id"`
	Price     decimal.Decimal `json:"price"`
	Version   int64           `json:"version"`
	State     string          `json:"state"`
	FetchedAt time.Time       `json:"fetched_at"`
}

type refPriceAnchorDTO struct {
	MarketID  string `json:"market_id"`
	Price     string `json:"price"`
	Version   int64  `json:"version"`
	FetchedAt string `json:"fetched_at"`
	Source    string `json:"source"`
	State     string `json:"state"`
}

// GetAnchor reads the current reference price anchor for marketID from Redis.
// Returns (nil, nil) if no anchor is found.
// Enforces market identity, FRESH state, and freshness of the fetched_at timestamp.
func (r *Reader) GetAnchor(ctx context.Context, marketID string) (*RefPriceAnchor, error) {
	key := "refprice:anchor:" + marketID
	val, err := r.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("read anchor %s: %w", key, err)
	}

	var dto refPriceAnchorDTO
	if err := json.Unmarshal([]byte(val), &dto); err != nil {
		return nil, fmt.Errorf("unmarshal anchor %s: %w", key, err)
	}

	if dto.MarketID != marketID {
		return nil, fmt.Errorf("anchor market mismatch: requested %s, got %s", marketID, dto.MarketID)
	}

	if dto.State != "FRESH" {
		return nil, fmt.Errorf("anchor state %q is not FRESH for %s", dto.State, marketID)
	}

	price, err := decimal.NewFromString(dto.Price)
	if err != nil || !price.GreaterThan(decimal.Zero) {
		return nil, fmt.Errorf("invalid anchor price %q", dto.Price)
	}

	var fetchedAt time.Time
	if dto.FetchedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, dto.FetchedAt); err == nil {
			fetchedAt = t
		} else if t, err := time.Parse(time.RFC3339, dto.FetchedAt); err == nil {
			fetchedAt = t
		}
	}

	if fetchedAt.IsZero() {
		return nil, fmt.Errorf("missing or unparseable fetched_at timestamp for anchor %s", marketID)
	}
	now := time.Now()
	if now.Sub(fetchedAt) > 60*time.Second {
		return nil, fmt.Errorf("anchor for %s is stale: fetched %s ago", marketID, now.Sub(fetchedAt))
	}
	if fetchedAt.Sub(now) > 1*time.Second {
		return nil, fmt.Errorf("anchor for %s has future fetched_at: %s ahead", marketID, fetchedAt.Sub(now))
	}

	return &RefPriceAnchor{
		MarketID:  dto.MarketID,
		Price:     price,
		Version:   dto.Version,
		State:     dto.State,
		FetchedAt: fetchedAt,
	}, nil
}

