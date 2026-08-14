package market

import (
	"time"

	marketv1 "tradedrift/platform/api/gen/market/v1"
)

type MarketDTO struct {
	ID          string    `json:"id"`
	BaseAsset   string    `json:"base_asset"`
	QuoteAsset  string    `json:"quote_asset"`
	TickSize    string    `json:"tick_size"`
	LotSize     string    `json:"lot_size"`
	Status      string    `json:"status"`
	MinQuantity string    `json:"min_quantity"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Ticker24hDTO struct {
	MarketID              string `json:"market_id"`
	LastPrice             string `json:"last_price"`
	High24h               string `json:"high_24h"`
	Low24h                string `json:"low_24h"`
	Volume24h             string `json:"volume_24h"`
	QuoteVolume24h        string `json:"quote_volume_24h"`
	PriceChange24hPercent string `json:"price_change_24h_percent"`
}

type CandleDTO struct {
	StartTime   time.Time `json:"start_time"`
	Open        string    `json:"open"`
	High        string    `json:"high"`
	Low         string    `json:"low"`
	Close       string    `json:"close"`
	Volume      string    `json:"volume"`
	QuoteVolume string    `json:"quote_volume"`
}

func marketDTO(m *marketv1.Market) MarketDTO {
	return MarketDTO{
		ID:          m.GetId(),
		BaseAsset:   m.GetBaseAsset(),
		QuoteAsset:  m.GetQuoteAsset(),
		TickSize:    m.GetTickSize(),
		LotSize:     m.GetLotSize(),
		Status:      m.GetStatus().String(),
		MinQuantity: m.GetMinQuantity(),
		CreatedAt:   m.GetCreatedAt().AsTime(),
		UpdatedAt:   m.GetUpdatedAt().AsTime(),
	}
}

func tickerDTO(t *marketv1.Ticker24H) Ticker24hDTO {
	return Ticker24hDTO{
		MarketID:              t.GetMarketId(),
		LastPrice:             t.GetLastPrice(),
		High24h:               t.GetHigh_24H(),
		Low24h:                t.GetLow_24H(),
		Volume24h:             t.GetVolume_24H(),
		QuoteVolume24h:        t.GetQuoteVolume_24H(),
		PriceChange24hPercent: t.GetPriceChange_24HPercent(),
	}
}

func candleDTO(c *marketv1.Candle) CandleDTO {
	return CandleDTO{
		StartTime:   c.GetStartTime().AsTime(),
		Open:        c.GetOpen(),
		High:        c.GetHigh(),
		Low:         c.GetLow(),
		Close:       c.GetClose(),
		Volume:      c.GetVolume(),
		QuoteVolume: c.GetQuoteVolume(),
	}
}
