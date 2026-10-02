package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
	ValidatePriceBand(ctx context.Context, marketID string, side repository.OrderSide, orderType repository.OrderType, price decimal.Decimal, userID string) error
}

type redisPriceFilter struct {
	redisClient    RedisGetter
	maxDeviation   decimal.Decimal
	mmUserID       string
	mmMaxDeviation decimal.Decimal
	maxAnchorAge   time.Duration
	logger         *zap.Logger
}

// NewPriceFilter creates a price filter with default MM configuration for backward compatibility.
func NewPriceFilter(client RedisGetter, maxDeviationStr string, logger *zap.Logger) PriceFilter {
	return NewDualPriceFilter(client, maxDeviationStr, "00000000-0000-0000-0000-000000000001", "0.10", 60*time.Second, logger)
}

// NewDualPriceFilter constructs a PriceFilter enforcing the Dual-Authority invariant:
// Retail orders validated against ME order book depth (depth:{marketID}).
// Market Maker orders validated against authoritative RefPrice anchor (refprice:anchor:{marketID}).
func NewDualPriceFilter(
	client RedisGetter,
	maxDeviationStr string,
	mmUserID string,
	mmMaxDeviationStr string,
	maxAnchorAge time.Duration,
	logger *zap.Logger,
) PriceFilter {
	if logger == nil {
		logger = zap.NewNop()
	}
	dev, err := decimal.NewFromString(maxDeviationStr)
	if err != nil || !dev.GreaterThan(decimal.Zero) {
		dev = decimal.NewFromFloat(0.05) // fallback default 5%
	}
	mmDev, err := decimal.NewFromString(mmMaxDeviationStr)
	if err != nil || !mmDev.GreaterThan(decimal.Zero) {
		mmDev = decimal.NewFromFloat(0.10) // fallback default 10%
	}
	if maxAnchorAge <= 0 {
		maxAnchorAge = 60 * time.Second
	}
	if mmUserID == "" {
		mmUserID = "00000000-0000-0000-0000-000000000001"
	}

	return &redisPriceFilter{
		redisClient:    client,
		maxDeviation:   dev,
		mmUserID:       mmUserID,
		mmMaxDeviation: mmDev,
		maxAnchorAge:   maxAnchorAge,
		logger:         logger,
	}
}

type refpriceAnchorDTO struct {
	MarketID  string `json:"market_id"`
	Price     string `json:"price"`
	Version   int64  `json:"version"`
	FetchedAt string `json:"fetched_at"`
	Source    string `json:"source"`
	State     string `json:"state"`
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

func (f *redisPriceFilter) ValidatePriceBand(ctx context.Context, marketID string, side repository.OrderSide, orderType repository.OrderType, price decimal.Decimal, userID string) error {
	if f.redisClient == nil {
		return nil // Redis not configured, bypass
	}

	// ── DUAL AUTHORITY: MM Order Validation vs Authoritative External RefPrice Anchor ──
	if userID != "" && userID == f.mmUserID {
		return f.validateMMOrder(ctx, marketID, side, orderType, price)
	}

	// ── DUAL AUTHORITY: Retail Order Validation vs Internal ME Order Book Depth ──
	return f.validateRetailOrder(ctx, marketID, side, orderType, price)
}

func (f *redisPriceFilter) validateMMOrder(ctx context.Context, marketID string, side repository.OrderSide, orderType repository.OrderType, price decimal.Decimal) error {
	anchorKey := "refprice:anchor:" + marketID
	val, err := f.redisClient.Get(ctx, anchorKey).Result()
	if err != nil {
		// INVARIANT (Mandatory Directive 1): MM orders must NEVER fall back to ME book when RefPrice anchor is unavailable!
		// Fail closed to prevent recreating the ME startup book deadlock.
		f.logger.Warn("MM order rejected — reference price anchor unavailable in Redis",
			zap.String("market", marketID),
			zap.Error(err))
		return &PriceFilterViolation{
			Code:            "FILTER_FAILURE_PERCENT_PRICE",
			MarketID:        marketID,
			Side:            string(side),
			OrderType:       string(orderType),
			SubmittedPrice:  price.String(),
			Reason:          "Market maker order rejected: reference price anchor unavailable in Redis (fail-closed)",
		}
	}

	var anchor refpriceAnchorDTO
	if err := json.Unmarshal([]byte(val), &anchor); err != nil {
		f.logger.Warn("MM order rejected — invalid anchor payload in Redis",
			zap.String("market", marketID),
			zap.Error(err))
		return &PriceFilterViolation{
			Code:            "FILTER_FAILURE_PERCENT_PRICE",
			MarketID:        marketID,
			Side:            string(side),
			OrderType:       string(orderType),
			SubmittedPrice:  price.String(),
			Reason:          "Market maker order rejected: invalid reference price anchor format in Redis",
		}
	}

	// OS-06 INVARIANT: Anchor market_id must match the requested market.
	// Prevents cross-market contamination where a stale Redis key at
	// refprice:anchor:SOL-USDT might still carry an ETH-USDT payload.
	if anchor.MarketID != "" && anchor.MarketID != marketID {
		f.logger.Warn("MM order rejected — anchor market_id mismatch (cross-market contamination guard)",
			zap.String("requested_market", marketID),
			zap.String("anchor_market", anchor.MarketID))
		return &PriceFilterViolation{
			Code:            "FILTER_FAILURE_PERCENT_PRICE",
			MarketID:        marketID,
			Side:            string(side),
			OrderType:       string(orderType),
			SubmittedPrice:  price.String(),
			Reason:          fmt.Sprintf("Market maker order rejected: anchor market_id (%s) does not match requested market (%s)", anchor.MarketID, marketID),
		}
	}

	anchorPrice, err := decimal.NewFromString(anchor.Price)
	if err != nil || !anchorPrice.GreaterThan(decimal.Zero) {
		return &PriceFilterViolation{
			Code:            "FILTER_FAILURE_PERCENT_PRICE",
			MarketID:        marketID,
			Side:            string(side),
			OrderType:       string(orderType),
			SubmittedPrice:  price.String(),
			Reason:          fmt.Sprintf("Market maker order rejected: reference anchor price (%s) is non-positive or invalid", anchor.Price),
		}
	}

	if anchor.Version <= 0 {
		return &PriceFilterViolation{
			Code:            "FILTER_FAILURE_PERCENT_PRICE",
			MarketID:        marketID,
			Side:            string(side),
			OrderType:       string(orderType),
			SubmittedPrice:  price.String(),
			Reason:          fmt.Sprintf("Market maker order rejected: reference anchor version (%d) must be positive", anchor.Version),
		}
	}

	if anchor.State != "FRESH" {
		return &PriceFilterViolation{
			Code:            "FILTER_FAILURE_PERCENT_PRICE",
			MarketID:        marketID,
			Side:            string(side),
			OrderType:       string(orderType),
			SubmittedPrice:  price.String(),
			Reason:          fmt.Sprintf("Market maker order rejected: reference anchor state (%s) is not FRESH", anchor.State),
		}
	}

	// Validate anchor age
	fetchedAt, err := time.Parse(time.RFC3339Nano, anchor.FetchedAt)
	if err != nil {
		fetchedAt, err = time.Parse(time.RFC3339, anchor.FetchedAt)
	}
	if err != nil {
		return &PriceFilterViolation{
			Code:            "FILTER_FAILURE_PERCENT_PRICE",
			MarketID:        marketID,
			Side:            string(side),
			OrderType:       string(orderType),
			SubmittedPrice:  price.String(),
			Reason:          "Market maker order rejected: invalid fetched_at timestamp in reference anchor",
		}
	}

	age := time.Since(fetchedAt)
	if age < 0 || age > f.maxAnchorAge {
		return &PriceFilterViolation{
			Code:            "FILTER_FAILURE_PERCENT_PRICE",
			MarketID:        marketID,
			Side:            string(side),
			OrderType:       string(orderType),
			SubmittedPrice:  price.String(),
			Reason:          fmt.Sprintf("Market maker order rejected: reference anchor age (%v) exceeds maximum allowed age (%v)", age.Round(time.Millisecond), f.maxAnchorAge),
		}
	}

	// Evaluate against MM reference deviation band
	minAllowed := anchorPrice.Mul(decimal.NewFromInt(1).Sub(f.mmMaxDeviation))
	maxAllowed := anchorPrice.Mul(decimal.NewFromInt(1).Add(f.mmMaxDeviation))

	if price.GreaterThan(maxAllowed) || price.LessThan(minAllowed) {
		return &PriceFilterViolation{
			Code:            "FILTER_FAILURE_PERCENT_PRICE",
			MarketID:        marketID,
			Side:            string(side),
			OrderType:       string(orderType),
			SubmittedPrice:  price.String(),
			MidPrice:        anchorPrice.String(),
			MinAllowedPrice: minAllowed.String(),
			MaxAllowedPrice: maxAllowed.String(),
			Reason:          fmt.Sprintf("MM limit price (%s) is outside allowed reference band (%s to %s) around reference anchor (%s)", price.String(), minAllowed.String(), maxAllowed.String(), anchorPrice.String()),
		}
	}

	return nil
}

func (f *redisPriceFilter) validateRetailOrder(ctx context.Context, marketID string, side repository.OrderSide, orderType repository.OrderType, price decimal.Decimal) error {
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
		switch side {
		case repository.SideBuy:
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
		case repository.SideSell:
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
