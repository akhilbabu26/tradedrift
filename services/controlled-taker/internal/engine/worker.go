package engine

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"tradedrift/services/controlled-taker/internal/clients/orderservice"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
	"tradedrift/services/controlled-taker/internal/config"
	"tradedrift/services/controlled-taker/internal/metrics"
)

// DepthReader defines the read-only contract for retrieving order-book depth.
type DepthReader interface {
	GetDepth(ctx context.Context, marketID string) (*redisdepth.DepthSnapshot, error)
}

// OrderSubmitter defines the contract for submitting and managing orders with Order Service.
type OrderSubmitter interface {
	CreateCrossingOrder(ctx context.Context, marketID, side, priceCap, quantity, idempotencyKey string) (*orderservice.OrderResult, error)
	GetOrder(ctx context.Context, orderID string) (*orderservice.OrderResult, error)
	CancelOrder(ctx context.Context, orderID string) (*orderservice.OrderResult, error)
	FindOrderByIdempotencyKey(ctx context.Context, marketID, idempotencyKey string) (*orderservice.OrderResult, error)
}

// MarketWorker runs an autonomous, isolated taker loop for a single market.
type MarketWorker struct {
	market                   config.MarketConfig
	cfg                      config.Config
	ordersClient             OrderSubmitter
	redisReader              DepthReader
	safetyMgr                *SafetyManager
	selector                 *DirectionSelector
	regimeMgr                *RegimeManager
	adaptivePostHighCooldown time.Duration
	postHighCooldownSampler  func(severity HighSweepSeverity, rng *rand.Rand) time.Duration
	rng                      *rand.Rand
	logger                   *zap.Logger
}

// NewMarketWorker creates an isolated worker for the given trading pair.
func NewMarketWorker(
	market config.MarketConfig,
	cfg config.Config,
	ordersClient OrderSubmitter,
	redisReader DepthReader,
	selector *DirectionSelector,
	logger *zap.Logger,
) *MarketWorker {
	marketLogger := logger.With(zap.String("market", market.MarketID))

	cb := NewCircuitBreaker(cfg.CircuitBreakerFailures, 60*time.Second, marketLogger)
	cb.SetMarketID(market.MarketID)
	safetyMgr := NewSafetyManager(
		cb,
		cfg.MaxHourlyVolumeUSDT,
		cfg.MaxDailyVolumeUSDT,
		cfg.MaxTradesPerHour,
		cfg.MaxSpreadPercent,
		cfg.MaxVisibleDepthConsumption,
	)

	now := time.Now()
	var seed int64
	if cfg.RNGSeed != 0 {
		seed = cfg.RNGSeed
	} else {
		seed = now.UnixNano()
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(market.MarketID))
	workerSeed := seed ^ int64(h.Sum64())
	workerRNG := rand.New(rand.NewSource(workerSeed))

	regimeMgr := NewRegimeManager(now, workerRNG, nil)

	return &MarketWorker{
		market:                  market,
		cfg:                     cfg,
		ordersClient:            ordersClient,
		redisReader:             redisReader,
		safetyMgr:               safetyMgr,
		selector:                selector,
		regimeMgr:               regimeMgr,
		postHighCooldownSampler: SamplePostHighCooldown,
		rng:                     workerRNG,
		logger:                  marketLogger,
	}
}

// Run executes the per-market loop until ctx is cancelled.
// startupDelay enforces cold-start anti-burst staggering across markets.
func (w *MarketWorker) Run(ctx context.Context, startupDelay time.Duration) {
	w.logger.Info("Market worker initialized, waiting for warm-up delay", zap.Duration("delay", startupDelay))

	select {
	case <-ctx.Done():
		return
	case <-time.After(startupDelay):
	}

	w.logger.Info("Market worker started active execution loop",
		zap.String("initial_regime", string(w.regimeMgr.CurrentRegime())),
		zap.Time("dwell_until", w.regimeMgr.DwellUntil()),
	)
	metrics.SetCurrentRegime(w.market.MarketID, string(w.regimeMgr.CurrentRegime()))

	for {
		// 1. Evaluate Macro Activity Regime lifecycle
		// RegimeManager.Tick evaluates transitions strictly when the active dwell timer expires.
		prevRegime := w.regimeMgr.CurrentRegime()
		currentRegime, transitioned := w.regimeMgr.Tick(time.Now(), w.rng)
		if transitioned {
			metrics.RegimeTransitions.WithLabelValues(w.market.MarketID, string(prevRegime), string(currentRegime)).Inc()
			metrics.SetCurrentRegime(w.market.MarketID, string(currentRegime))
			w.logger.Info("Market regime transitioned",
				zap.String("market", w.market.MarketID),
				zap.String("new_regime", string(currentRegime)),
				zap.Time("dwell_until", w.regimeMgr.DwellUntil()),
			)
		}

		// 2. Choose Activity Profile based entirely on active regime distribution
		profile := w.chooseProfile()
		metrics.ProfileSelected.WithLabelValues(w.market.MarketID, string(currentRegime), string(profile)).Inc()

		// 3. Execute Order Cycle first
		w.ExecuteCycle(ctx, profile)

		// 4. Compute Next Interval using active regime's interval configuration
		// Sampled via Log-Normal distribution (sigma=0.4) for human-like arrival pacing
		nextWait := SampleLogNormalInterval(w.rng, w.regimeMgr.ActiveIntervalConfig())
		metrics.InterarrivalSeconds.WithLabelValues(w.market.MarketID, string(currentRegime)).Observe(nextWait.Seconds())

		// 5. Apply adaptive post-HIGH cooldown as minimum wait floor before the NEXT order
		if w.adaptivePostHighCooldown > 0 {
			if w.adaptivePostHighCooldown > nextWait {
				w.logger.Info("Applying adaptive post-HIGH pacing floor",
					zap.String("market", w.market.MarketID),
					zap.Duration("regime_wait", nextWait),
					zap.Duration("adaptive_floor", w.adaptivePostHighCooldown),
				)
				nextWait = w.adaptivePostHighCooldown
			}
			w.adaptivePostHighCooldown = 0
		}

		// 6. Wait before the NEXT cycle can execute
		select {
		case <-ctx.Done():
			w.logger.Info("Market worker stopping (context cancelled)")
			return
		case <-time.After(nextWait):
		}
	}
}

// SafetyManager returns the worker's safety manager instance.
func (w *MarketWorker) SafetyManager() *SafetyManager {
	return w.safetyMgr
}

// RegimeManager returns the worker's regime manager instance.
func (w *MarketWorker) RegimeManager() *RegimeManager {
	return w.regimeMgr
}

// AdaptivePostHighCooldown returns the currently pending adaptive post-HIGH cooldown.
func (w *MarketWorker) AdaptivePostHighCooldown() time.Duration {
	return w.adaptivePostHighCooldown
}

// SetAdaptivePostHighCooldown overrides the pending adaptive post-HIGH cooldown (primarily for testing).
func (w *MarketWorker) SetAdaptivePostHighCooldown(cd time.Duration) {
	w.adaptivePostHighCooldown = cd
}

// SetPostHighCooldownSampler allows unit tests to inject a custom/scaled cooldown sampler.
func (w *MarketWorker) SetPostHighCooldownSampler(fn func(severity HighSweepSeverity, rng *rand.Rand) time.Duration) {
	w.postHighCooldownSampler = fn
}

// chooseProfile delegates profile selection to RegimeManager.
// The macro regime owns profile probability (WeightLow, WeightMid, WeightHigh).
func (w *MarketWorker) chooseProfile() config.ProfileType {
	return w.regimeMgr.ChooseProfile(w.rng, true, true)
}

// ExecuteCycle executes an autonomous taker order cycle for this market.
func (w *MarketWorker) ExecuteCycle(ctx context.Context, profile config.ProfileType) {
	// Step 1: Read Live Depth from Redis (Immediate Order-Book Reality)
	depthCtx, depthCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	depth, err := w.redisReader.GetDepth(depthCtx, w.market.MarketID)
	depthCancel()

	if err != nil {
		w.logger.Warn("Failed to read live depth from Redis, skipping cycle", zap.Error(err))
		w.safetyMgr.RecordFailure()
		metrics.OrdersFailed.WithLabelValues(w.market.MarketID, "depth_read_error").Inc()
		return
	}
	if depth != nil && !depth.SnapshotAt.IsZero() {
		metrics.DepthAgeSeconds.WithLabelValues(w.market.MarketID, "read").Observe(time.Since(depth.SnapshotAt).Seconds())
	}

	// Step 2: Read External Reference Anchor (Context Only)
	var refPrice decimal.Decimal
	if ar, ok := w.redisReader.(interface {
		GetAnchor(ctx context.Context, marketID string) (*redisdepth.RefPriceAnchor, error)
	}); ok {
		anchorCtx, anchorCancel := context.WithTimeout(ctx, 200*time.Millisecond)
		anchor, anchorErr := ar.GetAnchor(anchorCtx, w.market.MarketID)
		anchorCancel()
		if anchorErr == nil && anchor != nil && anchor.State == "FRESH" {
			refPrice = anchor.Price
		}
	}

	// Step 3: Select Direction with Inventory Bias + RefPrice Gravity + Order-Book Imbalance
	midPrice, _ := depth.MidPrice()
	side := w.selector.SelectSideWithDepth(w.market.MarketID, midPrice, refPrice, depth, w.rng)

	// Step 4: Compute Quantity Dynamically from CURRENT Live Depth
	opts := SizingOptions{
		RNG:                        w.rng,
		HighSweepProbability:       w.cfg.HighSweepProbability,
		HasHighSweepProbability:    true,
		MaxSweepLevels:             w.cfg.MaxSweepLevels,
		MaxVisibleDepthConsumption: &w.cfg.MaxVisibleDepthConsumption,
		MaxDepthAge:                w.cfg.MaxDepthAge,
	}
	params, err := CalculateOrderParametersWithOptions(
		profile,
		depth,
		side,
		w.market,
		w.cfg.MaxOrderNotionalUSDT,
		w.cfg.MaxSlippageBps,
		opts,
	)
	if err != nil {
		w.logger.Warn("Skipping order cycle during parameter calculation", zap.String("market", w.market.MarketID), zap.Error(err))
		metrics.OrdersFailed.WithLabelValues(w.market.MarketID, "parameter_calc_error").Inc()
		return
	}

	// Step 4: Validate Pre-Order Safety Constraints (using ConservativePrice for side-safe notional checks)
	if err := w.safetyMgr.ValidatePreOrder(depth, side, params.Quantity, params.ProtectionPrice, params.ConservativePrice); err != nil {
		w.logger.Warn("Safety guard prevented order submission", zap.String("market", w.market.MarketID), zap.Error(err))
		metrics.OrdersFailed.WithLabelValues(w.market.MarketID, "safety_guard_rejected").Inc()
		return
	}

	// Step 5: Reserve Circuit Breaker Probe and Submit Aggressive Limit Order
	if !w.safetyMgr.ReserveProbe() {
		w.logger.Debug("Circuit breaker probe could not be reserved; skipping cycle", zap.String("market", w.market.MarketID))
		return
	}

	orderUUID, err := uuid.NewV7()
	if err != nil {
		w.logger.Error("Failed to generate order UUIDv7", zap.Error(err))
		w.safetyMgr.ReleaseProbe()
		metrics.OrdersFailed.WithLabelValues(w.market.MarketID, "uuid_gen_error").Inc()
		return
	}
	idempotencyKey := fmt.Sprintf("CTS-%s-%s", w.market.MarketID, orderUUID.String())

	// CTS-1: Final pre-submission depth freshness check (fail-closed)
	// Guarantees that depth is valid, non-zero timestamp, and within MaxDepthAge immediately before submission.
	if depth == nil || depth.SnapshotAt.IsZero() {
		w.logger.Warn("Missing or zero depth timestamp before order submission; skipping cycle",
			zap.String("market", w.market.MarketID),
		)
		w.safetyMgr.ReleaseProbe()
		metrics.OrdersFailed.WithLabelValues(w.market.MarketID, "invalid_depth_pre_submit").Inc()
		return
	}
	depthAge := time.Since(depth.SnapshotAt)
	metrics.DepthAgeSeconds.WithLabelValues(w.market.MarketID, "submit").Observe(depthAge.Seconds())
	if w.cfg.MaxDepthAge > 0 && depthAge > w.cfg.MaxDepthAge {
		w.logger.Warn("Depth became stale before order submission; skipping cycle",
			zap.String("market", w.market.MarketID),
			zap.Duration("age", depthAge),
			zap.Duration("max_age", w.cfg.MaxDepthAge),
		)
		w.safetyMgr.ReleaseProbe()
		metrics.OrdersFailed.WithLabelValues(w.market.MarketID, "stale_depth_pre_submit").Inc()
		return
	}

	startTime := time.Now()
	orderCtx, orderCancel := context.WithTimeout(ctx, 3*time.Second)
	defer orderCancel()

	res, err := w.ordersClient.CreateCrossingOrder(
		orderCtx,
		w.market.MarketID,
		side,
		params.ProtectionPrice.String(),
		params.Quantity.String(),
		idempotencyKey,
	)
	metrics.SubmissionLatencySeconds.WithLabelValues(w.market.MarketID, string(profile)).Observe(time.Since(startTime).Seconds())
	if err != nil {
		errType := classifySubmissionError(ctx, err)
		switch errType {
		case SubmissionErrorDefiniteRejection:
			w.logger.Warn("Order submission rejected deterministically by Order Service; skipping cycle without recovery",
				zap.String("market", w.market.MarketID),
				zap.Error(err),
			)
			w.safetyMgr.ReleaseProbe()
			metrics.OrdersFailed.WithLabelValues(w.market.MarketID, "order_service_rejected").Inc()
			return

		case SubmissionErrorCallerCancelled:
			w.logger.Info("Order submission cancelled during CTS shutdown; checking if order reached Order Service",
				zap.String("market", w.market.MarketID),
				zap.String("idempotency_key", idempotencyKey),
			)
			// Probe Order Service in a detached context to determine whether the order was committed
			checkCtx, checkCancel := context.WithTimeout(context.Background(), 2*time.Second)
			existing, checkErr := w.ordersClient.FindOrderByIdempotencyKey(checkCtx, w.market.MarketID, idempotencyKey)
			checkCancel()

			if checkErr == nil && existing != nil && existing.OrderID != "" {
				w.logger.Info("Order was committed prior to shutdown cancellation; proceeding to residual cleanup",
					zap.String("order_id", existing.OrderID),
					zap.String("idempotency_key", idempotencyKey),
				)
				res = existing
			} else if checkErr == nil && existing == nil {
				// Definite non-existence: the order never reached the database.
				w.logger.Info("Order was not committed to Order Service prior to shutdown; releasing probe cleanly",
					zap.String("market", w.market.MarketID),
					zap.String("idempotency_key", idempotencyKey),
				)
				w.safetyMgr.ReleaseProbe()
				return
			} else {
				// Query failed during shutdown; outcome is genuinely unknown. Fail closed.
				w.logger.Error("Failed to verify order status during shutdown; failing closed",
					zap.String("market", w.market.MarketID),
					zap.String("idempotency_key", idempotencyKey),
					zap.Error(checkErr),
				)
				w.safetyMgr.RecordFailure()
				metrics.OrdersFailed.WithLabelValues(w.market.MarketID, "shutdown_unresolved_timeout").Inc()
				metrics.UnresolvedResiduals.WithLabelValues(w.market.MarketID).Inc()
				return
			}

		case SubmissionErrorAmbiguous:
			w.logger.Warn("Ambiguous order submission failure or timeout; checking idempotency recovery",
				zap.String("market", w.market.MarketID),
				zap.String("idempotency_key", idempotencyKey),
				zap.Error(err),
			)
			// Query Order Service in a detached context for the order
			recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 2*time.Second)
			recoveredRes, recErr := w.ordersClient.FindOrderByIdempotencyKey(recoveryCtx, w.market.MarketID, idempotencyKey)
			recoveryCancel()

			if recErr == nil && recoveredRes != nil && recoveredRes.OrderID != "" {
				w.logger.Info("Successfully recovered existing order after ambiguous submission error",
					zap.String("order_id", recoveredRes.OrderID),
					zap.String("idempotency_key", idempotencyKey),
				)
				res = recoveredRes
			} else if recErr == nil && recoveredRes == nil {
				// The submission failed and was not committed in Order Service.
				w.logger.Warn("Order was not committed to Order Service after ambiguous error; failing closed",
					zap.String("market", w.market.MarketID),
					zap.String("idempotency_key", idempotencyKey),
				)
				w.safetyMgr.RecordFailure()
				w.safetyMgr.ReleaseProbe()
				metrics.OrdersFailed.WithLabelValues(w.market.MarketID, "submit_uncommitted_timeout").Inc()
				return
			} else {
				// If recovery lookup also fails, state is genuinely unknown.
				w.logger.Error("CRITICAL: Failed to lookup ambiguous order status; failing closed",
					zap.String("market", w.market.MarketID),
					zap.String("idempotency_key", idempotencyKey),
					zap.Error(recErr),
				)
				w.safetyMgr.RecordFailure()
				metrics.OrdersFailed.WithLabelValues(w.market.MarketID, "submit_unresolved_timeout").Inc()
				metrics.UnresolvedResiduals.WithLabelValues(w.market.MarketID).Inc()
				return
			}
		}
	}

	metrics.OrdersSubmitted.WithLabelValues(w.market.MarketID, side, string(profile)).Inc()
	if params.Mode == ExecutionSweep {
		metrics.SweepOrders.WithLabelValues(w.market.MarketID, string(profile), side).Inc()
		metrics.SweepLevels.WithLabelValues(w.market.MarketID, string(profile), strconv.Itoa(params.ReachableLevels)).Inc()
	}

	// Step 6: Autonomous Post-Submission Verification & Residual Cleanup (Option A)
	orderState, ok := w.awaitOrderCleanup(res.OrderID)
	if !ok {
		return
	}

	// Step 7: Record Actual Fill & Update Inventory
	// Invariant: ONLY actual filled_quantity is recorded into CTS inventory and volume
	isFullyFilled := strings.HasSuffix(orderState.Status, "FILLED") && !strings.Contains(orderState.Status, "PARTIALLY")
	filledQty, parseErr := decimal.NewFromString(orderState.FilledQty)
	if parseErr != nil {
		filledQty = decimal.Zero
	}

	// Always resolve circuit breaker and conditionally update volume accumulators
	// Uses params.ConservativePrice (PriceCap for BUY, L1.Price for SELL)
	w.safetyMgr.RecordOrderResolved(filledQty, params.ConservativePrice)

	if filledQty.GreaterThan(decimal.Zero) {
		conservativeNotional := filledQty.Mul(params.ConservativePrice)
		w.selector.RecordFill(w.market.MarketID, side, conservativeNotional)

		metrics.ConservativeNotionalVolume.WithLabelValues(w.market.MarketID).Add(conservativeNotional.InexactFloat64())

		if isFullyFilled {
			metrics.OrdersFullyFilled.WithLabelValues(w.market.MarketID).Inc()
		} else {
			metrics.OrdersPartiallyFilled.WithLabelValues(w.market.MarketID).Inc()
		}

		if params.Mode == ExecutionSweep {
			metrics.SweepQuantity.WithLabelValues(w.market.MarketID, string(profile)).Add(filledQty.InexactFloat64())
		}

		w.logger.Info("Controlled taker order fill processed",
			zap.String("market", w.market.MarketID),
			zap.String("order_id", res.OrderID),
			zap.String("side", side),
			zap.String("profile", string(profile)),
			zap.String("mode", params.Mode.String()),
			zap.Int("reachable_levels", params.ReachableLevels),
			zap.String("protection_price", params.ProtectionPrice.String()),
			zap.String("requested_qty", params.Quantity.String()),
			zap.String("filled_qty", filledQty.String()),
			zap.String("status", orderState.Status),
			zap.String("conservative_notional_usdt", conservativeNotional.StringFixed(2)),
		)
	} else {
		metrics.OrdersCancelledUnfilled.WithLabelValues(w.market.MarketID).Inc()
		w.logger.Info("Controlled taker order cancelled with zero fills",
			zap.String("order_id", res.OrderID),
			zap.String("status", orderState.Status),
		)
	}

	// Update adaptive pacing cooldown on terminal resolution if HIGH filled > 0
	if profile == config.ProfileHigh {
		if filledQty.GreaterThan(decimal.Zero) {
			var oppositeLevels []redisdepth.DepthLevel
			if side == "BUY" {
				oppositeLevels = depth.Asks
			} else {
				oppositeLevels = depth.Bids
			}
			severity := DetermineSweepSeverity(filledQty, oppositeLevels)
			sampler := w.postHighCooldownSampler
			if sampler == nil {
				sampler = SamplePostHighCooldown
			}
			w.adaptivePostHighCooldown = sampler(severity, w.rng)
			metrics.PostHighCooldownSeconds.WithLabelValues(w.market.MarketID, severity.String()).Observe(w.adaptivePostHighCooldown.Seconds())

			w.logger.Info("Adaptive post-HIGH cooldown calculated",
				zap.String("market", w.market.MarketID),
				zap.String("severity", severity.String()),
				zap.String("filled_qty", filledQty.String()),
				zap.Duration("cooldown", w.adaptivePostHighCooldown),
			)
		} else {
			// Zero fill: no sweep occurred, no post-HIGH cooldown
			w.adaptivePostHighCooldown = 0
			w.logger.Info("HIGH order zero-fill — skipping post-sweep cooldown",
				zap.String("market", w.market.MarketID),
				zap.String("order_id", res.OrderID),
			)
		}
	}

	metrics.OrderLatency.WithLabelValues(w.market.MarketID, string(profile)).Observe(time.Since(startTime).Seconds())
}

