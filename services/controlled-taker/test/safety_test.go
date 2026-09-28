package test

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
	"tradedrift/services/controlled-taker/internal/engine"
)

func TestCircuitBreaker_StateTransitions(t *testing.T) {
	cb := engine.NewCircuitBreaker(3, 50*time.Millisecond, zap.NewNop())

	// Initially CLOSED
	if !cb.Allow() {
		t.Fatalf("expected circuit breaker to be ALLOW initially")
	}

	// 2 failures: still CLOSED
	cb.RecordFailure()
	cb.RecordFailure()
	if !cb.Allow() {
		t.Fatalf("expected circuit breaker to be ALLOW after 2 failures (< 3)")
	}

	// 3rd failure: TRIPPED to OPEN
	cb.RecordFailure()
	if cb.Allow() {
		t.Fatalf("expected circuit breaker to be OPEN (disallow) after 3 failures")
	}

	// Wait for cooldown
	time.Sleep(60 * time.Millisecond)

	// In HALF_OPEN, allows probe check
	if !cb.Allow() {
		t.Fatalf("expected circuit breaker to allow probe order in HALF_OPEN")
	}

	// Dispatch reserves probe
	cb.ReserveProbe()

	// Immediate second call in HALF_OPEN must be blocked (probe is in-flight)
	if cb.Allow() {
		t.Fatalf("expected circuit breaker to block subsequent orders in HALF_OPEN while probe is in flight")
	}

	// Success resets to CLOSED
	cb.RecordSuccess()
	if !cb.Allow() {
		t.Fatalf("expected circuit breaker to return to CLOSED after success")
	}
}

func TestCircuitBreaker_HalfOpenProbeFailureTripsToOpen(t *testing.T) {
	cb := engine.NewCircuitBreaker(2, 40*time.Millisecond, zap.NewNop())
	cb.SetMarketID("BTC-USDT")

	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != "OPEN" {
		t.Fatalf("expected OPEN state, got %s", cb.State())
	}

	time.Sleep(50 * time.Millisecond)

	// First probe allowed
	if !cb.Allow() {
		t.Fatalf("expected probe order allowed in HALF_OPEN")
	}
	if cb.State() != "HALF_OPEN" {
		t.Fatalf("expected HALF_OPEN state, got %s", cb.State())
	}

	cb.ReserveProbe()

	// Probe fails -> trips immediately back to OPEN
	cb.RecordFailure()
	if cb.State() != "OPEN" {
		t.Fatalf("expected OPEN state after probe failure, got %s", cb.State())
	}
	if cb.Allow() {
		t.Fatalf("expected Allow to be false after probe failure")
	}
}

// TestCircuitBreaker_HalfOpenPreOrderRejectionDoesNotStick verifies that if an order
// passes cb.Allow() in HALF_OPEN but is subsequently rejected by a pre-order safety check
// (without dispatching or calling ReserveProbe), the circuit breaker does NOT get stuck.
func TestCircuitBreaker_HalfOpenPreOrderRejectionDoesNotStick(t *testing.T) {
	cb := engine.NewCircuitBreaker(2, 30*time.Millisecond, zap.NewNop())
	cb.SetMarketID("BTC-USDT")

	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != "OPEN" {
		t.Fatalf("expected OPEN, got %s", cb.State())
	}

	time.Sleep(40 * time.Millisecond)

	// Cycle 1: pre-order check runs Allow() -> transitions to HALF_OPEN
	if !cb.Allow() {
		t.Fatalf("expected Allow() to return true when cooldown expired")
	}
	// Simulate: pre-order safety check fails (e.g. spread too wide), cycle exits without ReserveProbe()

	// Cycle 2: next cycle runs Allow() -> MUST STILL ALLOW PROBE (not stuck!)
	if !cb.Allow() {
		t.Fatalf("expected circuit breaker to NOT be stuck in HALF_OPEN after pre-order rejection")
	}
}

// TestCircuitBreaker_HalfOpenZeroFill_TransitionsToClosed verifies that when a probe order
// in HALF_OPEN results in zero fills and is cleanly cancelled, RecordOrderResolved resolves
// the circuit breaker to CLOSED without permanently sticking in HALF_OPEN.
func TestCircuitBreaker_HalfOpenZeroFill_TransitionsToClosed(t *testing.T) {
	cb := engine.NewCircuitBreaker(2, 30*time.Millisecond, zap.NewNop())
	sm := engine.NewSafetyManager(
		cb,
		decimal.NewFromInt(1000000),
		decimal.NewFromInt(5000000),
		1000,
		decimal.NewFromFloat(0.01),
	)

	// Trip to OPEN
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != "OPEN" {
		t.Fatalf("expected OPEN, got %s", cb.State())
	}

	time.Sleep(40 * time.Millisecond)

	// Probe starts in HALF_OPEN
	if !cb.Allow() {
		t.Fatalf("expected Allow() to return true when cooldown expired")
	}
	cb.ReserveProbe()

	// Order resolves with 0 fills (clean cancellation)
	sm.RecordOrderResolved(decimal.Zero, decimal.NewFromInt(96500))

	// Circuit breaker must have transitioned to CLOSED, probeInFlight must be false
	if cb.State() != "CLOSED" {
		t.Fatalf("expected CLOSED after clean zero-fill resolution, got %s", cb.State())
	}
	if !cb.Allow() {
		t.Fatalf("expected Allow() to return true after returning to CLOSED")
	}
	if !sm.HourlyNotional().IsZero() {
		t.Fatalf("expected zero hourly notional for 0 fills, got %s", sm.HourlyNotional())
	}
}

func TestSafetyManager_CumulativeDepthGuard(t *testing.T) {
	cb := engine.NewCircuitBreaker(5, time.Minute, zap.NewNop())
	sm := engine.NewSafetyManager(
		cb,
		decimal.NewFromInt(1000000), // Max hourly notional
		decimal.NewFromInt(5000000), // Max daily notional
		1000,                        // Max trades per hour
		decimal.NewFromFloat(0.01),  // Max spread 1%
	)

	// Book with 0.10 BTC at 96,500 Ask, 0.10 BTC at 96,490 Bid
	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96490), Quantity: decimal.NewFromFloat(0.10)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.10)},
		},
	}

	// Case 1: Target Qty 0.05 BTC.
	// Required 1.5x depth = 0.05 * 1.5 = 0.075 BTC.
	// Available = 0.10 BTC. Should PASS.
	err := sm.ValidatePreOrder(depth, "BUY", decimal.NewFromFloat(0.05), decimal.NewFromInt(96500))
	if err != nil {
		t.Errorf("expected pre-order validation to pass, got: %v", err)
	}

	// Case 2: Target Qty 0.08 BTC.
	// Required 1.5x depth = 0.08 * 1.5 = 0.12 BTC.
	// Available = 0.10 BTC. Should FAIL with ErrCumulativeDepthRatio.
	err = sm.ValidatePreOrder(depth, "BUY", decimal.NewFromFloat(0.08), decimal.NewFromInt(96500))
	if err == nil {
		t.Fatalf("expected ErrCumulativeDepthRatio, got nil")
	}
}

func TestSafetyManager_SpreadAndVolumeLimits(t *testing.T) {
	cb := engine.NewCircuitBreaker(5, time.Minute, zap.NewNop())
	sm := engine.NewSafetyManager(
		cb,
		decimal.NewFromInt(10000),   // Max hourly notional $10,000
		decimal.NewFromInt(50000),   // Max daily notional $50,000
		2,                           // Max trades per hour: 2
		decimal.NewFromFloat(0.005), // Max spread 0.5%
	)

	// Wide spread book: Bid 90,000, Ask 100,000 (~10.5% spread)
	wideDepth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(90000), Quantity: decimal.NewFromFloat(1.0)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(100000), Quantity: decimal.NewFromFloat(1.0)},
		},
	}

	err := sm.ValidatePreOrder(wideDepth, "BUY", decimal.NewFromFloat(0.01), decimal.NewFromInt(100000))
	if err == nil {
		t.Fatalf("expected wide spread to be rejected, got nil")
	}

	// Normal spread book: Bid 96,490, Ask 96,500
	normalDepth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96490), Quantity: decimal.NewFromFloat(1.0)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(1.0)},
		},
	}

	// Order 1: $4,000 notional (passes)
	err = sm.ValidatePreOrder(normalDepth, "BUY", decimal.NewFromFloat(0.04), decimal.NewFromInt(96500))
	if err != nil {
		t.Fatalf("expected normal order 1 to pass: %v", err)
	}
	sm.RecordOrderExecution(decimal.NewFromFloat(0.04).Mul(decimal.NewFromInt(96500)))

	// Order 2: $4,000 notional (passes, total $8,000)
	err = sm.ValidatePreOrder(normalDepth, "BUY", decimal.NewFromFloat(0.04), decimal.NewFromInt(96500))
	if err != nil {
		t.Fatalf("expected normal order 2 to pass: %v", err)
	}
	sm.RecordOrderExecution(decimal.NewFromFloat(0.04).Mul(decimal.NewFromInt(96500)))

	// Order 3: Exceeds max 2 trades per hour
	err = sm.ValidatePreOrder(normalDepth, "BUY", decimal.NewFromFloat(0.01), decimal.NewFromInt(96500))
	if !errors.Is(err, engine.ErrTradesPerHourExceeded) {
		t.Fatalf("expected ErrTradesPerHourExceeded, got: %v", err)
	}
}

// TestCircuitBreaker_ConcurrencyStress spins up 50 concurrent goroutines rapidly reserving,
// releasing, and transitioning circuit breaker state to verify thread-safety and absence of deadlocks.
func TestCircuitBreaker_ConcurrencyStress(t *testing.T) {
	cb := engine.NewCircuitBreaker(5, 10*time.Millisecond, zap.NewNop())
	cb.SetMarketID("BTC-USDT")

	const goroutines = 50
	const iterations = 100

	done := make(chan struct{})
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < iterations; i++ {
				if id%4 == 0 {
					if cb.ReserveProbe() {
						cb.ReleaseProbe()
					}
				} else if id%4 == 1 {
					cb.RecordFailure()
				} else if id%4 == 2 {
					cb.RecordSuccess()
				} else {
					_ = cb.Allow()
					_ = cb.State()
				}
			}
		}(g)
	}

	for g := 0; g < goroutines; g++ {
		<-done
	}

	// Breaker must be in a valid known state
	state := cb.State()
	if state != "CLOSED" && state != "OPEN" && state != "HALF_OPEN" {
		t.Fatalf("unexpected circuit breaker state after concurrency stress: %s", state)
	}
}
