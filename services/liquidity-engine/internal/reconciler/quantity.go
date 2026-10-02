package reconciler

import (
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// resolveReplacementQuantity determines the safe replacement quantity for an MM order level.
//
// Invariants enforced:
//  1. remainingQty <= 0 -> false (order fully filled or consumed; never resurrect)
//  2. remainingQty > originalQty (if originalQty > 0) -> invariant violation logged, metric incremented, capped to originalQty
//  3. remainingQty is the hard upper bound: replacement is min(desiredQty, remainingQty)
//  4. lotSize normalization: if lotSize > 0, floor to multiple of lotSize
//  5. minOrderSize validation: if replacement < minOrderSize, return false (dust)
//  6. lotSize validation: if replacement < lotSize, return false (dust)
func (r *Reconciler) resolveReplacementQuantity(
	desiredQty, remainingQty, originalQty, minOrderSize, lotSize decimal.Decimal,
	marketID, levelID string,
) (decimal.Decimal, bool) {
	// 1. Authoritative remaining quantity must be strictly positive
	if remainingQty.LessThanOrEqual(decimal.Zero) {
		r.logger.Info("order has zero or negative remaining quantity — skipping replacement",
			zap.String("market_id", marketID),
			zap.String("level_id", levelID),
			zap.String("remaining_qty", remainingQty.String()))
		return decimal.Zero, false
	}

	// 2. Invariant: RemainingQty cannot exceed OriginalQty
	if originalQty.GreaterThan(decimal.Zero) && remainingQty.GreaterThan(originalQty) {
		r.logger.Error("invariant violation: order RemainingQty exceeds OriginalQty — capping to OriginalQty",
			zap.String("market_id", marketID),
			zap.String("level_id", levelID),
			zap.String("remaining_qty", remainingQty.String()),
			zap.String("original_qty", originalQty.String()))
		r.metrics.IncQuantityInvariantViolation(marketID)
		remainingQty = originalQty
	}

	// 3. Invariant: remaining quantity is the hard upper bound; desired ladder quantity specifies intended size if smaller
	qty := desiredQty
	if qty.IsZero() || remainingQty.LessThan(qty) {
		qty = remainingQty
	}

	// 4. Lot-size normalization: round down to nearest lotSize multiple if lotSize > 0
	if lotSize.GreaterThan(decimal.Zero) {
		remainder := qty.Mod(lotSize)
		if !remainder.IsZero() {
			qty = qty.Sub(remainder)
		}
	}

	// 5. Check against MinOrderSize and LotSize to prevent dust orders
	if (!minOrderSize.IsZero() && qty.LessThan(minOrderSize)) ||
		(!lotSize.IsZero() && qty.LessThan(lotSize)) {
		r.logger.Info("skipping replacement order — quantity below minimum order size / lot size (dust)",
			zap.String("market_id", marketID),
			zap.String("level_id", levelID),
			zap.String("quantity", qty.String()),
			zap.String("min_order_size", minOrderSize.String()),
			zap.String("lot_size", lotSize.String()))
		return decimal.Zero, false
	}

	return qty, true
}

// ResolveReplacementQuantity exports quantity resolution for unit testing.
func (r *Reconciler) ResolveReplacementQuantity(
	desiredQty, remainingQty, originalQty, minOrderSize, lotSize decimal.Decimal,
	marketID, levelID string,
) (decimal.Decimal, bool) {
	return r.resolveReplacementQuantity(desiredQty, remainingQty, originalQty, minOrderSize, lotSize, marketID, levelID)
}
