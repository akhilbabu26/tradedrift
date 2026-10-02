// Package reconciler implements the core MM order reconciliation logic.
// It applies Diff() results and handles the CANCELLING/STALE state machine.
//
// CONCURRENCY: All exported methods must be called from the engine's
// single event loop goroutine.
package reconciler

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/platform/refprice"
	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/meclient"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/orderservice"
	"tradedrift/services/liquidity-engine/internal/pricing"
)

// ReferencePriceProvider defines the refprice Provider interface used by the reconciler.
type ReferencePriceProvider interface {
	Get(marketID string) (refprice.Entry, bool)
}

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

	rng *rand.Rand

	refProvider ReferencePriceProvider
	refStates   map[string]*RefState

	// rebaseFailureCount tracks consecutive level price generation failures during rebase
	rebaseFailureCount map[string]int // levelID → consecutive failure count
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
	IncRebaseGenerationFailure(marketID, levelID string)
	IncExpiryGenerationFailure(marketID string)
	IncQuantityInvariantViolation(marketID string)
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
		rng:                        rand.New(rand.NewSource(time.Now().UnixNano())),
		refStates:                  make(map[string]*RefState),
		rebaseFailureCount:         make(map[string]int),
	}
}

// SetRefProvider sets the external reference price provider.
func (r *Reconciler) SetRefProvider(p ReferencePriceProvider) {
	r.refProvider = p
}

// removeTrackedOrder removes a level from the tracker and cleans up all auxiliary
// tracking maps for that levelID (LE-14).
func (r *Reconciler) removeTrackedOrder(levelID string) {
	r.tracker.Remove(levelID)
	delete(r.notFoundCount, levelID)
	delete(r.missingCycles, levelID)
	delete(r.inFlightSince, levelID)
	delete(r.rebaseFailureCount, levelID)
}

// NewReconcilerWithRNG creates a new Reconciler with a specific RNG source (for deterministic testing).
func NewReconcilerWithRNG(
	tracker *order.Tracker,
	producer CommandProducer,
	orderSvc OrderServiceClient,
	meClient *meclient.Client,
	cfg *config.Config,
	logger *zap.Logger,
	metrics ReconcilerMetrics,
	rng *rand.Rand,
) *Reconciler {
	r := NewReconciler(tracker, producer, orderSvc, meClient, cfg, logger, metrics)
	if rng != nil {
		r.rng = rng
	}
	return r
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

	// 1. Fetch Reference Price and Classify Movement
	prevRefState := r.refStates[marketID]
	var prevRef decimal.Decimal
	if prevRefState != nil {
		prevRef = prevRefState.LastRef
	}

	refSnap := r.currentReference(mc)
	ref := refSnap.Price
	refVersion := refSnap.Version
	freshness := refSnap.Freshness
	refState := r.refStates[marketID]

	action := ClassifyMovement(prevRef, ref, freshness, mc.Repricing)

	// Record or update refState tracking
	if refState == nil {
		refState = &RefState{}
		r.refStates[marketID] = refState
	}
	refState.LastRef = ref
	refState.LastVersion = refVersion
	refState.Freshness = freshness
	// LE-2 INVARIANT: Do NOT overwrite refState.LastUpdated with time.Now() if already set by
	// currentReference() from the provider's authoritative FetchedAt.
	if refState.LastUpdated.IsZero() {
		refState.LastUpdated = time.Now()
	}

	// ARCHITECTURAL INVARIANT: PAUSED SEMANTICS
	// ------------------------------------------------------------------------------------
	// When reference price is PAUSED (or Matching Engine is unavailable/non-LIVE):
	//   1. NO new creates: ReconcileMarket halts before ME snapshot sync and ladder generation.
	//   2. NO new replacements: Expiry cancel-replace is halted; queued corrections on
	//      CANCELLING/absent orders are dropped/skipped.
	//   3. NO rebase repricing: Ladder rebase is halted; resting orders remain resting.
	//   4. IN-FLIGHT RETRIES ONLY: Unconfirmed PENDING orders (Kafka publish previously failed)
	//      MAY retry their exact original create command (same orderID, clientOrderID, OriginalQty)
	//      to guarantee idempotent create delivery without altering quantities.
	// ------------------------------------------------------------------------------------
	if action == ActionPause {
		r.logger.Warn("[REFPRICE_PAUSED] reference price PAUSED — skipping reconcile cycle (zero mutations)",
			zap.String("market_id", marketID))
		return 0, nil
	}

	// 2. Matching Engine Snapshot Verification (Authority for actual resting orders)
	// INVARIANT: ME UNKNOWN / unreachable / nil client → zero mutations (fail-closed)
	if r.meClient == nil {
		r.logger.Error("[ME_UNAVAILABLE] ME client is nil — fail closed: skipping reconciliation without ME snapshot authority",
			zap.String("market_id", marketID))
		return 0, nil
	}

	snapCtx, cancelSnap := context.WithTimeout(ctx, 2*time.Second)
	snap, snapErr := r.meClient.FetchSnapshot(snapCtx, marketID)
	cancelSnap()

	if snapErr != nil {
		r.logger.Warn("[ME_UNAVAILABLE] ME snapshot fetch failed — skipping reconcile cycle for market",
			zap.String("market_id", marketID),
			zap.Error(snapErr))
		return 0, nil
	}

	if snap.State != "LIVE" {
		r.logger.Info("[ME_NOT_LIVE] ME is not in LIVE state — pausing reconciliation",
			zap.String("market_id", marketID),
			zap.String("me_state", snap.State),
			zap.Uint64("sequence", snap.Sequence))
		return 0, nil
	}

	r.syncWithMESnapshot(ctx, marketID, snap, mc)
	if action == ActionHold {
		r.logger.Info("reference price STALE — maintaining existing liquidity, skipping reprice",
			zap.String("market_id", marketID))
	}

	// Controlled rebase or selective repricing under significant reference move
	if freshness == FreshnessFresh {
		if action == ActionControlledRebase || action == ActionSelectiveReprice {
			refState.RebaseActive = true
			refState.RebaseTargetRef = ref
			refState.RebaseTargetVer = refVersion
			refState.RebaseAction = action
		} else if refState.RebaseActive {
			// Invariant: RebaseTargetVer must always represent newest accepted reference version
			if refVersion > refState.RebaseTargetVer {
				refState.RebaseTargetRef = ref
				refState.RebaseTargetVer = refVersion
			}
		}

		if refState.RebaseActive {
			commandsPublished += r.applyRefMovementReprice(ctx, mc, refState.RebaseTargetRef, refState.RebaseTargetVer, refState.RebaseAction)
		}
	}

	desired, err := pricing.GenerateZonedDesired(
		mc, ref, r.tracker, mc.BidZones, mc.AskZones, refVersion,
		bidCount, askCount, r.rng,
	)
	if err != nil {
		r.logger.Error("failed to generate zoned desired ladder",
			zap.String("market_id", marketID),
			zap.Error(err))
		return 0, err
	}
	entries := order.Diff(desired, r.tracker, marketID, mc)

	// INVARIANT: STALE reference maintains existing healthy liquidity and cancellations,
	// but STRICTLY blocks creating any new liquidity orders.
	if action == ActionHold {
		filtered := make([]order.DiffEntry, 0, len(entries))
		for _, e := range entries {
			if e.Action != order.DiffCreate {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	if len(entries) == 0 {
		if commandsPublished == 0 {
			r.metrics.IncReconcileNoop(marketID)
		}
		r.logger.Debug("reconcile diff complete",
			zap.String("market_id", marketID),
			zap.Int("bid_count", bidCount),
			zap.Int("ask_count", askCount),
			zap.Int("commands_published", commandsPublished))
		return commandsPublished, nil
	}

	r.logger.Info("reconcile diff",
		zap.String("market_id", marketID),
		zap.Int("entries", len(entries)))

	for _, e := range entries {
		if published, err := r.applyEntry(ctx, e, mc); err != nil {
			r.logger.Error("failed to apply diff entry",
				zap.String("market_id", marketID),
				zap.String("level_id", e.LevelID),
				zap.Int("action", int(e.Action)),
				zap.Error(err))
		} else if published {
			commandsPublished++
		}
	}
	return commandsPublished, nil
}


