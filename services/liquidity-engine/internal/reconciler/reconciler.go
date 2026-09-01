// Package reconciler implements the core MM order reconciliation logic.
// It applies Diff() results and handles the CANCELLING/STALE state machine.
//
// CONCURRENCY: All exported methods must be called from the engine's
// single event loop goroutine.
package reconciler

import (
	"context"
	"fmt"

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
