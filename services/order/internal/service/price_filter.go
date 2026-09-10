package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/order/internal/repository"
)

// PriceFilterViolation is returned when an order violates the pre-trade price band or slippage limit.
type PriceFilterViolation struct {
	Code            string `json:"code"`
	MarketID        string `json:"market_id"`
	Side            string `json:"side"`
	OrderType       string `json:"order_type"`
	SubmittedPrice  string `json:"submitted_price"`
	MidPrice        string `json:"mid_price"`
	BestBid         string `json:"best_bid"`
	BestAsk         string `json:"best_ask"`
	MinAllowedPrice string `json:"min_allowed_price"`
	MaxAllowedPrice string `json:"max_allowed_price"`
	Reason          string `json:"reason"`
}

func (v *PriceFilterViolation) Error() string {
	b, _ := json.Marshal(v)
	return string(b)
}

// RedisGetter abstracts Redis Get for unit testing.
type RedisGetter interface {
	Get(ctx context.Context, key string) *redis.StringCmd
}

type PriceFilter interface {
	ValidatePriceBand(ctx context.Context, marketID string, side repository.OrderSide, orderType repository.OrderType, price decimal.Decimal) error
}

type redisPriceFilter struct {
	redisClient  RedisGetter
	maxDeviation decimal.Decimal
	logger       *zap.Logger
}

func NewPriceFilter(client RedisGetter, maxDeviationStr string, logger *zap.Logger) PriceFilter {
	dev, err := decimal.NewFromString(maxDeviationStr)
	if err != nil || !dev.GreaterThan(decimal.Zero) {
		dev = decimal.NewFromFloat(0.05) // fallback default 5%
	}
	return &redisPriceFilter{
		redisClient:  client,
		maxDeviation: dev,
		logger:       logger,
	}
}

type depthLevelDTO struct {
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
}

type depthSnapshotDTO struct {
	MarketID string          `json:"market_id"`
	Bids     []depthLevelDTO `json:"bids"`
	Asks     []depthLevelDTO `json:"asks"`
}

func (f *redisPriceFilter) ValidatePriceBand(ctx context.Context, marketID string, side repository.OrderSide, orderType repository.OrderType, price decimal.Decimal) error {
	if f.redisClient == nil {
		return nil // Redis not configured, bypass
	}

	key := "depth:" + marketID
	val, err := f.redisClient.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			// No depth yet in book (market cold start) — allow order to seed the book
			return nil
		}
		f.logger.Warn("Failed to read depth projection from Redis, bypassing price filter", zap.String("market", marketID), zap.Error(err))
		return nil
	}

	var depth depthSnapshotDTO
	if err := json.Unmarshal([]byte(val), &depth); err != nil {
		f.logger.Warn("Failed to unmarshal depth projection, bypassing price filter", zap.String("market", marketID), zap.Error(err))
		return nil
	}

	var hasBid, hasAsk bool
	var bestBid, bestAsk decimal.Decimal

	if len(depth.Bids) > 0 {
		if p, err := decimal.NewFromString(depth.Bids[0].Price); err == nil && p.GreaterThan(decimal.Zero) {
			bestBid = p
			hasBid = true
		}
	}
	if len(depth.Asks) > 0 {
		if p, err := decimal.NewFromString(depth.Asks[0].Price); err == nil && p.GreaterThan(decimal.Zero) {
			bestAsk = p
			hasAsk = true
		}
	}

	if !hasBid && !hasAsk {
		// Empty book — allow order to seed book
		return nil
	}

	// Calculate Mid/Reference Price
	var midPrice decimal.Decimal
	if hasBid && hasAsk {
		midPrice = bestBid.Add(bestAsk).Div(decimal.NewFromInt(2))
	} else if hasAsk {
		midPrice = bestAsk
	} else {
		midPrice = bestBid
	}

	minAllowed := midPrice.Mul(decimal.NewFromInt(1).Sub(f.maxDeviation))
	maxAllowed := midPrice.Mul(decimal.NewFromInt(1).Add(f.maxDeviation))

	bidStr := "N/A"
	if hasBid {
		bidStr = bestBid.String()
	}
	askStr := "N/A"
	if hasAsk {
		askStr = bestAsk.String()
	}

	// 1. MARKET Orders
	if orderType == repository.TypeMarket {
		if side == repository.SideBuy {
			// Cannot Market BUY below the best available ask
			if hasAsk && price.LessThan(bestAsk) {
				return &PriceFilterViolation{
					Code:            "FILTER_FAILURE_PERCENT_PRICE",
					MarketID:        marketID,
					Side:            string(side),
					OrderType:       string(orderType),
					SubmittedPrice:  price.String(),
					MidPrice:        midPrice.String(),
					BestBid:         bidStr,
					BestAsk:         askStr,
					MinAllowedPrice: bestAsk.String(),
					MaxAllowedPrice: maxAllowed.String(),
					Reason:          fmt.Sprintf("Market BUY price (%s) is below current market best ask (%s). Use a price between %s and %s, or place a LIMIT order.", price.String(), bestAsk.String(), bestAsk.String(), maxAllowed.String()),
				}
			}
			// Slippage guard: Cannot Market BUY higher than maxAllowed
			if price.GreaterThan(maxAllowed) {
				return &PriceFilterViolation{
					Code:            "FILTER_FAILURE_PERCENT_PRICE",
					MarketID:        marketID,
					Side:            string(side),
					OrderType:       string(orderType),
					SubmittedPrice:  price.String(),
					MidPrice:        midPrice.String(),
					BestBid:         bidStr,
					BestAsk:         askStr,
					MinAllowedPrice: bestAsk.String(),
					MaxAllowedPrice: maxAllowed.String(),
					Reason:          fmt.Sprintf("Market BUY price (%s) exceeds maximum allowed price (%s) based on slippage guard.", price.String(), maxAllowed.String()),
				}
			}
		} else if side == repository.SideSell {
			// Cannot Market SELL above best available bid
			if hasBid && price.GreaterThan(bestBid) {
				return &PriceFilterViolation{
					Code:            "FILTER_FAILURE_PERCENT_PRICE",
					MarketID:        marketID,
					Side:            string(side),
					OrderType:       string(orderType),
					SubmittedPrice:  price.String(),
					MidPrice:        midPrice.String(),
					BestBid:         bidStr,
					BestAsk:         askStr,
					MinAllowedPrice: minAllowed.String(),
					MaxAllowedPrice: bestBid.String(),
					Reason:          fmt.Sprintf("Market SELL price (%s) is above current market best bid (%s). Use a price between %s and %s, or place a LIMIT order.", price.String(), bestBid.String(), minAllowed.String(), bestBid.String()),
				}
			}
			// Slippage guard: Cannot Market SELL lower than minAllowed
			if price.LessThan(minAllowed) {
				return &PriceFilterViolation{
					Code:            "FILTER_FAILURE_PERCENT_PRICE",
					MarketID:        marketID,
					Side:            string(side),
					OrderType:       string(orderType),
					SubmittedPrice:  price.String(),
					MidPrice:        midPrice.String(),
					BestBid:         bidStr,
					BestAsk:         askStr,
					MinAllowedPrice: minAllowed.String(),
					MaxAllowedPrice: bestBid.String(),
					Reason:          fmt.Sprintf("Market SELL price (%s) is below minimum allowed price (%s) based on slippage guard.", price.String(), minAllowed.String()),
				}
			}
		}
	}

	// 2. LIMIT Orders (Fat-finger protection)
	if orderType == repository.TypeLimit {
		if price.GreaterThan(maxAllowed) || price.LessThan(minAllowed) {
			return &PriceFilterViolation{
				Code:            "FILTER_FAILURE_PERCENT_PRICE",
				MarketID:        marketID,
				Side:            string(side),
				OrderType:       string(orderType),
				SubmittedPrice:  price.String(),
				MidPrice:        midPrice.String(),
				BestBid:         bidStr,
				BestAsk:         askStr,
				MinAllowedPrice: minAllowed.String(),
				MaxAllowedPrice: maxAllowed.String(),
				Reason:          fmt.Sprintf("Limit price (%s) is outside the allowed price band (%s to %s) around market price (%s).", price.String(), minAllowed.String(), maxAllowed.String(), midPrice.String()),
			}
		}
	}

	return nil
}
