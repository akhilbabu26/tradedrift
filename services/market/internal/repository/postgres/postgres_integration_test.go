package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"tradedrift/services/market/internal/repository"
	postgresrepo "tradedrift/services/market/internal/repository/postgres"
)

// TestPostgresRepository_RealDatabase_Upsert executes against a dedicated test database
// ONLY when TEST_DATABASE_URL is explicitly set.
// It protects the development environment from accidental truncation.
func TestPostgresRepository_RealDatabase_Upsert(t *testing.T) {
	testDSN := os.Getenv("TEST_DATABASE_URL")
	if testDSN == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live PostgreSQL integration test to protect development database")
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, testDSN)
	if err != nil {
		t.Fatalf("failed to connect to test database at %s: %v", testDSN, err)
	}
	defer pool.Close()

	// Ensure BTC-USDT market exists in test DB
	_, err = pool.Exec(ctx, `
		INSERT INTO markets (id, base_asset, quote_asset, tick_size, lot_size, min_quantity, status)
		VALUES ('BTC-USDT', 'BTC', 'USDT', '0.01', '0.0001', '0.0001', 'ACTIVE')
		ON CONFLICT (id) DO NOTHING;
	`)
	if err != nil {
		t.Fatalf("failed to ensure test market exists: %v", err)
	}

	// Clean test tables at start and end of this test
	cleanup := func() {
		_, _ = pool.Exec(ctx, "DELETE FROM market_trades WHERE market_id = 'BTC-USDT';")
		_, _ = pool.Exec(ctx, "DELETE FROM ohlc_candles WHERE market_id = 'BTC-USDT';")
	}
	cleanup()
	defer cleanup()

	marketRepo := postgresrepo.NewMarketRepository(pool)
	candleRepo := postgresrepo.NewCandleRepository(pool)

	// Invariant 1: Fresh state -> 0 candles
	c0, err := candleRepo.GetCandles(ctx, "BTC-USDT", "1m", nil, nil, 10)
	if err != nil {
		t.Fatalf("unexpected error getting initial candles: %v", err)
	}
	if len(c0) != 0 {
		t.Fatalf("expected 0 candles in test DB, got %d", len(c0))
	}

	// ── Trade #1: T = 10:00:10 UTC, Price = 100.00, Qty = 1.00 ──────────────────
	t1Time := time.Date(2026, 10, 2, 10, 0, 10, 0, time.UTC)
	t1ID := uuid.New()
	trade1 := &repository.MarketTrade{
		ID:         t1ID,
		MarketID:   "BTC-USDT",
		Price:      decimal.NewFromInt(100),
		Quantity:   decimal.NewFromInt(1),
		ExecutedAt: t1Time,
	}

	processed1, err := marketRepo.ProcessTrade(ctx, trade1)
	if err != nil {
		t.Fatalf("failed to process trade #1 in PostgreSQL: %v", err)
	}
	if !processed1 {
		t.Fatal("expected trade #1 to be processed")
	}

	// Verify real PostgreSQL table contents for Trade #1
	c1, err := candleRepo.GetCandles(ctx, "BTC-USDT", "1m", nil, nil, 10)
	if err != nil {
		t.Fatalf("failed to get 1m candles after trade #1: %v", err)
	}
	if len(c1) != 1 {
		t.Fatalf("expected exactly 1 candle in PostgreSQL after trade #1, got %d", len(c1))
	}
	candle1 := c1[0]
	if !candle1.OpenPrice.Equal(decimal.NewFromInt(100)) ||
		!candle1.HighPrice.Equal(decimal.NewFromInt(100)) ||
		!candle1.LowPrice.Equal(decimal.NewFromInt(100)) ||
		!candle1.ClosePrice.Equal(decimal.NewFromInt(100)) ||
		!candle1.Volume.Equal(decimal.NewFromInt(1)) {
		t.Fatalf("PostgreSQL candle mismatch after trade #1: O=%s H=%s L=%s C=%s V=%s",
			candle1.OpenPrice, candle1.HighPrice, candle1.LowPrice, candle1.ClosePrice, candle1.Volume)
	}

	// ── Trade #2: T = 10:00:30 UTC, Price = 108.00, Qty = 2.00 (Same 1m bucket) ──
	t2Time := time.Date(2026, 10, 2, 10, 0, 30, 0, time.UTC)
	t2ID := uuid.New()
	trade2 := &repository.MarketTrade{
		ID:         t2ID,
		MarketID:   "BTC-USDT",
		Price:      decimal.NewFromInt(108),
		Quantity:   decimal.NewFromInt(2),
		ExecutedAt: t2Time,
	}

	processed2, err := marketRepo.ProcessTrade(ctx, trade2)
	if err != nil {
		t.Fatalf("failed to process trade #2 in PostgreSQL: %v", err)
	}
	if !processed2 {
		t.Fatal("expected trade #2 to be processed")
	}

	// Verify real PostgreSQL ON CONFLICT DO UPDATE behavior:
	// 1m candle count MUST still be 1 (no duplicate row in PostgreSQL table)
	c2, err := candleRepo.GetCandles(ctx, "BTC-USDT", "1m", nil, nil, 10)
	if err != nil {
		t.Fatalf("failed to get 1m candles after trade #2: %v", err)
	}
	if len(c2) != 1 {
		t.Fatalf("PostgreSQL ON CONFLICT update failed: expected 1 candle, got %d", len(c2))
	}
	candle2 := c2[0]
	if !candle2.OpenPrice.Equal(decimal.NewFromInt(100)) ||
		!candle2.HighPrice.Equal(decimal.NewFromInt(108)) ||
		!candle2.LowPrice.Equal(decimal.NewFromInt(100)) ||
		!candle2.ClosePrice.Equal(decimal.NewFromInt(108)) ||
		!candle2.Volume.Equal(decimal.NewFromInt(3)) {
		t.Fatalf("PostgreSQL ON CONFLICT DO UPDATE values mismatch: O=%s H=%s L=%s C=%s V=%s",
			candle2.OpenPrice, candle2.HighPrice, candle2.LowPrice, candle2.ClosePrice, candle2.Volume)
	}

	// ── Trade #2 Duplicate: Replaying trade #2 with same UUID must be idempotent ─
	processed2Dup, err := marketRepo.ProcessTrade(ctx, trade2)
	if err != nil {
		t.Fatalf("unexpected error replaying duplicate trade: %v", err)
	}
	if processed2Dup {
		t.Fatal("expected duplicate trade to return processed=false from PostgreSQL ON CONFLICT (id) DO NOTHING")
	}
}
