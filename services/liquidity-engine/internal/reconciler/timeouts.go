package reconciler

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/orderservice"
)

// CheckPendingTimeouts examines all PENDING orders across all markets.
//
// ARCHITECTURAL INVARIANT (LE-13):
// This method deliberately does NOT check isMarketPaused(marketID).
// Even when a market is in PAUSED state (e.g. due to a reference price provider outage),
// any order already registered as PENDING represents an existing committed command that
// was already created in Order Service. Retrying its Kafka publish is strictly idempotent
// and completes an already-committed lifecycle operation; it does NOT create new liquidity
// bands or expand exposure. Permitting these retries during PAUSED preserves state
// consistency and prevents dangling PENDING orders from being abandoned.
func (r *Reconciler) CheckPendingTimeouts(ctx context.Context, marketID string) (consecutiveTimeouts int) {
	mc := r.cfg.ForMarket(marketID)
	if mc == nil {
		return 0
	}

	allOrders := r.tracker.All(marketID)
	timedOut := false

	for _, o := range allOrders {
		if o.Status != order.StatusPending {
			continue
		}
		age := time.Since(o.PendingSince)
		if age < r.cfg.PendingTimeout {
			continue
		}

		// If Kafka publication previously failed, retry publishing the exact same command!
		// INVARIANT: orderID, clientOrderID, and generation remain identical across retries.
		if !o.KafkaPublished {
			r.logger.Info("retrying Kafka publish for unconfirmed PENDING order",
				zap.String("level_id", o.LevelID),
				zap.String("order_id", o.OrderID),
				zap.String("client_order_id", o.ClientOrderID))

			err := r.producer.PublishCreate(ctx,
				mc.MarketID,
				mc.Partition,
				o.OrderID,
				o.ClientOrderID,
				o.Side,
				o.Price.String(),
				o.OriginalQty.String(),
			)
			if err != nil {
				r.logger.Warn("Kafka publish retry failed — will retry again on next check",
					zap.String("level_id", o.LevelID),
					zap.Error(err))
				timedOut = true
				continue
			}
			r.tracker.SetKafkaPublished(o.LevelID, true)
			r.logger.Info("Kafka publish retry succeeded", zap.String("level_id", o.LevelID))
		}

		r.logger.Info("PENDING timeout — checking Order Service",
			zap.String("level_id", o.LevelID),
			zap.String("client_order_id", o.ClientOrderID),
			zap.Duration("age", age))

		if r.orderSvc == nil {
			timedOut = true
			continue
		}

		osState, err := r.orderSvc.GetOrderByClientID(ctx, o.ClientOrderID)
		if err != nil {
			if err == orderservice.ErrOrderNotFound {
				// NOT_FOUND can be a transient timing window: LE published to OS
				// but OS hasn't committed yet, or network delay. Only count as a
				// liveness failure after notFoundThreshold consecutive misses.
				r.notFoundCount[o.LevelID]++
				r.logger.Warn("PENDING order not found in Order Service",
					zap.String("level_id", o.LevelID),
					zap.String("client_order_id", o.ClientOrderID),
					zap.Int("consecutive_not_found", r.notFoundCount[o.LevelID]),
					zap.Int("threshold", notFoundThreshold))
				if r.notFoundCount[o.LevelID] >= notFoundThreshold {
					r.logger.Error("PENDING order persistently absent from Order Service — possible ME delivery failure",
						zap.String("level_id", o.LevelID))
					timedOut = true
				}
			} else {
				r.logger.Warn("Order Service check failed for PENDING order",
					zap.String("client_order_id", o.ClientOrderID),
					zap.Error(err))
				timedOut = true
			}
			continue
		}

		// Reset not-found counter on successful OS response
		delete(r.notFoundCount, o.LevelID)

		switch osState.Status {
		case "OPEN", "PARTIALLY_FILLED":
			// OS confirmed the order exists. Transition to OS_REGISTERED — NOT RESTING.
			// OS OPEN means the order is registered for recovery.
			// ME RESTING means ME has it in the live order book.
			// These are distinct: CheckOSRegisteredTimeouts handles the OS_REGISTERED → RESTING promotion.
			r.logger.Info("PENDING confirmed by Order Service — transitioning to OS_REGISTERED",
				zap.String("level_id", o.LevelID))
			r.tracker.SetOSRegistered(o.LevelID, osState.OrderID, osState.OriginalQty, osState.RemainingQty)
			r.consecutivePendingTimeouts[marketID] = 0

		case "FILLED":
			r.logger.Info("PENDING order was filled before RESTING",
				zap.String("level_id", o.LevelID))
			r.removeTrackedOrder(o.LevelID)
			r.metrics.IncOrdersFilled(marketID, o.Side)

		case "CANCELLED":
			r.logger.Warn("PENDING order was cancelled",
				zap.String("level_id", o.LevelID))
			r.removeTrackedOrder(o.LevelID)

		default:
			timedOut = true
		}
	}

	if timedOut {
		r.consecutivePendingTimeouts[marketID]++
	}

	return r.consecutivePendingTimeouts[marketID]
}

// CheckCancellingTimeouts examines all CANCELLING orders for a market.
func (r *Reconciler) CheckCancellingTimeouts(ctx context.Context, marketID string) {
	mc := r.cfg.ForMarket(marketID)
	if mc == nil {
		return
	}

	allOrders := r.tracker.All(marketID)
	for _, o := range allOrders {
		if o.Status != order.StatusCancelling {
			continue
		}
		age := time.Since(o.CancellingSince)
		if age < r.cfg.CancellingTimeout {
			continue
		}
		r.handleCancellingTimeout(ctx, o, mc)
	}
}

// handleCancellingTimeout is the SINGLE authoritative implementation of CANCELLING resolution.
func (r *Reconciler) handleCancellingTimeout(ctx context.Context, o *order.LiveOrder, mc *config.MarketConfig) {
	r.logger.Info("CANCELLING timeout — querying Order Service",
		zap.String("level_id", o.LevelID),
		zap.String("client_order_id", o.ClientOrderID),
		zap.Int("cancel_retries", o.CancelRetries))

	osState, err := r.orderSvc.GetOrderByClientID(ctx, o.ClientOrderID)
	isOrderNotFound := false
	if err != nil {
		if err != orderservice.ErrOrderNotFound {
			r.logger.Warn("CANCELLING check failed — will retry next cycle",
				zap.String("client_order_id", o.ClientOrderID),
				zap.Error(err))
			return
		}
		isOrderNotFound = true
		// LE-5: Order Service NOT_FOUND does not mean fully filled.
		// Preserve tracked remaining quantity rather than zeroing it out.
		osState = &orderservice.OrderState{
			Status:       "CANCELLED",
			RemainingQty: o.RemainingQty,
		}
	}

	switch osState.Status {
	case "CANCELLED":
		r.logger.Info("CANCELLING confirmed",
			zap.String("level_id", o.LevelID))
		r.removeTrackedOrder(o.LevelID)

		if o.QueuedCorrection != nil {
			desired := *o.QueuedCorrection

			// Invariant: If order was fully filled before cancellation took effect (RemainingQty == 0),
			// NEVER recreate the order.
			// LE-5: ErrOrderNotFound must NEVER be treated as a full fill, nor increment orders filled metric.
			if !isOrderNotFound && osState != nil && osState.RemainingQty.IsZero() {
				r.logger.Info("CANCELLING order was fully filled before cancel took effect — skipping replacement",
					zap.String("level_id", o.LevelID),
					zap.String("client_order_id", o.ClientOrderID))
				r.metrics.IncOrdersFilled(mc.MarketID, o.Side)
				return
			}

			// Invariant: PAUSED reference prevents new replacement creations
			if r.isMarketPaused(mc.MarketID) {
				r.logger.Warn("reference price is PAUSED — dropping queued replacement creation",
					zap.String("market_id", mc.MarketID),
					zap.String("level_id", o.LevelID))
				return
			}

			remQty := decimal.Zero
			if osState != nil {
				remQty = osState.RemainingQty
			}
			safeQty, ok := r.resolveReplacementQuantity(
				desired.Quantity,
				remQty,
				o.OriginalQty,
				mc.MinOrderSize,
				mc.LotSize,
				mc.MarketID,
				o.LevelID,
			)
			if !ok {
				return
			}
			desired.Quantity = safeQty

			// Capital budget check: verify projected exposure stays within market limits
			if allowed, reason := r.canCreateLevel(mc, desired); !allowed {
				r.logger.Warn("skipping replacement creation — "+reason,
					zap.String("market_id", mc.MarketID),
					zap.String("level_id", o.LevelID),
				)
				return
			}

			gen := r.tracker.NextGeneration(o.LevelID)
			clientOrderID := order.ClientOrderID(o.LevelID, gen)

			// Register replacement with Order Service
			osOrder, err := r.orderSvc.CreateMMOrder(ctx, mc.MarketID, desired.Side,
				desired.Price.String(), desired.Quantity.String(), clientOrderID)
			if err != nil || osOrder == nil {
				r.logger.Error("failed to register CORRECT replacement in Order Service",
					zap.String("level_id", o.LevelID),
					zap.Error(err))
				return
			}

			orderID := osOrder.OrderID
			r.tracker.SetPending(o.LevelID, orderID, clientOrderID, gen, desired)

			if err := r.producer.PublishCreate(ctx, mc.MarketID, mc.Partition,
				orderID, clientOrderID, desired.Side,
				desired.Price.String(), desired.Quantity.String()); err != nil {
				r.logger.Warn("failed to publish CORRECT replacement to Kafka — will retry in CheckPendingTimeouts",
					zap.String("level_id", o.LevelID),
					zap.String("order_id", orderID),
					zap.Error(err))
				return
			}

			r.tracker.SetKafkaPublished(o.LevelID, true)
			r.logger.Info("CORRECT replacement published",
				zap.String("level_id", o.LevelID),
				zap.String("new_client_order_id", clientOrderID),
				zap.String("order_id", orderID))
		}

	case "FILLED", "PARTIALLY_FILLED":
		if osState.RemainingQty.IsZero() || osState.Status == "FILLED" {
			r.logger.Info("CANCELLING: order was filled — creating replacement",
				zap.String("level_id", o.LevelID))
			r.removeTrackedOrder(o.LevelID)
			r.metrics.IncOrdersFilled(mc.MarketID, o.Side)
		} else {
			r.logger.Warn("CANCELLING: PARTIALLY_FILLED with remaining — retrying cancel",
				zap.String("level_id", o.LevelID))
			r.retryCancelOrStale(ctx, o, mc)
		}

	case "OPEN", "CANCELLING":
		r.retryCancelOrStale(ctx, o, mc)

	default:
		r.logger.Warn("CANCELLING: unknown order status — retrying",
			zap.String("level_id", o.LevelID),
			zap.String("status", osState.Status))
		r.retryCancelOrStale(ctx, o, mc)
	}
}

// retryCancelOrStale retries the cancel command or transitions to STALE on retry limit.
func (r *Reconciler) retryCancelOrStale(ctx context.Context, o *order.LiveOrder, mc *config.MarketConfig) {
	if o.CancelRetries >= r.cfg.CancelRetryLimit {
		r.logger.Error("CANCELLING retry limit exceeded — entering STALE",
			zap.String("level_id", o.LevelID),
			zap.String("client_order_id", o.ClientOrderID),
			zap.Int("retries", o.CancelRetries))

		r.tracker.SetStale(o.LevelID)
		r.metrics.IncStaleOrders(mc.MarketID)

		r.logger.Info("triggering authoritative resync after STALE",
			zap.String("market_id", mc.MarketID))
		return
	}

	r.logger.Warn("retrying cancel command",
		zap.String("level_id", o.LevelID),
		zap.Int("retry", o.CancelRetries+1))

	if err := r.producer.PublishCancel(ctx, mc.MarketID, mc.Partition, o.OrderID); err != nil {
		r.logger.Error("retry cancel publish failed",
			zap.String("level_id", o.LevelID),
			zap.Error(err))
		return
	}
	o.IncrementCancelRetry()
}


