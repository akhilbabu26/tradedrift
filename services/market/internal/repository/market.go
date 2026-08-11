package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Market struct {
	ID          string          `db:"id"`           // e.g. "BTC-USDT"
	BaseAsset   string          `db:"base_asset"`   // e.g. "BTC"
	QuoteAsset  string          `db:"quote_asset"`  // e.g. "USDT"
	TickSize    decimal.Decimal `db:"tick_size"`    // price step increment (e.g. 0.01)
	LotSize     decimal.Decimal `db:"lot_size"`     // base asset quantity step increment (e.g. 0.0001)
	Status      string          `db:"status"`       // "ACTIVE", "HALTED", "MAINTENANCE"
	MinQuantity decimal.Decimal `db:"min_quantity"` // minimum base asset quantity per order
	CreatedAt   time.Time       `db:"created_at"`
	UpdatedAt   time.Time       `db:"updated_at"`
}

type MarketTrade struct {
	ID         uuid.UUID       `db:"id"`
	MarketID   string          `db:"market_id"`
	Price      decimal.Decimal `db:"price"`
	Quantity   decimal.Decimal `db:"quantity"`
	ExecutedAt time.Time       `db:"executed_at"`
}

type OHLCCandle struct {
	MarketID     string          `db:"market_id"`
	Resolution   string          `db:"resolution"` // "1m", "5m", "15m", "1h", "1d"
	StartTime    time.Time       `db:"start_time"`
	OpenPrice    decimal.Decimal `db:"open_price"`
	HighPrice    decimal.Decimal `db:"high_price"`
	LowPrice     decimal.Decimal `db:"low_price"`
	ClosePrice   decimal.Decimal `db:"close_price"`
	Volume       decimal.Decimal `db:"volume"`       // base volume
	QuoteVolume  decimal.Decimal `db:"quote_volume"` // quote volume
	OpenTradeAt  time.Time       `db:"open_trade_at"`
	CloseTradeAt time.Time       `db:"close_trade_at"`
}

type Ticker24h struct {
	MarketID              string          `db:"market_id"`
	LastPrice             decimal.Decimal `db:"last_price"`
	High24h               decimal.Decimal `db:"high_24h"`
	Low24h                decimal.Decimal `db:"low_24h"`
	Volume24h             decimal.Decimal `db:"volume_24h"`
	QuoteVolume24h        decimal.Decimal `db:"quote_volume_24h"`
	PriceChange24hPercent decimal.Decimal `db:"price_change_24h_percent"`
}

type MarketRepository interface {
	GetMarket(ctx context.Context, id string) (*Market, error)
	ListMarkets(ctx context.Context) ([]*Market, error)
	ProcessTrade(ctx context.Context, trade *MarketTrade) (bool, error)
	GetTicker24h(ctx context.Context, marketID string) (*Ticker24h, error)
	DeleteOldTrades(ctx context.Context, olderThan time.Duration) (int64, error)
}

type CandleRepository interface {
	GetCandles(ctx context.Context, marketID string, resolution string, from, to *time.Time, limit int) ([]*OHLCCandle, error)
}
