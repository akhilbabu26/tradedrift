package projection_test

import (
	"context"
	"errors"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"tradedrift/services/matching-engine/internal/projection"
)

// ─── Fake Redis ───────────────────────────────────────────────────────────────

type fakeRedis struct {
	data map[string]string
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{data: make(map[string]string)}
}

func (f *fakeRedis) Get(_ context.Context, key string) *redis.StringCmd {
	cmd := redis.NewStringCmd(context.Background())
	val, ok := f.data[key]
	if !ok {
		cmd.SetErr(redis.Nil)
		return cmd
	}
	cmd.SetVal(val)
	return cmd
}

func (f *fakeRedis) MGet(_ context.Context, keys ...string) *redis.SliceCmd {
	cmd := redis.NewSliceCmd(context.Background())
	results := make([]any, len(keys))
	for i, k := range keys {
		if v, ok := f.data[k]; ok {
			results[i] = v
		} else {
			results[i] = nil
		}
	}
	cmd.SetVal(results)
	return cmd
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestReadSnapshot_Success(t *testing.T) {
	rdb := newFakeRedis()
	rdb.data["depth:BTC-USDT"] = `{
		"market_id": "BTC-USDT",
		"bids": [{"price": "65000.50", "quantity": "1.50000"}],
		"asks": [{"price": "65005.00", "quantity": "0.80000"}],
		"snapshot_at": "2026-08-18T10:00:00.123456789Z"
	}`

	reader := projection.NewCustomReader(rdb)
	proj, err := reader.GetOrderBook(context.Background(), "BTC-USDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if proj.MarketID != "BTC-USDT" {
		t.Errorf("expected BTC-USDT, got %s", proj.MarketID)
	}
}

func TestMissingSnapshot(t *testing.T) {
	rdb := newFakeRedis()
	reader := projection.NewCustomReader(rdb)

	_, err := reader.GetOrderBook(context.Background(), "ETH-USDT")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, projection.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMalformedJSON(t *testing.T) {
	rdb := newFakeRedis()
	rdb.data["depth:BTC-USDT"] = `{"market_id": "BTC-USDT", bids: broken`

	reader := projection.NewCustomReader(rdb)
	_, err := reader.GetOrderBook(context.Background(), "BTC-USDT")
	if err == nil || !errors.Is(err, projection.ErrInvalidData) {
		t.Fatalf("expected ErrInvalidData, got %v", err)
	}
}

func TestInvalidPrice(t *testing.T) {
	rdb := newFakeRedis()
	rdb.data["depth:BTC-USDT"] = `{
		"market_id": "BTC-USDT",
		"bids": [{"price": "not-a-number", "quantity": "1.0"}],
		"asks": [],
		"snapshot_at": "2026-08-18T10:00:00Z"
	}`

	reader := projection.NewCustomReader(rdb)
	_, err := reader.GetOrderBook(context.Background(), "BTC-USDT")
	if err == nil || !errors.Is(err, projection.ErrInvalidData) {
		t.Fatalf("expected ErrInvalidData for non-numeric price, got %v", err)
	}
}

func TestInvalidQuantity(t *testing.T) {
	rdb := newFakeRedis()
	rdb.data["depth:BTC-USDT"] = `{
		"market_id": "BTC-USDT",
		"bids": [{"price": "60000.00", "quantity": "abc"}],
		"asks": [],
		"snapshot_at": "2026-08-18T10:00:00Z"
	}`

	reader := projection.NewCustomReader(rdb)
	_, err := reader.GetOrderBook(context.Background(), "BTC-USDT")
	if err == nil || !errors.Is(err, projection.ErrInvalidData) {
		t.Fatalf("expected ErrInvalidData for non-numeric quantity, got %v", err)
	}
}

func TestNegativePrice(t *testing.T) {
	rdb := newFakeRedis()
	rdb.data["depth:BTC-USDT"] = `{
		"market_id": "BTC-USDT",
		"bids": [{"price": "-100.00", "quantity": "1.0"}],
		"asks": [],
		"snapshot_at": "2026-08-18T10:00:00Z"
	}`

	reader := projection.NewCustomReader(rdb)
	_, err := reader.GetOrderBook(context.Background(), "BTC-USDT")
	if err == nil || !errors.Is(err, projection.ErrInvalidData) {
		t.Fatalf("expected ErrInvalidData for negative price, got %v", err)
	}
}

func TestNegativeQuantity(t *testing.T) {
	rdb := newFakeRedis()
	rdb.data["depth:BTC-USDT"] = `{
		"market_id": "BTC-USDT",
		"bids": [{"price": "60000.00", "quantity": "-0.5"}],
		"asks": [],
		"snapshot_at": "2026-08-18T10:00:00Z"
	}`

	reader := projection.NewCustomReader(rdb)
	_, err := reader.GetOrderBook(context.Background(), "BTC-USDT")
	if err == nil || !errors.Is(err, projection.ErrInvalidData) {
		t.Fatalf("expected ErrInvalidData for negative quantity, got %v", err)
	}
}

func TestZeroPrice(t *testing.T) {
	rdb := newFakeRedis()
	rdb.data["depth:BTC-USDT"] = `{
		"market_id": "BTC-USDT",
		"bids": [{"price": "0.00", "quantity": "1.0"}],
		"asks": [],
		"snapshot_at": "2026-08-18T10:00:00Z"
	}`

	reader := projection.NewCustomReader(rdb)
	_, err := reader.GetOrderBook(context.Background(), "BTC-USDT")
	if err == nil || !errors.Is(err, projection.ErrInvalidData) {
		t.Fatalf("expected ErrInvalidData for zero price, got %v", err)
	}
}

func TestMarketIDMismatch(t *testing.T) {
	rdb := newFakeRedis()
	rdb.data["depth:BTC-USDT"] = `{
		"market_id": "ETH-USDT",
		"bids": [{"price": "60000.00", "quantity": "1.0"}],
		"asks": [],
		"snapshot_at": "2026-08-18T10:00:00Z"
	}`

	reader := projection.NewCustomReader(rdb)
	_, err := reader.GetOrderBook(context.Background(), "BTC-USDT")
	if err == nil || !errors.Is(err, projection.ErrInvalidData) {
		t.Fatalf("expected ErrInvalidData for market ID mismatch, got %v", err)
	}
}

func TestEmptyBids(t *testing.T) {
	rdb := newFakeRedis()
	rdb.data["depth:BTC-USDT"] = `{
		"market_id": "BTC-USDT",
		"bids": [],
		"asks": [{"price": "65000.00", "quantity": "1.0"}],
		"snapshot_at": "2026-08-18T10:00:00Z"
	}`

	reader := projection.NewCustomReader(rdb)
	proj, err := reader.GetOrderBook(context.Background(), "BTC-USDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := proj.BestBid(); ok {
		t.Error("expected BestBid to return false for empty bids")
	}
	if _, ok := proj.Spread(); ok {
		t.Error("expected Spread to return false when bids are empty")
	}
	if _, ok := proj.MidPrice(); ok {
		t.Error("expected MidPrice to return false when bids are empty")
	}
}

func TestEmptyAsks(t *testing.T) {
	rdb := newFakeRedis()
	rdb.data["depth:BTC-USDT"] = `{
		"market_id": "BTC-USDT",
		"bids": [{"price": "65000.00", "quantity": "1.0"}],
		"asks": [],
		"snapshot_at": "2026-08-18T10:00:00Z"
	}`

	reader := projection.NewCustomReader(rdb)
	proj, err := reader.GetOrderBook(context.Background(), "BTC-USDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := proj.BestAsk(); ok {
		t.Error("expected BestAsk to return false for empty asks")
	}
	if _, ok := proj.Spread(); ok {
		t.Error("expected Spread to return false when asks are empty")
	}
	if _, ok := proj.MidPrice(); ok {
		t.Error("expected MidPrice to return false when asks are empty")
	}
}

func TestEmptyBook(t *testing.T) {
	rdb := newFakeRedis()
	rdb.data["depth:BTC-USDT"] = `{
		"market_id": "BTC-USDT",
		"bids": [],
		"asks": [],
		"snapshot_at": "2026-08-18T10:00:00Z"
	}`

	reader := projection.NewCustomReader(rdb)
	proj, err := reader.GetOrderBook(context.Background(), "BTC-USDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !proj.IsEmpty() {
		t.Error("expected IsEmpty to be true")
	}
}

func TestMultipleLevels_BestBidAskSpreadMidPrice(t *testing.T) {
	rdb := newFakeRedis()
	rdb.data["depth:BTC-USDT"] = `{
		"market_id": "BTC-USDT",
		"bids": [
			{"price": "65000.00", "quantity": "1.0"},
			{"price": "64990.00", "quantity": "2.0"},
			{"price": "64980.00", "quantity": "3.0"}
		],
		"asks": [
			{"price": "65010.00", "quantity": "0.5"},
			{"price": "65020.00", "quantity": "1.5"}
		],
		"snapshot_at": "2026-08-18T10:00:00Z"
	}`

	reader := projection.NewCustomReader(rdb)
	proj, err := reader.GetOrderBook(context.Background(), "BTC-USDT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bestBid, ok := proj.BestBid()
	if !ok || !bestBid.Price.Equal(decimal.RequireFromString("65000.00")) {
		t.Errorf("expected best bid 65000.00, got %+v", bestBid)
	}

	bestAsk, ok := proj.BestAsk()
	if !ok || !bestAsk.Price.Equal(decimal.RequireFromString("65010.00")) {
		t.Errorf("expected best ask 65010.00, got %+v", bestAsk)
	}

	spread, ok := proj.Spread()
	if !ok || !spread.Equal(decimal.RequireFromString("10.00")) {
		t.Errorf("expected spread 10.00, got %s", spread)
	}

	mid, ok := proj.MidPrice()
	if !ok || !mid.Equal(decimal.RequireFromString("65005.00")) {
		t.Errorf("expected mid price 65005.00, got %s", mid)
	}
}
