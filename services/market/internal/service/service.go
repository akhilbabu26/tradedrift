package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"tradedrift/services/market/internal/repository"
)

var validCandleResolutions = map[string]struct{}{
	"1m":  {},
	"5m":  {},
	"15m": {},
	"1h":  {},
	"1d":  {},
}

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
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrInvalidMarketID
	}
	m, err := s.marketRepo.GetMarket(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrMarketNotFound) {
			return nil, ErrMarketNotFound
		}
		return nil, fmt.Errorf("get market: %w", err)
	}
	return m, nil
}

func (s *marketService) ListMarkets(ctx context.Context) ([]*repository.Market, error) {
	return s.marketRepo.ListMarkets(ctx)
}

func (s *marketService) GetTicker(ctx context.Context, marketID string) (*repository.Ticker24h, error) {
	marketID = strings.TrimSpace(marketID)
	if marketID == "" {
		return nil, ErrInvalidMarketID
	}
	ticker, err := s.marketRepo.GetTicker24h(ctx, marketID)
	if err != nil {
		if errors.Is(err, repository.ErrMarketNotFound) {
			return nil, ErrMarketNotFound
		}
		return nil, fmt.Errorf("get ticker: %w", err)
	}
	return ticker, nil
}

func (s *marketService) GetCandles(ctx context.Context, marketID string, resolution string, from, to *time.Time, limit int) ([]*repository.OHLCCandle, error) {
	marketID = strings.TrimSpace(marketID)
	if marketID == "" {
		return nil, ErrInvalidMarketID
	}
	if _, ok := validCandleResolutions[resolution]; !ok {
		return nil, ErrInvalidResolution
	}
	if from != nil && to != nil && !from.Before(*to) {
		return nil, ErrInvalidTimeRange
	}
	// Reject negative numbers and limits above 500
	if limit < 0 || limit > 500 {
		return nil, ErrInvalidLimit
	}
	// Default to 100 when unspecified (0)
	if limit == 0 {
		limit = 100
	}
	// Verify market exists
	if _, err := s.marketRepo.GetMarket(ctx, marketID); err != nil {
		if errors.Is(err, repository.ErrMarketNotFound) {
			return nil, ErrMarketNotFound
		}
		return nil, fmt.Errorf("verify market: %w", err)
	}
	return s.candleRepo.GetCandles(ctx, marketID, resolution, from, to, limit)
}

func (s *marketService) ProcessTradeEvent(ctx context.Context, payload *TradeEventPayload) (bool, error) {
	if payload == nil {
		return false, ErrInvalidTradeEvent
	}
	if payload.TradeID == uuid.Nil {
		return false, ErrInvalidTradeEvent
	}

	marketID := strings.TrimSpace(payload.MarketID)
	if marketID == "" {
		return false, ErrInvalidMarketID
	}
	if payload.Price.LessThanOrEqual(decimal.Zero) || payload.Quantity.LessThanOrEqual(decimal.Zero) {
		return false, ErrInvalidTradeEvent
	}
	if payload.ExecutedAt.IsZero() {
		return false, ErrInvalidTradeEvent
	}

	// Verify market exists before attempting trade insertion (prevents FK violation loop)
	if _, err := s.marketRepo.GetMarket(ctx, marketID); err != nil {
		if errors.Is(err, repository.ErrMarketNotFound) {
			return false, ErrMarketNotFound
		}
		return false, fmt.Errorf("verify market in trade event: %w", err)
	}

	trade := &repository.MarketTrade{
		ID:         payload.TradeID,
		MarketID:   marketID,
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

