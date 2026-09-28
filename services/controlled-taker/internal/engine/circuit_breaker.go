package engine

import (
	"sync"
	"time"

	"go.uber.org/zap"
	"tradedrift/services/controlled-taker/internal/metrics"
)

// CircuitBreaker manages fail-closed state after consecutive errors.
type CircuitBreaker struct {
	mu            sync.Mutex
	marketID      string
	failures      int
	maxFailures   int
	state         string // "CLOSED", "OPEN", "HALF_OPEN"
	trippedAt     time.Time
	cooldown      time.Duration
	probeInFlight bool
	logger        *zap.Logger
}

// NewCircuitBreaker creates a new circuit breaker with the given failure threshold and cooldown.
func NewCircuitBreaker(maxFailures int, cooldown time.Duration, logger *zap.Logger) *CircuitBreaker {
	return &CircuitBreaker{
		maxFailures: maxFailures,
		cooldown:    cooldown,
		state:       "CLOSED",
		logger:      logger,
	}
}

// SetMarketID associates a market ID with the circuit breaker for logging and metrics.
func (cb *CircuitBreaker) SetMarketID(marketID string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.marketID = marketID
}

// State returns the current circuit breaker state.
func (cb *CircuitBreaker) State() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// ProbeInFlight reports whether a probe order is currently outstanding in HALF_OPEN state.
func (cb *CircuitBreaker) ProbeInFlight() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.probeInFlight
}

// Allow reports whether a new operation is permitted through the circuit breaker.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == "CLOSED" {
		return true
	}

	if cb.state == "OPEN" {
		if time.Since(cb.trippedAt) > cb.cooldown {
			cb.state = "HALF_OPEN"
			cb.probeInFlight = false
			cb.logger.Info("Circuit breaker entering HALF_OPEN state; ready for probe order",
				zap.String("market", cb.marketID),
			)
			return true
		}
		return false
	}

	// HALF_OPEN: allow only if no probe is currently in flight
	return !cb.probeInFlight
}

// ReserveProbe atomically verifies and locks the circuit breaker probe order before dispatch.
// Returns true if dispatch is permitted (CLOSED state, or HALF_OPEN with probe successfully reserved).
// Returns false if the breaker is OPEN, or if another probe is already in flight in HALF_OPEN.
func (cb *CircuitBreaker) ReserveProbe() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case "CLOSED":
		return true
	case "HALF_OPEN":
		if cb.probeInFlight {
			return false
		}
		cb.probeInFlight = true
		cb.logger.Info("Circuit breaker probe reserved for order dispatch",
			zap.String("market", cb.marketID),
		)
		return true
	default: // OPEN
		return false
	}
}

// ReleaseProbe resets the probe-in-flight lock if dispatch is aborted before an order is sent.
func (cb *CircuitBreaker) ReleaseProbe() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == "HALF_OPEN" {
		cb.probeInFlight = false
	}
}

// RecordSuccess resets the consecutive failure counter and restores the breaker to CLOSED state.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures = 0
	cb.state = "CLOSED"
	cb.probeInFlight = false
}

// RecordFailure increments consecutive failures and trips the breaker to OPEN if the threshold is met.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	if cb.state == "HALF_OPEN" {
		cb.state = "OPEN"
		cb.trippedAt = time.Now()
		cb.probeInFlight = false
		cb.logger.Error("Circuit breaker probe failed in HALF_OPEN state; tripping back to OPEN",
			zap.String("market", cb.marketID),
			zap.Int("failures", cb.failures),
			zap.Duration("cooldown", cb.cooldown),
		)
		if cb.marketID != "" {
			metrics.CircuitBreakerTripped.WithLabelValues(cb.marketID).Inc()
		}
		return
	}

	if cb.failures >= cb.maxFailures && cb.state != "OPEN" {
		cb.state = "OPEN"
		cb.trippedAt = time.Now()
		cb.probeInFlight = false
		cb.logger.Error("Circuit breaker TRIPPED to OPEN state",
			zap.String("market", cb.marketID),
			zap.Int("failures", cb.failures),
			zap.Duration("cooldown", cb.cooldown),
		)
		if cb.marketID != "" {
			metrics.CircuitBreakerTripped.WithLabelValues(cb.marketID).Inc()
		}
	}
}
