package engine

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"tradedrift/services/controlled-taker/internal/clients/orderservice"
	"tradedrift/services/controlled-taker/internal/metrics"
)

// awaitOrderCleanup handles post-submission verification and autonomous cancellation of unfilled remainder.
// CRITICAL INVARIANT: Once CreateCrossingOrder succeeds, the order is active in the matching pipeline.
// We MUST enter an independent cleanup context detached from worker cancellation (e.g. shutdown)
// so that CTS guarantees any unfilled remainder is cancelled and never rests as a maker order.
// Returns the resolved terminal OrderResult and true on success, or nil and false if verification fails closed.
func (w *MarketWorker) awaitOrderCleanup(orderID string) (*orderservice.OrderResult, bool) {
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cleanupCancel()

	crossingDelay := w.cfg.CrossingDelay
	if crossingDelay <= 0 {
		crossingDelay = 300 * time.Millisecond
	}

	// Crossing delay gives the Matching Engine an opportunity to cross against resting MM quotes
	select {
	case <-cleanupCtx.Done():
	case <-time.After(crossingDelay):
	}

	var orderState *orderservice.OrderResult
	var cancelSent bool
	var consecutiveReadErrors int
	var graceDeadline time.Time
	deadlineExceeded := false

	pollInterval := w.cfg.ResidualPollInterval
	if pollInterval <= 0 {
		pollInterval = 100 * time.Millisecond
	}
	gracePeriod := w.cfg.ResidualGracePeriod
	if gracePeriod <= 0 {
		gracePeriod = 500 * time.Millisecond
	}

	// Bounded cleanup polling loop
cleanupLoop:
	for {
		if cleanupCtx.Err() != nil {
			deadlineExceeded = true
			break cleanupLoop
		}

		st, err := w.ordersClient.GetOrder(cleanupCtx, orderID)
		if err != nil {
			consecutiveReadErrors++
			w.logger.Warn("Failed to fetch order status during cleanup loop",
				zap.String("order_id", orderID),
				zap.Int("consecutive_read_errors", consecutiveReadErrors),
				zap.Error(err),
			)
			// INVARIANT: Never cancel an order solely because GetOrder is failing.
			// Read errors control retry polling only; if errors persist until cleanupCtx deadline,
			// the loop exits and fails closed (tripping circuit breaker) without unsafe premature cancellation.
			select {
			case <-cleanupCtx.Done():
				deadlineExceeded = true
				break cleanupLoop
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}

		consecutiveReadErrors = 0
		orderState = st

		switch {
		case strings.HasSuffix(orderState.Status, "FILLED") && !strings.Contains(orderState.Status, "PARTIALLY"):
			// FILLED -> fully executed, success terminal state
			break cleanupLoop

		case strings.HasSuffix(orderState.Status, "CANCELLED"), strings.HasSuffix(orderState.Status, "REJECTED"):
			// CANCELLED / REJECTED -> terminal state achieved; do not attempt cancellation again
			break cleanupLoop

		case strings.HasSuffix(orderState.Status, "CANCELLING"):
			// CANCELLING -> cancel is actively being processed by Order Service / Matching Engine.
			// Mark cancelSent to avoid redundant cancels, and continue polling for terminal state.
			cancelSent = true

		case strings.HasSuffix(orderState.Status, "OPEN"), strings.Contains(orderState.Status, "PARTIALLY"):
			// Authoritative observation that order remains OPEN or PARTIALLY_FILLED.
			if graceDeadline.IsZero() {
				graceDeadline = time.Now().Add(gracePeriod)
			}

			if time.Now().Before(graceDeadline) {
				// Within grace period: allow multi-hop asynchronous trade settlement to propagate to Order Service.
				select {
				case <-cleanupCtx.Done():
					deadlineExceeded = true
					break cleanupLoop
				case <-time.After(pollInterval):
				}
				continue
			}

			// Grace deadline reached + order authoritatively confirmed still OPEN / PARTIALLY_FILLED
			if !cancelSent {
				w.logger.Info("Order not completely filled after grace period; cancelling residual to prevent maker resting",
					zap.String("order_id", orderID),
					zap.String("status", orderState.Status),
					zap.String("remaining_qty", orderState.RemainingQty),
					zap.Duration("grace_period", gracePeriod),
				)
				if _, cancelErr := w.ordersClient.CancelOrder(cleanupCtx, orderID); cancelErr != nil {
					if isNotCancellableError(cancelErr) {
						w.logger.Info("Order no longer in cancellable state during residual cleanup (likely filled concurrently)",
							zap.String("order_id", orderID),
						)
						cancelSent = true
					} else {
						w.logger.Warn("Failed to send CancelOrder for residual; will retry in loop",
							zap.String("order_id", orderID),
							zap.Error(cancelErr),
						)
					}
				} else {
					cancelSent = true
					metrics.ResidualsCancelled.WithLabelValues(w.market.MarketID).Inc()
				}
			}

		default:
			// UNKNOWN / unexpected -> log warning, do NOT cancel blindly without authoritative state
			w.logger.Warn("Encountered unexpected or transitional order status during cleanup",
				zap.String("order_id", orderID),
				zap.String("status", orderState.Status),
			)
		}

		// Wait briefly before next status query in a context-aware manner
		select {
		case <-cleanupCtx.Done():
			deadlineExceeded = true
			break cleanupLoop
		case <-time.After(pollInterval):
		}
	}

	// Fail closed if cleanup deadline expired without confirming terminal status
	if deadlineExceeded || orderState == nil || (!strings.HasSuffix(orderState.Status, "FILLED") && !strings.HasSuffix(orderState.Status, "CANCELLED")) {
		w.logger.Error("CRITICAL: order residual could not be verified cancelled before cleanup deadline",
			zap.String("order_id", orderID),
			zap.Any("order_state", orderState),
		)
		metrics.UnresolvedResiduals.WithLabelValues(w.market.MarketID).Inc()
		w.safetyMgr.RecordFailure() // Fail closed: trip breaker
		return nil, false
	}

	return orderState, true
}

func isNotCancellableError(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		type statusError interface {
			GRPCStatus() *status.Status
		}
		var se statusError
		if errors.As(err, &se) {
			st = se.GRPCStatus()
			ok = true
		}
	}
	if ok && st.Code() == codes.FailedPrecondition {
		return true
	}
	return strings.Contains(err.Error(), "not in a cancellable state")
}
