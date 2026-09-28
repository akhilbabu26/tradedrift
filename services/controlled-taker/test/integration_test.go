package test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"tradedrift/services/controlled-taker/internal/account"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
	"tradedrift/services/controlled-taker/internal/config"
	"tradedrift/services/controlled-taker/internal/engine"
)

// TestWorker_OrderLifecycleIntegration tests that the worker executes an order cycle cleanly
// and obeys the identity, idempotency key, and dynamic sizing invariants.
func TestWorker_OrderLifecycleIntegration(t *testing.T) {
	logger := zap.NewNop()
	cfg := testDefaultConfig()
	market := sampleMarketConfig()

	// 0.50 BTC on Asks and Bids
	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96490), Quantity: decimal.NewFromFloat(0.50)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.50)},
		},
	}

	depthReader := &mockDepthReader{snapshot: depth}
	orderSubmitter := &mockOrderSubmitter{}
	selector := engine.NewDirectionSelector()

	worker := engine.NewMarketWorker(market, cfg, orderSubmitter, depthReader, selector, logger)

	// Execute a single cycle
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	worker.ExecuteCycle(ctx, config.ProfileLow)

	orderSubmitter.mu.Lock()
	defer orderSubmitter.mu.Unlock()

	if len(orderSubmitter.orders) != 1 {
		t.Fatalf("expected 1 order submitted, got %d", len(orderSubmitter.orders))
	}

	order := orderSubmitter.orders[0]

	// Invariant: Market ID must match
	if order.MarketID != "BTC-USDT" {
		t.Errorf("expected MarketID BTC-USDT, got %s", order.MarketID)
	}

	// Invariant: Idempotency key starts with CTS-BTC-USDT-
	if !strings.HasPrefix(order.IdempotencyKey, "CTS-BTC-USDT-") {
		t.Errorf("expected IdempotencyKey prefix 'CTS-BTC-USDT-', got %s", order.IdempotencyKey)
	}

	// Invariant: Dynamic quantity from 0.50 BTC L1 (5%-15% is [0.0250, 0.0750])
	qty, err := decimal.NewFromString(order.Quantity)
	if err != nil {
		t.Fatalf("invalid quantity decimal: %v", err)
	}
	if qty.LessThan(decimal.NewFromFloat(0.025)) || qty.GreaterThan(decimal.NewFromFloat(0.075)) {
		t.Errorf("expected order quantity in [0.025, 0.075], got %s", qty)
	}

	// Invariant: Identity check
	if account.WalletUUIDStr != "00000000-0000-0000-0000-000000000002" {
		t.Errorf("expected CT-001 UUID, got %s", account.WalletUUIDStr)
	}
}

// TestWorker_CircuitBreakerFailClosedIntegration tests that consecutive failures trip the circuit breaker.
func TestWorker_CircuitBreakerFailClosedIntegration(t *testing.T) {
	logger := zap.NewNop()
	cfg := testDefaultConfig()
	cfg.CircuitBreakerFailures = 2
	market := sampleMarketConfig()

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96490), Quantity: decimal.NewFromFloat(1.0)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(1.0)},
		},
	}

	depthReader := &mockDepthReader{snapshot: depth}
	orderSubmitter := &mockOrderSubmitter{returnErr: errors.New("gRPC simulated network outage")}
	selector := engine.NewDirectionSelector()

	worker := engine.NewMarketWorker(market, cfg, orderSubmitter, depthReader, selector, logger)

	ctx := context.Background()

	// Cycle 1: fails
	worker.ExecuteCycle(ctx, config.ProfileLow)
	if worker.SafetyManager().CircuitBreaker().Allow() != true {
		t.Errorf("circuit breaker should still allow after 1 failure (max 2)")
	}

	// Cycle 2: fails -> trips
	worker.ExecuteCycle(ctx, config.ProfileLow)
	if worker.SafetyManager().CircuitBreaker().Allow() != false {
		t.Errorf("circuit breaker should be tripped (disallow) after 2 failures")
	}

	// Cycle 3: should be blocked by circuit breaker before submitting
	countBefore := len(orderSubmitter.orders)
	worker.ExecuteCycle(ctx, config.ProfileLow)
	countAfter := len(orderSubmitter.orders)

	if countBefore != countAfter {
		t.Errorf("expected no additional submission when circuit breaker is tripped")
	}
}

// TestWorker_PartialFill_ResidualCancelled_ActualFillRecorded verifies:
// 1. Partial fill triggers immediate CancelOrder for remaining quantity.
// 2. Inventory and volume accounting records ONLY actual filled quantity (0.03 BTC), NOT submitted quantity.
// 3. CT-001 never leaves resting orders in the book.
func TestWorker_PartialFill_ResidualCancelled_ActualFillRecorded(t *testing.T) {
	logger := zap.NewNop()
	cfg := testDefaultConfig()
	market := sampleMarketConfig()

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96490), Quantity: decimal.NewFromFloat(0.50)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.50)},
		},
	}

	depthReader := &mockDepthReader{snapshot: depth}
	orderSubmitter := &mockOrderSubmitter{
		status:       "ORDER_STATUS_PARTIALLY_FILLED",
		filledQty:    "0.0300",
		remainingQty: "0.0200",
	}
	selector := engine.NewDirectionSelector()

	worker := engine.NewMarketWorker(market, cfg, orderSubmitter, depthReader, selector, logger)

	ctx := context.Background()
	worker.ExecuteCycle(ctx, config.ProfileLow)

	orderSubmitter.mu.Lock()
	defer orderSubmitter.mu.Unlock()

	// Invariant: Order was submitted
	if len(orderSubmitter.orders) != 1 {
		t.Fatalf("expected 1 order submitted, got %d", len(orderSubmitter.orders))
	}

	// Invariant: Residual cancellation MUST be called immediately for uncompleted order
	if len(orderSubmitter.cancelledIDs) != 1 {
		t.Fatalf("expected 1 CancelOrder call for residual, got %d", len(orderSubmitter.cancelledIDs))
	}

	// Invariant: DirectionSelector inventory must record ONLY the filled 0.03 BTC notional
	delta := selector.GetNetNotionalDelta("BTC-USDT")
	priceCap, _ := decimal.NewFromString(orderSubmitter.orders[0].PriceCap)
	expectedFill := decimal.NewFromFloat(0.03)
	expectedNotional := expectedFill.Mul(priceCap)
	if !delta.Abs().Equal(expectedNotional) {
		t.Fatalf("expected inventory notional delta to reflect actual fill %s, got %s", expectedNotional, delta)
	}

	// Invariant: SafetyManager hourly volume must record actual fill (0.03 * priceCap)
	recordedNotional := worker.SafetyManager().HourlyNotional()
	if !recordedNotional.Equal(expectedNotional) {
		t.Fatalf("expected recorded notional %s, got %s", expectedNotional, recordedNotional)
	}
}

// TestWorker_ZeroFill_ResidualCancelled_NoInventoryUpdate verifies that an unfilled order
// has its residual cancelled and does NOT increment inventory skew or volume.
func TestWorker_ZeroFill_ResidualCancelled_NoInventoryUpdate(t *testing.T) {
	logger := zap.NewNop()
	cfg := testDefaultConfig()
	market := sampleMarketConfig()

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96490), Quantity: decimal.NewFromFloat(0.50)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.50)},
		},
	}

	depthReader := &mockDepthReader{snapshot: depth}
	orderSubmitter := &mockOrderSubmitter{
		status:       "ORDER_STATUS_OPEN",
		filledQty:    "0.0000",
		remainingQty: "0.0500",
	}
	selector := engine.NewDirectionSelector()

	worker := engine.NewMarketWorker(market, cfg, orderSubmitter, depthReader, selector, logger)

	ctx := context.Background()
	worker.ExecuteCycle(ctx, config.ProfileLow)

	orderSubmitter.mu.Lock()
	defer orderSubmitter.mu.Unlock()

	// Invariant: CancelOrder was called to purge the resting open order
	if len(orderSubmitter.cancelledIDs) != 1 {
		t.Fatalf("expected 1 CancelOrder call for resting open order, got %d", len(orderSubmitter.cancelledIDs))
	}

	// Invariant: Zero inventory change
	delta := selector.GetNetNotionalDelta("BTC-USDT")
	if !delta.IsZero() {
		t.Fatalf("expected zero inventory delta for unfilled order, got %s", delta)
	}

	// Invariant: Zero hourly notional
	recordedNotional := worker.SafetyManager().HourlyNotional()
	if !recordedNotional.IsZero() {
		t.Fatalf("expected zero notional for unfilled order, got %s", recordedNotional)
	}
}

// TestWorker_ShutdownAfterSubmission_CancelsResidual verifies that if the worker's parent context
// is cancelled immediately after CreateOrder succeeds (e.g. during SIGTERM shutdown),
// the autonomous cleanup context takes over, calls CancelOrder, and ensures no residual rests on the book.
func TestWorker_ShutdownAfterSubmission_CancelsResidual(t *testing.T) {
	logger := zap.NewNop()
	cfg := testDefaultConfig()
	market := sampleMarketConfig()

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96490), Quantity: decimal.NewFromFloat(0.50)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.50)},
		},
	}

	depthReader := &mockDepthReader{snapshot: depth}

	ctx, cancel := context.WithCancel(context.Background())

	orderSubmitter := &mockOrderSubmitter{
		status:       "ORDER_STATUS_PARTIALLY_FILLED",
		filledQty:    "0.0200",
		remainingQty: "0.0300",
	}

	selector := engine.NewDirectionSelector()
	worker := engine.NewMarketWorker(market, cfg, orderSubmitter, depthReader, selector, logger)

	// Trigger shutdown immediately as execution starts
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel() // Cancel parent context!
	}()

	worker.ExecuteCycle(ctx, config.ProfileLow)

	orderSubmitter.mu.Lock()
	defer orderSubmitter.mu.Unlock()

	// Invariant: Order was submitted
	if len(orderSubmitter.orders) != 1 {
		t.Fatalf("expected 1 order submitted, got %d", len(orderSubmitter.orders))
	}

	// Invariant: DESPITE parent ctx cancellation, cleanup MUST still run and cancel the residual!
	if len(orderSubmitter.cancelledIDs) != 1 {
		t.Fatalf("expected CancelOrder to be executed despite shutdown/cancellation, got %d calls", len(orderSubmitter.cancelledIDs))
	}

	// Invariant: Actual fill is recorded cleanly
	delta := selector.GetNetNotionalDelta("BTC-USDT")
	priceCap, _ := decimal.NewFromString(orderSubmitter.orders[0].PriceCap)
	expectedFill := decimal.NewFromFloat(0.02)
	expectedNotional := expectedFill.Mul(priceCap)
	if !delta.Abs().Equal(expectedNotional) {
		t.Fatalf("expected inventory delta %s, got %s", expectedNotional, delta)
	}
}

// TestWorker_AmbiguousCreateOrder_IdempotencyRecovery_CancelsResidual tests the critical end-to-end failure scenario:
// 1. Order Service successfully commits the order to DB/outbox
// 2. Network response is lost (CTS receives timeout/error)
// 3. CTS retries with the SAME idempotency key in a detached recovery context
// 4. Order ID is recovered from Order Service
// 5. Residual is detected and CancelOrder is issued
// 6. Terminal state is confirmed and actual fill is recorded without leaving any resting maker order
func TestWorker_AmbiguousCreateOrder_IdempotencyRecovery_CancelsResidual(t *testing.T) {
	logger := zap.NewNop()
	cfg := testDefaultConfig()
	market := sampleMarketConfig()

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96490), Quantity: decimal.NewFromFloat(0.50)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.50)},
		},
	}

	depthReader := &mockDepthReader{snapshot: depth}
	orderSubmitter := &mockOrderSubmitter{
		failFirstCreateWithTimeout: true, // First CreateCrossingOrder simulates lost response/timeout
		orderCommittedOnTimeout:    true, // Order was committed to Order Service DB
		status:                     "ORDER_STATUS_PARTIALLY_FILLED",
		filledQty:                  "0.0250",
		remainingQty:               "0.0250",
	}

	selector := engine.NewDirectionSelector()
	worker := engine.NewMarketWorker(market, cfg, orderSubmitter, depthReader, selector, logger)

	ctx := context.Background()
	worker.ExecuteCycle(ctx, config.ProfileLow)

	orderSubmitter.mu.Lock()
	defer orderSubmitter.mu.Unlock()

	// Invariant: Exactly 1 call was made to CreateCrossingOrder (no duplicate order creation!)
	if orderSubmitter.createAttempts != 1 {
		t.Fatalf("expected 1 create attempt, got %d", orderSubmitter.createAttempts)
	}

	// Invariant: Exactly 1 call was made to FindOrderByIdempotencyKey to recover OrderID
	if orderSubmitter.findAttempts != 1 {
		t.Fatalf("expected 1 find attempt for recovery, got %d", orderSubmitter.findAttempts)
	}

	// Invariant: Once recovered, residual cancellation MUST be executed
	if len(orderSubmitter.cancelledIDs) != 1 {
		t.Fatalf("expected 1 CancelOrder call for residual after recovery, got %d", len(orderSubmitter.cancelledIDs))
	}

	// Invariant: Actual fill is recorded cleanly
	delta := selector.GetNetNotionalDelta("BTC-USDT")
	priceCap, _ := decimal.NewFromString(orderSubmitter.orders[0].PriceCap)
	expectedFill := decimal.NewFromFloat(0.025)
	expectedNotional := expectedFill.Mul(priceCap)
	if !delta.Abs().Equal(expectedNotional) {
		t.Fatalf("expected inventory delta %s, got %s", expectedNotional, delta)
	}

	// Invariant: Circuit breaker must NOT be tripped because the order was recovered and resolved cleanly
	if worker.SafetyManager().CircuitBreaker().State() != "CLOSED" {
		t.Fatalf("expected circuit breaker to remain CLOSED after successful idempotency recovery, got %s",
			worker.SafetyManager().CircuitBreaker().State())
	}
}

// TestWorker_DuplicateOrderPrevention_RecoveryNeverCreatesSecondOrder explicitly proves that:
//  1. When an ambiguous CreateOrder occurs where Order Service committed the order, CTS uses
//     FindOrderByIdempotencyKey and NEVER re-calls CreateCrossingOrder (no duplicate order creation).
//  2. Recovery resolves the committed order's residual cleanly via CancelOrder.
//  3. Subsequent worker cycles generate fresh, unique idempotency keys without duplicate submissions.
func TestWorker_DuplicateOrderPrevention_RecoveryNeverCreatesSecondOrder(t *testing.T) {
	logger := zap.NewNop()
	cfg := testDefaultConfig()
	market := sampleMarketConfig()

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96490), Quantity: decimal.NewFromFloat(0.50)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.50)},
		},
	}

	depthReader := &mockDepthReader{snapshot: depth}
	orderSubmitter := &mockOrderSubmitter{
		failFirstCreateWithTimeout: true, // First submission times out after Order Service commits
		orderCommittedOnTimeout:    true,
		status:                     "ORDER_STATUS_PARTIALLY_FILLED",
		filledQty:                  "0.0200",
		remainingQty:               "0.0300",
	}

	selector := engine.NewDirectionSelector()
	worker := engine.NewMarketWorker(market, cfg, orderSubmitter, depthReader, selector, logger)

	ctx := context.Background()

	// --- Cycle 1: Ambiguous submission with recovery ---
	worker.ExecuteCycle(ctx, config.ProfileLow)

	orderSubmitter.mu.Lock()
	createAttemptsAfterCycle1 := orderSubmitter.createAttempts
	findAttemptsAfterCycle1 := orderSubmitter.findAttempts
	cancelledCountAfterCycle1 := len(orderSubmitter.cancelledIDs)
	ordersCountAfterCycle1 := len(orderSubmitter.orders)
	firstIdempotencyKey := orderSubmitter.orders[0].IdempotencyKey
	orderSubmitter.mu.Unlock()

	// Invariant 1: Exactly 1 create attempt was made during Cycle 1 (recovery DID NOT retry CreateCrossingOrder)
	if createAttemptsAfterCycle1 != 1 {
		t.Fatalf("expected exactly 1 create attempt in cycle 1, got %d (duplicate order created!)", createAttemptsAfterCycle1)
	}

	// Invariant 2: Exactly 1 order exists in Order Service DB
	if ordersCountAfterCycle1 != 1 {
		t.Fatalf("expected exactly 1 order in storage, got %d", ordersCountAfterCycle1)
	}

	// Invariant 3: Idempotency lookup was called exactly once to recover the committed order
	if findAttemptsAfterCycle1 != 1 {
		t.Fatalf("expected exactly 1 find attempt, got %d", findAttemptsAfterCycle1)
	}

	// Invariant 4: Residual was cancelled via CancelOrder without resting maker orders
	if cancelledCountAfterCycle1 != 1 {
		t.Fatalf("expected 1 cancel call for residual, got %d", cancelledCountAfterCycle1)
	}

	// --- Cycle 2: Next cycle runs normally ---
	// Update submitter state for normal execution in cycle 2
	orderSubmitter.mu.Lock()
	orderSubmitter.status = "ORDER_STATUS_FILLED"
	orderSubmitter.filledQty = "0.0100"
	orderSubmitter.remainingQty = "0"
	orderSubmitter.mu.Unlock()

	worker.ExecuteCycle(ctx, config.ProfileLow)

	orderSubmitter.mu.Lock()
	createAttemptsAfterCycle2 := orderSubmitter.createAttempts
	findAttemptsAfterCycle2 := orderSubmitter.findAttempts
	ordersCountAfterCycle2 := len(orderSubmitter.orders)
	secondIdempotencyKey := orderSubmitter.orders[1].IdempotencyKey
	orderSubmitter.mu.Unlock()

	// Invariant 5: Cycle 2 executed exactly 1 fresh CreateCrossingOrder
	if createAttemptsAfterCycle2 != 2 {
		t.Fatalf("expected 2 total create attempts across 2 cycles, got %d", createAttemptsAfterCycle2)
	}
	if ordersCountAfterCycle2 != 2 {
		t.Fatalf("expected 2 total distinct orders, got %d", ordersCountAfterCycle2)
	}

	// Invariant 6: Cycle 2 generated a fresh, distinct idempotency key (no re-use of previous key)
	if firstIdempotencyKey == secondIdempotencyKey {
		t.Fatalf("expected distinct idempotency keys across cycles, got duplicate key: %s", firstIdempotencyKey)
	}

	// Invariant 7: No idempotency lookup was triggered in Cycle 2 (normal success)
	if findAttemptsAfterCycle2 != findAttemptsAfterCycle1 {
		t.Fatalf("expected find attempts to remain %d, got %d", findAttemptsAfterCycle1, findAttemptsAfterCycle2)
	}
}

// TestWorker_AmbiguousCreateOrder_PartialFill40Percent_ResidualCancelled_InventoryUpdated
// explicitly validates the canonical real-world recovery lifecycle:
// 1. CreateOrder RPC times out after Order Service commits the order.
// 2. Matching Engine immediately filled 40% and left 60% resting.
// 3. CTS enters idempotency recovery via FindOrderByIdempotencyKey.
// 4. CTS recovers the order, detects the 60% residual, and cancels it via CancelOrder.
// 5. CTS records ONLY the 40% filled quantity in inventory accounting, NOT the full quantity.
// 6. Circuit breaker remains CLOSED because the order was completely resolved.
func TestWorker_AmbiguousCreateOrder_PartialFill40Percent_ResidualCancelled_InventoryUpdated(t *testing.T) {
	logger := zap.NewNop()
	cfg := testDefaultConfig()
	market := sampleMarketConfig()

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96490), Quantity: decimal.NewFromFloat(1.00)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(1.00)},
		},
	}

	// Sizing will produce an order (e.g. 0.0500 BTC)
	// We simulate that exactly 40% (0.0200 BTC) filled, leaving 60% (0.0300 BTC) remaining.
	depthReader := &mockDepthReader{snapshot: depth}
	orderSubmitter := &mockOrderSubmitter{
		failFirstCreateWithTimeout: true, // CreateOrder response lost
		orderCommittedOnTimeout:    true, // Order committed to DB
		status:                     "ORDER_STATUS_PARTIALLY_FILLED",
		filledQty:                  "0.0200", // Exactly 40%
		remainingQty:               "0.0300", // Exactly 60% residual
	}

	selector := engine.NewDirectionSelector()
	worker := engine.NewMarketWorker(market, cfg, orderSubmitter, depthReader, selector, logger)

	ctx := context.Background()
	worker.ExecuteCycle(ctx, config.ProfileLow)

	orderSubmitter.mu.Lock()
	defer orderSubmitter.mu.Unlock()

	// Invariant 1: Exactly 1 CreateCrossingOrder was attempted (no retry)
	if orderSubmitter.createAttempts != 1 {
		t.Fatalf("expected 1 create attempt, got %d", orderSubmitter.createAttempts)
	}

	// Invariant 2: FindOrderByIdempotencyKey recovered the committed order
	if orderSubmitter.findAttempts != 1 {
		t.Fatalf("expected 1 find attempt, got %d", orderSubmitter.findAttempts)
	}

	// Invariant 3: Residual 60% was cancelled via CancelOrder
	if len(orderSubmitter.cancelledIDs) != 1 {
		t.Fatalf("expected 1 cancel call for 60%% residual, got %d", len(orderSubmitter.cancelledIDs))
	}
	expectedOrderID := "ord-" + orderSubmitter.orders[0].IdempotencyKey
	if orderSubmitter.cancelledIDs[0] != expectedOrderID {
		t.Fatalf("expected cancelled ID to match recovered order %s, got %s",
			expectedOrderID, orderSubmitter.cancelledIDs[0])
	}

	// Invariant 4: Inventory tracking records ONLY the 40% filled portion
	delta := selector.GetNetNotionalDelta("BTC-USDT")
	priceCap, _ := decimal.NewFromString(orderSubmitter.orders[0].PriceCap)
	filledQtyDecimal := decimal.NewFromFloat(0.0200)
	expectedNotional := filledQtyDecimal.Mul(priceCap)

	if !delta.Abs().Equal(expectedNotional) {
		t.Fatalf("expected inventory delta to record strictly 40%% fill (%s), but got %s",
			expectedNotional, delta)
	}

	// Invariant 5: Circuit breaker must remain CLOSED (recovery succeeded, residual resolved)
	if worker.SafetyManager().CircuitBreaker().State() != "CLOSED" {
		t.Fatalf("expected circuit breaker state CLOSED, got %s",
			worker.SafetyManager().CircuitBreaker().State())
	}
}

// TestWorker_DefiniteRejection_SkipsRecoveryAndDoesNotTripBreaker tests that when Order Service
// returns a deterministic rejection (e.g. codes.FailedPrecondition for insufficient funds or market halt),
// CTS does NOT attempt idempotency recovery, does NOT flag an unresolved residual, and releases any probe.
func TestWorker_DefiniteRejection_SkipsRecoveryAndDoesNotTripBreaker(t *testing.T) {
	logger := zap.NewNop()
	cfg := testDefaultConfig()
	market := sampleMarketConfig()

	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96490), Quantity: decimal.NewFromFloat(0.50)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.50)},
		},
	}

	depthReader := &mockDepthReader{snapshot: depth}
	orderSubmitter := &mockOrderSubmitter{
		returnErr: status.Error(codes.FailedPrecondition, "insufficient funds for order placement"),
	}

	selector := engine.NewDirectionSelector()
	worker := engine.NewMarketWorker(market, cfg, orderSubmitter, depthReader, selector, logger)

	ctx := context.Background()
	worker.ExecuteCycle(ctx, config.ProfileLow)

	orderSubmitter.mu.Lock()
	defer orderSubmitter.mu.Unlock()

	// Invariant: Exactly 1 create attempt was made (NO idempotency recovery retries on definite rejection)
	if orderSubmitter.createAttempts != 1 {
		t.Fatalf("expected exactly 1 create attempt on definite rejection, got %d", orderSubmitter.createAttempts)
	}

	// Invariant: No orders were cancelled because none were created
	if len(orderSubmitter.cancelledIDs) != 0 {
		t.Fatalf("expected 0 CancelOrder calls on definite rejection, got %d", len(orderSubmitter.cancelledIDs))
	}

	// Invariant: Circuit breaker probe was released, so circuit breaker remains healthy CLOSED (not tripped)
	if worker.SafetyManager().CircuitBreaker().State() != "CLOSED" {
		t.Fatalf("expected circuit breaker to remain CLOSED after definite rejection, got %s",
			worker.SafetyManager().CircuitBreaker().State())
	}
}

// TestWorker_SELL_ConservativePrice_UsesL1 verifies that for SELL orders, the conservative
// notional uses L1.Price (the highest bid price) rather than PriceCap (the lowest slippage floor).
func TestWorker_SELL_ConservativePrice_UsesL1(t *testing.T) {
	cfg := testDefaultConfig()
	market := sampleMarketConfig()

	// Top Bid is 96,490; Asks are at 96,500
	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96490), Quantity: decimal.NewFromFloat(0.50)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.50)},
		},
	}

	params, err := engine.CalculateOrderParameters(
		config.ProfileLow,
		depth,
		"SELL",
		market,
		cfg.MaxOrderNotionalUSDT,
		cfg.MaxSlippageBps,
	)
	if err != nil {
		t.Fatalf("unexpected error calculating SELL params: %v", err)
	}

	// For SELL: ConservativePrice must be L1.Price (96,490), NOT the lower PriceCap
	expectedConservative := decimal.NewFromInt(96490)
	if !params.ConservativePrice.Equal(expectedConservative) {
		t.Fatalf("expected ConservativePrice to be %s (L1.Price for SELL), got %s",
			expectedConservative, params.ConservativePrice)
	}
}

// TestWorker_IdempotencyRecoveryMatrix tests the authoritative submission failure & recovery decision tree:
// 1. InvalidArgument                  -> No retry, rejected, probe released, breaker CLOSED
// 2. FailedPrecondition               -> No retry, rejected, probe released, breaker CLOSED
// 3. DeadlineExceeded (order created) -> Recover OrderID -> Cancel residual -> verify terminal -> breaker CLOSED
// 4. Unavailable (order created)      -> Recover OrderID -> Cancel residual -> verify terminal -> breaker CLOSED
// 5. DeadlineExceeded (not created)   -> Fail closed -> probe released -> breaker records failure
// 6. Unavailable (not created)        -> Fail closed -> probe released -> breaker records failure
// 7. Recovery lookup RPC timeout      -> Fail closed -> unresolved residual -> breaker records failure
// 8. CTS shutdown (order created)     -> Order exists -> Detached cleanup cancels residual
// 9. CTS shutdown (not created)       -> Order does not exist -> Clean exit, probe released, breaker CLOSED
func TestWorker_IdempotencyRecoveryMatrix(t *testing.T) {
	depth := &redisdepth.DepthSnapshot{
		MarketID:   "BTC-USDT",
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96490), Quantity: decimal.NewFromFloat(0.50)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96500), Quantity: decimal.NewFromFloat(0.50)},
		},
	}

	tests := []struct {
		name                 string
		setupSubmitter       func(m *mockOrderSubmitter)
		cancelParentCtx      bool
		expectedCreateCalls  int
		expectedFindCalls    int
		expectedCancelCalls  int
		expectedBreakerState string
		expectedProbeFree    bool
	}{
		{
			name: "InvalidArgument -> definite rejection, no retry",
			setupSubmitter: func(m *mockOrderSubmitter) {
				m.returnErr = status.Error(codes.InvalidArgument, "invalid quantity")
			},
			expectedCreateCalls:  1,
			expectedFindCalls:    0,
			expectedCancelCalls:  0,
			expectedBreakerState: "CLOSED",
			expectedProbeFree:    true,
		},
		{
			name: "FailedPrecondition -> definite rejection, no retry",
			setupSubmitter: func(m *mockOrderSubmitter) {
				m.returnErr = status.Error(codes.FailedPrecondition, "insufficient balance")
			},
			expectedCreateCalls:  1,
			expectedFindCalls:    0,
			expectedCancelCalls:  0,
			expectedBreakerState: "CLOSED",
			expectedProbeFree:    true,
		},
		{
			name: "DeadlineExceeded (order committed) -> recover -> cancel residual",
			setupSubmitter: func(m *mockOrderSubmitter) {
				m.failFirstCreateWithTimeout = true
				m.orderCommittedOnTimeout = true
				m.status = "ORDER_STATUS_PARTIALLY_FILLED"
				m.filledQty = "0.0200"
				m.remainingQty = "0.0300"
			},
			expectedCreateCalls:  1,
			expectedFindCalls:    1,
			expectedCancelCalls:  1,
			expectedBreakerState: "CLOSED",
			expectedProbeFree:    true,
		},
		{
			name: "Unavailable (order committed) -> recover -> cancel residual",
			setupSubmitter: func(m *mockOrderSubmitter) {
				m.failFirstCreateWithUnavailable = true
				m.orderCommittedOnTimeout = true
				m.status = "ORDER_STATUS_PARTIALLY_FILLED"
				m.filledQty = "0.0200"
				m.remainingQty = "0.0300"
			},
			expectedCreateCalls:  1,
			expectedFindCalls:    1,
			expectedCancelCalls:  1,
			expectedBreakerState: "CLOSED",
			expectedProbeFree:    true,
		},
		{
			name: "DeadlineExceeded (order not committed) -> fail closed, probe released",
			setupSubmitter: func(m *mockOrderSubmitter) {
				m.failFirstCreateWithTimeout = true
				m.orderCommittedOnTimeout = false // Order never made it to DB
			},
			expectedCreateCalls:  1,
			expectedFindCalls:    1,
			expectedCancelCalls:  0,
			expectedBreakerState: "CLOSED", // 1 failure does not trip (threshold is 2)
			expectedProbeFree:    true,
		},
		{
			name: "Unavailable (order not committed) -> fail closed, probe released",
			setupSubmitter: func(m *mockOrderSubmitter) {
				m.failFirstCreateWithUnavailable = true
				m.orderCommittedOnTimeout = false
			},
			expectedCreateCalls:  1,
			expectedFindCalls:    1,
			expectedCancelCalls:  0,
			expectedBreakerState: "CLOSED",
			expectedProbeFree:    true,
		},
		{
			name: "Recovery lookup RPC timeout -> fail closed with unresolved residual",
			setupSubmitter: func(m *mockOrderSubmitter) {
				m.failFirstCreateWithTimeout = true
				m.findOrderErr = errors.New("recovery RPC failed")
			},
			expectedCreateCalls:  1,
			expectedFindCalls:    1,
			expectedCancelCalls:  0,
			expectedBreakerState: "CLOSED", // 1 failure recorded
			expectedProbeFree:    true,
		},
		{
			name: "CTS shutdown (order committed) -> detached cleanup cancels residual",
			setupSubmitter: func(m *mockOrderSubmitter) {
				m.returnErr = context.Canceled
				m.orderCommittedOnTimeout = true
				m.status = "ORDER_STATUS_PARTIALLY_FILLED"
				m.filledQty = "0.0200"
				m.remainingQty = "0.0300"
				// Add an existing order to simulate commit before cancellation
				m.orders = append(m.orders, submittedOrder{
					MarketID: "BTC-USDT",
					Side:     "BUY",
				})
			},
			cancelParentCtx:      true,
			expectedCreateCalls:  1,
			expectedFindCalls:    1,
			expectedCancelCalls:  1,
			expectedBreakerState: "CLOSED",
			expectedProbeFree:    true,
		},
		{
			name: "CTS shutdown (order not committed) -> clean exit, no residual, probe released",
			setupSubmitter: func(m *mockOrderSubmitter) {
				m.returnErr = context.Canceled
				m.orderCommittedOnTimeout = false
			},
			cancelParentCtx:      true,
			expectedCreateCalls:  1,
			expectedFindCalls:    1,
			expectedCancelCalls:  0,
			expectedBreakerState: "CLOSED",
			expectedProbeFree:    true,
		},
		{
			name: "Upstream codes.Canceled while CTS parent context alive (order not committed) -> fail closed",
			setupSubmitter: func(m *mockOrderSubmitter) {
				m.returnErr = status.Error(codes.Canceled, "rpc canceled by upstream proxy")
				m.orderCommittedOnTimeout = false
			},
			cancelParentCtx:      false, // CTS is healthy!
			expectedCreateCalls:  1,
			expectedFindCalls:    1,
			expectedCancelCalls:  0,
			expectedBreakerState: "CLOSED", // 1 failure recorded (< 2 threshold)
			expectedProbeFree:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logger := zap.NewNop()
			cfg := testDefaultConfig()
			market := sampleMarketConfig()
			depthReader := &mockDepthReader{snapshot: depth}
			orderSubmitter := &mockOrderSubmitter{}
			tc.setupSubmitter(orderSubmitter)

			// If testing pre-existing order for shutdown commit, make sure idempotency key matches
			selector := engine.NewDirectionSelector()
			worker := engine.NewMarketWorker(market, cfg, orderSubmitter, depthReader, selector, logger)

			ctx, cancel := context.WithCancel(context.Background())
			if tc.cancelParentCtx {
				cancel() // Parent context already cancelled (simulating shutdown)
			} else {
				defer cancel()
			}

			// Pre-set idempotency key on existing order if any
			orderSubmitter.mu.Lock()
			if len(orderSubmitter.orders) > 0 && orderSubmitter.orders[0].IdempotencyKey == "" {
				// Allow worker to generate key and align it in FindOrderByIdempotencyKey
				orderSubmitter.findOrderErr = nil
			}
			orderSubmitter.mu.Unlock()

			worker.ExecuteCycle(ctx, config.ProfileLow)

			orderSubmitter.mu.Lock()
			defer orderSubmitter.mu.Unlock()

			if orderSubmitter.createAttempts != tc.expectedCreateCalls {
				t.Errorf("expected %d create attempts, got %d", tc.expectedCreateCalls, orderSubmitter.createAttempts)
			}
			if orderSubmitter.findAttempts != tc.expectedFindCalls {
				t.Errorf("expected %d find attempts, got %d", tc.expectedFindCalls, orderSubmitter.findAttempts)
			}
			if len(orderSubmitter.cancelledIDs) != tc.expectedCancelCalls {
				t.Errorf("expected %d cancel calls, got %d", tc.expectedCancelCalls, len(orderSubmitter.cancelledIDs))
			}
			if worker.SafetyManager().CircuitBreaker().State() != tc.expectedBreakerState {
				t.Errorf("expected circuit breaker state %s, got %s", tc.expectedBreakerState, worker.SafetyManager().CircuitBreaker().State())
			}
			// Verify probe reservation is permitted again (probe was released or resolved)
			if !worker.SafetyManager().CircuitBreaker().Allow() {
				t.Errorf("expected circuit breaker Allow() to be true (probe unlocked/healthy)")
			}
		})
	}
}
