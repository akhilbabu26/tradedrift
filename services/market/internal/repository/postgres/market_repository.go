package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"tradedrift/services/market/internal/repository"
)

type MarketRepository struct {
	pool *pgxpool.Pool
}

func NewMarketRepository(pool *pgxpool.Pool) *MarketRepository {
	return &MarketRepository{pool: pool}
}

func (r *MarketRepository) GetMarket(ctx context.Context, id string) (*repository.Market, error) {
	query := `
		SELECT id, base_asset, quote_asset, tick_size, lot_size, status, min_quantity, created_at, updated_at
		FROM markets
		WHERE id = $1
	`
	var m repository.Market
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&m.ID, &m.BaseAsset, &m.QuoteAsset, &m.TickSize, &m.LotSize,
		&m.Status, &m.MinQuantity, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrMarketNotFound
		}
		return nil, fmt.Errorf("query market: %w", err)
	}
	return &m, nil
}

func (r *MarketRepository) ListMarkets(ctx context.Context) ([]*repository.Market, error) {
	query := `
		SELECT id, base_asset, quote_asset, tick_size, lot_size, status, min_quantity, created_at, updated_at
		FROM markets
		ORDER BY id ASC
	`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list markets: %w", err)
	}
	defer rows.Close()

	var markets []*repository.Market
	for rows.Next() {
		var m repository.Market
		if err := rows.Scan(
			&m.ID, &m.BaseAsset, &m.QuoteAsset, &m.TickSize, &m.LotSize,
			&m.Status, &m.MinQuantity, &m.CreatedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan market row: %w", err)
		}
		markets = append(markets, &m)
	}
	return markets, rows.Err()
}

func (r *MarketRepository) ProcessTrade(ctx context.Context, trade *repository.MarketTrade) (bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	insertTradeQuery := `
		INSERT INTO market_trades (id, market_id, price, quantity, executed_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO NOTHING
	`
	tag, err := tx.Exec(ctx, insertTradeQuery, trade.ID, trade.MarketID, trade.Price, trade.Quantity, trade.ExecutedAt)
	if err != nil {
		return false, fmt.Errorf("insert market trade: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return false, nil // Duplicate trade event absorbed cleanly
	}

	quoteVolume := trade.Price.Mul(trade.Quantity)
	resolutions := []struct {
		res      string
		duration time.Duration
	}{
		{"1m", time.Minute},
		{"5m", 5 * time.Minute},
		{"15m", 15 * time.Minute},
		{"1h", time.Hour},
		{"1d", 24 * time.Hour},
	}

	upsertCandleQuery := `
		INSERT INTO ohlc_candles (
			market_id, resolution, start_time, open_price, high_price, low_price, close_price,
			volume, quote_volume, open_trade_at, close_trade_at
		) VALUES ($1, $2, $3, $4, $4, $4, $4, $5, $6, $7, $7)
		ON CONFLICT (market_id, resolution, start_time) DO UPDATE SET
			open_price     = CASE 
								WHEN EXCLUDED.open_trade_at < ohlc_candles.open_trade_at THEN EXCLUDED.open_price 
								ELSE ohlc_candles.open_price 
							 END,
			open_trade_at  = LEAST(ohlc_candles.open_trade_at, EXCLUDED.open_trade_at),
			high_price     = GREATEST(ohlc_candles.high_price, EXCLUDED.high_price),
			low_price      = LEAST(ohlc_candles.low_price, EXCLUDED.low_price),
			close_price    = CASE 
								WHEN EXCLUDED.close_trade_at >= ohlc_candles.close_trade_at THEN EXCLUDED.close_price 
								ELSE ohlc_candles.close_price 
							 END,
			close_trade_at = GREATEST(ohlc_candles.close_trade_at, EXCLUDED.close_trade_at),
			volume         = ohlc_candles.volume + EXCLUDED.volume,
			quote_volume   = ohlc_candles.quote_volume + EXCLUDED.quote_volume
	`

	for _, item := range resolutions {
		startTime := trade.ExecutedAt.Truncate(item.duration)
		if item.res == "1d" {
			t := trade.ExecutedAt.UTC()
			startTime = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		}

		_, err := tx.Exec(ctx, upsertCandleQuery,
			trade.MarketID, item.res, startTime, trade.Price, trade.Quantity, quoteVolume, trade.ExecutedAt,
		)
		if err != nil {
			return false, fmt.Errorf("upsert candle (%s): %w", item.res, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit tx: %w", err)
	}

	return true, nil
}

func (r *MarketRepository) GetTicker24h(ctx context.Context, marketID string) (*repository.Ticker24h, error) {
	query := `
		WITH target_market AS (
			SELECT id FROM markets WHERE id = $1
		),
		trades_24h AS (
			SELECT price, quantity, executed_at
			FROM market_trades
			WHERE market_id = $1 AND executed_at >= NOW() - INTERVAL '24 hours'
		),
		latest_trade AS (
			SELECT price AS last_price
			FROM market_trades
			WHERE market_id = $1
			ORDER BY executed_at DESC
			LIMIT 1
		),
		first_trade_24h AS (
			SELECT price AS first_price
			FROM trades_24h
			ORDER BY executed_at ASC
			LIMIT 1
		)
		SELECT 
			m.id,
			COALESCE((SELECT last_price FROM latest_trade), 0) AS last_price,
			COALESCE(MAX(t.price), 0) AS high_24h,
			COALESCE(MIN(t.price), 0) AS low_24h,
			COALESCE(SUM(t.quantity), 0) AS volume_24h,
			COALESCE(SUM(t.price * t.quantity), 0) AS quote_volume_24h,
			COALESCE((SELECT first_price FROM first_trade_24h), 0) AS first_price_24h
		FROM target_market m
		LEFT JOIN trades_24h t ON TRUE
		GROUP BY m.id
	`

	var id string
	var lastPrice, high24h, low24h, volume24h, quoteVolume24h, firstPrice24h decimal.Decimal

	err := r.pool.QueryRow(ctx, query, marketID).Scan(
		&id, &lastPrice, &high24h, &low24h, &volume24h, &quoteVolume24h, &firstPrice24h,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrMarketNotFound
		}
		return nil, fmt.Errorf("query ticker 24h: %w", err)
	}

	priceChangePct := decimal.Zero
	if !firstPrice24h.IsZero() {
		priceChangePct = lastPrice.Sub(firstPrice24h).Div(firstPrice24h).Mul(decimal.NewFromInt(100))
	}

	return &repository.Ticker24h{
		MarketID:              marketID,
		LastPrice:             lastPrice,
		High24h:               high24h,
		Low24h:                low24h,
		Volume24h:             volume24h,
		QuoteVolume24h:        quoteVolume24h,
		PriceChange24hPercent: priceChangePct,
	}, nil
}


func (r *MarketRepository) DeleteOldTrades(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	query := `DELETE FROM market_trades WHERE executed_at < $1`
	ct, err := r.pool.Exec(ctx, query, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete old trades: %w", err)
	}
	return ct.RowsAffected(), nil
}
