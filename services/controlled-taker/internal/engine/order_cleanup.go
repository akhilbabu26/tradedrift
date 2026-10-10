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

// awaitOrderCleanup handles post-submission verification for MARKET IOC orders.
// For MARKET orders, the Matching Engine executes immediately and expires any unfilled
// remainder as ioc_expired. This function polls briefly (~2.0s normal target) for Order Service
// to reflect the terminal status (FILLED or CANCELLED) and retrieves the final filled quantity.
// If unresolved past ResidualGracePeriod (default 4.0s), a defensive cancel is issued.
// If unresolved past ResidualDeadline (default 8.0s), verification fails closed.
// Returns the resolved terminal OrderResult and true on success, or nil and false if verification fails closed.
func (w *MarketWorker) awaitOrderCleanup(orderID string) (*orderservice.OrderResult, bool) {
	gracePeriod := w.cfg.ResidualGracePeriod
	if gracePeriod <= 0 {
		gracePeriod = 4 * time.Second
	}
	timeout := w.cfg.ResidualDeadline
	if timeout <= gracePeriod {
		timeout = 2 * gracePeriod
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), timeout)
	defer cleanupCancel()

	crossingDelay := w.cfg.CrossingDelay
	if crossingDelay <= 0 || crossingDelay > 500*time.Millisecond {
		crossingDelay = 100 * time.Millisecond
	}

	// Brief delay for the Matching Engine & Trade settlement pipeline to propagate
	select {
	case <-cleanupCtx.Done():
	case <-time.After(crossingDelay):
	}

	var orderState *orderservice.OrderResult
	var cancelSent bool
	var consecutiveReadErrors int
	deadlineExceeded := false

	pollInterval := w.cfg.ResidualPollInterval
	if pollInterval <= 0 || pollInterval > 200*time.Millisecond {
		pollInterval = 100 * time.Millisecond
	}

	startTime := time.Now()

	// Bounded cleanup polling loop
cleanupLoop:
	for {
		if cleanupCtx.Err() != nil || time.Since(startTime) >= timeout {
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
			select {
			case <-cleanupCtx.Done():
				deadlineExceeded = true
				break cleanupLoop
			case <-time.After(50 * time.Millisecond):
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
			// CANCELLED (e.g. ioc_expired) / REJECTED -> terminal state achieved
			break cleanupLoop

		case strings.HasSuffix(orderState.Status, "CANCELLING"):
			// CANCELLING -> cancel is actively being processed by Order Service / Matching Engine.
			cancelSent = true

		case strings.HasSuffix(orderState.Status, "OPEN"), strings.Contains(orderState.Status, "PARTIALLY"):
			// Order is still in-flight through outbox -> Kafka -> ME -> DB pipeline.
			// Since MARKET orders are IOC, ME automatically executes and expires remainder.
			// Only as a defensive safety fallback when exceeding gracePeriod do we issue CancelOrder.
			if time.Since(startTime) > gracePeriod && !cancelSent {
				w.logger.Info("MARKET order still unresolved after grace period; defensively issuing cancel",
					zap.String("order_id", orderID),
					zap.String("status", orderState.Status),
					zap.Duration("grace_period", gracePeriod),
				)
				if _, cancelErr := w.ordersClient.CancelOrder(cleanupCtx, orderID); cancelErr != nil {
					if isNotCancellableError(cancelErr) {
						cancelSent = true
					}
				} else {
					cancelSent = true
					metrics.ResidualsCancelled.WithLabelValues(w.market.MarketID).Inc()
				}
			}

		default:
			w.logger.Debug("Encountered transitional order status during cleanup",
				zap.String("order_id", orderID),
				zap.String("status", orderState.Status),
			)
		}

		select {
		case <-cleanupCtx.Done():
			deadlineExceeded = true
			break cleanupLoop
		case <-time.After(pollInterval):
		}
	}

	// Drain remaining budget only if genuinely available before the hard deadline (ResidualDeadline)
	// and the order was actively in-flight cancellation.
	// If the deadline has already expired, fail closed immediately — do NOT extend the deadline.
	remainingBudget := timeout - time.Since(startTime)
	if !deadlineExceeded && remainingBudget > 0 && cleanupCtx.Err() == nil && (orderState != nil && strings.HasSuffix(orderState.Status, "CANCELLING")) && cancelSent {
		drainCtx, drainCancel := context.WithTimeout(cleanupCtx, remainingBudget)
		defer drainCancel()
		for drainCtx.Err() == nil {
			st, err := w.ordersClient.GetOrder(drainCtx, orderID)
			if err == nil && st != nil {
				orderState = st
				if strings.HasSuffix(st.Status, "FILLED") || strings.HasSuffix(st.Status, "CANCELLED") || strings.HasSuffix(st.Status, "REJECTED") {
					break
				}
			}
			select {
			case <-drainCtx.Done():
				deadlineExceeded = true
			case <-time.After(pollInterval):
			}
		}
	}

	// Fail closed if cleanup deadline expired without confirming terminal status
	isTerminal := orderState != nil && (strings.HasSuffix(orderState.Status, "FILLED") ||
		strings.HasSuffix(orderState.Status, "CANCELLED") ||
		strings.HasSuffix(orderState.Status, "REJECTED"))

	if deadlineExceeded || cleanupCtx.Err() != nil || time.Since(startTime) >= timeout || !isTerminal {
		w.logger.Error("CRITICAL: MARKET order could not be verified in terminal status before cleanup deadline",
			zap.String("order_id", orderID),
			zap.Any("order_state", orderState),
			zap.Duration("elapsed", time.Since(startTime)),
			zap.Duration("deadline", timeout),
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
