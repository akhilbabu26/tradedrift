package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/market/internal/repository"
)

type CandleRepository struct {
	pool *pgxpool.Pool
}

func NewCandleRepository(pool *pgxpool.Pool) *CandleRepository {
	return &CandleRepository{pool: pool}
}

func (r *CandleRepository) GetCandles(
	ctx context.Context, marketID string, resolution string, from, to *time.Time, limit int,
) ([]*repository.OHLCCandle, error) {
	query := `
		SELECT market_id, resolution, start_time, open_price, high_price, low_price, close_price, volume, quote_volume, open_trade_at, close_trade_at
		FROM ohlc_candles
		WHERE market_id = $1 AND resolution = $2
	`
	args := []any{marketID, resolution}
	argIdx := 3

	if from != nil {
		query += fmt.Sprintf(" AND start_time >= $%d", argIdx)
		args = append(args, *from)
		argIdx++
	}
	if to != nil {
		query += fmt.Sprintf(" AND start_time < $%d", argIdx)
		args = append(args, *to)
		argIdx++
	}

	query += fmt.Sprintf(" ORDER BY start_time DESC LIMIT $%d", argIdx)
	args = append(args, limit)

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get candles: %w", err)
	}
	defer rows.Close()

	var candles []*repository.OHLCCandle
	for rows.Next() {
		var c repository.OHLCCandle
		if err := rows.Scan(
			&c.MarketID, &c.Resolution, &c.StartTime, &c.OpenPrice,
			&c.HighPrice, &c.LowPrice, &c.ClosePrice, &c.Volume, &c.QuoteVolume,
			&c.OpenTradeAt, &c.CloseTradeAt,
		); err != nil {
			return nil, fmt.Errorf("scan candle row: %w", err)
		}
		candles = append(candles, &c)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Reverse slice so returned array is chronological (ASC) for chart engines
	for i, j := 0, len(candles)-1; i < j; i, j = i+1, j-1 {
		candles[i], candles[j] = candles[j], candles[i]
	}

	return candles, nil
}
