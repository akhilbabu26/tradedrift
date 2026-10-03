package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/market/internal/repository"
)

var validCandleResolutions = map[string]struct{}{
	"1m":  {},
	"5m":  {},
	"15m": {},
	"1h":  {},
	"4h":  {},
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
	GetMarketsOverview(ctx context.Context, resolution string, limit int) ([]*repository.MarketOverviewItem, error)
}

type marketService struct {
	marketRepo repository.MarketRepository
	candleRepo repository.CandleRepository
	rdb        *redis.Client
	logger     *zap.Logger
}

func NewMarketService(marketRepo repository.MarketRepository, candleRepo repository.CandleRepository, opts ...any) MarketService {
	svc := &marketService{
		marketRepo: marketRepo,
		candleRepo: candleRepo,
		logger:     zap.NewNop(),
	}
	for _, opt := range opts {
		switch v := opt.(type) {
		case *redis.Client:
			svc.rdb = v
		case *zap.Logger:
			if v != nil {
				svc.logger = v
			}
		}
	}
	return svc
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

	// Invariant 6: Publish updated 24h ticker to Redis ONLY after Postgres transaction succeeds.
	if processed && s.rdb != nil {
		s.publishTickerToRedis(ctx, marketID)
	}

	return processed, nil
}

type redisTickerPayload struct {
	MarketID              string `json:"marketId"`
	LastPrice             string `json:"lastPrice"`
	High24h               string `json:"high24h"`
	Low24h                string `json:"low24h"`
	Volume24h             string `json:"volume24h"`
	QuoteVolume24h        string `json:"quoteVolume24h"`
	PriceChange24hPercent string `json:"priceChange24hPercent"`
	Timestamp             int64  `json:"timestamp"` // Unix milliseconds
}

func (s *marketService) publishTickerToRedis(ctx context.Context, marketID string) {
	ticker, err := s.GetTicker(ctx, marketID)
	if err != nil {
		s.logger.Error("Failed to calculate 24h ticker for Redis publication",
			zap.String("market_id", marketID),
			zap.Error(err),
		)
		return
	}
	if ticker == nil {
		s.logger.Warn("Calculated 24h ticker is nil, skipping Redis publication",
			zap.String("market_id", marketID),
		)
		return
	}

	payload := redisTickerPayload{
		MarketID:              ticker.MarketID,
		LastPrice:             ticker.LastPrice.String(),
		High24h:               ticker.High24h.String(),
		Low24h:                ticker.Low24h.String(),
		Volume24h:             ticker.Volume24h.String(),
		QuoteVolume24h:        ticker.QuoteVolume24h.String(),
		PriceChange24hPercent: ticker.PriceChange24hPercent.String(),
		Timestamp:             time.Now().UnixMilli(),
	}

	data, err := json.Marshal(payload)
	if err != nil {
		s.logger.Error("Failed to marshal 24h ticker payload for Redis",
			zap.String("market_id", marketID),
			zap.Error(err),
		)
		return
	}

	// 60-second TTL prevents serving stale ticker if Market Service crashes (Invariant 7)
	redisCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	redisKey := "ticker:" + marketID
	if err := s.rdb.Set(redisCtx, redisKey, data, 60*time.Second).Err(); err != nil {
		s.logger.Error("Failed to publish ticker to Redis",
			zap.String("market_id", marketID),
			zap.String("redis_key", redisKey),
			zap.Error(err),
		)
	}
}

func (s *marketService) GetMarketsOverview(ctx context.Context, resolution string, limit int) ([]*repository.MarketOverviewItem, error) {
	resolution = strings.TrimSpace(resolution)
	if resolution == "" {
		resolution = "1h"
	}
	if _, ok := validCandleResolutions[resolution]; !ok {
		return nil, ErrInvalidResolution
	}
	if limit <= 0 {
		limit = 168
	} else if limit > 500 {
		limit = 500
	}

	return s.marketRepo.GetMarketsOverview(ctx, resolution, limit)
}


