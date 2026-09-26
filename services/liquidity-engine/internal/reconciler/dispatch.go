package reconciler

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/order"
)

// applyEntry applies a single DiffEntry to the tracker and Kafka.
func (r *Reconciler) applyEntry(ctx context.Context, e order.DiffEntry, mc *config.MarketConfig) error {
	switch e.Action {
	case order.DiffCreate:
		return r.applyCreate(ctx, e, mc)

	case order.DiffCancel:
		return r.applyCancel(ctx, e, mc)

	case order.DiffCorrect:
		// CORRECT = Cancel + wait + Create
		if err := r.applyCancel(ctx, e, mc); err != nil {
			return err
		}
		r.tracker.QueueCorrection(e.LevelID, *e.DesiredLevel)
		r.metrics.IncReconcileCorrect(mc.MarketID)
		return nil

	default:
		return fmt.Errorf("unknown diff action %d for level %s", e.Action, e.LevelID)
	}
}

// applyCreate generates a new client_order_id, registers it with the Order Service,
// sets the tracker to PENDING, and publishes an OrderCreated command to Kafka.
func (r *Reconciler) applyCreate(ctx context.Context, e order.DiffEntry, mc *config.MarketConfig) error {
	gen := r.tracker.NextGeneration(e.LevelID)
	clientOrderID := order.ClientOrderID(e.LevelID, gen)

	// Step 1: Register in Order Service (idempotent, skips wallet ReserveFunds and outbox for MM)
	osOrder, err := r.orderSvc.CreateMMOrder(ctx,
		mc.MarketID,
		e.DesiredLevel.Side,
		e.DesiredLevel.Price.String(),
		e.DesiredLevel.Quantity.String(),
		clientOrderID,
	)
	if err != nil {
		return fmt.Errorf("register MM order in Order Service for %s: %w", e.LevelID, err)
	}

	orderID := osOrder.OrderID

	// Step 2: Set PENDING in tracker with OS-assigned orderID (KafkaPublished initially false)
	r.tracker.SetPending(e.LevelID, orderID, clientOrderID, gen, *e.DesiredLevel)
	r.inFlightSince[e.LevelID] = time.Now()

	// Step 3: Publish OrderCreated command to Kafka
	err = r.producer.PublishCreate(ctx,
		mc.MarketID,
		mc.Partition,
		orderID,
		clientOrderID,
		e.DesiredLevel.Side,
		e.DesiredLevel.Price.String(),
		e.DesiredLevel.Quantity.String(),
	)
	if err != nil {
		r.logger.Warn("kafka publish OrderCreated failed after OS registration — will retry with same orderID on next cycle",
			zap.String("level_id", e.LevelID),
			zap.String("order_id", orderID),
			zap.Error(err))
		return fmt.Errorf("publish OrderCreated for %s: %w", e.LevelID, err)
	}

	r.tracker.SetKafkaPublished(e.LevelID, true)
	r.metrics.IncReconcileCreate(mc.MarketID)

	r.logger.Info("published OrderCreated with OS registration",
		zap.String("level_id", e.LevelID),
		zap.String("client_order_id", clientOrderID),
		zap.String("order_id", orderID),
		zap.String("side", e.DesiredLevel.Side),
		zap.String("price", e.DesiredLevel.Price.String()),
		zap.String("quantity", e.DesiredLevel.Quantity.String()))

	return nil
}

// applyCancel publishes an OrderCancelRequested command using the ME-assigned order_id.
// It also initiates cancellation in the Order Service to update the ledger state.
func (r *Reconciler) applyCancel(ctx context.Context, e order.DiffEntry, mc *config.MarketConfig) error {
	if e.ExistingOID == "" {
		return fmt.Errorf("cannot cancel level %s: missing order_id (ME UUID)", e.LevelID)
	}

	// 1. Notify Order Service via gRPC to transition order towards CANCELLING in ledger
	if r.orderSvc != nil {
		if err := r.orderSvc.CancelMMOrder(ctx, e.ExistingOID); err != nil {
			r.logger.Warn("cancel MM order in OS failed (will still publish cancel to Kafka)",
				zap.String("order_id", e.ExistingOID),
				zap.Error(err))
		}
	}

	// 2. Publish OrderCancelRequested directly to Kafka partition for immediate ME processing
	err := r.producer.PublishCancel(ctx, mc.MarketID, mc.Partition, e.ExistingOID)
	if err != nil {
		return fmt.Errorf("publish OrderCancelRequested for %s (order_id=%s): %w", e.LevelID, e.ExistingOID, err)
	}

	r.tracker.SetCancelling(e.LevelID)
	r.inFlightSince[e.LevelID] = time.Now()
	if e.Action == order.DiffCancel {
		r.metrics.IncReconcileCancel(mc.MarketID)
	}

	r.logger.Info("published OrderCancelRequested",
		zap.String("level_id", e.LevelID),
		zap.String("order_id", e.ExistingOID),
		zap.String("client_order_id", e.ExistingCOID))

	return nil
}
