package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"tradedrift/services/market/internal/repository"
)

type TradeEventPayload struct {
	TradeID    uuid.UUID
	MarketID   string
	Price      decimal.Decimal
	Quantity   decimal.Decimal
	ExecutedAt time.Time
}

type MarketService interface {
	GetMarket(ctx context.Context, id string) (*repository.Market, error)
	ListMarkets(ctx context.Context) ([]*repository.Market, error)
	GetTicker(ctx context.Context, marketID string) (*repository.Ticker24h, error)
	GetCandles(ctx context.Context, marketID string, resolution string, from, to *time.Time, limit int) ([]*repository.OHLCCandle, error)
	ProcessTradeEvent(ctx context.Context, payload *TradeEventPayload) (bool, error)
}

type marketService struct {
	marketRepo repository.MarketRepository
	candleRepo repository.CandleRepository
}

func NewMarketService(marketRepo repository.MarketRepository, candleRepo repository.CandleRepository) MarketService {
	return &marketService{
		marketRepo: marketRepo,
		candleRepo: candleRepo,
	}
}

func (s *marketService) GetMarket(ctx context.Context, id string) (*repository.Market, error) {
	if id == "" {
		return nil, ErrInvalidMarketID
	}
	m, err := s.marketRepo.GetMarket(ctx, id)
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (s *marketService) ListMarkets(ctx context.Context) ([]*repository.Market, error) {
	return s.marketRepo.ListMarkets(ctx)
}

func (s *marketService) GetTicker(ctx context.Context, marketID string) (*repository.Ticker24h, error) {
	if marketID == "" {
		return nil, ErrInvalidMarketID
	}
	return s.marketRepo.GetTicker24h(ctx, marketID)
}

func (s *marketService) GetCandles(ctx context.Context, marketID string, resolution string, from, to *time.Time, limit int) ([]*repository.OHLCCandle, error) {
	if marketID == "" {
		return nil, ErrInvalidMarketID
	}

	validResolutions := map[string]bool{
		"1m": true, "5m": true, "15m": true, "1h": true, "1d": true,
	}
	if !validResolutions[resolution] {
		return nil, ErrInvalidResolution
	}

	if from != nil && to != nil && !from.Before(*to) {
		return nil, ErrInvalidTimeRange
	}

	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		return nil, ErrInvalidLimit
	}

	return s.candleRepo.GetCandles(ctx, marketID, resolution, from, to, limit)
}

func (s *marketService) ProcessTradeEvent(ctx context.Context, payload *TradeEventPayload) (bool, error) {
	if payload == nil || payload.MarketID == "" || payload.TradeID == uuid.Nil {
		return false, ErrInvalidMarketID
	}

	trade := &repository.MarketTrade{
		ID:         payload.TradeID,
		MarketID:   payload.MarketID,
		Price:      payload.Price,
		Quantity:   payload.Quantity,
		ExecutedAt: payload.ExecutedAt.UTC(),
	}

	processed, err := s.marketRepo.ProcessTrade(ctx, trade)
	if err != nil {
		return false, fmt.Errorf("process trade in service: %w", err)
	}

	return processed, nil
}
