package service_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"tradedrift/services/market/internal/repository"
	"tradedrift/services/market/internal/service"
)

// simulatedDbRepo simulates the exact Postgres upsert semantics of market_repository.go
type simulatedDbRepo struct {
	mu      sync.Mutex
	markets map[string]*repository.Market
	trades  map[uuid.UUID]*repository.MarketTrade
	candles map[string]*repository.OHLCCandle // key: marketID + resolution + startTime
}

func newSimulatedDbRepo() *simulatedDbRepo {
	repo := &simulatedDbRepo{
		markets: make(map[string]*repository.Market),
		trades:  make(map[uuid.UUID]*repository.MarketTrade),
		candles: make(map[string]*repository.OHLCCandle),
	}
	repo.markets["BTC-USDT"] = &repository.Market{
		ID:     "BTC-USDT",
		Status: "ACTIVE",
	}
	return repo
}

func (r *simulatedDbRepo) GetMarket(ctx context.Context, id string) (*repository.Market, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.markets[id]
	if !ok {
		return nil, repository.ErrMarketNotFound
	}
	return m, nil
}

func (r *simulatedDbRepo) ListMarkets(ctx context.Context) ([]*repository.Market, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var list []*repository.Market
	for _, m := range r.markets {
		list = append(list, m)
	}
	return list, nil
}

func (r *simulatedDbRepo) ProcessTrade(ctx context.Context, trade *repository.MarketTrade) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Idempotency: check trade conflict
	if _, exists := r.trades[trade.ID]; exists {
		return false, nil
	}
	r.trades[trade.ID] = trade

	quoteVolume := trade.Price.Mul(trade.Quantity)
	resolutions := []struct {
		res      string
		duration time.Duration
	}{
		{"1m", 1 * time.Minute},
		{"5m", 5 * time.Minute},
		{"15m", 15 * time.Minute},
		{"1h", 1 * time.Hour},
		{"4h", 4 * time.Hour},
		{"1d", 24 * time.Hour},
	}

	for _, item := range resolutions {
		startTime := trade.ExecutedAt.Truncate(item.duration)
		if item.res == "1d" {
			t := trade.ExecutedAt.UTC()
			startTime = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		}

		key := trade.MarketID + ":" + item.res + ":" + startTime.Format(time.RFC3339)
		existing, ok := r.candles[key]
		if !ok {
			r.candles[key] = &repository.OHLCCandle{
				MarketID:     trade.MarketID,
				Resolution:   item.res,
				StartTime:    startTime,
				OpenPrice:    trade.Price,
				HighPrice:    trade.Price,
				LowPrice:     trade.Price,
				ClosePrice:   trade.Price,
				Volume:       trade.Quantity,
				QuoteVolume:  quoteVolume,
				OpenTradeAt:  trade.ExecutedAt,
				CloseTradeAt: trade.ExecutedAt,
			}
		} else {
			// Matches Postgres upsert GREATEST/LEAST semantics
			if trade.ExecutedAt.Before(existing.OpenTradeAt) {
				existing.OpenPrice = trade.Price
				existing.OpenTradeAt = trade.ExecutedAt
			}
			if trade.Price.GreaterThan(existing.HighPrice) {
				existing.HighPrice = trade.Price
			}
			if trade.Price.LessThan(existing.LowPrice) {
				existing.LowPrice = trade.Price
			}
			if !trade.ExecutedAt.Before(existing.CloseTradeAt) {
				existing.ClosePrice = trade.Price
				existing.CloseTradeAt = trade.ExecutedAt
			}
			existing.Volume = existing.Volume.Add(trade.Quantity)
			existing.QuoteVolume = existing.QuoteVolume.Add(quoteVolume)
		}
	}

	return true, nil
}

func (r *simulatedDbRepo) GetTicker24h(ctx context.Context, marketID string) (*repository.Ticker24h, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ticker := &repository.Ticker24h{
		MarketID:              marketID,
		LastPrice:             decimal.Zero,
		High24h:               decimal.Zero,
		Low24h:                decimal.Zero,
		Volume24h:             decimal.Zero,
		QuoteVolume24h:        decimal.Zero,
		PriceChange24hPercent: decimal.Zero,
	}

	var latestTrade *repository.MarketTrade
	var firstTrade *repository.MarketTrade
	now := time.Now()
	cutoff := now.Add(-24 * time.Hour)

	for _, t := range r.trades {
		if t.MarketID != marketID {
			continue
		}
		if latestTrade == nil || t.ExecutedAt.After(latestTrade.ExecutedAt) {
			latestTrade = t
		}
		if t.ExecutedAt.After(cutoff) {
			if firstTrade == nil || t.ExecutedAt.Before(firstTrade.ExecutedAt) {
				firstTrade = t
			}
			if ticker.High24h.IsZero() || t.Price.GreaterThan(ticker.High24h) {
				ticker.High24h = t.Price
			}
			if ticker.Low24h.IsZero() || t.Price.LessThan(ticker.Low24h) {
				ticker.Low24h = t.Price
			}
			ticker.Volume24h = ticker.Volume24h.Add(t.Quantity)
			ticker.QuoteVolume24h = ticker.QuoteVolume24h.Add(t.Price.Mul(t.Quantity))
		}
	}

	if latestTrade != nil {
		ticker.LastPrice = latestTrade.Price
	}
	if firstTrade != nil && !firstTrade.Price.IsZero() {
		change := ticker.LastPrice.Sub(firstTrade.Price)
		ticker.PriceChange24hPercent = change.Div(firstTrade.Price).Mul(decimal.NewFromInt(100))
	}

	return ticker, nil
}

func (r *simulatedDbRepo) DeleteOldTrades(ctx context.Context, olderThan time.Duration) (int64, error) {
	return 0, nil
}

func (r *simulatedDbRepo) GetMarketsOverview(ctx context.Context, resolution string, limit int) ([]*repository.MarketOverviewItem, error) {
	return nil, nil
}

func (r *simulatedDbRepo) HasRecentTrades(ctx context.Context, marketID string, within time.Duration) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cutoff := time.Now().Add(-within)
	for _, t := range r.trades {
		if t.MarketID == marketID && t.ExecutedAt.After(cutoff) {
			return true, nil
		}
	}
	return false, nil
}

func (r *simulatedDbRepo) GetCandles(ctx context.Context, marketID, resolution string, from, to *time.Time, limit int) ([]*repository.OHLCCandle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var result []*repository.OHLCCandle
	for _, c := range r.candles {
		if c.MarketID == marketID && c.Resolution == resolution {
			result = append(result, c)
		}
	}
	return result, nil
}

func (r *simulatedDbRepo) GetCandleCount(ctx context.Context, marketID, resolution string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, c := range r.candles {
		if c.MarketID == marketID && c.Resolution == resolution {
			count++
		}
	}
	return count, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// INTEGRATION TESTS: Clean-Start Invariants & Service Recovery
// ─────────────────────────────────────────────────────────────────────────────

func TestMarketPipeline_CleanStart_And_RecoveryInvariants(t *testing.T) {
	ctx := context.Background()

	// Setup embedded redis
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer rdb.Close()

	dbRepo := newSimulatedDbRepo()

	// Invariant 1: Fresh state -> market_trades = 0, all candle resolutions = 0
	resolutions := []string{"1m", "5m", "15m", "1h", "4h", "1d"}
	for _, res := range resolutions {
		count, err := dbRepo.GetCandleCount(ctx, "BTC-USDT", res)
		if err != nil {
			t.Fatalf("failed to get candle count for %s: %v", res, err)
		}
		if count != 0 {
			t.Fatalf("Invariant 1 Violated: expected 0 candles for %s on fresh DB, got %d", res, count)
		}
	}

	// Initialize Market Service
	marketSvc := service.NewMarketService(dbRepo, dbRepo, rdb)

	// ── Trade #1: T = 10:00:10 UTC, Price = 100, Qty = 1 ─────────────────────────
	t1Time := time.Date(2026, 10, 2, 10, 0, 10, 0, time.UTC)
	t1ID := uuid.New()
	p1, _ := marketSvc.ProcessTradeEvent(ctx, &service.TradeEventPayload{
		TradeID:    t1ID,
		MarketID:   "BTC-USDT",
		Price:      decimal.NewFromInt(100),
		Quantity:   decimal.NewFromInt(1),
		ExecutedAt: t1Time,
	})
	if !p1 {
		t.Fatal("expected trade #1 to be processed")
	}

	// Verify Trade #1 Invariants
	for _, res := range resolutions {
		candles, err := marketSvc.GetCandles(ctx, "BTC-USDT", res, nil, nil, 10)
		if err != nil {
			t.Fatalf("failed to get candles for %s: %v", res, err)
		}
		if len(candles) != 1 {
			t.Fatalf("expected 1 candle for %s after Trade 1, got %d", res, len(candles))
		}
		if res == "1m" {
			c := candles[0]
			if !c.OpenPrice.Equal(decimal.NewFromInt(100)) ||
				!c.HighPrice.Equal(decimal.NewFromInt(100)) ||
				!c.LowPrice.Equal(decimal.NewFromInt(100)) ||
				!c.ClosePrice.Equal(decimal.NewFromInt(100)) ||
				!c.Volume.Equal(decimal.NewFromInt(1)) {
				t.Fatalf("1m candle 1 mismatch: O=%s H=%s L=%s C=%s V=%s",
					c.OpenPrice, c.HighPrice, c.LowPrice, c.ClosePrice, c.Volume)
			}
		}
	}

	// Verify Redis Ticker after Trade #1
	val, err := rdb.Get(ctx, "ticker:BTC-USDT").Result()
	if err != nil {
		t.Fatalf("expected ticker:BTC-USDT in redis, got error: %v", err)
	}
	var tickerPayload map[string]interface{}
	if err := json.Unmarshal([]byte(val), &tickerPayload); err != nil {
		t.Fatalf("failed to parse redis ticker json: %v", err)
	}
	if tickerPayload["lastPrice"] != "100" {
		t.Fatalf("expected redis ticker lastPrice '100', got '%v'", tickerPayload["lastPrice"])
	}
	ttl := mr.TTL("ticker:BTC-USDT")
	if ttl <= 0 || ttl > 60*time.Second {
		t.Fatalf("expected redis ticker TTL between 1s and 60s, got %v", ttl)
	}

	// ── Trade #2: T = 10:00:20 UTC (same 1m bucket), Price = 105, Qty = 2 ────────
	t2Time := time.Date(2026, 10, 2, 10, 0, 20, 0, time.UTC)
	t2ID := uuid.New()
	p2, _ := marketSvc.ProcessTradeEvent(ctx, &service.TradeEventPayload{
		TradeID:    t2ID,
		MarketID:   "BTC-USDT",
		Price:      decimal.NewFromInt(105),
		Quantity:   decimal.NewFromInt(2),
		ExecutedAt: t2Time,
	})
	if !p2 {
		t.Fatal("expected trade #2 to be processed")
	}

	// Verify 1m candle count is still 1 (NOT 2), and updated: O=100, H=105, L=100, C=105, V=3
	candles1m, _ := marketSvc.GetCandles(ctx, "BTC-USDT", "1m", nil, nil, 10)
	if len(candles1m) != 1 {
		t.Fatalf("Invariant 3 Violated: expected 1m candle count to remain 1, got %d", len(candles1m))
	}
	c1m := candles1m[0]
	if !c1m.OpenPrice.Equal(decimal.NewFromInt(100)) ||
		!c1m.HighPrice.Equal(decimal.NewFromInt(105)) ||
		!c1m.LowPrice.Equal(decimal.NewFromInt(100)) ||
		!c1m.ClosePrice.Equal(decimal.NewFromInt(105)) ||
		!c1m.Volume.Equal(decimal.NewFromInt(3)) {
		t.Fatalf("1m candle update mismatch: O=%s H=%s L=%s C=%s V=%s",
			c1m.OpenPrice, c1m.HighPrice, c1m.LowPrice, c1m.ClosePrice, c1m.Volume)
	}

	// ── Trade #3: T = 10:01:15 UTC (next 1m bucket), Price = 95, Qty = 1 ─────────
	t3Time := time.Date(2026, 10, 2, 10, 1, 15, 0, time.UTC)
	t3ID := uuid.New()
	p3, _ := marketSvc.ProcessTradeEvent(ctx, &service.TradeEventPayload{
		TradeID:    t3ID,
		MarketID:   "BTC-USDT",
		Price:      decimal.NewFromInt(95),
		Quantity:   decimal.NewFromInt(1),
		ExecutedAt: t3Time,
	})
	if !p3 {
		t.Fatal("expected trade #3 to be processed")
	}

	// Verify 1m has 2 candles, 5m / 15m / 1h remain 1 candle
	candles1mAfterT3, _ := marketSvc.GetCandles(ctx, "BTC-USDT", "1m", nil, nil, 10)
	if len(candles1mAfterT3) != 2 {
		t.Fatalf("Invariant 4 Violated: expected 2 candles for 1m, got %d", len(candles1mAfterT3))
	}
	candles5m, _ := marketSvc.GetCandles(ctx, "BTC-USDT", "5m", nil, nil, 10)
	if len(candles5m) != 1 {
		t.Fatalf("Invariant 4 Violated: expected 5m candle count to remain 1, got %d", len(candles5m))
	}
	candles1h, _ := marketSvc.GetCandles(ctx, "BTC-USDT", "1h", nil, nil, 10)
	if len(candles1h) != 1 {
		t.Fatalf("Invariant 4 Violated: expected 1h candle count to remain 1, got %d", len(candles1h))
	}

	// ── Test Market Service Restart / Recovery ───────────────────────────────────
	// Destroy marketSvc reference and create a fresh instance pointing to same DB & Redis
	marketSvcRestarted := service.NewMarketService(dbRepo, dbRepo, rdb)

	// Trade #4: T = 10:01:45 UTC (same 1m bucket as Trade #3), Price = 98, Qty = 1
	t4Time := time.Date(2026, 10, 2, 10, 1, 45, 0, time.UTC)
	t4ID := uuid.New()
	p4, _ := marketSvcRestarted.ProcessTradeEvent(ctx, &service.TradeEventPayload{
		TradeID:    t4ID,
		MarketID:   "BTC-USDT",
		Price:      decimal.NewFromInt(98),
		Quantity:   decimal.NewFromInt(1),
		ExecutedAt: t4Time,
	})
	if !p4 {
		t.Fatal("expected trade #4 to be processed after restart")
	}

	// Invariant: After restart, 1m candle count must STILL be 2 (NO duplicate candles)
	candles1mAfterRestart, _ := marketSvcRestarted.GetCandles(ctx, "BTC-USDT", "1m", nil, nil, 10)
	if len(candles1mAfterRestart) != 2 {
		t.Fatalf("Restart Recovery Violated: expected 2 candles for 1m, got %d", len(candles1mAfterRestart))
	}

	// Re-sending Trade #4 must be idempotent (return false, 0 new candles)
	p4Duplicate, err := marketSvcRestarted.ProcessTradeEvent(ctx, &service.TradeEventPayload{
		TradeID:    t4ID,
		MarketID:   "BTC-USDT",
		Price:      decimal.NewFromInt(98),
		Quantity:   decimal.NewFromInt(1),
		ExecutedAt: t4Time,
	})
	if err != nil {
		t.Fatalf("unexpected error on duplicate trade: %v", err)
	}
	if p4Duplicate {
		t.Fatalf("expected duplicate trade #4 to return processed=false")
	}
}
