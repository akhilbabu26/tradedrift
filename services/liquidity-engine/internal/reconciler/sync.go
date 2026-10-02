package reconciler

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/meclient"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/orderservice"
)

// SyncFromOrderService calls ListMMOrders and updates the tracker.
// Missing orders are resolved based on their current status:
// - RESTING, OS_REGISTERED, STALE: removed
// - CANCELLING: removed, and any QueuedCorrection replacement is created
// - PENDING: retained for CheckPendingTimeouts
func (r *Reconciler) SyncFromOrderService(ctx context.Context, marketID string) error {
	mc := r.cfg.ForMarket(marketID)
	if mc == nil {
		return fmt.Errorf("unknown market %s", marketID)
	}

	// Recover maximum observed generation across all historical orders (including filled/cancelled)
	// to guarantee generation monotonicity across restarts (INV-MM-07).
	if recov, ok := r.orderSvc.(interface {
		RecoverHighestGenerations(ctx context.Context, marketID string) (map[string]int, error)
	}); ok {
		if maxGens, err := recov.RecoverHighestGenerations(ctx, marketID); err == nil {
			for lvl, gen := range maxGens {
				r.tracker.SetMaxGeneration(lvl, gen)
			}
		} else {
			r.logger.Warn("failed to recover highest generations from order history",
				zap.String("market_id", marketID),
				zap.Error(err))
		}
	}

	osOrders, err := r.orderSvc.ListMMOrders(ctx, marketID)
	if err != nil {
		return fmt.Errorf("ListMMOrders for %s: %w", marketID, err)
	}

	added, duplicates := r.tracker.SyncFromOrders(marketID, osOrders)
	if duplicates > 0 {
		r.logger.Info("overlapping MM order generations detected during OS sync — deduplicated by highest generation",
			zap.String("market_id", marketID),
			zap.Int("duplicate_count", duplicates))
		r.metrics.IncDuplicateMMLevel(marketID)
	}

	r.logger.Info("synced from Order Service",
		zap.String("market_id", marketID),
		zap.Int("orders_found", len(osOrders)),
		zap.Int("new_entries", added),
		zap.Int("duplicates", duplicates))

	osLevelIDs := make(map[string]bool, len(osOrders))
	for _, o := range osOrders {
		osLevelIDs[o.LevelID] = true
	}

	for _, tracked := range r.tracker.All(marketID) {
		if !osLevelIDs[tracked.LevelID] {
			switch tracked.Status {
			case order.StatusResting, order.StatusOSRegistered, order.StatusStale:
				r.logger.Info("inactive order absent from OS — removing from tracker",
					zap.String("level_id", tracked.LevelID),
					zap.String("status", string(tracked.Status)),
					zap.String("client_order_id", tracked.ClientOrderID))
				r.removeTrackedOrder(tracked.LevelID)

			case order.StatusCancelling:
				r.logger.Info("CANCELLING order confirmed absent from OS — removing and handling queued correction",
					zap.String("level_id", tracked.LevelID),
					zap.String("client_order_id", tracked.ClientOrderID))
				r.removeTrackedOrder(tracked.LevelID)

				if tracked.QueuedCorrection != nil {
					desired := *tracked.QueuedCorrection

					// Invariant: If order was fully filled (RemainingQty == 0), never recreate
					if tracked.RemainingQty.IsZero() {
						r.logger.Info("tracked order was fully filled — skipping queued replacement during resync",
							zap.String("level_id", tracked.LevelID))
						r.metrics.IncOrdersFilled(mc.MarketID, tracked.Side)
						continue
					}

					// Invariant: PAUSED reference prevents new replacement creations
					if r.isMarketPaused(mc.MarketID) {
						r.logger.Warn("reference price is PAUSED — dropping queued replacement during resync",
							zap.String("market_id", mc.MarketID),
							zap.String("level_id", tracked.LevelID))
						continue
					}

					safeQty, ok := r.resolveReplacementQuantity(
						desired.Quantity,
						tracked.RemainingQty,
						tracked.OriginalQty,
						mc.MinOrderSize,
						mc.LotSize,
						mc.MarketID,
						tracked.LevelID,
					)
					if !ok {
						continue
					}
					desired.Quantity = safeQty

					// Capital budget check: verify projected exposure stays within market limits
					if allowed, reason := r.canCreateLevel(mc, desired); !allowed {
						r.logger.Warn("skipping queued replacement during resync — "+reason,
							zap.String("market_id", mc.MarketID),
							zap.String("level_id", tracked.LevelID),
						)
						continue
					}

					gen := r.tracker.NextGeneration(tracked.LevelID)
					clientOrderID := order.ClientOrderID(tracked.LevelID, gen)

					// Register replacement order in Order Service (new generation, new COID, new OID)
					osOrder, err := r.orderSvc.CreateMMOrder(ctx, mc.MarketID, desired.Side,
						desired.Price.String(), desired.Quantity.String(), clientOrderID)
					if err != nil {
						r.logger.Error("failed to register queued replacement in Order Service during resync",
							zap.String("level_id", tracked.LevelID),
							zap.Error(err))
						continue
					}

					orderID := osOrder.OrderID
					r.tracker.SetPending(tracked.LevelID, orderID, clientOrderID, gen, desired)

					if err := r.producer.PublishCreate(ctx, mc.MarketID, mc.Partition,
						orderID, clientOrderID, desired.Side,
						desired.Price.String(), desired.Quantity.String()); err != nil {
						r.logger.Warn("failed to publish queued replacement to Kafka during resync — will retry in CheckPendingTimeouts",
							zap.String("level_id", tracked.LevelID),
							zap.String("order_id", orderID),
							zap.Error(err))
						continue
					}

					r.tracker.SetKafkaPublished(tracked.LevelID, true)
					r.logger.Info("queued correction replacement registered and published during resync",
						zap.String("level_id", tracked.LevelID),
						zap.String("new_client_order_id", clientOrderID),
						zap.String("order_id", orderID))
				}

			case order.StatusPending:
				// Retain PENDING in tracker so CheckPendingTimeouts handles Kafka publish retry or OS verification
				r.logger.Debug("retaining PENDING order during resync",
					zap.String("level_id", tracked.LevelID),
					zap.String("client_order_id", tracked.ClientOrderID))
			}
		}
	}

	return nil
}

// syncWithMESnapshot aligns the tracker with the authoritative ME snapshot.
func (r *Reconciler) syncWithMESnapshot(ctx context.Context, marketID string, snap *meclient.MarketSnapshot, mc *config.MarketConfig) {
	// Index ME orders by exact identity: OrderID and ClientOrderID
	snapByOrderID := make(map[string]meclient.MMOrderSummary, len(snap.Orders))
	snapByCOID := make(map[string]meclient.MMOrderSummary, len(snap.Orders))
	for _, o := range snap.Orders {
		if o.OrderID != "" {
			snapByOrderID[o.OrderID] = o
		}
		if o.ClientOrderID != "" {
			snapByCOID[o.ClientOrderID] = o
		}
	}

	// A. Promote confirmed orders from snapshot & reset missing cycles
	for _, meOrder := range snap.Orders {
		tracked := r.tracker.Get(meOrder.LevelID)

		sameOrderID := tracked != nil && tracked.OrderID != "" && meOrder.OrderID != "" && tracked.OrderID == meOrder.OrderID
		sameClientOrderID := tracked != nil && tracked.ClientOrderID != "" && meOrder.ClientOrderID != "" && tracked.ClientOrderID == meOrder.ClientOrderID
		matchesTracked := sameOrderID || sameClientOrderID

		if matchesTracked {
			if tracked.Status == order.StatusCancelling {
				// The cancel has not yet been reflected by ME.
				// Invariant: CANCELLING is a locked state. Presence in ME does not make it RESTING again.
				r.logger.Debug("cancelling order still present in ME — waiting for cancellation",
					zap.String("level_id", tracked.LevelID),
					zap.String("order_id", meOrder.OrderID),
					zap.String("client_order_id", meOrder.ClientOrderID))
				continue
			}
			if tracked.Status == order.StatusStale {
				// Invariant: STALE is locked until authoritative OS resync.
				continue
			}

			// A1. Confirmed Order: extract authoritative remaining quantity from ME (LE-01, LE-03)
			remQty := parseMERemainingQuantity(meOrder, tracked.RemainingQty, r.logger)

			if tracked.Status != order.StatusResting {
				r.logger.Info("order confirmed RESTING in Matching Engine from snapshot",
					zap.String("level_id", tracked.LevelID),
					zap.String("order_id", meOrder.OrderID),
					zap.String("client_order_id", meOrder.ClientOrderID),
					zap.String("prev_status", string(tracked.Status)))
			}
			// INV-MM-08: Always update RemainingQty to keep CommittedBase/Quote accurate
			r.tracker.SetResting(tracked.LevelID, meOrder.OrderID, tracked.OriginalQty, remQty)

			delete(r.missingCycles, tracked.LevelID)
			delete(r.notFoundCount, tracked.LevelID)
			delete(r.inFlightSince, tracked.LevelID)
		} else {
			// A2. INV-MM-05: Orphan order resting in ME (stale generation or untracked order)
			cooldown := r.cfg.OrphanCancelCooldown
			if cooldown <= 0 {
				cooldown = orphanCancelCooldown
			}

			r.orphanCancelMu.Lock()
			rec, inFlight := r.orphanCancelInFlight[meOrder.OrderID]
			if inFlight && time.Since(rec.lastCancel) < cooldown {
				r.orphanCancelMu.Unlock()
				r.logger.Debug("orphan cancel already in-flight within cooldown, skipping duplicate publish",
					zap.String("order_id", meOrder.OrderID),
					zap.Duration("elapsed", time.Since(rec.lastCancel)))
				continue
			}
			r.orphanCancelMu.Unlock()

			r.logger.Warn("orphan order resting in ME not matching tracked identity — cancelling",
				zap.String("market_id", marketID),
				zap.String("level_id", meOrder.LevelID),
				zap.String("order_id", meOrder.OrderID),
				zap.String("client_order_id", meOrder.ClientOrderID))
			if r.orderSvc != nil {
				if err := r.orderSvc.CancelMMOrder(ctx, meOrder.OrderID); err != nil {
					r.logger.Warn("failed to request OS cancellation for orphan order",
						zap.String("order_id", meOrder.OrderID),
						zap.Error(err))
				}
			}

			if r.producer == nil {
				r.logger.Error("Kafka producer unavailable — orphan cancel not dispatched",
					zap.String("order_id", meOrder.OrderID))
				continue
			}

			if err := r.producer.PublishCancel(ctx, mc.MarketID, mc.Partition, meOrder.OrderID); err != nil {
				r.logger.Error("failed to publish orphan cancel to Kafka — will retry next cycle without cooldown",
					zap.String("order_id", meOrder.OrderID),
					zap.Error(err))
				continue
			}

			// Only record cooldown if cancellation command was successfully published
			r.orphanCancelMu.Lock()
			r.orphanCancelInFlight[meOrder.OrderID] = orphanCancelRecord{
				marketID:   marketID,
				lastCancel: time.Now(),
			}
			r.orphanCancelMu.Unlock()
			// Note: We do NOT touch tracked here — tracked may be in-flight or waiting for ME acceptance.
		}
	}

	// Prune orphanCancelInFlight of orders for this market that are no longer in ME snapshot
	r.orphanCancelMu.Lock()
	for oid, rec := range r.orphanCancelInFlight {
		if rec.marketID == marketID {
			if _, stillInME := snapByOrderID[oid]; !stillInME {
				delete(r.orphanCancelInFlight, oid)
			}
		}
	}
	r.orphanCancelMu.Unlock()

	// B. Detect missing RESTING or OS_REGISTERED orders with grace period and 2-cycle hysteresis
	// Uses exact order identity check (INV-MM-04).
	now := time.Now()
	for _, tracked := range r.tracker.All(marketID) {
		// Only check orders that LE believes are (or should be) resting in ME
		if tracked.Status != order.StatusResting && tracked.Status != order.StatusOSRegistered {
			continue
		}

		presentInME := (tracked.OrderID != "" && snapByOrderID[tracked.OrderID].OrderID != "") ||
			(tracked.ClientOrderID != "" && snapByCOID[tracked.ClientOrderID].ClientOrderID != "")

		if !presentInME {
			if inFlightAt, ok := r.inFlightSince[tracked.LevelID]; ok && now.Sub(inFlightAt) < inFlightGracePeriod {
				// Within grace period (10s), skip missing check
				continue
			}

			r.missingCycles[tracked.LevelID]++
			r.logger.Warn("order missing from ME snapshot",
				zap.String("level_id", tracked.LevelID),
				zap.String("order_id", tracked.OrderID),
				zap.String("status", string(tracked.Status)),
				zap.Int("missing_cycle", r.missingCycles[tracked.LevelID]))

			if r.missingCycles[tracked.LevelID] >= 2 {
				r.handleMissingRestingOrder(ctx, tracked, mc)
				delete(r.missingCycles, tracked.LevelID)
			}
		}
	}
}

// handleMissingRestingOrder resolves an order confirmed absent from ME after hysteresis.
// Called for both RESTING and OS_REGISTERED orders missing from ME snapshot.
//
// Invariant contract:
// OS Result        | Action
// OPEN / PARTIAL   | CANCELLING + publish cancel + lock slot
// CANCELLING       | CANCELLING + retry
// CANCELLED        | delete notFoundCount + tracker.Remove()
// FILLED           | delete notFoundCount + tracker.Remove()
// NOT_FOUND        | hysteresis: count consecutive misses; remove only after 2 consecutive checks
// timeout/error    | hold slot (blocked from DiffCreate), retry next cycle
func (r *Reconciler) handleMissingRestingOrder(ctx context.Context, o *order.LiveOrder, mc *config.MarketConfig) {
	r.logger.Info("resolving order confirmed missing from ME snapshot",
		zap.String("level_id", o.LevelID),
		zap.String("order_id", o.OrderID),
		zap.String("status", string(o.Status)),
		zap.String("client_order_id", o.ClientOrderID))

	if r.orderSvc == nil {
		r.logger.Error("order service not configured — fail closed: locking slot as CANCELLING without removing",
			zap.String("level_id", o.LevelID))
		r.tracker.SetCancelling(o.LevelID)
		delete(r.missingCycles, o.LevelID)
		return
	}

	osState, err := r.orderSvc.GetOrderByClientID(ctx, o.ClientOrderID)
	if err != nil {
		if err == orderservice.ErrOrderNotFound {
			r.notFoundCount[o.LevelID]++
			if r.notFoundCount[o.LevelID] < 2 {
				r.logger.Warn("missing order not in OS on first check — holding slot to confirm on next cycle",
					zap.String("level_id", o.LevelID),
					zap.Int("not_found_count", r.notFoundCount[o.LevelID]))
				return
			}
			r.logger.Info("missing order confirmed absent from OS across consecutive cycles — removing from tracker",
				zap.String("level_id", o.LevelID))
			r.removeTrackedOrder(o.LevelID)
			return
		}
		r.logger.Warn("temporary error checking OS for missing order — holding slot until next cycle", zap.Error(err))
		return
	}

	// Reset notFoundCount on any successful OS lookup
	delete(r.notFoundCount, o.LevelID)

	switch osState.Status {
	case "FILLED":
		r.logger.Info("missing order was filled", zap.String("level_id", o.LevelID))
		r.removeTrackedOrder(o.LevelID)
		r.metrics.IncOrdersFilled(mc.MarketID, o.Side)

	case "CANCELLED":
		r.logger.Info("missing order was cancelled", zap.String("level_id", o.LevelID))
		r.removeTrackedOrder(o.LevelID)

	case "OPEN", "PARTIALLY_FILLED", "CANCELLING":
		// INV-2 + INV-3 + INV-MM-06: Cancel the old OS order first. Do NOT remove the tracker entry.
		// SetCancelling locks the slot — Diff() will not issue a CREATE until the
		// CANCELLING timeout path confirms cancellation and calls tracker.Remove().
		r.logger.Warn("order OPEN/PARTIALLY_FILLED in OS but absent from ME — cancelling before replacement",
			zap.String("level_id", o.LevelID),
			zap.String("order_id", o.OrderID),
			zap.String("client_order_id", o.ClientOrderID))
		if err := r.orderSvc.CancelMMOrder(ctx, o.OrderID); err != nil {
			r.logger.Error("failed to cancel phantom order in OS — slot locked as CANCELLING for retry",
				zap.String("level_id", o.LevelID),
				zap.Error(err))
		}
		if r.producer != nil {
			_ = r.producer.PublishCancel(ctx, mc.MarketID, mc.Partition, o.OrderID)
		}
		// Lock the slot — CANCELLING timeout path handles retry + eventual Remove()
		delete(r.missingCycles, o.LevelID)
		r.tracker.SetCancelling(o.LevelID)

	default:
		r.removeTrackedOrder(o.LevelID)
	}
}

// parseMERemainingQuantity extracts authoritative remaining quantity from a Matching Engine order summary.
// Invariants (LE-01, LE-03):
// - RemainingQuantity is the sole authoritative remaining quantity from the ME.
// - "0" is valid authoritative information indicating the order has been fully filled/consumed.
// - meOrder.Quantity is NEVER used as a fallback because it represents original order size,
//   which would un-fill partial executions.
// - Missing, invalid, or negative values are logged as warnings and fall back to the tracker's existing quantity.
func parseMERemainingQuantity(meOrder meclient.MMOrderSummary, fallback decimal.Decimal, logger *zap.Logger) decimal.Decimal {
	if meOrder.RemainingQuantity != "" {
		parsed, err := decimal.NewFromString(meOrder.RemainingQuantity)
		if err != nil {
			if logger != nil {
				logger.Warn("ME order has unparseable RemainingQuantity — falling back to tracker remaining qty",
					zap.String("order_id", meOrder.OrderID),
					zap.String("remaining_qty_raw", meOrder.RemainingQuantity),
					zap.Error(err))
			}
			return fallback
		}
		if parsed.IsNegative() {
			if logger != nil {
				logger.Warn("ME order has negative RemainingQuantity invariant violation — falling back to tracker remaining qty",
					zap.String("order_id", meOrder.OrderID),
					zap.String("remaining_qty", parsed.String()))
			}
			return fallback
		}
		return parsed
	}

	// meOrder.RemainingQuantity is empty/missing: instrument and fall back to tracker quantity
	if logger != nil {
		logger.Warn("ME order snapshot missing RemainingQuantity — falling back to tracker remaining qty",
			zap.String("order_id", meOrder.OrderID),
			zap.String("level_id", meOrder.LevelID))
	}
	return fallback
}

