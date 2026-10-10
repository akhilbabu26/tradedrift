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
	ErrCumulativeDepthRatio  = errors.New("available reachable depth does not meet the visible depth consumption ceiling")
)

// SafetyManager tracks rolling volumes and validates order safety constraints.
type SafetyManager struct {
	mu                sync.Mutex
	circuitBreaker    *CircuitBreaker
	maxHourlyNotional decimal.Decimal
	maxDailyNotional  decimal.Decimal
	maxTradesPerHour  int
	maxSpreadPercent    decimal.Decimal
	maxDepthConsumption decimal.Decimal

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
	maxDepthConsumption ...decimal.Decimal,
) *SafetyManager {
	now := time.Now().UTC()
	depthConsumption := decimal.NewFromFloat(0.90)
	if len(maxDepthConsumption) > 0 && maxDepthConsumption[0].GreaterThan(decimal.Zero) {
		depthConsumption = maxDepthConsumption[0]
	}
	return &SafetyManager{
		circuitBreaker:      cb,
		maxHourlyNotional:   maxHourlyNotional,
		maxDailyNotional:    maxDailyNotional,
		maxTradesPerHour:    maxTradesPerHour,
		maxSpreadPercent:    maxSpreadPercent,
		maxDepthConsumption: depthConsumption,
		hourWindowStart:     now,
		dayWindowStart:      now,
	}
}

// ValidatePreOrder validates spread, depth ratio, volume caps, and circuit breaker before submission.
// conservativePrice is the upper-bound conservative price (PriceCap for BUY, L1.Price for SELL).
func (sm *SafetyManager) ValidatePreOrder(
	depth *redisdepth.DepthSnapshot,
	side string,
	targetQty decimal.Decimal,
	priceCap decimal.Decimal,
	conservativePrice decimal.Decimal,
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

	// Invariant: Use ConservativePrice for notional exposure.
	// BUY: PriceCap (highest ask reachable)
	// SELL: L1.Price (highest bid reachable, since swept bids are lower)
	// If conservativePrice is zero or not provided, fallback conservatively to priceCap or L1.
	evalPrice := conservativePrice
	if !evalPrice.GreaterThan(decimal.Zero) {
		evalPrice = priceCap
		if side == "SELL" && len(depth.Bids) > 0 && depth.Bids[0].Price.GreaterThan(evalPrice) {
			evalPrice = depth.Bids[0].Price
		}
	}

	notional := targetQty.Mul(evalPrice)
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
	// Verify that targetQty does not exceed maxDepthConsumption of reachable depth.
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

	maxAllowedQty := reachableDepth.Mul(sm.maxDepthConsumption)
	if targetQty.GreaterThan(maxAllowedQty) {
		return fmt.Errorf("%w: target %s > max allowed %s (depth %s * %s)", ErrCumulativeDepthRatio, targetQty, maxAllowedQty, reachableDepth, sm.maxDepthConsumption)
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
