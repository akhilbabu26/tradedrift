// Package inventory skew computes how many bid/ask levels to activate based on effective inventory.
package inventory

import (
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/config"
)

// InventoryTier represents how much inventory the LE currently has.
type InventoryTier int

const (
	TierNormal   InventoryTier = iota // > MinBase / MinQuote
	TierLow                           // <= MinBase / MinQuote but > Critical
	TierCritical                      // <= CriticalBase / CriticalQuote
)

// Skew holds the computed bid/ask level counts for a market reconcile cycle.
type Skew struct {
	BidCount  int
	AskCount  int
	BaseTier  InventoryTier
	QuoteTier InventoryTier
}

// ComputeSkew computes the number of active bid/ask levels based on effective inventory.
func ComputeSkew(mc *config.MarketConfig, effectiveBase, effectiveQuote decimal.Decimal, logger *zap.Logger) Skew {
	maxLevels := mc.LevelCount

	// --- ASK side: driven by base asset inventory ---
	baseTier := TierNormal
	askCount := maxLevels
	switch {
	case effectiveBase.LessThanOrEqual(mc.CriticalBase):
		baseTier = TierCritical
		askCount = 0
	case effectiveBase.LessThanOrEqual(mc.MinBase):
		baseTier = TierLow
		askCount = maxLevels / 2
	}

	// --- BID side: driven by USDT (quote) inventory ---
	quoteTier := TierNormal
	bidCount := maxLevels
	switch {
	case effectiveQuote.LessThanOrEqual(mc.CriticalQuote):
		quoteTier = TierCritical
		bidCount = 0
	case effectiveQuote.LessThanOrEqual(mc.MinQuote):
		quoteTier = TierLow
		bidCount = maxLevels / 2
	}

	if baseTier != TierNormal || quoteTier != TierNormal {
		logger.Warn("inventory skew active",
			zap.String("market_id", mc.MarketID),
			zap.String("effective_base", effectiveBase.String()),
			zap.String("effective_quote", effectiveQuote.String()),
			zap.Int("bid_count", bidCount),
			zap.Int("ask_count", askCount),
			zap.Int("base_tier", int(baseTier)),
			zap.Int("quote_tier", int(quoteTier)))
	}

	return Skew{
		BidCount:  bidCount,
		AskCount:  askCount,
		BaseTier:  baseTier,
		QuoteTier: quoteTier,
	}
}
