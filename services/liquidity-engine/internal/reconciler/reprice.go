package reconciler

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/platform/refprice"
	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/pricing"
)

// currentReference retrieves the authoritative reference price, version, and freshness for a market.
// It prioritizes the live refprice Provider if wired, applying age-based transitions (FRESH → STALE → PAUSED)
// on provider failure based on the timestamp of the last successful reference.
func (r *Reconciler) currentReference(mc *config.MarketConfig) ReferenceSnapshot {
	marketID := mc.MarketID
	state, exists := r.refStates[marketID]

	pauseThreshold := 10 * time.Minute
	if r.cfg != nil && r.cfg.RefPrice.PauseThreshold > 0 {
		pauseThreshold = r.cfg.RefPrice.PauseThreshold
	}

	if r.refProvider != nil {
		if entry, ok := r.refProvider.Get(marketID); ok && !entry.Price.IsZero() && !entry.Price.IsNegative() {
			var freshness Freshness
			switch entry.State {
			case refprice.StateFresh:
				freshness = FreshnessFresh
			case refprice.StateStale:
				freshness = FreshnessStale
			case refprice.StatePaused:
				freshness = FreshnessPaused
			default:
				freshness = FreshnessStale // Fail-closed: unknown state must never default to fresh
			}
			if !exists {
				state = &RefState{
					LastRef:     entry.Price,
					LastVersion: entry.Version,
				}
				r.refStates[marketID] = state
			} else if state.LastRef.IsZero() {
				state.LastRef = entry.Price
				state.LastVersion = entry.Version
			}
			if !entry.FetchedAt.IsZero() {
				state.LastUpdated = entry.FetchedAt
			} else {
				state.LastUpdated = time.Now()
			}
			state.Freshness = freshness
			return ReferenceSnapshot{
				Price:     entry.Price,
				Version:   entry.Version,
				FetchedAt: state.LastUpdated,
				Freshness: freshness,
			}
		}

		// Provider is configured, but Get() returned false or empty entry (provider failure).
		// If we have a previously recorded successful reference, evaluate age against thresholds.
		// LE-1: Persist the derived STALE or PAUSED state into state.Freshness.
		if exists && !state.LastRef.IsZero() {
			age := time.Since(state.LastUpdated)
			freshness := FreshnessStale
			if age >= pauseThreshold {
				freshness = FreshnessPaused
			}
			state.Freshness = freshness
			return ReferenceSnapshot{
				Price:     state.LastRef,
				Version:   state.LastVersion,
				FetchedAt: state.LastUpdated,
				Freshness: freshness,
			}
		}

		// Provider configured, Get() failed, and no previous reference exists:
		// Fall back to seed price but FAIL CLOSED to STALE (never silently FRESH).
		ref := decimal.Zero
		if r.cfg != nil {
			ref = r.cfg.RefPrice.SeedForMarket(mc.MarketID)
		}
		if ref.IsZero() {
			ref = mc.ReferencePrice
		}
		if !exists {
			state = &RefState{}
			r.refStates[marketID] = state
			state.LastUpdated = time.Now()
		}
		state.LastRef = ref
		state.LastVersion = 0
		state.Freshness = FreshnessStale
		return ReferenceSnapshot{
			Price:     ref,
			Version:   0,
			FetchedAt: state.LastUpdated,
			Freshness: FreshnessStale,
		}
	}

	// No provider configured (e.g. unit tests without refProvider)
	if exists && !state.LastRef.IsZero() {
		return ReferenceSnapshot{
			Price:     state.LastRef,
			Version:   state.LastVersion,
			FetchedAt: state.LastUpdated,
			Freshness: state.Freshness,
		}
	}

	ref := decimal.Zero
	if r.cfg != nil {
		ref = r.cfg.RefPrice.SeedForMarket(mc.MarketID)
	}
	if ref.IsZero() {
		ref = mc.ReferencePrice
	}
	now := time.Now()
	if !exists {
		state = &RefState{
			LastRef:     ref,
			LastVersion: 0,
			LastUpdated: now,
			Freshness:   FreshnessFresh,
		}
		r.refStates[marketID] = state
	}
	return ReferenceSnapshot{
		Price:     ref,
		Version:   0,
		FetchedAt: state.LastUpdated,
		Freshness: FreshnessFresh,
	}
}

// isMarketPaused checks whether the market reference price is currently PAUSED.
// LE-7 / LE-8: Consumes authoritative currentReference snapshot semantics so all lifecycle
// paths share the identical freshness contract.
func (r *Reconciler) isMarketPaused(marketID string) bool {
	mc := &config.MarketConfig{MarketID: marketID}
	if r.cfg != nil {
		if m := r.cfg.ForMarket(marketID); m != nil {
			mc = m
		}
	}
	snap := r.currentReference(mc)
	return snap.Freshness == FreshnessPaused
}

// applyRefMovementReprice handles controlled rebase or selective repricing when reference price moves.
// For ActionSelectiveReprice: reprices outermost levels (HIGH first, then MID), LOW remains anchored.
// For ActionControlledRebase: reprices complete ladder (HIGH first, then MID, then LOW).
// Batches up to MaxBatch levels per cycle. Updates refState.RebaseActive until all eligible levels have converged.
func (r *Reconciler) applyRefMovementReprice(ctx context.Context, mc *config.MarketConfig, targetRef decimal.Decimal, targetVersion int64, action RepricingAction) int {
	maxBatch := mc.Repricing.MaxBatch
	if maxBatch <= 0 {
		maxBatch = 4
	}

	allOrders := r.tracker.All(mc.MarketID)
	var candidates []*order.LiveOrder
	for _, o := range allOrders {
		if o.Status == order.StatusResting && o.RefVersion < targetVersion {
			if action == ActionControlledRebase {
				if o.Zone == "HIGH" || o.Zone == "MID" || o.Zone == "LOW" {
					candidates = append(candidates, o)
				}
			} else { // ActionSelectiveReprice
				if o.Zone == "HIGH" || o.Zone == "MID" {
					candidates = append(candidates, o)
				}
			}
		}
	}

	// Reorder candidates: HIGH first, then MID, then LOW
	reordered := make([]*order.LiveOrder, 0, len(candidates))
	for _, o := range candidates {
		if o.Zone == "HIGH" {
			reordered = append(reordered, o)
		}
	}
	for _, o := range candidates {
		if o.Zone == "MID" {
			reordered = append(reordered, o)
		}
	}
	if action == ActionControlledRebase {
		for _, o := range candidates {
			if o.Zone == "LOW" {
				reordered = append(reordered, o)
			}
		}
	}

	// Build shared price reservation maps across the ENTIRE batch (one per side)
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

	count := 0
	for _, o := range reordered {
		if count >= maxBatch {
			break
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
		case "HIGH":
			band = zones.High
		case "MID":
			band = zones.Mid
		default:
			band = zones.Low
		}

		// Invariant: If resting order has zero remaining quantity, it is fully consumed — do not recreate
		if o.RemainingQty.IsZero() {
			r.logger.Info("reprice candidate has zero remaining quantity — cancelling without replacement",
				zap.String("level_id", o.LevelID))
			diffEntry := order.DiffEntry{
				Action:       order.DiffCancel,
				LevelID:      o.LevelID,
				ExistingOID:  o.OrderID,
				ExistingCOID: o.ClientOrderID,
			}
			_ = r.applyCancel(ctx, diffEntry, mc)
			count++
			continue
		}

		safeQty, ok := r.resolveReplacementQuantity(
			o.RemainingQty,
			o.RemainingQty,
			o.OriginalQty,
			mc.MinOrderSize,
			mc.LotSize,
			mc.MarketID,
			o.LevelID,
		)
		if !ok {
			r.logger.Info("reprice candidate remaining quantity zero/dust — cancelling without replacement",
				zap.String("level_id", o.LevelID),
				zap.String("remaining_qty", o.RemainingQty.String()))
			diffEntry := order.DiffEntry{
				Action:       order.DiffCancel,
				LevelID:      o.LevelID,
				ExistingOID:  o.OrderID,
				ExistingCOID: o.ClientOrderID,
			}
			if err := r.applyCancel(ctx, diffEntry, mc); err != nil {
				r.logger.Warn("failed to cancel zero/dust order during reprice",
					zap.String("level_id", o.LevelID), zap.Error(err))
				continue
			}
			count++
			continue
		}
		replacementQty := safeQty

		taken := takenBid
		if o.Side == "SELL" {
			taken = takenAsk
		}

		newPrice, err := pricing.GenerateLevelPrice(mc, targetRef, o.Side, band, taken, r.rng)
		if err != nil {
			r.metrics.IncRebaseGenerationFailure(mc.MarketID, o.LevelID)
			r.rebaseFailureCount[o.LevelID]++
			r.logger.Warn("failed to generate reprice level",
				zap.String("market_id", mc.MarketID),
				zap.String("level_id", o.LevelID),
				zap.String("side", o.Side),
				zap.String("zone", o.Zone),
				zap.String("target_ref", targetRef.String()),
				zap.Int64("target_version", targetVersion),
				zap.Int("failure_count", r.rebaseFailureCount[o.LevelID]),
				zap.Error(err))
			continue
		}
		delete(r.rebaseFailureCount, o.LevelID)

		// Reserve generated price in shared map immediately
		taken[newPrice.String()] = true

		diffEntry := order.DiffEntry{
			Action:       order.DiffCorrect,
			LevelID:      o.LevelID,
			ExistingOID:  o.OrderID,
			ExistingCOID: o.ClientOrderID,
		}

		if err := r.applyCancel(ctx, diffEntry, mc); err != nil {
			r.logger.Warn("failed to cancel order during reprice", zap.String("level_id", o.LevelID), zap.Error(err))
			continue
		}

		newLevel := pricing.PriceLevel{
			LevelID:    o.LevelID,
			MarketID:   mc.MarketID,
			Side:       o.Side,
			Price:      newPrice,
			Quantity:   replacementQty,
			Zone:       o.Zone,
			RefVersion: targetVersion,
		}
		r.tracker.QueueCorrection(o.LevelID, newLevel)
		r.metrics.IncReconcileCorrect(mc.MarketID)
		count++
	}

	// Update rebase state: check if all eligible levels have now been processed
	if state, ok := r.refStates[mc.MarketID]; ok {
		remaining := len(reordered) - count
		targetVer := state.RebaseTargetVer
		if targetVer == 0 {
			targetVer = targetVersion
		}

		// Invariant: Rebase is complete only when zero orders across RESTING, PENDING,
		// and OS_REGISTERED remain with RefVersion < targetVer.
		// If in-flight PENDING/OS_REGISTERED orders hold an older version, RebaseActive
		// remains true so subsequent cycles reprice them once confirmed RESTING.
		hasInFlightOlderVersions := false
		for _, o := range allOrders {
			if (o.Status == order.StatusPending || o.Status == order.StatusOSRegistered) && o.RefVersion < targetVer {
				hasInFlightOlderVersions = true
				break
			}
		}

		if remaining <= 0 && !hasInFlightOlderVersions {
			state.RebaseActive = false
			r.rebaseFailureCount = make(map[string]int)
			r.logger.Info("rebase completed — all eligible levels converged",
				zap.String("market_id", mc.MarketID),
				zap.Int64("target_version", targetVer))
		} else {
			state.RebaseActive = true
			r.logger.Info("rebase batch processed — more levels pending",
				zap.String("market_id", mc.MarketID),
				zap.Int("repriced_this_cycle", count),
				zap.Int("remaining_eligible_resting", remaining),
				zap.Bool("has_inflight_older_versions", hasInFlightOlderVersions))
		}
	}

	if count > 0 {
		r.logger.Info("initiated controlled repricing",
			zap.String("market_id", mc.MarketID),
			zap.String("action", action.String()),
			zap.Int("orders_repriced", count))
	}
	return count
}
