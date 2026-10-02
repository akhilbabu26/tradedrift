// Package pricing — zoned_ladder.go
//
// GenerateZonedDesired is the Phase 4 replacement for GenerateLadder at the
// reconciler call site. It produces the desired MM state using dynamic, zone-based
// pricing with price locking.
//
// KEY INVARIANTS
//
//  1. Prices are generated ONCE per generation, then LOCKED.
//     For any PENDING/OS_REGISTERED/RESTING level, the tracker's existing price
//     is returned unchanged. Diff() sees an exact match and produces KEEP — not
//     CORRECT — so no cancel/recreate storm occurs.
//
//  2. A replacement gets a NEW generation. Only nil/missing tracker slots receive
//     a freshly randomised price from GenerateLevelPrice.
//
//  3. Every generated price is unique within its side after tick normalisation.
//     GenerateLevelPrice retries up to maxPriceRetries times on collision.
//
//  4. BID prices are always strictly below ref. ASK prices are always above ref.
//     Crossed liquidity is impossible by construction.
//
//  5. RefVersion is stamped on each new level — ties the generated price to a
//     specific external reference snapshot for post-hoc debugging.
//
// IMPORT CYCLE NOTE:
// The order package imports pricing (for pricing.PriceLevel).
// To avoid a cycle, this file defines TrackerReader and ActiveLevel locally
// instead of importing order. The reconciler provides an adapter that satisfies
// TrackerReader using order.Tracker.Get().
package pricing

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/shopspring/decimal"

	"tradedrift/services/liquidity-engine/internal/config"
)

const maxPriceRetries = 20

// zoneName labels for the three zone bands.
var zoneNames = []string{"LOW", "MID", "HIGH"}

// TrackerReader is the minimal read-only interface GenerateZonedDesired requires.
// Defined here (in pricing) to avoid the order → pricing → order import cycle.
//
// order.Tracker satisfies this interface via the TrackerAdapter in the reconciler.
type TrackerReader interface {
	// GetActive returns the active price snapshot for a given levelID, or nil if absent.
	GetActive(levelID string) *ActiveLevel
}

// ActiveLevel is a read-only snapshot of the order state that GenerateZonedDesired needs.
// It contains only what is required for price locking decisions.
type ActiveLevel struct {
	// Status as a string ("PENDING", "OS_REGISTERED", "RESTING", "CANCELLING", "STALE").
	Status string
	// Price is the locked price for this generation. Returned unchanged for active levels.
	Price decimal.Decimal
	// RefVersion is the refprice.Entry.Version that was active when this level was created.
	RefVersion int64
}

// AllocateZones distributes targetCount levels across zones using LOW-first policy.
// When inventory is stressed, concentration near reference is preferred.
func AllocateZones(targetCount int, zones config.ZoneConfig) config.ZoneConfig {
	if zones.TotalLevels() == 0 {
		zones = config.DefaultZoneConfig()
	}
	if targetCount <= 0 {
		zones.Low.Count = 0
		zones.Mid.Count = 0
		zones.High.Count = 0
		return zones
	}

	remaining := targetCount

	lowCount := remaining
	if lowCount > zones.Low.Count {
		lowCount = zones.Low.Count
	}
	remaining -= lowCount

	midCount := remaining
	if midCount > zones.Mid.Count {
		midCount = zones.Mid.Count
	}
	remaining -= midCount

	highCount := remaining
	if highCount > zones.High.Count {
		highCount = zones.High.Count
	}

	zones.Low.Count = lowCount
	zones.Mid.Count = midCount
	zones.High.Count = highCount
	return zones
}

// GenerateZonedDesired produces the desired MM ladder using dynamic zone-based pricing.
//
// For each of the configured BID and ASK slots (controlled by bidCount and askCount):
//   - PENDING / OS_REGISTERED / RESTING → return tracker price UNCHANGED (LOCK).
//   - Absent / CANCELLING / STALE       → generate a new random price in the zone.
//
// refVersion is the Version field from platform/refprice.Entry for this market.
func GenerateZonedDesired(
	mc *config.MarketConfig,
	ref decimal.Decimal,
	tracker TrackerReader,
	bidZones config.ZoneConfig,
	askZones config.ZoneConfig,
	refVersion int64,
	bidCount int,
	askCount int,
	rng *rand.Rand,
) ([]PriceLevel, error) {
	if ref.IsZero() || ref.IsNegative() {
		return nil, fmt.Errorf("invalid reference price for %s: %s", mc.MarketID, ref)
	}

	// Resolve defaults if caller passes zero-value ZoneConfig.
	if bidZones.TotalLevels() == 0 {
		bidZones = config.DefaultZoneConfig()
	}
	if askZones.TotalLevels() == 0 {
		askZones = config.DefaultZoneConfig()
	}

	effBidZones := AllocateZones(bidCount, bidZones)
	effAskZones := AllocateZones(askCount, askZones)

	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	qty := resolveQtyPerLevel(mc)

	levels := make([]PriceLevel, 0, effBidZones.TotalLevels()+effAskZones.TotalLevels())

	takenBid := make(map[string]bool, effBidZones.TotalLevels())
	bidLevels, err := generateSide(mc, ref, "BUY", effBidZones, qty, tracker, takenBid, refVersion, rng)
	if err != nil {
		return nil, fmt.Errorf("BID side for %s: %w", mc.MarketID, err)
	}
	levels = append(levels, bidLevels...)

	takenAsk := make(map[string]bool, effAskZones.TotalLevels())
	askLevels, err := generateSide(mc, ref, "SELL", effAskZones, qty, tracker, takenAsk, refVersion, rng)
	if err != nil {
		return nil, fmt.Errorf("ASK side for %s: %w", mc.MarketID, err)
	}
	levels = append(levels, askLevels...)

	return levels, nil
}

// generateSide produces all levels for one side (BUY or SELL).
func generateSide(
	mc *config.MarketConfig,
	ref decimal.Decimal,
	side string,
	zones config.ZoneConfig,
	qty decimal.Decimal,
	tracker TrackerReader,
	taken map[string]bool,
	refVersion int64,
	rng *rand.Rand,
) ([]PriceLevel, error) {
	sideLabel := "BID"
	if side == "SELL" {
		sideLabel = "ASK"
	}

	bands := []config.ZoneBand{zones.Low, zones.Mid, zones.High}
	levels := make([]PriceLevel, 0, zones.TotalLevels())
	levelIndex := 1

	for zoneIdx, band := range bands {
		zoneName := zoneNames[zoneIdx]

		for slot := 0; slot < band.Count; slot++ {
			levelID := fmt.Sprintf("MM-%s-%s-%02d", mc.MarketID, sideLabel, levelIndex)

			existing := tracker.GetActive(levelID)
			if existing != nil && isActivePricingStatus(existing.Status) {
				// LOCK: return the tracker's current generation price unchanged.
				// Diff() compares this exact price and produces KEEP.
				levels = append(levels, PriceLevel{
					LevelID:    levelID,
					MarketID:   mc.MarketID,
					Side:       side,
					Price:      existing.Price,
					Quantity:   qty,
					Zone:       zoneName,
					RefVersion: existing.RefVersion,
				})
				// Register the locked price as taken so that any subsequent GENERATE
				// slot in this call cannot draw the same tick-rounded price.
				taken[existing.Price.String()] = true
			} else {
				// GENERATE: pick a new unique random price in this zone band.
				price, err := GenerateLevelPrice(mc, ref, side, band, taken, rng)
				if err != nil {
					return nil, fmt.Errorf("level %s (zone=%s): %w", levelID, zoneName, err)
				}
				taken[price.String()] = true

				levels = append(levels, PriceLevel{
					LevelID:    levelID,
					MarketID:   mc.MarketID,
					Side:       side,
					Price:      price,
					Quantity:   qty,
					Zone:       zoneName,
					RefVersion: refVersion,
				})
			}

			levelIndex++
		}
	}

	return levels, nil
}

// isActivePricingStatus returns true for statuses where the price must not be re-randomised.
// CANCELLING and STALE are treated as absent — a new price is generated for those slots.
func isActivePricingStatus(status string) bool {
	switch status {
	case "PENDING", "OS_REGISTERED", "RESTING":
		return true
	default:
		return false
	}
}

// GenerateLevelPrice generates one tick-normalised, unique, random price
// for a new level within the given zone band.
//
// BID:  price range is (ref / (1+maxBps/10000)) to (ref / (1+minBps/10000))
// ASK:  price range is (ref * (1+minBps/10000)) to (ref * (1+maxBps/10000))
//
// The returned price is always strictly on the correct side of ref.
func GenerateLevelPrice(
	mc *config.MarketConfig,
	ref decimal.Decimal,
	side string,
	band config.ZoneBand,
	taken map[string]bool,
	rng *rand.Rand,
) (decimal.Decimal, error) {
	ten4 := decimal.NewFromInt(10000)
	minMult := decimal.NewFromInt(int64(band.MinBps)).Div(ten4)
	maxMult := decimal.NewFromInt(int64(band.MaxBps)).Div(ten4)
	one := decimal.NewFromInt(1)

	var minPrice, maxPrice decimal.Decimal
	if side == "BUY" {
		// BID: higher bps → further below ref → lower price
		maxPrice = ref.Div(one.Add(minMult)) // closest to ref (minBps)
		minPrice = ref.Div(one.Add(maxMult)) // furthest from ref (maxBps)
	} else {
		// ASK: higher bps → further above ref → higher price
		minPrice = ref.Mul(one.Add(minMult)) // closest to ref
		maxPrice = ref.Mul(one.Add(maxMult)) // furthest from ref
	}

	tickSize := mc.TickSize
	if tickSize.IsZero() {
		switch mc.MarketID {
		case "BTC-USDT":
			tickSize = decimal.RequireFromString("0.01")
		case "ETH-USDT":
			tickSize = decimal.RequireFromString("0.01")
		case "SOL-USDT":
			tickSize = decimal.RequireFromString("0.001")
		default:
			tickSize = decimal.RequireFromString("0.01")
		}
	}

	rangeTicks := maxPrice.Sub(minPrice).Div(tickSize).Floor().IntPart()
	if rangeTicks <= 0 {
		// Zone narrower than one tick (guards against misconfigured bps values).
		mid := minPrice.Add(maxPrice).Div(decimal.NewFromInt(2))
		normalised := roundToTick(mid, tickSize)
		// LE-10: Guard zone boundary after tick rounding
		if normalised.LessThan(minPrice) || normalised.GreaterThan(maxPrice) {
			return decimal.Zero, fmt.Errorf("narrow zone price %s out of bounds [%s, %s]", normalised, minPrice, maxPrice)
		}
		if side == "BUY" && !normalised.LessThan(ref) {
			return decimal.Zero, fmt.Errorf("narrow zone price %s violates BUY < ref %s", normalised, ref)
		}
		if side == "SELL" && !normalised.GreaterThan(ref) {
			return decimal.Zero, fmt.Errorf("narrow zone price %s violates SELL > ref %s", normalised, ref)
		}
		if taken[normalised.String()] {
			return decimal.Zero, fmt.Errorf("narrow zone price %s already taken", normalised)
		}
		return normalised, nil
	}

	for attempt := 0; attempt < maxPriceRetries; attempt++ {
		offset := rng.Int63n(rangeTicks + 1)
		candidate := minPrice.Add(tickSize.Mul(decimal.NewFromInt(offset)))
		normalised := roundToTick(candidate, tickSize)

		// Guard zone boundary after tick rounding (prevent stepping outside the zone band).
		if normalised.LessThan(minPrice) || normalised.GreaterThan(maxPrice) {
			continue
		}

		// Enforce strict side invariant.
		if side == "BUY" && !normalised.LessThan(ref) {
			continue
		}
		if side == "SELL" && !normalised.GreaterThan(ref) {
			continue
		}

		key := normalised.String()
		if !taken[key] {
			return normalised, nil
		}
	}

	return decimal.Zero, fmt.Errorf(
		"could not generate unique price for %s %s zone after %d attempts (ref=%s min=%s max=%s)",
		mc.MarketID, side, maxPriceRetries, ref, minPrice, maxPrice,
	)
}

// resolveQtyPerLevel returns the lot-rounded quantity per level.
// Prefers mc.QtyPerLevel (Phase 3 config field); falls back to V1 hardcoded values.
func resolveQtyPerLevel(mc *config.MarketConfig) decimal.Decimal {
	if !mc.QtyPerLevel.IsZero() {
		return roundToLot(mc.QtyPerLevel, mc.LotSize)
	}
	switch mc.MarketID {
	case "BTC-USDT":
		return roundToLot(decimal.RequireFromString("0.85"), mc.LotSize)
	case "ETH-USDT":
		return roundToLot(decimal.RequireFromString("1.5"), mc.LotSize)
	case "SOL-USDT":
		return roundToLot(decimal.RequireFromString("20.0"), mc.LotSize)
	default:
		return roundToLot(decimal.RequireFromString("1.0"), mc.LotSize)
	}
}
