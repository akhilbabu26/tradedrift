package repository

import "github.com/shopspring/decimal"

// MarketOverviewItem is the domain struct returned by GetMarketsOverview.
// It combines 24h ticker data with a lightweight close-price trend series.
// For full OHLCV data, use GetCandles.
type MarketOverviewItem struct {
	MarketID              string
	BaseAsset             string
	QuoteAsset            string
	LastPrice             decimal.Decimal
	High24h               decimal.Decimal
	Low24h                decimal.Decimal
	Volume24h             decimal.Decimal
	QuoteVolume24h        decimal.Decimal
	PriceChange24hPercent decimal.Decimal
	// Trend contains close prices as decimal strings, ordered oldest → newest.
	// May contain fewer than the requested limit if history is sparse.
	Trend []string
}
