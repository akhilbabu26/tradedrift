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
	consecutivePendingTimeouts map[string]int // marketID → count of consecutive timeouts
	// notFoundCount tracks how many consecutive times GetOrderByClientID returned
	// ErrOrderNotFound for a specific level. Only after notFoundThreshold consecutive
	// misses does the LE count it as a ME liveness failure.
	notFoundCount map[string]int // levelID → consecutive NOT_FOUND count
}

const notFoundThreshold = 3 // consecutive NOT_FOUND before counting as liveness failure

// ReconcilerMetrics is the minimal metrics interface used by the reconciler.
type ReconcilerMetrics interface {
	IncStaleOrders(marketID string)
	IncReconcileCreate(marketID string)
	IncReconcileCancel(marketID string)
	IncReconcileCorrect(marketID string)
	IncReconcileNoop(marketID string)
	IncOrdersFilled(marketID, side string)
	IncDuplicateMMLevel(marketID string)
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
		notFoundCount:              make(map[string]int),
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
			delete(r.notFoundCount, o.LevelID)
			r.tracker.Remove(o.LevelID)
			r.metrics.IncOrdersFilled(marketID, o.Side)

		case "CANCELLED":
			r.logger.Warn("PENDING order was cancelled",
				zap.String("level_id", o.LevelID))
			delete(r.notFoundCount, o.LevelID)
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

// CheckOSRegisteredTimeouts promotes OS_REGISTERED orders to RESTING after MEConfirmationTimeout,
// provided the Matching Engine is currently healthy (no consecutive ME timeouts).
// This is the V1 proxy for ME confirmation: if OS has the order and ME is healthy,
// we assume ME has accepted it after sufficient time has elapsed.
// V2 should replace this with a direct OrderRested event from the ME.
func (r *Reconciler) CheckOSRegisteredTimeouts(marketID string, meConfirmationTimeout time.Duration, meHealthy bool) {
	if !meHealthy {
		r.logger.Warn("ME is unhealthy — holding OS_REGISTERED orders without promoting to RESTING",
			zap.String("market_id", marketID))
		return
	}

	allOrders := r.tracker.All(marketID)
	for _, o := range allOrders {
		if o.Status != order.StatusOSRegistered {
			continue
		}
		if time.Since(o.OSRegisteredSince) < meConfirmationTimeout {
			continue
		}
		r.logger.Info("OS_REGISTERED order promoted to RESTING (ME healthy & confirmation timeout elapsed)",
			zap.String("level_id", o.LevelID),
			zap.String("order_id", o.OrderID),
			zap.Duration("elapsed", time.Since(o.OSRegisteredSince)))
		r.tracker.SetResting(o.LevelID, o.OrderID, o.OriginalQty, o.RemainingQty)
	}
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

			// Register replacement with Order Service
			osOrder, err := r.orderSvc.CreateMMOrder(ctx, mc.MarketID, desired.Side,
				desired.Price.String(), desired.Quantity.String(), clientOrderID)
			if err != nil {
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
