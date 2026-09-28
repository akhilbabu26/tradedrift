package test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"
	"tradedrift/services/controlled-taker/internal/clients/orderservice"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
	"tradedrift/services/controlled-taker/internal/config"
	"tradedrift/services/controlled-taker/internal/engine"
)

type customStateSubmitter struct {
	mockOrderSubmitter
	mu             sync.Mutex
	getOrderFunc   func(ctx context.Context, orderID string) (*orderservice.OrderResult, error)
	cancelAttempts int
}

func (c *customStateSubmitter) GetOrder(ctx context.Context, orderID string) (*orderservice.OrderResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.getOrderFunc != nil {
		return c.getOrderFunc(ctx, orderID)
	}
	return c.mockOrderSubmitter.GetOrder(ctx, orderID)
}

func (c *customStateSubmitter) CancelOrder(ctx context.Context, orderID string) (*orderservice.OrderResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelAttempts++
	return c.mockOrderSubmitter.CancelOrder(ctx, orderID)
}

func setupTestWorker(submitter engine.OrderSubmitter, cfg config.Config) *engine.MarketWorker {
	logger := zap.NewNop()
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
	selector := engine.NewDirectionSelector()
	return engine.NewMarketWorker(market, cfg, submitter, depthReader, selector, logger)
}

// 1. Fast fill within grace period -> zero cancellation calls
func TestWorker_ResidualGrace_FastFill_NoCancellation(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.ResidualGracePeriod = 200 * time.Millisecond
	cfg.ResidualPollInterval = 20 * time.Millisecond

	submitter := &customStateSubmitter{
		mockOrderSubmitter: mockOrderSubmitter{
			status: "ORDER_STATUS_FILLED",
		},
	}

	worker := setupTestWorker(submitter, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	worker.ExecuteCycle(ctx, config.ProfileLow)

	if submitter.cancelAttempts != 0 {
		t.Fatalf("expected 0 cancel attempts for fast fill, got %d", submitter.cancelAttempts)
	}
}

// 2. Slow fill within grace window -> zero cancellation calls
func TestWorker_ResidualGrace_SlowFillWithinGrace_NoCancellation(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.ResidualGracePeriod = 200 * time.Millisecond
	cfg.ResidualPollInterval = 20 * time.Millisecond

	callCount := 0
	submitter := &customStateSubmitter{
		mockOrderSubmitter: mockOrderSubmitter{
			status: "ORDER_STATUS_OPEN",
		},
	}
	submitter.getOrderFunc = func(ctx context.Context, orderID string) (*orderservice.OrderResult, error) {
		callCount++
		// First 2 calls report PARTIALLY_FILLED, 3rd call reports FILLED before 200ms grace expires
		if callCount < 3 {
			return &orderservice.OrderResult{
				OrderID:      orderID,
				Status:       "ORDER_STATUS_PARTIALLY_FILLED",
				FilledQty:    "0.01",
				RemainingQty: "0.09",
			}, nil
		}
		return &orderservice.OrderResult{
			OrderID:      orderID,
			Status:       "ORDER_STATUS_FILLED",
			FilledQty:    "0.10",
			RemainingQty: "0",
		}, nil
	}

	worker := setupTestWorker(submitter, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	worker.ExecuteCycle(ctx, config.ProfileLow)

	if submitter.cancelAttempts != 0 {
		t.Fatalf("expected 0 cancel attempts for fill within grace window, got %d", submitter.cancelAttempts)
	}
}

// 3. Genuine residual after grace window -> cancelled exactly once
func TestWorker_ResidualGrace_GenuineResidual_CancelledAfterGrace(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.ResidualGracePeriod = 60 * time.Millisecond
	cfg.ResidualPollInterval = 15 * time.Millisecond

	submitter := &customStateSubmitter{
		mockOrderSubmitter: mockOrderSubmitter{
			status: "ORDER_STATUS_PARTIALLY_FILLED",
		},
	}
	submitter.getOrderFunc = func(ctx context.Context, orderID string) (*orderservice.OrderResult, error) {
		// Remains partially filled until cancelled
		if submitter.cancelAttempts > 0 {
			return &orderservice.OrderResult{
				OrderID:      orderID,
				Status:       "ORDER_STATUS_CANCELLED",
				FilledQty:    "0.05",
				RemainingQty: "0",
			}, nil
		}
		return &orderservice.OrderResult{
			OrderID:      orderID,
			Status:       "ORDER_STATUS_PARTIALLY_FILLED",
			FilledQty:    "0.05",
			RemainingQty: "0.05",
		}, nil
	}

	worker := setupTestWorker(submitter, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	worker.ExecuteCycle(ctx, config.ProfileLow)

	if submitter.cancelAttempts != 1 {
		t.Fatalf("expected 1 cancel attempt for persistent residual, got %d", submitter.cancelAttempts)
	}
}

// 4. Transient GetOrder error does NOT trigger premature cancellation
func TestWorker_ResidualGrace_TransientGetOrderError_DoesNotPrematurelyCancel(t *testing.T) {
	cfg := testDefaultConfig()
	cfg.ResidualGracePeriod = 200 * time.Millisecond
	cfg.ResidualPollInterval = 20 * time.Millisecond

	callCount := 0
	submitter := &customStateSubmitter{
		mockOrderSubmitter: mockOrderSubmitter{
			status: "ORDER_STATUS_OPEN",
		},
	}
	submitter.getOrderFunc = func(ctx context.Context, orderID string) (*orderservice.OrderResult, error) {
		callCount++
		// First call fails with transient network error
		if callCount == 1 {
			return nil, errors.New("transient network timeout")
		}
		// Subsequent call succeeds as FILLED
		return &orderservice.OrderResult{
			OrderID:      orderID,
			Status:       "ORDER_STATUS_FILLED",
			FilledQty:    "0.10",
			RemainingQty: "0",
		}, nil
	}

	worker := setupTestWorker(submitter, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	worker.ExecuteCycle(ctx, config.ProfileLow)

	// INVARIANT: Read error must NOT cause CancelOrder!
	if submitter.cancelAttempts != 0 {
		t.Fatalf("expected 0 cancel attempts despite transient GetOrder error, got %d", submitter.cancelAttempts)
	}
}
