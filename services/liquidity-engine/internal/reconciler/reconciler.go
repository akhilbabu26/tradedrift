// Package reconciler implements the core MM order reconciliation logic.
// It applies Diff() results and handles the CANCELLING/STALE state machine.
//
// CONCURRENCY: All exported methods must be called from the engine's
// single event loop goroutine.
package reconciler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/meclient"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/orderservice"
	"tradedrift/services/liquidity-engine/internal/pricing"
)

// OrderServiceClient defines the Order Service operations needed by the Reconciler.
type OrderServiceClient interface {
	GetOrderByClientID(ctx context.Context, clientOrderID string) (*orderservice.OrderState, error)
	CancelMMOrder(ctx context.Context, orderID string) error
	CreateMMOrder(ctx context.Context, marketID, side, price, quantity, clientOrderID string) (*orderservice.OrderState, error)
	ListMMOrders(ctx context.Context, marketID string) ([]order.OSOrder, error)
}

// CommandProducer defines the Kafka command publishing operations needed by the Reconciler.
type CommandProducer interface {
	PublishCreate(ctx context.Context, marketID string, partition int, orderID, clientOrderID, side, price, quantity string) error
	PublishCancel(ctx context.Context, marketID string, partition int, orderID string) error
}

// Reconciler applies diff results and manages the PENDING/CANCELLING/STALE lifecycle.
type Reconciler struct {
	tracker                    *order.Tracker
	producer                   CommandProducer
	orderSvc                   OrderServiceClient
	meClient                   *meclient.Client
	cfg                        *config.Config
	logger                     *zap.Logger
	metrics                    ReconcilerMetrics
	consecutivePendingTimeouts map[string]int // marketID → count of consecutive timeouts
	// notFoundCount tracks how many consecutive times GetOrderByClientID returned
	// ErrOrderNotFound for a specific level. Only after notFoundThreshold consecutive
	// misses does the LE count it as a ME liveness failure.
	notFoundCount map[string]int // levelID → consecutive NOT_FOUND count

	// In-flight and hysteresis tracking
	missingCycles map[string]int       // levelID → consecutive cycles missing from ME snapshot
	inFlightSince map[string]time.Time // levelID → timestamp when create/cancel was dispatched
	reconcileBusy map[string]bool      // marketID → whether reconciliation is currently running
	busyMu        sync.Mutex

	// Orphan cancellation deduplication and rate limiting
	orphanCancelMu       sync.Mutex
	orphanCancelInFlight map[string]orphanCancelRecord // orderID → record
}

type orphanCancelRecord struct {
	marketID   string
	lastCancel time.Time
}

const (
	notFoundThreshold    = 3 // consecutive NOT_FOUND before counting as liveness failure
	inFlightGracePeriod  = 10 * time.Second
	orphanCancelCooldown = 5 * time.Second
)

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
	producer CommandProducer,
	orderSvc OrderServiceClient,
	meClient *meclient.Client,
	cfg *config.Config,
	logger *zap.Logger,
	metrics ReconcilerMetrics,
) *Reconciler {
	return &Reconciler{
		tracker:                    tracker,
		producer:                   producer,
		orderSvc:                   orderSvc,
		meClient:                   meClient,
		cfg:                        cfg,
		logger:                     logger,
		metrics:                    metrics,
		consecutivePendingTimeouts: make(map[string]int),
		notFoundCount:              make(map[string]int),
		missingCycles:              make(map[string]int),
		inFlightSince:              make(map[string]time.Time),
		reconcileBusy:              make(map[string]bool),
		orphanCancelInFlight:       make(map[string]orphanCancelRecord),
	}
}

// ReconcileMarket runs a full 3-state reconciliation cycle for one market.
// It verifies ME actual resting state via atomic snapshot, handles hysteresis and orphan cleanup,
// and publishes minimal Diff commands to Kafka.
func (r *Reconciler) ReconcileMarket(ctx context.Context, marketID string, bidCount, askCount int) (commandsPublished int, err error) {
	r.busyMu.Lock()
	if r.reconcileBusy[marketID] {
		r.busyMu.Unlock()
		r.logger.Debug("reconcile already active for market, skipping", zap.String("market_id", marketID))
		return 0, nil
	}
	r.reconcileBusy[marketID] = true
	r.busyMu.Unlock()
	defer func() {
		r.busyMu.Lock()
		r.reconcileBusy[marketID] = false
		r.busyMu.Unlock()
	}()

	mc := r.cfg.ForMarket(marketID)
	if mc == nil {
		return 0, fmt.Errorf("unknown market %s", marketID)
	}

	// 1. Matching Engine Snapshot Verification (Authority for actual resting orders)
	// INVARIANT: ME UNKNOWN / unreachable / nil client → zero mutations (fail-closed)
	if r.meClient == nil {
		r.logger.Error("ME client is nil — fail closed: skipping reconciliation without ME snapshot authority",
			zap.String("market_id", marketID))
		return 0, nil
	}

	snapCtx, cancelSnap := context.WithTimeout(ctx, 2*time.Second)
	snap, snapErr := r.meClient.FetchSnapshot(snapCtx, marketID)
	cancelSnap()

	if snapErr != nil {
		r.logger.Warn("ME snapshot fetch failed — skipping reconcile cycle for market",
			zap.String("market_id", marketID),
			zap.Error(snapErr))
		return 0, nil
	}

	if snap.State != "LIVE" {
		r.logger.Info("ME is not in LIVE state — pausing reconciliation",
			zap.String("market_id", marketID),
			zap.String("me_state", snap.State),
			zap.Uint64("sequence", snap.Sequence))
		return 0, nil
	}

	r.syncWithMESnapshot(ctx, marketID, snap, mc)

	// 2. Diff Desired against Tracker
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

			// A1. Confirmed Order: extract authoritative remaining quantity from ME
			remQty := tracked.RemainingQty
			if meOrder.RemainingQuantity != "" {
				if parsed, err := decimal.NewFromString(meOrder.RemainingQuantity); err == nil && !parsed.IsZero() {
					remQty = parsed
				}
			} else if meOrder.Quantity != "" {
				if parsed, err := decimal.NewFromString(meOrder.Quantity); err == nil && !parsed.IsZero() {
					remQty = parsed
				}
			}

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
			delete(r.notFoundCount, o.LevelID)
			delete(r.missingCycles, o.LevelID)
			r.tracker.Remove(o.LevelID)
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
		delete(r.missingCycles, o.LevelID)
		r.tracker.Remove(o.LevelID)
		r.metrics.IncOrdersFilled(mc.MarketID, o.Side)

	case "CANCELLED":
		r.logger.Info("missing order was cancelled", zap.String("level_id", o.LevelID))
		delete(r.missingCycles, o.LevelID)
		r.tracker.Remove(o.LevelID)

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
		delete(r.missingCycles, o.LevelID)
		r.tracker.Remove(o.LevelID)
	}
}
