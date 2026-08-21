package projection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
)

// RedisGetter abstracts Redis read operations for production and unit tests.
type RedisGetter interface {
	Get(ctx context.Context, key string) *redis.StringCmd
	MGet(ctx context.Context, keys ...string) *redis.SliceCmd
}

// Reader queries Redis for OrderBook depth projections.
type Reader struct {
	client RedisGetter
}

// NewReader creates a projection Reader using a real Redis client.
func NewReader(client *redis.Client) *Reader {
	return &Reader{client: client}
}

// NewCustomReader allows injecting test fakes or custom interfaces.
func NewCustomReader(client RedisGetter) *Reader {
	return &Reader{client: client}
}

// rawDepthMessage matches the exact JSON wire format emitted by Publisher.
type rawDepthMessage struct {
	MarketID string `json:"market_id"`
	Bids     []struct {
		Price    string `json:"price"`
		Quantity string `json:"quantity"`
	} `json:"bids"`
	Asks []struct {
		Price    string `json:"price"`
		Quantity string `json:"quantity"`
	} `json:"asks"`
	SnapshotAt string `json:"snapshot_at"`
}

// GetOrderBook retrieves and parses the current depth projection for a single market.
// Returns ErrNotFound if no projection exists for the given market.
func (r *Reader) GetOrderBook(ctx context.Context, marketID string) (*OrderBookProjection, error) {
	if marketID == "" {
		return nil, fmt.Errorf("%w: marketID cannot be empty", ErrInvalidData)
	}

	key := "depth:" + marketID
	val, err := r.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, marketID)
		}
		return nil, fmt.Errorf("redis get %q: %w", key, err)
	}

	return parseAndValidateSnapshot([]byte(val), marketID)
}

// GetOrderBooks retrieves projections for multiple markets via Redis MGET.
// Missing markets are omitted from the map and not treated as valid empty books.
func (r *Reader) GetOrderBooks(ctx context.Context, marketIDs []string) (map[string]*OrderBookProjection, error) {
	if len(marketIDs) == 0 {
		return make(map[string]*OrderBookProjection), nil
	}

	keys := make([]string, len(marketIDs))
	for i, id := range marketIDs {
		keys[i] = "depth:" + id
	}

	vals, err := r.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("redis mget: %w", err)
	}

	projections := make(map[string]*OrderBookProjection, len(marketIDs))
	for i, raw := range vals {
		if raw == nil {
			continue // key does not exist in Redis (not an empty book)
		}

		strVal, ok := raw.(string)
		if !ok {
			if byteVal, isBytes := raw.([]byte); isBytes {
				strVal = string(byteVal)
			} else {
				continue
			}
		}

		expectedMarketID := marketIDs[i]
		proj, err := parseAndValidateSnapshot([]byte(strVal), expectedMarketID)
		if err != nil {
			continue // skip corrupt or mismatched entries
		}
		projections[expectedMarketID] = proj
	}

	return projections, nil
}

// parseAndValidateSnapshot unmarshals JSON and strictly validates all domain rules:
// - MarketID matches expected
// - Timestamp is present and valid
// - Every Price and Quantity is strictly positive (> 0)
func parseAndValidateSnapshot(data []byte, expectedMarketID string) (*OrderBookProjection, error) {
	var raw rawDepthMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%w: malformed JSON: %v", ErrInvalidData, err)
	}

	if expectedMarketID != "" && raw.MarketID != expectedMarketID {
		return nil, fmt.Errorf("%w: market_id mismatch (expected %s, got %s)", ErrInvalidData, expectedMarketID, raw.MarketID)
	}

	if raw.SnapshotAt == "" {
		return nil, fmt.Errorf("%w: missing snapshot_at timestamp", ErrInvalidData)
	}

	snapshotTime, err := time.Parse(time.RFC3339Nano, raw.SnapshotAt)
	if err != nil {
		snapshotTime, err = time.Parse(time.RFC3339, raw.SnapshotAt)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid snapshot_at format: %v", ErrInvalidData, err)
		}
	}

	bids := make([]DepthLevel, len(raw.Bids))
	for i, b := range raw.Bids {
		price, err := decimal.NewFromString(b.Price)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid bid price %q: %v", ErrInvalidData, b.Price, err)
		}
		if price.LessThanOrEqual(decimal.Zero) {
			return nil, fmt.Errorf("%w: bid price must be > 0 (got %s)", ErrInvalidData, price)
		}

		qty, err := decimal.NewFromString(b.Quantity)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid bid quantity %q: %v", ErrInvalidData, b.Quantity, err)
		}
		if qty.LessThanOrEqual(decimal.Zero) {
			return nil, fmt.Errorf("%w: bid quantity must be > 0 (got %s)", ErrInvalidData, qty)
		}

		bids[i] = DepthLevel{Price: price, Quantity: qty}
	}

	asks := make([]DepthLevel, len(raw.Asks))
	for i, a := range raw.Asks {
		price, err := decimal.NewFromString(a.Price)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid ask price %q: %v", ErrInvalidData, a.Price, err)
		}
		if price.LessThanOrEqual(decimal.Zero) {
			return nil, fmt.Errorf("%w: ask price must be > 0 (got %s)", ErrInvalidData, price)
		}

		qty, err := decimal.NewFromString(a.Quantity)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid ask quantity %q: %v", ErrInvalidData, a.Quantity, err)
		}
		if qty.LessThanOrEqual(decimal.Zero) {
			return nil, fmt.Errorf("%w: ask quantity must be > 0 (got %s)", ErrInvalidData, qty)
		}

		asks[i] = DepthLevel{Price: price, Quantity: qty}
	}

	return &OrderBookProjection{
		MarketID:   raw.MarketID,
		Bids:       bids,
		Asks:       asks,
		SnapshotAt: snapshotTime.UTC(),
	}, nil
}
