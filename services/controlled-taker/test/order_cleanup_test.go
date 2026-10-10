package test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/controlled-taker/internal/clients/orderservice"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
	"tradedrift/services/controlled-taker/internal/config"
	"tradedrift/services/controlled-taker/internal/engine"
)

type mockRejectedOrderSubmitter struct {
	createdOrderID string
	getOrderCalls  int
}

func (m *mockRejectedOrderSubmitter) CreateCrossingOrder(ctx context.Context, marketID, side, priceCap, quantity, idempotencyKey string) (*orderservice.OrderResult, error) {
	m.createdOrderID = "ord-rejected-123"
	return &orderservice.OrderResult{
		OrderID:   m.createdOrderID,
		Status:    "ORDER_STATUS_OPEN",
		FilledQty: "0",
	}, nil
}

func (m *mockRejectedOrderSubmitter) GetOrder(ctx context.Context, orderID string) (*orderservice.OrderResult, error) {
	m.getOrderCalls++
	return &orderservice.OrderResult{
		OrderID:   orderID,
		Status:    "ORDER_STATUS_REJECTED",
		FilledQty: "0",
	}, nil
}

func (m *mockRejectedOrderSubmitter) CancelOrder(ctx context.Context, orderID string) (*orderservice.OrderResult, error) {
	return nil, nil
}

func (m *mockRejectedOrderSubmitter) FindOrderByIdempotencyKey(ctx context.Context, marketID, idempotencyKey string) (*orderservice.OrderResult, error) {
	return nil, nil
}

// TestOrderCleanup_RejectedTerminal_NoResidualOrBreakerTrip verifies CTS-1:
// An order that resolves to ORDER_STATUS_REJECTED is recognized as a terminal resolution,
// does not trigger an unresolved residual error, and does NOT trip the safety circuit breaker.
func TestOrderCleanup_RejectedTerminal_NoResidualOrBreakerTrip(t *testing.T) {
	logger := zap.NewNop()
	market := sampleMarketConfig()
	cfg := testDefaultConfig()
	cfg.ResidualGracePeriod = 200 * time.Millisecond
	cfg.ResidualPollInterval = 20 * time.Millisecond

	depth := &redisdepth.DepthSnapshot{
		MarketID:   market.MarketID,
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96000), Quantity: decimal.NewFromFloat(1.0)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96100), Quantity: decimal.NewFromFloat(1.0)},
		},
	}

	depthReader := &mockDepthReader{snapshot: depth}
	submitter := &mockRejectedOrderSubmitter{}
	selector := engine.NewDirectionSelector()

	worker := engine.NewMarketWorker(market, cfg, submitter, depthReader, selector, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Execute cycle where order resolves to REJECTED
	worker.ExecuteCycle(ctx, config.ProfileLow)

	// INVARIANT: Breaker must NOT be tripped; circuit breaker remains in ALLOW state
	if !worker.SafetyManager().CircuitBreaker().Allow() {
		t.Fatalf("expected circuit breaker to remain CLOSED/ALLOW after REJECTED order, but it was tripped")
	}

	// Verify GetOrder was called to inspect terminal state
	if submitter.getOrderCalls == 0 {
		t.Fatalf("expected GetOrder to be called during cleanup loop")
	}
}

type mockStuckCancellingSubmitter struct {
	createdOrderID string
	cancelCalls    int
}

func (m *mockStuckCancellingSubmitter) CreateCrossingOrder(ctx context.Context, marketID, side, priceCap, quantity, idempotencyKey string) (*orderservice.OrderResult, error) {
	m.createdOrderID = "ord-stuck-cancelling-123"
	return &orderservice.OrderResult{
		OrderID:   m.createdOrderID,
		Status:    "ORDER_STATUS_OPEN",
		FilledQty: "0",
	}, nil
}

func (m *mockStuckCancellingSubmitter) GetOrder(ctx context.Context, orderID string) (*orderservice.OrderResult, error) {
	return &orderservice.OrderResult{
		OrderID:   orderID,
		Status:    "ORDER_STATUS_CANCELLING",
		FilledQty: "0",
	}, nil
}

func (m *mockStuckCancellingSubmitter) CancelOrder(ctx context.Context, orderID string) (*orderservice.OrderResult, error) {
	m.cancelCalls++
	return &orderservice.OrderResult{
		OrderID:   orderID,
		Status:    "ORDER_STATUS_CANCELLING",
		FilledQty: "0",
	}, nil
}

func (m *mockStuckCancellingSubmitter) FindOrderByIdempotencyKey(ctx context.Context, marketID, idempotencyKey string) (*orderservice.OrderResult, error) {
	return nil, nil
}

// TestOrderCleanup_HardDeadline_FailsClosedAndTripsBreaker verifies that when an order is stuck in
// CANCELLING, awaitOrderCleanup strictly bounds execution to ResidualDeadline (does not invent post-deadline grace)
// and fails closed by tripping the safety circuit breaker.
func TestOrderCleanup_HardDeadline_FailsClosedAndTripsBreaker(t *testing.T) {
	logger := zap.NewNop()
	market := sampleMarketConfig()
	cfg := testDefaultConfig()
	cfg.CircuitBreakerFailures = 1
	cfg.CrossingDelay = 10 * time.Millisecond
	cfg.ResidualGracePeriod = 40 * time.Millisecond
	cfg.ResidualDeadline = 100 * time.Millisecond
	cfg.ResidualPollInterval = 10 * time.Millisecond

	depth := &redisdepth.DepthSnapshot{
		MarketID:   market.MarketID,
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96000), Quantity: decimal.NewFromFloat(1.0)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96100), Quantity: decimal.NewFromFloat(1.0)},
		},
	}

	depthReader := &mockDepthReader{snapshot: depth}
	submitter := &mockStuckCancellingSubmitter{}
	selector := engine.NewDirectionSelector()

	worker := engine.NewMarketWorker(market, cfg, submitter, depthReader, selector, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	worker.ExecuteCycle(ctx, config.ProfileLow)
	duration := time.Since(start)

	// INVARIANT: Hard upper bound must NOT exceed ResidualDeadline significantly (e.g. must NOT add old 3s grace)
	if duration > 500*time.Millisecond {
		t.Fatalf("expected cleanup loop to fail closed within ~100ms hard deadline, but took %v", duration)
	}

	// INVARIANT: Breaker must be tripped after failing closed on unresolved residual
	if worker.SafetyManager().CircuitBreaker().Allow() {
		t.Fatalf("expected circuit breaker to be TRIPPED/NOT ALLOW after unresolved residual, but it remained ALLOW")
	}
}

