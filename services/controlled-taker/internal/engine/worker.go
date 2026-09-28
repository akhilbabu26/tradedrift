package engine

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	market       config.MarketConfig
	cfg          config.Config
	ordersClient OrderSubmitter
	redisReader  DepthReader
	safetyMgr    *SafetyManager
	selector     *DirectionSelector
	lastHighAt   time.Time
	logger       *zap.Logger
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
	)

	return &MarketWorker{
		market:       market,
		cfg:          cfg,
		ordersClient: ordersClient,
		redisReader:  redisReader,
		safetyMgr:    safetyMgr,
		selector:     selector,
		logger:       marketLogger,
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

	w.logger.Info("Market worker started active execution loop")

	for {
		// 1. Choose Activity Profile
		profile := w.chooseProfile()

		// 2. Compute Next Bounded Jitter Interval
		var intervalCfg config.IntervalConfig
		switch profile {
		case config.ProfileHigh:
			intervalCfg = w.cfg.HighInterval
		case config.ProfileMid:
			intervalCfg = w.cfg.MidInterval
		default:
			intervalCfg = w.cfg.LowInterval
		}
		nextWait := calculateNextInterval(intervalCfg)

		// 3. Execute Order Cycle
		w.ExecuteCycle(ctx, profile)

		// 4. Wait for Next Interval
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

func (w *MarketWorker) chooseProfile() config.ProfileType {
	// Respect HIGH profile cooldown
	canHigh := time.Since(w.lastHighAt) >= w.cfg.HighCooldown

	r := rand.Float64()
	if canHigh && r < 0.08 {
		return config.ProfileHigh // 8% chance when off cooldown
	} else if r < 0.35 {
		return config.ProfileMid // 27% chance
	}
	return config.ProfileLow // 65% baseline heartbeat
}

// ExecuteCycle executes an autonomous taker order cycle for this market.
func (w *MarketWorker) ExecuteCycle(ctx context.Context, profile config.ProfileType) {
	// Step 1: Select Direction with Balance Bias
	side := w.selector.SelectSide(w.market.MarketID)

	// Step 2: Read Live Depth from Redis
	depthCtx, depthCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	depth, err := w.redisReader.GetDepth(depthCtx, w.market.MarketID)
	depthCancel()

	if err != nil {
		w.logger.Warn("Failed to read live depth from Redis, skipping cycle", zap.Error(err))
		w.safetyMgr.RecordFailure()
		metrics.OrdersFailed.WithLabelValues(w.market.MarketID, "depth_read_error").Inc()
		return
	}

	// Step 3: Compute Quantity Dynamically from CURRENT Live Depth
	params, err := CalculateOrderParameters(
		profile,
		depth,
		side,
		w.market,
		w.cfg.MaxOrderNotionalUSDT,
		w.cfg.MaxSlippageBps,
	)
	if err != nil {
		w.logger.Debug("Skipping order cycle during parameter calculation", zap.String("reason", err.Error()))
		metrics.OrdersFailed.WithLabelValues(w.market.MarketID, "parameter_calc_error").Inc()
		return
	}

	// Step 4: Validate Pre-Order Safety Constraints
	if err := w.safetyMgr.ValidatePreOrder(depth, side, params.Quantity, params.PriceCap); err != nil {
		w.logger.Debug("Safety guard prevented order submission", zap.String("reason", err.Error()))
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

	startTime := time.Now()
	orderCtx, orderCancel := context.WithTimeout(ctx, 3*time.Second)
	defer orderCancel()

	res, err := w.ordersClient.CreateCrossingOrder(
		orderCtx,
		w.market.MarketID,
		side,
		params.PriceCap.String(),
		params.Quantity.String(),
		idempotencyKey,
	)
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

	// Step 6: Autonomous Post-Submission Verification & Residual Cleanup (Option A)
	// CRITICAL INVARIANT: Once CreateCrossingOrder succeeds, the order is active in the matching pipeline.
	// We MUST enter an independent cleanup context detached from worker cancellation (e.g. shutdown)
	// so that CTS guarantees any unfilled remainder is cancelled and never rests as a maker order.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cleanupCancel()

	crossingDelay := w.cfg.CrossingDelay
	if crossingDelay <= 0 {
		crossingDelay = 1000 * time.Millisecond
	}

	// Crossing delay gives the Matching Engine an opportunity to cross against resting MM quotes
	select {
	case <-cleanupCtx.Done():
	case <-time.After(crossingDelay):
	}

	var orderState *orderservice.OrderResult
	var cancelSent bool
	var residualRetries int
	const maxResidualRetries = 3
	deadlineExceeded := false

	// Bounded cleanup polling loop
cleanupLoop:
	for {
		if cleanupCtx.Err() != nil {
			deadlineExceeded = true
			break cleanupLoop
		}

		st, err := w.ordersClient.GetOrder(cleanupCtx, res.OrderID)
		if err != nil {
			w.logger.Warn("Failed to fetch order status during cleanup loop",
				zap.String("order_id", res.OrderID),
				zap.Error(err),
			)
			// On read error, defensively attempt cancellation if not already sent
			if !cancelSent {
				if _, cancelErr := w.ordersClient.CancelOrder(cleanupCtx, res.OrderID); cancelErr == nil {
					cancelSent = true
					metrics.ResidualsCancelled.WithLabelValues(w.market.MarketID).Inc()
				}
			}
			select {
			case <-cleanupCtx.Done():
				deadlineExceeded = true
				break cleanupLoop
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}

		orderState = st

		switch {
		case strings.HasSuffix(orderState.Status, "FILLED") && !strings.Contains(orderState.Status, "PARTIALLY"):
			// FILLED -> fully executed, success terminal state
			break cleanupLoop

		case strings.HasSuffix(orderState.Status, "CANCELLED"):
			// CANCELLED -> terminal state achieved; actual filled quantity will be recorded
			break cleanupLoop

		case strings.HasSuffix(orderState.Status, "CANCELLING"):
			// CANCELLING -> cancel is actively being processed by Order Service / Matching Engine.
			// Mark cancelSent to avoid redundant cancels, and continue polling for terminal state.
			cancelSent = true

		case strings.HasSuffix(orderState.Status, "OPEN"), strings.Contains(orderState.Status, "PARTIALLY"):
			// OPEN or PARTIALLY_FILLED: Provide short context-aware grace window (3 x 200ms)
			// to allow multi-hop asynchronous trade settlement to propagate to Order Service.
			if !cancelSent && residualRetries < maxResidualRetries {
				residualRetries++
				select {
				case <-cleanupCtx.Done():
					deadlineExceeded = true
					break cleanupLoop
				case <-time.After(200 * time.Millisecond):
				}
				continue
			}

			// Residual remains after grace period -> CancelOrder -> continue polling for terminal state
			if !cancelSent {
				w.logger.Info("Order not completely filled after grace period; cancelling residual to prevent maker resting",
					zap.String("order_id", res.OrderID),
					zap.String("status", orderState.Status),
					zap.String("remaining_qty", orderState.RemainingQty),
					zap.Int("grace_retries", residualRetries),
				)
				if _, cancelErr := w.ordersClient.CancelOrder(cleanupCtx, res.OrderID); cancelErr != nil {
					w.logger.Warn("Failed to send CancelOrder for residual; will retry in loop",
						zap.String("order_id", res.OrderID),
						zap.Error(cancelErr),
					)
				} else {
					cancelSent = true
					metrics.ResidualsCancelled.WithLabelValues(w.market.MarketID).Inc()
				}
			}

		default:
			// UNKNOWN / unexpected -> log warning, attempt defensive cancellation, continue bounded verification
			w.logger.Warn("Encountered unexpected or transitional order status during cleanup",
				zap.String("order_id", res.OrderID),
				zap.String("status", orderState.Status),
			)
			if !cancelSent {
				if _, cancelErr := w.ordersClient.CancelOrder(cleanupCtx, res.OrderID); cancelErr == nil {
					cancelSent = true
					metrics.ResidualsCancelled.WithLabelValues(w.market.MarketID).Inc()
				}
			}
		}

		// Wait briefly before next status query in a context-aware manner
		select {
		case <-cleanupCtx.Done():
			deadlineExceeded = true
			break cleanupLoop
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Fail closed if cleanup deadline expired without confirming terminal status
	if deadlineExceeded || orderState == nil || (!strings.HasSuffix(orderState.Status, "FILLED") && !strings.HasSuffix(orderState.Status, "CANCELLED")) {
		w.logger.Error("CRITICAL: order residual could not be verified cancelled before cleanup deadline",
			zap.String("order_id", res.OrderID),
			zap.Any("order_state", orderState),
		)
		metrics.UnresolvedResiduals.WithLabelValues(w.market.MarketID).Inc()
		w.safetyMgr.RecordFailure() // Fail closed: trip breaker
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

		w.logger.Info("Controlled taker order fill processed",
			zap.String("order_id", res.OrderID),
			zap.String("side", side),
			zap.String("profile", string(profile)),
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

	// Update HIGH profile cooldown on any terminal resolution (filled or cancelled)
	// to ensure repeated large sweep attempts are kept deliberately infrequent
	if profile == config.ProfileHigh {
		w.lastHighAt = time.Now()
	}

	metrics.OrderLatency.WithLabelValues(w.market.MarketID, string(profile)).Observe(time.Since(startTime).Seconds())
}

// SubmissionErrorType categorizes errors resulting from Order Service CreateOrder.
type SubmissionErrorType int

const (
	SubmissionErrorDefiniteRejection SubmissionErrorType = iota
	SubmissionErrorCallerCancelled
	SubmissionErrorAmbiguous
)

// classifySubmissionError categorizes an order submission error.
// Design Note: Unknown errors are treated as ambiguous only when recognizable
// transport/network indicators are present; otherwise they fail closed as deterministic failures.
// This centralizes error classification in one place to allow future replacement with typed gRPC errors.
func classifySubmissionError(parentCtx context.Context, err error) SubmissionErrorType {
	if err == nil {
		return SubmissionErrorDefiniteRejection
	}

	// 1. Caller Intentional Cancellation (CTS shutdown initiated)
	// Must strictly depend on CTS's parent context being cancelled.
	if parentCtx.Err() != nil {
		return SubmissionErrorCallerCancelled
	}

	st, ok := status.FromError(err)
	if !ok {
		// Non-status errors are transport, dial, network drops, or raw context errors (ambiguous outcome)
		return SubmissionErrorAmbiguous
	}

	switch st.Code() {
	case codes.InvalidArgument,
		codes.FailedPrecondition,
		codes.NotFound,
		codes.AlreadyExists,
		codes.PermissionDenied,
		codes.Unauthenticated,
		codes.ResourceExhausted:
		return SubmissionErrorDefiniteRejection

	case codes.DeadlineExceeded,
		codes.Unavailable,
		codes.Canceled:
		return SubmissionErrorAmbiguous

	case codes.Unknown:
		// Unknown errors are treated as ambiguous only when recognizable transport/network indicators
		// are present; otherwise they fail closed as deterministic failures.
		errMsg := strings.ToLower(st.Message())
		if strings.Contains(errMsg, "timeout") ||
			strings.Contains(errMsg, "connection") ||
			strings.Contains(errMsg, "deadline") ||
			strings.Contains(errMsg, "transport") ||
			strings.Contains(errMsg, "eof") ||
			strings.Contains(errMsg, "broken pipe") ||
			strings.Contains(errMsg, "reset") {
			return SubmissionErrorAmbiguous
		}
		return SubmissionErrorDefiniteRejection

	default:
		return SubmissionErrorDefiniteRejection
	}
}

func calculateNextInterval(cfg config.IntervalConfig) time.Duration {
	jitterPercent := (rand.Float64() * 0.50) - 0.25 // -25% to +25%
	interval := time.Duration(float64(cfg.BaseInterval) * (1.0 + jitterPercent))

	if interval < cfg.MinInterval {
		return cfg.MinInterval
	}
	if interval > cfg.MaxInterval {
		return cfg.MaxInterval
	}
	return interval
}
