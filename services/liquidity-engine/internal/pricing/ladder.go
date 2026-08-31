// Package pricing generates the desired MM ladder — the set of limit orders
// the Liquidity Engine wants to maintain in the order book at all times.
//
// The ladder is symmetric around a reference price:
//
//	BID-12 ... BID-01 | referencePrice | ASK-01 ... ASK-12
//
// Spread Formula:
//
//	Bid_i = ref / (1 + bps*i/10000)
//	Ask_i = ref * (1 + bps*i/10000)
//	Inner spread (Bid-01 to Ask-01) is ~2 * SpreadBps basis points around reference.
//
// Quantities:
//
//	V1 uses deterministic uniform quantities across all levels for each market.
//	Tapered, skewed, or randomized sizing across levels is deferred to V2.
//
// All prices are rounded to the market's tick size (ME rejects invalid values).
// All quantities are rounded to the market's lot size (ME rejects invalid values).
//
// Level IDs follow the pattern: MM-{MARKET}-{SIDE}-{NN}
//
//	MM-BTC-USDT-BID-01 = closest bid to reference price
//	MM-BTC-USDT-ASK-01 = closest ask to reference price
//	MM-BTC-USDT-ASK-12 = furthest ask
package pricing

import (
	"fmt"

	"github.com/shopspring/decimal"

	"tradedrift/services/liquidity-engine/internal/config"
)

// PriceLevel represents one desired MM order at a specific price level.
type PriceLevel struct {
	// LevelID is the stable logical identity of this slot.
	// Format: "MM-BTC-USDT-ASK-01" (never changes for the life of the market)
	LevelID  string
	MarketID string
	Side     string          // "BUY" | "SELL"
	Price    decimal.Decimal // tick-rounded
	Quantity decimal.Decimal // lot-rounded
}

// bpsMultiplier returns (1 + bps/10000) as a decimal, for price level calculations.
func bpsMultiplier(bps int) decimal.Decimal {
	tenThousand := decimal.NewFromInt(10000)
	return decimal.NewFromInt(1).Add(decimal.NewFromInt(int64(bps)).Div(tenThousand))
}

// roundToTick rounds price down to the nearest tick size increment.
// Uses truncation (floor) to avoid generating a price above the intended level.
func roundToTick(price, tickSize decimal.Decimal) decimal.Decimal {
	if tickSize.IsZero() {
		return price
	}
	steps := price.Div(tickSize).Floor()
	return steps.Mul(tickSize)
}

// roundToLot rounds quantity down to the nearest lot size increment.
// Uses truncation (floor) to avoid over-quoting inventory.
func roundToLot(qty, lotSize decimal.Decimal) decimal.Decimal {
	if lotSize.IsZero() {
		return qty
	}
	steps := qty.Div(lotSize).Floor()
	return steps.Mul(lotSize)
}

// levelQuantity returns the quantity for a given market and level index.
// V1: uniform quantity across all levels.
func levelQuantity(mc *config.MarketConfig, side string, levelIndex int) decimal.Decimal {
	_ = side       // reserved for V2 (skewed quantity per level)
	_ = levelIndex // reserved for V2 (tapering quantity at far levels)

	var rawQty decimal.Decimal
	switch mc.MarketID {
	case "BTC-USDT":
		rawQty = decimal.RequireFromString("0.85000")
	case "ETH-USDT":
		rawQty = decimal.RequireFromString("1.5000")
	case "SOL-USDT":
		rawQty = decimal.RequireFromString("20.00")
	default:
		rawQty = decimal.RequireFromString("1.0")
	}
	return roundToLot(rawQty, mc.LotSize)
}

// GenerateLadder generates the desired MM ladder for a given market.
// bidCount and askCount control how many levels to generate on each side.
//
// The returned levels are ordered: BID-01 (closest) ... BID-N (furthest),
// then ASK-01 (closest) ... ASK-N (furthest).
func GenerateLadder(mc *config.MarketConfig, bidCount, askCount int) []PriceLevel {
	ref := mc.ReferencePrice
	bps := mc.SpreadBps

	levels := make([]PriceLevel, 0, bidCount+askCount)

	// --- BID levels: below reference price ---
	for i := 1; i <= bidCount; i++ {
		mult := bpsMultiplier(bps * i)
		rawPrice := ref.Div(mult)
		price := roundToTick(rawPrice, mc.TickSize)

		levelID := fmt.Sprintf("MM-%s-BID-%02d", mc.MarketID, i)
		qty := levelQuantity(mc, "BUY", i)

		levels = append(levels, PriceLevel{
			LevelID:  levelID,
			MarketID: mc.MarketID,
			Side:     "BUY",
			Price:    price,
			Quantity: qty,
		})
	}

	// --- ASK levels: above reference price ---
	for i := 1; i <= askCount; i++ {
		mult := bpsMultiplier(bps * i)
		rawPrice := ref.Mul(mult)
		price := roundToTick(rawPrice, mc.TickSize)

		levelID := fmt.Sprintf("MM-%s-ASK-%02d", mc.MarketID, i)
		qty := levelQuantity(mc, "SELL", i)

		levels = append(levels, PriceLevel{
			LevelID:  levelID,
			MarketID: mc.MarketID,
			Side:     "SELL",
			Price:    price,
			Quantity: qty,
		})
	}

	return levels
}
