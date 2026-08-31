// Package reconciler implements the core MM order reconciliation logic.
// It applies Diff() results and handles the CANCELLING/STALE state machine.
//
// SINGLE IMPLEMENTATION RULE: There is exactly ONE implementation of
// handleCancellingTimeout in this codebase.
//
// CONCURRENCY: All exported methods must be called from the engine's
// single event loop goroutine.
package reconciler

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/kafka"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/orderservice"
	"tradedrift/services/liquidity-engine/internal/pricing"
)

// Reconciler applies diff results and manages the PENDING/CANCELLING/STALE lifecycle.
type Reconciler struct {
	tracker                    *order.Tracker
	producer                   *kafka.Producer
	orderSvc                   *orderservice.Client
	cfg                        *config.Config
	logger                     *zap.Logger
	metrics                    ReconcilerMetrics
	consecutivePendingTimeouts map[string]int // marketID -> count of consecutive timeouts
}

// ReconcilerMetrics is the minimal metrics interface used by the reconciler.
type ReconcilerMetrics interface {
	IncStaleOrders(marketID string)
	IncReconcileCreate(marketID string)
	IncReconcileCancel(marketID string)
	IncReconcileCorrect(marketID string)
	IncReconcileNoop(marketID string)
	IncOrdersFilled(marketID, side string)
}

// NewReconciler creates a new Reconciler.
func NewReconciler(
	tracker *order.Tracker,
	producer *kafka.Producer,
	orderSvc *orderservice.Client,
	cfg *config.Config,
	logger *zap.Logger,
	metrics ReconcilerMetrics,
) *Reconciler {
	return &Reconciler{
		tracker:                    tracker,
		producer:                   producer,
		orderSvc:                   orderSvc,
		cfg:                        cfg,
		logger:                     logger,
		metrics:                    metrics,
		consecutivePendingTimeouts: make(map[string]int),
	}
}

// ReconcileMarket runs a full reconciliation cycle for one market.
func (r *Reconciler) ReconcileMarket(ctx context.Context, marketID string, bidCount, askCount int) (commandsPublished int, err error) {
	mc := r.cfg.ForMarket(marketID)
	if mc == nil {
		return 0, fmt.Errorf("unknown market %s", marketID)
	}

	desired := pricing.GenerateLadder(mc, bidCount, askCount)
	entries := order.Diff(desired, r.tracker, marketID, mc)

	if len(entries) == 0 {
		r.metrics.IncReconcileNoop(marketID)
		r.logger.Debug("reconcile noop — desired == actual",
			zap.String("market_id", marketID),
			zap.Int("bid_count", bidCount),
			zap.Int("ask_count", askCount))
		return 0, nil
	}

	r.logger.Info("reconcile diff",
		zap.String("market_id", marketID),
		zap.Int("entries", len(entries)))

	for _, e := range entries {
		if err := r.applyEntry(ctx, e, mc); err != nil {
			r.logger.Error("failed to apply diff entry",
				zap.String("market_id", marketID),
				zap.String("level_id", e.LevelID),
				zap.Int("action", int(e.Action)),
				zap.Error(err))
		} else {
			commandsPublished++
		}
	}
	return commandsPublished, nil
}

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

// applyCreate generates a new client_order_id and publishes an OrderCreated command.
func (r *Reconciler) applyCreate(ctx context.Context, e order.DiffEntry, mc *config.MarketConfig) error {
	gen := r.tracker.NextGeneration(e.LevelID)
	clientOrderID := order.ClientOrderID(e.LevelID, gen)
	orderID := uuid.New().String()

	err := r.producer.PublishCreate(ctx,
		mc.MarketID,
		mc.Partition,
		orderID,
		clientOrderID,
		e.DesiredLevel.Side,
		e.DesiredLevel.Price.String(),
		e.DesiredLevel.Quantity.String(),
	)
	if err != nil {
		r.tracker.DecrementGeneration(e.LevelID)
		return fmt.Errorf("publish OrderCreated for %s: %w", e.LevelID, err)
	}

	r.tracker.SetPending(e.LevelID, clientOrderID, gen, *e.DesiredLevel)
	r.metrics.IncReconcileCreate(mc.MarketID)

	r.logger.Info("published OrderCreated",
		zap.String("level_id", e.LevelID),
		zap.String("client_order_id", clientOrderID),
		zap.String("order_id", orderID),
		zap.String("side", e.DesiredLevel.Side),
		zap.String("price", e.DesiredLevel.Price.String()),
		zap.String("quantity", e.DesiredLevel.Quantity.String()))

	return nil
}

// applyCancel publishes an OrderCancelRequested command using the ME-assigned order_id.
func (r *Reconciler) applyCancel(ctx context.Context, e order.DiffEntry, mc *config.MarketConfig) error {
	if e.ExistingOID == "" {
		return fmt.Errorf("cannot cancel level %s: missing order_id (ME UUID)", e.LevelID)
	}

	err := r.producer.PublishCancel(ctx, mc.MarketID, mc.Partition, e.ExistingOID)
	if err != nil {
		return fmt.Errorf("publish OrderCancelRequested for %s (order_id=%s): %w", e.LevelID, e.ExistingOID, err)
	}

	r.tracker.SetCancelling(e.LevelID)
	if e.Action == order.DiffCancel {
		r.metrics.IncReconcileCancel(mc.MarketID)
	}

	r.logger.Info("published OrderCancelRequested",
		zap.String("level_id", e.LevelID),
		zap.String("order_id", e.ExistingOID),
		zap.String("client_order_id", e.ExistingCOID))

	return nil
}

// CheckPendingTimeouts examines all PENDING orders across all markets.
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

		r.logger.Info("PENDING timeout — checking Order Service",
			zap.String("level_id", o.LevelID),
			zap.String("client_order_id", o.ClientOrderID),
			zap.Duration("age", age))

		osState, err := r.orderSvc.GetOrderByClientID(ctx, o.ClientOrderID)
		if err != nil {
			if err == orderservice.ErrOrderNotFound {
				r.logger.Warn("PENDING order not found in Order Service — removing",
					zap.String("level_id", o.LevelID),
					zap.String("client_order_id", o.ClientOrderID))
				r.tracker.Remove(o.LevelID)
				timedOut = true
			} else {
				r.logger.Warn("Order Service check failed for PENDING order",
					zap.String("client_order_id", o.ClientOrderID),
					zap.Error(err))
				timedOut = true
			}
			continue
		}

		switch osState.Status {
		case "OPEN", "PARTIALLY_FILLED":
			r.logger.Info("PENDING confirmed RESTING via Order Service",
				zap.String("level_id", o.LevelID))
			r.tracker.SetResting(o.LevelID, osState.OrderID, osState.OriginalQty, osState.RemainingQty)
			r.consecutivePendingTimeouts[marketID] = 0

		case "FILLED":
			r.logger.Info("PENDING order was filled before RESTING",
				zap.String("level_id", o.LevelID))
			r.tracker.Remove(o.LevelID)
			r.metrics.IncOrdersFilled(marketID, o.Side)

		case "CANCELLED":
			r.logger.Warn("PENDING order was cancelled",
				zap.String("level_id", o.LevelID))
			r.tracker.Remove(o.LevelID)

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
	if err != nil {
		if err != orderservice.ErrOrderNotFound {
			r.logger.Warn("CANCELLING check failed — will retry next cycle",
				zap.String("client_order_id", o.ClientOrderID),
				zap.Error(err))
			return
		}
		osState = &orderservice.OrderState{Status: "CANCELLED"}
	}

	switch osState.Status {
	case "CANCELLED":
		r.logger.Info("CANCELLING confirmed",
			zap.String("level_id", o.LevelID))
		r.tracker.Remove(o.LevelID)

		if o.QueuedCorrection != nil {
			desired := *o.QueuedCorrection
			gen := r.tracker.NextGeneration(o.LevelID)
			clientOrderID := order.ClientOrderID(o.LevelID, gen)
			orderID := uuid.New().String()

			if err := r.producer.PublishCreate(ctx, mc.MarketID, mc.Partition,
				orderID, clientOrderID, desired.Side,
				desired.Price.String(), desired.Quantity.String()); err != nil {
				r.logger.Error("failed to publish CORRECT replacement",
					zap.String("level_id", o.LevelID),
					zap.Error(err))
				return
			}
			r.tracker.SetPending(o.LevelID, clientOrderID, gen, desired)
			r.logger.Info("CORRECT replacement published",
				zap.String("level_id", o.LevelID),
				zap.String("new_client_order_id", clientOrderID))
		}

	case "FILLED", "PARTIALLY_FILLED":
		if osState.RemainingQty.IsZero() || osState.Status == "FILLED" {
			r.logger.Info("CANCELLING: order was filled — creating replacement",
				zap.String("level_id", o.LevelID))
			r.tracker.Remove(o.LevelID)
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

// SyncFromOrderService calls ListMMOrders and updates the tracker.
func (r *Reconciler) SyncFromOrderService(ctx context.Context, marketID string) error {
	osOrders, err := r.orderSvc.ListMMOrders(ctx, marketID)
	if err != nil {
		return fmt.Errorf("ListMMOrders for %s: %w", marketID, err)
	}

	added := r.tracker.SyncFromOrders(marketID, osOrders)
	r.logger.Info("synced from Order Service",
		zap.String("market_id", marketID),
		zap.Int("orders_found", len(osOrders)),
		zap.Int("new_entries", added))

	osLevelIDs := make(map[string]bool, len(osOrders))
	for _, o := range osOrders {
		osLevelIDs[o.LevelID] = true
	}

	for _, tracked := range r.tracker.All(marketID) {
		if tracked.Status == order.StatusResting && !osLevelIDs[tracked.LevelID] {
			r.logger.Info("order gone from OS — removing from tracker",
				zap.String("level_id", tracked.LevelID),
				zap.String("client_order_id", tracked.ClientOrderID))
			r.tracker.Remove(tracked.LevelID)
		}
	}

	return nil
}
