package reconciler

import (
	"context"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/pricing"
)

// CheckExpiredOrders examines RESTING orders and expires those older than OrderLifetime.
// Expired orders are cancelled, and a replacement price is generated for the specific slot's zone
// and queued for correction (slot-level expiry, without regenerating the full ladder).
// Processes at most mc.Repricing.MaxBatch orders per call.
func (r *Reconciler) CheckExpiredOrders(ctx context.Context, marketID string) (expiredCount int) {
	mc := r.cfg.ForMarket(marketID)
	if mc == nil {
		return 0
	}

	// LE-7: Always consume the authoritative reference snapshot directly from currentReference.
	snap := r.currentReference(mc)
	ref := snap.Price
	refVersion := snap.Version
	freshness := snap.Freshness

	if freshness != FreshnessFresh {
		r.logger.Debug("skipping order expiry check — reference not FRESH",
			zap.String("market_id", marketID),
			zap.String("freshness", freshness.String()))
		return 0
	}

	orderLifetime := mc.Repricing.OrderLifetime
	if orderLifetime <= 0 {
		orderLifetime = 30 * time.Minute
	}
	maxBatch := mc.Repricing.MaxBatch
	if maxBatch <= 0 {
		maxBatch = 4
	}

	allOrders := r.tracker.All(marketID)
	var expired []*order.LiveOrder
	now := time.Now()

	for _, o := range allOrders {
		if o.Status != order.StatusResting {
			continue
		}
		createdAt := o.CreatedAt
		if createdAt.IsZero() {
			createdAt = o.PendingSince
		}
		if !createdAt.IsZero() && now.Sub(createdAt) >= orderLifetime {
			expired = append(expired, o)
			if len(expired) >= maxBatch {
				break
			}
		}
	}

	if len(expired) == 0 {
		return 0
	}

	// Shared price reservation maps across the expiry batch (one per side)
	takenBid := make(map[string]bool)
	takenAsk := make(map[string]bool)
	for _, other := range allOrders {
		if other.Status == order.StatusResting || other.Status == order.StatusPending || other.Status == order.StatusOSRegistered {
			if other.Side == "BUY" {
				takenBid[other.Price.String()] = true
			} else {
				takenAsk[other.Price.String()] = true
			}
		}
		if other.QueuedCorrection != nil && !other.QueuedCorrection.Price.IsZero() {
			if other.Side == "BUY" {
				takenBid[other.QueuedCorrection.Price.String()] = true
			} else {
				takenAsk[other.QueuedCorrection.Price.String()] = true
			}
		}
	}

	for _, o := range expired {
		taken := takenBid
		if o.Side == "SELL" {
			taken = takenAsk
		}

		zones := mc.BidZones
		if o.Side == "SELL" {
			zones = mc.AskZones
		}
		if zones.TotalLevels() == 0 {
			zones = config.DefaultZoneConfig()
		}

		var band config.ZoneBand
		switch o.Zone {
		case "LOW":
			band = zones.Low
		case "MID":
			band = zones.Mid
		case "HIGH":
			band = zones.High
		default:
			band = zones.Low
		}

		// Invariant: If resting order has zero remaining quantity, it is fully consumed — do not recreate
		if o.RemainingQty.IsZero() {
			r.logger.Info("expired order has zero remaining quantity — cancelling without replacement",
				zap.String("level_id", o.LevelID))
			diffEntry := order.DiffEntry{
				Action:       order.DiffCancel,
				LevelID:      o.LevelID,
				ExistingOID:  o.OrderID,
				ExistingCOID: o.ClientOrderID,
			}
			_ = r.applyCancel(ctx, diffEntry, mc)
			continue
		}

		// Use centralized resolveReplacementQuantity helper for authoritative capping & dust checks
		replacementQty, ok := r.resolveReplacementQuantity(
			o.RemainingQty,
			o.RemainingQty,
			o.OriginalQty,
			mc.MinOrderSize,
			mc.LotSize,
			mc.MarketID,
			o.LevelID,
		)
		if !ok {
			r.logger.Info("expired order remaining quantity below min size / lot size — cancelling without replacement",
				zap.String("level_id", o.LevelID),
				zap.String("remaining_qty", o.RemainingQty.String()))
			diffEntry := order.DiffEntry{
				Action:       order.DiffCancel,
				LevelID:      o.LevelID,
				ExistingOID:  o.OrderID,
				ExistingCOID: o.ClientOrderID,
			}
			_ = r.applyCancel(ctx, diffEntry, mc)
			continue
		}

		newPrice, err := pricing.GenerateLevelPrice(mc, ref, o.Side, band, taken, r.rng)
		if err != nil {
			r.logger.Warn("failed to generate replacement price for expired order",
				zap.String("market_id", mc.MarketID),
				zap.String("level_id", o.LevelID),
				zap.String("side", o.Side),
				zap.String("zone", o.Zone),
				zap.String("ref", ref.String()),
				zap.Int64("version", refVersion),
				zap.Error(err))
			r.metrics.IncExpiryGenerationFailure(mc.MarketID)
			continue
		}

		// Reserve generated price in shared map immediately
		taken[newPrice.String()] = true

		diffEntry := order.DiffEntry{
			Action:       order.DiffCorrect,
			LevelID:      o.LevelID,
			ExistingOID:  o.OrderID,
			ExistingCOID: o.ClientOrderID,
		}

		if err := r.applyCancel(ctx, diffEntry, mc); err != nil {
			r.logger.Warn("failed to apply cancel for expired order",
				zap.String("level_id", o.LevelID),
				zap.Error(err))
			continue
		}

		newLevel := pricing.PriceLevel{
			LevelID:    o.LevelID,
			MarketID:   mc.MarketID,
			Side:       o.Side,
			Price:      newPrice,
			Quantity:   replacementQty,
			Zone:       o.Zone,
			RefVersion: refVersion, // Current live RefVersion, NOT stale o.RefVersion
		}
		r.tracker.QueueCorrection(o.LevelID, newLevel)
		r.metrics.IncReconcileCorrect(mc.MarketID)
		expiredCount++
	}

	return expiredCount
}
