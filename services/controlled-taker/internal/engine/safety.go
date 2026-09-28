package engine

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
)

var (
	ErrCircuitBreakerTripped = errors.New("circuit breaker is open (too many consecutive errors)")
	ErrHourlyVolumeExceeded  = errors.New("hourly notional volume limit exceeded")
	ErrDailyVolumeExceeded   = errors.New("daily notional volume limit exceeded")
	ErrTradesPerHourExceeded = errors.New("maximum trades per hour exceeded")
	ErrSpreadTooWide         = errors.New("market bid/ask spread exceeds maximum allowable threshold")
	ErrCumulativeDepthRatio  = errors.New("available reachable depth does not meet the 1.5x safety multiplier")
)

// SafetyManager tracks rolling volumes and validates order safety constraints.
type SafetyManager struct {
	mu                sync.Mutex
	circuitBreaker    *CircuitBreaker
	maxHourlyNotional decimal.Decimal
	maxDailyNotional  decimal.Decimal
	maxTradesPerHour  int
	maxSpreadPercent  decimal.Decimal

	hourlyNotional  decimal.Decimal
	dailyNotional   decimal.Decimal
	hourlyTrades    int
	hourWindowStart time.Time
	dayWindowStart  time.Time
}

func NewSafetyManager(
	cb *CircuitBreaker,
	maxHourlyNotional, maxDailyNotional decimal.Decimal,
	maxTradesPerHour int,
	maxSpreadPercent decimal.Decimal,
) *SafetyManager {
	now := time.Now().UTC()
	return &SafetyManager{
		circuitBreaker:    cb,
		maxHourlyNotional: maxHourlyNotional,
		maxDailyNotional:  maxDailyNotional,
		maxTradesPerHour:  maxTradesPerHour,
		maxSpreadPercent:  maxSpreadPercent,
		hourWindowStart:   now,
		dayWindowStart:    now,
	}
}

// ValidatePreOrder validates spread, depth ratio, volume caps, and circuit breaker before submission.
func (sm *SafetyManager) ValidatePreOrder(
	depth *redisdepth.DepthSnapshot,
	side string,
	targetQty decimal.Decimal,
	priceCap decimal.Decimal,
) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// 1. Circuit Breaker Check
	if !sm.circuitBreaker.Allow() {
		return ErrCircuitBreakerTripped
	}

	// 2. Refresh Rolling Windows
	now := time.Now().UTC()
	if now.Sub(sm.hourWindowStart) >= time.Hour {
		sm.hourlyNotional = decimal.Zero
		sm.hourlyTrades = 0
		sm.hourWindowStart = now
	}
	if now.Sub(sm.dayWindowStart) >= 24*time.Hour {
		sm.dailyNotional = decimal.Zero
		sm.dayWindowStart = now
	}

	// 3. Rate & Volume Limit Checks
	if sm.hourlyTrades >= sm.maxTradesPerHour {
		return ErrTradesPerHourExceeded
	}

	notional := targetQty.Mul(priceCap)
	if sm.hourlyNotional.Add(notional).GreaterThan(sm.maxHourlyNotional) {
		return ErrHourlyVolumeExceeded
	}
	if sm.dailyNotional.Add(notional).GreaterThan(sm.maxDailyNotional) {
		return ErrDailyVolumeExceeded
	}

	// 4. Spread Check
	if len(depth.Bids) == 0 || len(depth.Asks) == 0 {
		return errors.New("incomplete book depth")
	}
	bestBid := depth.Bids[0].Price
	bestAsk := depth.Asks[0].Price
	if !bestAsk.GreaterThan(bestBid) {
		return errors.New("crossed or locked book")
	}

	spread := bestAsk.Sub(bestBid)
	midPrice := bestBid.Add(bestAsk).Div(decimal.NewFromInt(2))
	if midPrice.GreaterThan(decimal.Zero) {
		spreadPct := spread.Div(midPrice)
		if spreadPct.GreaterThan(sm.maxSpreadPercent) {
			return fmt.Errorf("%w: spread %s%% > max %s%%", ErrSpreadTooWide, spreadPct.Mul(decimal.NewFromInt(100)).StringFixed(2), sm.maxSpreadPercent.Mul(decimal.NewFromInt(100)).StringFixed(2))
		}
	}

	// 5. Cumulative Depth Guard:
	// Verify that cumulative reachable depth within the price cap is at least 1.5x targetQty
	var reachableDepth decimal.Decimal
	if side == "BUY" {
		for _, ask := range depth.Asks {
			if ask.Price.LessThanOrEqual(priceCap) {
				reachableDepth = reachableDepth.Add(ask.Quantity)
			}
		}
	} else {
		for _, bid := range depth.Bids {
			if bid.Price.GreaterThanOrEqual(priceCap) {
				reachableDepth = reachableDepth.Add(bid.Quantity)
			}
		}
	}

	requiredDepth := targetQty.Mul(decimal.NewFromFloat(1.5))
	if reachableDepth.LessThan(requiredDepth) {
		return fmt.Errorf("%w: reachable %s < required 1.5x %s", ErrCumulativeDepthRatio, reachableDepth, requiredDepth)
	}

	return nil
}

// RecordOrderResolved updates volume/trade accumulators if filledQty > 0 and
// always transitions the circuit breaker probe to healthy CLOSED state, ensuring
// zero-fill cancellations in HALF_OPEN do not permanently trap the breaker.
func (sm *SafetyManager) RecordOrderResolved(filledQty, conservativePrice decimal.Decimal) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if filledQty.GreaterThan(decimal.Zero) {
		notional := filledQty.Mul(conservativePrice)
		sm.hourlyNotional = sm.hourlyNotional.Add(notional)
		sm.dailyNotional = sm.dailyNotional.Add(notional)
		sm.hourlyTrades++
	}
	sm.circuitBreaker.RecordSuccess()
}

// RecordOrderExecution updates the volume accumulators after a successful order.
func (sm *SafetyManager) RecordOrderExecution(notional decimal.Decimal) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.hourlyNotional = sm.hourlyNotional.Add(notional)
	sm.dailyNotional = sm.dailyNotional.Add(notional)
	sm.hourlyTrades++
	sm.circuitBreaker.RecordSuccess()
}

// RecordFailure signals a failed order cycle to the circuit breaker.
func (sm *SafetyManager) RecordFailure() {
	sm.circuitBreaker.RecordFailure()
}

// ReserveProbe atomically locks the circuit breaker probe immediately before order dispatch.
func (sm *SafetyManager) ReserveProbe() bool {
	return sm.circuitBreaker.ReserveProbe()
}

// ReleaseProbe unlocks the circuit breaker probe if dispatch is cancelled before submission.
func (sm *SafetyManager) ReleaseProbe() {
	sm.circuitBreaker.ReleaseProbe()
}

// CircuitBreaker returns the underlying circuit breaker instance.
func (sm *SafetyManager) CircuitBreaker() *CircuitBreaker {
	return sm.circuitBreaker
}

// HourlyNotional returns the current accumulated hourly notional volume.
func (sm *SafetyManager) HourlyNotional() decimal.Decimal {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.hourlyNotional
}
