package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"tradedrift/services/market/internal/repository"
	"tradedrift/services/market/internal/service"
)

type mockMarketRepo struct {
	getMarketFunc          func(ctx context.Context, id string) (*repository.Market, error)
	listMarketsFunc        func(ctx context.Context) ([]*repository.Market, error)
	processTradeFunc       func(ctx context.Context, trade *repository.MarketTrade) (bool, error)
	getTicker24hFunc       func(ctx context.Context, marketID string) (*repository.Ticker24h, error)
	deleteOldTradesFunc    func(ctx context.Context, olderThan time.Duration) (int64, error)
	getMarketsOverviewFunc func(ctx context.Context, resolution string, limit int) ([]*repository.MarketOverviewItem, error)
	hasRecentTradesFunc    func(ctx context.Context, marketID string, within time.Duration) (bool, error)
}

func (m *mockMarketRepo) GetMarket(ctx context.Context, id string) (*repository.Market, error) {
	if m.getMarketFunc != nil {
		return m.getMarketFunc(ctx, id)
	}
	return &repository.Market{ID: id, Status: "ACTIVE"}, nil
}
func (m *mockMarketRepo) ListMarkets(ctx context.Context) ([]*repository.Market, error) {
	if m.listMarketsFunc != nil {
		return m.listMarketsFunc(ctx)
	}
	return nil, nil
}
func (m *mockMarketRepo) ProcessTrade(ctx context.Context, trade *repository.MarketTrade) (bool, error) {
	if m.processTradeFunc != nil {
		return m.processTradeFunc(ctx, trade)
	}
	return true, nil
}
func (m *mockMarketRepo) GetTicker24h(ctx context.Context, marketID string) (*repository.Ticker24h, error) {
	if m.getTicker24hFunc != nil {
		return m.getTicker24hFunc(ctx, marketID)
	}
	return nil, nil
}
func (m *mockMarketRepo) DeleteOldTrades(ctx context.Context, olderThan time.Duration) (int64, error) {
	if m.deleteOldTradesFunc != nil {
		return m.deleteOldTradesFunc(ctx, olderThan)
	}
	return 0, nil
}
func (m *mockMarketRepo) GetMarketsOverview(ctx context.Context, resolution string, limit int) ([]*repository.MarketOverviewItem, error) {
	if m.getMarketsOverviewFunc != nil {
		return m.getMarketsOverviewFunc(ctx, resolution, limit)
	}
	return nil, nil
}
func (m *mockMarketRepo) HasRecentTrades(ctx context.Context, marketID string, within time.Duration) (bool, error) {
	if m.hasRecentTradesFunc != nil {
		return m.hasRecentTradesFunc(ctx, marketID, within)
	}
	return false, nil
}

type mockCandleRepo struct {
	getCandlesFunc     func(ctx context.Context, marketID, resolution string, from, to *time.Time, limit int) ([]*repository.OHLCCandle, error)
	getCandleCountFunc func(ctx context.Context, marketID, resolution string) (int, error)
}

func (c *mockCandleRepo) GetCandles(ctx context.Context, marketID, resolution string, from, to *time.Time, limit int) ([]*repository.OHLCCandle, error) {
	if c.getCandlesFunc != nil {
		return c.getCandlesFunc(ctx, marketID, resolution, from, to, limit)
	}
	return nil, nil
}
func (c *mockCandleRepo) GetCandleCount(ctx context.Context, marketID, resolution string) (int, error) {
	if c.getCandleCountFunc != nil {
		return c.getCandleCountFunc(ctx, marketID, resolution)
	}
	return 0, nil
}

func TestMarketService_GetCandles_Resolutions(t *testing.T) {
	resolutions := []string{"1m", "5m", "15m", "1h", "4h", "1d"}
	for _, res := range resolutions {
		t.Run("Valid resolution "+res, func(t *testing.T) {
			mRepo := &mockMarketRepo{}
			var queriedRes string
			cRepo := &mockCandleRepo{
				getCandlesFunc: func(ctx context.Context, marketID, resolution string, from, to *time.Time, limit int) ([]*repository.OHLCCandle, error) {
					queriedRes = resolution
					return []*repository.OHLCCandle{}, nil
				},
			}
			svc := service.NewMarketService(mRepo, cRepo)
			_, err := svc.GetCandles(context.Background(), "BTC-USDT", res, nil, nil, 50)
			if err != nil {
				t.Fatalf("unexpected error for resolution %s: %v", res, err)
			}
			if queriedRes != res {
				t.Fatalf("expected resolution %s, got %s", res, queriedRes)
			}
		})
	}

	t.Run("Invalid resolution rejected", func(t *testing.T) {
		svc := service.NewMarketService(&mockMarketRepo{}, &mockCandleRepo{})
		_, err := svc.GetCandles(context.Background(), "BTC-USDT", "2h", nil, nil, 50)
		if err != service.ErrInvalidResolution {
			t.Fatalf("expected ErrInvalidResolution, got %v", err)
		}
	})
}

func TestMarketService_GetMarketsOverview_LimitAndResolutionValidation(t *testing.T) {
	t.Run("Default limit when 0 or negative", func(t *testing.T) {
		var capturedLimit int
		mRepo := &mockMarketRepo{
			getMarketsOverviewFunc: func(ctx context.Context, resolution string, limit int) ([]*repository.MarketOverviewItem, error) {
				capturedLimit = limit
				return []*repository.MarketOverviewItem{}, nil
			},
		}
		svc := service.NewMarketService(mRepo, &mockCandleRepo{})
		_, err := svc.GetMarketsOverview(context.Background(), "1h", 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedLimit != 168 {
			t.Fatalf("expected default limit 168, got %d", capturedLimit)
		}

		_, err = svc.GetMarketsOverview(context.Background(), "1h", -10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedLimit != 168 {
			t.Fatalf("expected clamped limit 168, got %d", capturedLimit)
		}
	})

	t.Run("Clamped limit when greater than 500", func(t *testing.T) {
		var capturedLimit int
		mRepo := &mockMarketRepo{
			getMarketsOverviewFunc: func(ctx context.Context, resolution string, limit int) ([]*repository.MarketOverviewItem, error) {
				capturedLimit = limit
				return []*repository.MarketOverviewItem{}, nil
			},
		}
		svc := service.NewMarketService(mRepo, &mockCandleRepo{})
		_, err := svc.GetMarketsOverview(context.Background(), "4h", 1000)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedLimit != 500 {
			t.Fatalf("expected clamped limit 500, got %d", capturedLimit)
		}
	})

	t.Run("Default resolution when empty", func(t *testing.T) {
		var capturedRes string
		mRepo := &mockMarketRepo{
			getMarketsOverviewFunc: func(ctx context.Context, resolution string, limit int) ([]*repository.MarketOverviewItem, error) {
				capturedRes = resolution
				return []*repository.MarketOverviewItem{}, nil
			},
		}
		svc := service.NewMarketService(mRepo, &mockCandleRepo{})
		_, err := svc.GetMarketsOverview(context.Background(), "", 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedRes != "1h" {
			t.Fatalf("expected default resolution '1h', got %s", capturedRes)
		}
	})

	t.Run("Invalid resolution rejected", func(t *testing.T) {
		svc := service.NewMarketService(&mockMarketRepo{}, &mockCandleRepo{})
		_, err := svc.GetMarketsOverview(context.Background(), "invalid", 100)
		if err != service.ErrInvalidResolution {
			t.Fatalf("expected ErrInvalidResolution, got %v", err)
		}
	})
}

func TestMarketService_RefreshAllTickers(t *testing.T) {
	t.Run("Refreshes only active markets into Redis", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		defer rdb.Close()

		refreshedMarkets := make(map[string]bool)
		mRepo := &mockMarketRepo{
			listMarketsFunc: func(ctx context.Context) ([]*repository.Market, error) {
				return []*repository.Market{
					{ID: "BTC-USDT", Status: "ACTIVE"},
					{ID: "ETH-USDT", Status: "ACTIVE"},
					{ID: "PAUSED-USDT", Status: "PAUSED"},
				}, nil
			},
			getTicker24hFunc: func(ctx context.Context, marketID string) (*repository.Ticker24h, error) {
				refreshedMarkets[marketID] = true
				return &repository.Ticker24h{
					MarketID:              marketID,
					LastPrice:             decimal.NewFromInt(100),
					High24h:               decimal.NewFromInt(105),
					Low24h:                decimal.NewFromInt(95),
					Volume24h:             decimal.NewFromInt(10),
					QuoteVolume24h:        decimal.NewFromInt(1000),
					PriceChange24hPercent: decimal.NewFromFloat(5.0),
				}, nil
			},
		}

		svc := service.NewMarketService(mRepo, &mockCandleRepo{}, rdb)
		err = svc.RefreshAllTickers(context.Background())
		if err != nil {
			t.Fatalf("unexpected error refreshing tickers: %v", err)
		}

		if !refreshedMarkets["BTC-USDT"] || !refreshedMarkets["ETH-USDT"] {
			t.Fatalf("expected active markets to be refreshed, got: %v", refreshedMarkets)
		}
		if refreshedMarkets["PAUSED-USDT"] {
			t.Fatalf("paused market should not be refreshed")
		}

		// Verify keys exist in Redis
		valBTC, err := rdb.Get(context.Background(), "ticker:BTC-USDT").Result()
		if err != nil || valBTC == "" {
			t.Fatalf("expected ticker:BTC-USDT in redis, got: %v", err)
		}
		valETH, err := rdb.Get(context.Background(), "ticker:ETH-USDT").Result()
		if err != nil || valETH == "" {
			t.Fatalf("expected ticker:ETH-USDT in redis, got: %v", err)
		}
		_, err = rdb.Get(context.Background(), "ticker:PAUSED-USDT").Result()
		if err == nil {
			t.Fatalf("did not expect ticker:PAUSED-USDT in redis")
		}
	})

	t.Run("Failed refresh preserves existing legitimate cached data", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatalf("failed to start miniredis: %v", err)
		}
		defer mr.Close()

		rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		defer rdb.Close()

		ctx := context.Background()
		initialData := `{"marketId":"BTC-USDT","lastPrice":"82000"}`
		if err := rdb.Set(ctx, "ticker:BTC-USDT", initialData, 24*time.Hour).Err(); err != nil {
			t.Fatalf("failed to seed redis: %v", err)
		}

		// Mock repository returns an error on GetTicker24h
		mRepo := &mockMarketRepo{
			listMarketsFunc: func(ctx context.Context) ([]*repository.Market, error) {
				return []*repository.Market{
					{ID: "BTC-USDT", Status: "ACTIVE"},
				}, nil
			},
			getTicker24hFunc: func(ctx context.Context, marketID string) (*repository.Ticker24h, error) {
				return nil, errors.New("database connection failure")
			},
		}

		svc := service.NewMarketService(mRepo, &mockCandleRepo{}, rdb)
		_ = svc.RefreshAllTickers(ctx)

		// Pre-existing valid cached data MUST not be deleted or corrupted
		cached, err := rdb.Get(ctx, "ticker:BTC-USDT").Result()
		if err != nil || cached != initialData {
			t.Fatalf("expected legitimate cached data to be preserved, got err=%v data=%s", err, cached)
		}
	})
}

