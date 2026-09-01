package reconciler

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/order"
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

	osOrders, err := r.orderSvc.ListMMOrders(ctx, marketID)
	if err != nil {
		return fmt.Errorf("ListMMOrders for %s: %w", marketID, err)
	}

	added, duplicates := r.tracker.SyncFromOrders(marketID, osOrders)
	if duplicates > 0 {
		r.logger.Warn("duplicate LevelIDs detected in Order Service active response",
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
				r.tracker.Remove(tracked.LevelID)

			case order.StatusCancelling:
				r.logger.Info("CANCELLING order confirmed absent from OS — removing and handling queued correction",
					zap.String("level_id", tracked.LevelID),
					zap.String("client_order_id", tracked.ClientOrderID))
				r.tracker.Remove(tracked.LevelID)

				if tracked.QueuedCorrection != nil {
					desired := *tracked.QueuedCorrection
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
