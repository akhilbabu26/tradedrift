package reconciler

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/pricing"
)

// canCreateLevel enforces the complete, final creation safety invariant (LE-9):
// 1. Price > 0
// 2. Quantity > 0
// 3. Side is valid ("BUY" or "SELL")
// 4. MarketID matches configured market
// 5. Quantity satisfies MinOrderSize and LotSize (if configured)
// 6. Capital exposure does not exceed configured limits
func (r *Reconciler) canCreateLevel(mc *config.MarketConfig, level pricing.PriceLevel) (bool, string) {
	if mc == nil {
		return true, ""
	}
	if level.Price.IsZero() || level.Price.IsNegative() {
		return false, fmt.Sprintf("invalid level price: %s <= 0", level.Price.String())
	}
	if level.Quantity.IsZero() || level.Quantity.IsNegative() {
		return false, fmt.Sprintf("invalid level quantity: %s <= 0", level.Quantity.String())
	}
	if level.Side != "BUY" && level.Side != "SELL" {
		return false, fmt.Sprintf("invalid level side: %s", level.Side)
	}
	if level.MarketID == "" || level.MarketID != mc.MarketID {
		return false, fmt.Sprintf("market mismatch: level market %q != configured %q", level.MarketID, mc.MarketID)
	}
	if !mc.MinOrderSize.IsZero() && level.Quantity.LessThan(mc.MinOrderSize) {
		return false, fmt.Sprintf("level quantity %s < min order size %s", level.Quantity.String(), mc.MinOrderSize.String())
	}
	if !mc.LotSize.IsZero() && level.Quantity.LessThan(mc.LotSize) {
		return false, fmt.Sprintf("level quantity %s < lot size %s", level.Quantity.String(), mc.LotSize.String())
	}

	if level.Side == "BUY" && !mc.MaxBidExposureUSDT.IsZero() {
		projected := r.tracker.CommittedQuote(mc.MarketID).
			Add(level.Price.Mul(level.Quantity))
		if projected.GreaterThan(mc.MaxBidExposureUSDT) {
			return false, fmt.Sprintf("BID exposure cap reached: projected %s > max %s", projected.String(), mc.MaxBidExposureUSDT.String())
		}
	} else if level.Side == "SELL" && !mc.MaxAskExposureBase.IsZero() {
		projected := r.tracker.CommittedBase(mc.MarketID).
			Add(level.Quantity)
		if projected.GreaterThan(mc.MaxAskExposureBase) {
			return false, fmt.Sprintf("ASK exposure cap reached: projected %s > max %s", projected.String(), mc.MaxAskExposureBase.String())
		}
	}
	return true, ""
}

// ApplyEntry applies a single DiffEntry to the tracker and Kafka.
// Exported for testing and direct entry execution.
func (r *Reconciler) ApplyEntry(ctx context.Context, e order.DiffEntry, mc *config.MarketConfig) (bool, error) {
	return r.applyEntry(ctx, e, mc)
}

// applyEntry applies a single DiffEntry to the tracker and Kafka.
// Returns published=true if a command was successfully published to Kafka.
func (r *Reconciler) applyEntry(ctx context.Context, e order.DiffEntry, mc *config.MarketConfig) (bool, error) {
	switch e.Action {
	case order.DiffCreate:
		return r.applyCreate(ctx, e, mc)

	case order.DiffCancel:
		if err := r.applyCancel(ctx, e, mc); err != nil {
			return false, err
		}
		return true, nil

	case order.DiffCorrect:
		// CORRECT = Cancel + wait + Create
		if e.DesiredLevel == nil {
			return false, fmt.Errorf("DiffCorrect called with nil DesiredLevel for %s", e.LevelID)
		}

		replacementLevel := *e.DesiredLevel

		// LE-01: Resolve and sanitize replacement quantity BEFORE QueueCorrection()
		existing := r.tracker.Get(e.LevelID)
		if existing != nil {
			safeQty, ok := r.resolveReplacementQuantity(
				e.DesiredLevel.Quantity,
				existing.RemainingQty,
				existing.OriginalQty,
				mc.MinOrderSize,
				mc.LotSize,
				mc.MarketID,
				e.LevelID,
			)
			if !ok {
				// Remaining quantity is 0 (fully filled) or below dust threshold -> cancel without replacement
				r.logger.Info("DiffCorrect candidate has zero/dust remaining quantity — cancelling without replacement",
					zap.String("level_id", e.LevelID),
					zap.String("remaining_qty", existing.RemainingQty.String()))
				if err := r.applyCancel(ctx, e, mc); err != nil {
					return false, err
				}
				r.metrics.IncReconcileCancel(mc.MarketID)
				return true, nil
			}
			replacementLevel.Quantity = safeQty
		}

		if err := r.applyCancel(ctx, e, mc); err != nil {
			return false, err
		}
		r.tracker.QueueCorrection(e.LevelID, replacementLevel)
		r.metrics.IncReconcileCorrect(mc.MarketID)
		return true, nil

	default:
		return false, fmt.Errorf("unknown diff action %d for level %s", e.Action, e.LevelID)
	}
}

// applyCreate generates a new client_order_id, registers it with the Order Service,
// and publishes OrderCreateRequested to Kafka.
// Returns published=true if the create command was successfully published.
func (r *Reconciler) applyCreate(ctx context.Context, e order.DiffEntry, mc *config.MarketConfig) (bool, error) {
	if e.DesiredLevel == nil {
		return false, fmt.Errorf("applyCreate called with nil DesiredLevel for %s", e.LevelID)
	}

	// Capital budget check: verify projected exposure stays within market limits
	if allowed, reason := r.canCreateLevel(mc, *e.DesiredLevel); !allowed {
		r.logger.Warn("skipping CREATE — "+reason,
			zap.String("market_id", mc.MarketID),
			zap.String("level_id", e.LevelID),
		)
		return false, nil
	}

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
		return false, fmt.Errorf("register MM order in Order Service for %s: %w", e.LevelID, err)
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
		return false, fmt.Errorf("publish OrderCreated for %s: %w", e.LevelID, err)
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

	return true, nil
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
