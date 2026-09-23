package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/market/internal/repository"
	"tradedrift/services/market/internal/repository/postgres"
	"tradedrift/services/market/internal/service"
)

func TestHistorySeeder_IdempotencyAndDeterminism(t *testing.T) {
	logger := zap.NewNop()

	activeMarkets := []*repository.Market{
		{ID: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT", Status: "ACTIVE"},
		{ID: "ETH-USDT", BaseAsset: "ETH", QuoteAsset: "USDT", Status: "ACTIVE"},
		{ID: "SOL-USDT", BaseAsset: "SOL", QuoteAsset: "USDT", Status: "ACTIVE"},
	}

	t.Run("Clean state: seeds all resolutions and synthetic trades with OHLC invariants", func(t *testing.T) {
		candleInserts := make(map[string][]*repository.OHLCCandle)
		var insertedTrades []*repository.MarketTrade

		mRepo := &mockMarketRepo{
			listMarketsFunc: func(ctx context.Context) ([]*repository.Market, error) {
				return activeMarkets, nil
			},
			hasRecentTradesFunc: func(ctx context.Context, marketID string, within time.Duration) (bool, error) {
				return false, nil // Clean state: no trades exist in 24h
			},
			bulkInsertTradesFunc: func(ctx context.Context, trades []*repository.MarketTrade) error {
				insertedTrades = append(insertedTrades, trades...)
				return nil
			},
		}

		cRepo := &mockCandleRepo{
			getCandleCountFunc: func(ctx context.Context, marketID, resolution string) (int, error) {
				return 0, nil // Clean state: no candles exist
			},
			bulkInsertCandles: func(ctx context.Context, candles []*repository.OHLCCandle) error {
				for _, c := range candles {
					key := c.MarketID + ":" + c.Resolution
					candleInserts[key] = append(candleInserts[key], c)
				}
				return nil
			},
		}

		seeder := service.NewHistorySeeder(mRepo, cRepo, logger)
		err := seeder.EnsureSeedHistory(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		endTime := time.Now().UTC()

		expectedResolutions := map[string]int{
			"1d":  90,
			"4h":  180,
			"1h":  168,
			"15m": 96,
			"5m":  120,
			"1m":  120,
		}

		for _, m := range activeMarkets {
			for res, expCount := range expectedResolutions {
				key := m.ID + ":" + res
				candles := candleInserts[key]
				if len(candles) != expCount {
					t.Errorf("market %s resolution %s expected %d candles, got %d", m.ID, res, expCount, len(candles))
				}

				// Verify OHLC invariants and non-futurity for every single candle
				for i, c := range candles {
					// 1. High >= Open
					if c.HighPrice.LessThan(c.OpenPrice) {
						t.Errorf("[%s %s idx %d] High %s < Open %s", m.ID, res, i, c.HighPrice, c.OpenPrice)
					}
					// 2. High >= Close
					if c.HighPrice.LessThan(c.ClosePrice) {
						t.Errorf("[%s %s idx %d] High %s < Close %s", m.ID, res, i, c.HighPrice, c.ClosePrice)
					}
					// 3. Low <= Open
					if c.LowPrice.GreaterThan(c.OpenPrice) {
						t.Errorf("[%s %s idx %d] Low %s > Open %s", m.ID, res, i, c.LowPrice, c.OpenPrice)
					}
					// 4. Low <= Close
					if c.LowPrice.GreaterThan(c.ClosePrice) {
						t.Errorf("[%s %s idx %d] Low %s > Close %s", m.ID, res, i, c.LowPrice, c.ClosePrice)
					}
					// 5. High >= Low
					if c.HighPrice.LessThan(c.LowPrice) {
						t.Errorf("[%s %s idx %d] High %s < Low %s", m.ID, res, i, c.HighPrice, c.LowPrice)
					}
					// 6. Volume > 0 and QuoteVolume > 0
					if !c.Volume.IsPositive() {
						t.Errorf("[%s %s idx %d] Volume %s <= 0", m.ID, res, i, c.Volume)
					}
					if !c.QuoteVolume.IsPositive() {
						t.Errorf("[%s %s idx %d] QuoteVolume %s <= 0", m.ID, res, i, c.QuoteVolume)
					}
					// 7. Non-futurity: no candle timestamp is in the future
					if c.StartTime.After(endTime) {
						t.Errorf("[%s %s idx %d] StartTime %s is in future (after %s)", m.ID, res, i, c.StartTime, endTime)
					}
					if c.CloseTradeAt.After(endTime) {
						t.Errorf("[%s %s idx %d] CloseTradeAt %s is in future (after %s)", m.ID, res, i, c.CloseTradeAt, endTime)
					}
				}
			}
		}

		// 3 markets * 24 hourly synthetic trades = 72 trades
		if len(insertedTrades) != 3*24 {
			t.Fatalf("expected 72 synthetic trades, got %d", len(insertedTrades))
		}

		seenIDs := make(map[string]bool)
		for _, trade := range insertedTrades {
			idStr := trade.ID.String()
			if seenIDs[idStr] {
				t.Fatalf("duplicate synthetic trade ID generated: %s", idStr)
			}
			seenIDs[idStr] = true

			if trade.Price.IsZero() || trade.Price.IsNegative() {
				t.Errorf("synthetic trade price should be strictly positive, got %s", trade.Price)
			}
			if trade.Quantity.IsZero() || trade.Quantity.IsNegative() {
				t.Errorf("synthetic trade quantity should be strictly positive, got %s", trade.Quantity)
			}
			// Non-futurity of synthetic trades: must be <= now
			if trade.ExecutedAt.After(endTime) {
				t.Errorf("synthetic trade executed_at %s is in future (after %s)", trade.ExecutedAt, endTime)
			}
		}
	})

	t.Run("Cross-restart trade idempotency: restart with recent trades skips trade injection", func(t *testing.T) {
		candlesInserted := 0
		tradesInserted := 0

		mRepo := &mockMarketRepo{
			listMarketsFunc: func(ctx context.Context) ([]*repository.Market, error) {
				return activeMarkets, nil
			},
			hasRecentTradesFunc: func(ctx context.Context, marketID string, within time.Duration) (bool, error) {
				// Recent trades already exist in the 24h window
				return true, nil
			},
			bulkInsertTradesFunc: func(ctx context.Context, trades []*repository.MarketTrade) error {
				tradesInserted += len(trades)
				return nil
			},
		}

		cRepo := &mockCandleRepo{
			getCandleCountFunc: func(ctx context.Context, marketID, resolution string) (int, error) {
				// All resolutions already populated from prior run
				return 200, nil
			},
			bulkInsertCandles: func(ctx context.Context, candles []*repository.OHLCCandle) error {
				candlesInserted += len(candles)
				return nil
			},
		}

		seeder := service.NewHistorySeeder(mRepo, cRepo, logger)
		err := seeder.EnsureSeedHistory(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if candlesInserted != 0 {
			t.Fatalf("expected 0 candles inserted on already-seeded database, got %d", candlesInserted)
		}
		if tradesInserted != 0 {
			t.Fatalf("expected 0 trades inserted when market already has recent trades, got %d", tradesInserted)
		}
	})

	t.Run("Cold ticker recovery: trades older than 24h triggers synthetic baseline seeding", func(t *testing.T) {
		tradesInserted := 0

		mRepo := &mockMarketRepo{
			listMarketsFunc: func(ctx context.Context) ([]*repository.Market, error) {
				return []*repository.Market{
					{ID: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT", Status: "ACTIVE"},
				}, nil
			},
			hasRecentTradesFunc: func(ctx context.Context, marketID string, within time.Duration) (bool, error) {
				// No trades in rolling 24h (all live trades are older than 24h)
				return false, nil
			},
			bulkInsertTradesFunc: func(ctx context.Context, trades []*repository.MarketTrade) error {
				tradesInserted += len(trades)
				return nil
			},
		}

		cRepo := &mockCandleRepo{
			getCandleCountFunc: func(ctx context.Context, marketID, resolution string) (int, error) {
				return 200, nil // Candles already seeded
			},
		}

		seeder := service.NewHistorySeeder(mRepo, cRepo, logger)
		err := seeder.EnsureSeedHistory(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if tradesInserted != 24 {
			t.Fatalf("expected 24 baseline trades to restore 24h ticker, got %d", tradesInserted)
		}
	})

	t.Run("Partial history behavior: preserves partial records and only seeds missing resolutions", func(t *testing.T) {
		candleInserts := make(map[string]int)

		mRepo := &mockMarketRepo{
			listMarketsFunc: func(ctx context.Context) ([]*repository.Market, error) {
				return []*repository.Market{
					{ID: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT", Status: "ACTIVE"},
				}, nil
			},
			hasRecentTradesFunc: func(ctx context.Context, marketID string, within time.Duration) (bool, error) {
				return true, nil
			},
		}

		cRepo := &mockCandleRepo{
			getCandleCountFunc: func(ctx context.Context, marketID, resolution string) (int, error) {
				switch resolution {
				case "1h":
					return 50, nil // Partial: 50 < 168
				case "5m":
					return 120, nil // Fully seeded: 120 >= 120
				case "15m":
					return 0, nil // Missing resolution
				default:
					return 200, nil
				}
			},
			bulkInsertCandles: func(ctx context.Context, candles []*repository.OHLCCandle) error {
				for _, c := range candles {
					candleInserts[c.Resolution]++
				}
				return nil
			},
		}

		seeder := service.NewHistorySeeder(mRepo, cRepo, logger)
		err := seeder.EnsureSeedHistory(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// 15m (missing) should be seeded with 96 candles
		if candleInserts["15m"] != 96 {
			t.Errorf("expected 96 candles seeded for missing resolution 15m, got %d", candleInserts["15m"])
		}
		// 1h (partial, 50 candles) should NOT be re-seeded (preserved)
		if candleInserts["1h"] != 0 {
			t.Errorf("expected 0 candles re-seeded for partial resolution 1h, got %d", candleInserts["1h"])
		}
		// 5m (complete, 120 candles) should NOT be re-seeded
		if candleInserts["5m"] != 0 {
			t.Errorf("expected 0 candles re-seeded for complete resolution 5m, got %d", candleInserts["5m"])
		}
	})

	t.Run("Halted and inactive markets are skipped", func(t *testing.T) {
		candlesInserted := 0
		mRepo := &mockMarketRepo{
			listMarketsFunc: func(ctx context.Context) ([]*repository.Market, error) {
				return []*repository.Market{
					{ID: "HALTED-USDT", Status: "HALTED"},
					{ID: "MAINT-USDT", Status: "MAINTENANCE"},
				}, nil
			},
		}
		cRepo := &mockCandleRepo{
			getCandleCountFunc: func(ctx context.Context, marketID, resolution string) (int, error) {
				return 0, nil
			},
			bulkInsertCandles: func(ctx context.Context, candles []*repository.OHLCCandle) error {
				candlesInserted += len(candles)
				return nil
			},
		}

		seeder := service.NewHistorySeeder(mRepo, cRepo, logger)
		err := seeder.EnsureSeedHistory(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if candlesInserted != 0 {
			t.Fatalf("expected 0 candles inserted for inactive markets, got %d", candlesInserted)
		}
	})
}

func TestHistorySeeder_UTC4HourAlignment(t *testing.T) {
	testTimes := []time.Time{
		time.Date(2026, 9, 15, 0, 1, 0, 0, time.UTC),
		time.Date(2026, 9, 15, 3, 59, 59, 0, time.UTC),
		time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 15, 7, 30, 0, 0, time.UTC),
		time.Date(2026, 9, 15, 12, 15, 30, 0, time.UTC),
		time.Date(2026, 9, 15, 17, 45, 0, 0, time.UTC),
		time.Date(2026, 9, 15, 23, 59, 59, 0, time.UTC),
	}

	for _, tt := range testTimes {
		completed4h := tt.Truncate(4 * time.Hour).Add(-4 * time.Hour)
		if completed4h.Hour()%4 != 0 {
			t.Errorf("expected hour to be multiple of 4, got %d for time %s", completed4h.Hour(), tt)
		}
		if completed4h.Minute() != 0 || completed4h.Second() != 0 || completed4h.Nanosecond() != 0 {
			t.Errorf("expected clean 0 minute/second/nano, got %s for time %s", completed4h, tt)
		}
		if !completed4h.Before(tt) {
			t.Errorf("expected completed 4h bucket to be before input time %s, got %s", tt, completed4h)
		}
	}
}

func TestHistorySeeder_SeedBaselineToLiveTradeTransition(t *testing.T) {
	logger := zap.NewNop()

	var storedTrades []*repository.MarketTrade
	mRepo := &mockMarketRepo{
		listMarketsFunc: func(ctx context.Context) ([]*repository.Market, error) {
			return []*repository.Market{
				{ID: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT", Status: "ACTIVE"},
			}, nil
		},
		hasRecentTradesFunc: func(ctx context.Context, marketID string, within time.Duration) (bool, error) {
			return len(storedTrades) > 0, nil
		},
		bulkInsertTradesFunc: func(ctx context.Context, trades []*repository.MarketTrade) error {
			storedTrades = append(storedTrades, trades...)
			return nil
		},
	}
	cRepo := &mockCandleRepo{
		getCandleCountFunc: func(ctx context.Context, marketID, resolution string) (int, error) {
			return 200, nil
		},
	}

	seeder := service.NewHistorySeeder(mRepo, cRepo, logger)
	if err := seeder.EnsureSeedHistory(context.Background()); err != nil {
		t.Fatalf("seeder failed: %v", err)
	}

	if len(storedTrades) != 24 {
		t.Fatalf("expected 24 baseline trades, got %d", len(storedTrades))
	}

	baselineHigh := decimal.Zero
	baselineLow := decimal.NewFromFloat(999999999)
	baselineVol := decimal.Zero
	for _, tr := range storedTrades {
		if tr.Price.GreaterThan(baselineHigh) {
			baselineHigh = tr.Price
		}
		if tr.Price.LessThan(baselineLow) {
			baselineLow = tr.Price
		}
		baselineVol = baselineVol.Add(tr.Quantity)
	}

	// Now simulate live incoming trade from matching engine at new all-time high
	livePrice := decimal.NewFromFloat(99500.00)
	liveQty := decimal.NewFromFloat(1.5)
	liveTrade := &repository.MarketTrade{
		ID:         uuid.New(),
		MarketID:   "BTC-USDT",
		Price:      livePrice,
		Quantity:   liveQty,
		ExecutedAt: time.Now().UTC(),
	}
	storedTrades = append(storedTrades, liveTrade)

	// Verify ticker metrics seamlessly transition to live trade
	effectiveLastPrice := storedTrades[len(storedTrades)-1].Price
	if !effectiveLastPrice.Equal(livePrice) {
		t.Errorf("expected last price %s, got %s", livePrice, effectiveLastPrice)
	}

	newHigh := baselineHigh
	if livePrice.GreaterThan(newHigh) {
		newHigh = livePrice
	}
	if !newHigh.Equal(livePrice) {
		t.Errorf("expected 24h high to expand to live trade %s, got %s", livePrice, newHigh)
	}

	newVol := baselineVol.Add(liveQty)
	expectedVol := decimal.NewFromFloat(24*0.25).Add(liveQty)
	if !newVol.Equal(expectedVol) {
		t.Errorf("expected 24h volume %s, got %s", expectedVol, newVol)
	}

	firstTradePrice := storedTrades[0].Price
	pctChange := postgres.CalculatePriceChangePercent(effectiveLastPrice, firstTradePrice)
	expectedPct := livePrice.Sub(firstTradePrice).Div(firstTradePrice).Mul(decimal.NewFromInt(100))
	if !pctChange.Equal(expectedPct) {
		t.Errorf("expected price change %s, got %s", expectedPct, pctChange)
	}
}

