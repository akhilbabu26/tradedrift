package test

import (
	"context"
	"math/rand"
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

// TestWorkerScheduling_DecoupledHeartbeatAndCooldowns verifies that:
// 1. MID execution does not trigger adaptive post-HIGH cooldown.
// 2. LOW execution does not trigger adaptive post-HIGH cooldown.
// 3. HIGH execution with fill triggers adaptive post-HIGH cooldown.
func TestWorkerScheduling_DecoupledHeartbeatAndCooldowns(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	market := sampleMarketConfig()
	cfg := testDefaultConfig()

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
	orderSubmitter := &mockOrderSubmitter{}
	selector := engine.NewDirectionSelector()

	worker := engine.NewMarketWorker(market, cfg, orderSubmitter, depthReader, selector, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Execute MID profile (assert NO post-HIGH cooldown)
	worker.ExecuteCycle(ctx, config.ProfileMid)
	if worker.AdaptivePostHighCooldown() != 0 {
		t.Fatalf("expected 0 adaptivePostHighCooldown after MID cycle, got %v", worker.AdaptivePostHighCooldown())
	}

	orderSubmitter.mu.Lock()
	if len(orderSubmitter.orders) != 1 {
		t.Fatalf("expected 1 order submitted after MID cycle, got %d", len(orderSubmitter.orders))
	}
	orderSubmitter.mu.Unlock()

	// 2. Execute subsequent LOW cycles immediately (assert NO post-HIGH cooldown)
	for i := 0; i < 3; i++ {
		worker.ExecuteCycle(ctx, config.ProfileLow)
		if worker.AdaptivePostHighCooldown() != 0 {
			t.Fatalf("expected 0 adaptivePostHighCooldown after LOW cycle, got %v", worker.AdaptivePostHighCooldown())
		}
	}

	orderSubmitter.mu.Lock()
	if len(orderSubmitter.orders) != 4 {
		t.Fatalf("expected 4 total orders (1 MID + 3 LOW), got %d", len(orderSubmitter.orders))
	}
	orderSubmitter.mu.Unlock()

	// 3. Execute HIGH cycle (assert post-HIGH cooldown IS set within [15s, 50s])
	worker.ExecuteCycle(ctx, config.ProfileHigh)
	if worker.AdaptivePostHighCooldown() == 0 {
		t.Fatalf("expected adaptivePostHighCooldown > 0 after HIGH cycle, got 0")
	}
	if worker.AdaptivePostHighCooldown() < 15*time.Second || worker.AdaptivePostHighCooldown() > 50*time.Second {
		t.Fatalf("expected adaptivePostHighCooldown in [15s, 50s], got %v", worker.AdaptivePostHighCooldown())
	}

	orderSubmitter.mu.Lock()
	if len(orderSubmitter.orders) != 5 {
		t.Fatalf("expected 5 total orders after HIGH cycle, got %d", len(orderSubmitter.orders))
	}
	orderSubmitter.mu.Unlock()

	// 4. Continue executing LOW cycles
	worker.ExecuteCycle(ctx, config.ProfileLow)

	orderSubmitter.mu.Lock()
	if len(orderSubmitter.orders) != 6 {
		t.Fatalf("expected 6 total orders after LOW cycle, got %d", len(orderSubmitter.orders))
	}
	orderSubmitter.mu.Unlock()
}

// TestWorkerScheduling_RealLoopCooldownEnforcement verifies the ACTUAL WORKER LOOP:
// 1. Order #1 (HIGH) executes and fills at t=0.
// 2. Adaptive cooldown of 250ms is applied.
// 3. Invariant: Order #2 CANNOT execute before 250ms.
// 4. Invariant: Order #2 DOES execute once 250ms has passed.
func TestWorkerScheduling_RealLoopCooldownEnforcement(t *testing.T) {
	logger := zap.NewNop()
	market := sampleMarketConfig()
	cfg := testDefaultConfig()

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
	orderSubmitter := &mockOrderSubmitter{}
	selector := engine.NewDirectionSelector()

	worker := engine.NewMarketWorker(market, cfg, orderSubmitter, depthReader, selector, logger)

	// Configure fast base interval (5ms) so baseline pacing would normally fire immediately
	fastRegimeConfigs := engine.DefaultRegimeConfigs()
	fastNormal := fastRegimeConfigs[engine.RegimeNormal]
	fastNormal.Interval = config.IntervalConfig{
		MinInterval:  5 * time.Millisecond,
		BaseInterval: 10 * time.Millisecond,
		MaxInterval:  15 * time.Millisecond,
	}
	// Force 100% HIGH profile for this test
	fastNormal.WeightLow = 0.0
	fastNormal.WeightMid = 0.0
	fastNormal.WeightHigh = 1.0
	fastRegimeConfigs[engine.RegimeNormal] = fastNormal

	worker.RegimeManager().SetConfigs(fastRegimeConfigs)
	worker.RegimeManager().SetRegime(engine.RegimeNormal, time.Now().Add(1*time.Hour))

	// Inject 250ms adaptive cooldown
	const injectedCooldown = 250 * time.Millisecond
	worker.SetPostHighCooldownSampler(func(severity engine.HighSweepSeverity, rng *rand.Rand) time.Duration {
		return injectedCooldown
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		worker.Run(ctx, 0)
	}()

	// Wait for Order #1 to be submitted
	startTime := time.Now()
	for time.Since(startTime) < 500*time.Millisecond {
		orderSubmitter.mu.Lock()
		count := len(orderSubmitter.orders)
		orderSubmitter.mu.Unlock()
		if count >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	orderSubmitter.mu.Lock()
	if len(orderSubmitter.orders) != 1 {
		t.Fatalf("expected Order #1 to execute immediately, got %d orders", len(orderSubmitter.orders))
	}
	orderSubmitter.mu.Unlock()

	// Invariant Check 1: At 100ms (< 250ms cooldown), Order #2 MUST NOT be submitted
	time.Sleep(100 * time.Millisecond)
	orderSubmitter.mu.Lock()
	if len(orderSubmitter.orders) != 1 {
		t.Fatalf("BUG REGRESSION: Order #2 executed prematurely at 100ms before 250ms cooldown! Count=%d", len(orderSubmitter.orders))
	}
	orderSubmitter.mu.Unlock()

	// Invariant Check 2: At 180ms (< 250ms cooldown), Order #2 STILL MUST NOT be submitted
	time.Sleep(80 * time.Millisecond)
	orderSubmitter.mu.Lock()
	if len(orderSubmitter.orders) != 1 {
		t.Fatalf("BUG REGRESSION: Order #2 executed prematurely at 180ms before 250ms cooldown! Count=%d", len(orderSubmitter.orders))
	}
	orderSubmitter.mu.Unlock()

	// Invariant Check 3: At 320ms (> 250ms cooldown), Order #2 MUST be submitted
	time.Sleep(140 * time.Millisecond)
	orderSubmitter.mu.Lock()
	orderCount := len(orderSubmitter.orders)
	orderSubmitter.mu.Unlock()

	if orderCount < 2 {
		t.Fatalf("expected Order #2 to execute after cooldown expired (>250ms), got count=%d", orderCount)
	}

	cancel()
	wg.Wait()
}

// TestWorkerScheduling_HighZeroFill_NoCooldown verifies that:
// When a HIGH order is cancelled with zero fill, NO post-sweep cooldown is applied.
func TestWorkerScheduling_HighZeroFill_NoCooldown(t *testing.T) {
	logger := zap.NewNop()
	market := sampleMarketConfig()
	cfg := testDefaultConfig()

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
	// Submitter returns CANCELLED with 0 fill
	zeroFillSubmitter := &mockZeroFillSubmitter{}
	selector := engine.NewDirectionSelector()

	worker := engine.NewMarketWorker(market, cfg, zeroFillSubmitter, depthReader, selector, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Execute HIGH cycle with zero fill
	worker.ExecuteCycle(ctx, config.ProfileHigh)

	// Invariant: adaptivePostHighCooldown MUST remain 0
	if worker.AdaptivePostHighCooldown() != 0 {
		t.Fatalf("expected adaptivePostHighCooldown=0 for zero-fill HIGH order, got %v", worker.AdaptivePostHighCooldown())
	}
}

// mockZeroFillSubmitter simulates an order cancelled unfilled.
type mockZeroFillSubmitter struct {
	mu     sync.Mutex
	orders []*orderservice.OrderResult
}

func (m *mockZeroFillSubmitter) CreateCrossingOrder(ctx context.Context, marketID, side, priceCap, quantity, idempotencyKey string) (*orderservice.OrderResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := &orderservice.OrderResult{
		OrderID:   "ord-CTS-test-zero-fill",
		Status:    "ORDER_STATUS_CANCELLED",
		FilledQty: "0",
	}
	m.orders = append(m.orders, res)
	return res, nil
}

func (m *mockZeroFillSubmitter) GetOrder(ctx context.Context, orderID string) (*orderservice.OrderResult, error) {
	return &orderservice.OrderResult{
		OrderID:   orderID,
		Status:    "ORDER_STATUS_CANCELLED",
		FilledQty: "0",
	}, nil
}

func (m *mockZeroFillSubmitter) CancelOrder(ctx context.Context, orderID string) (*orderservice.OrderResult, error) {
	return &orderservice.OrderResult{
		OrderID:   orderID,
		Status:    "ORDER_STATUS_CANCELLED",
		FilledQty: "0",
	}, nil
}

func (m *mockZeroFillSubmitter) FindOrderByIdempotencyKey(ctx context.Context, marketID, idempotencyKey string) (*orderservice.OrderResult, error) {
	return nil, nil
}

// TestWorkerScheduling_HighTwoLevelFill_MediumCooldown verifies that:
// When a HIGH order consumes L1 and penetrates into L2, it is classified as MEDIUM severity (25s–35s cooldown).
func TestWorkerScheduling_HighTwoLevelFill_MediumCooldown(t *testing.T) {
	logger := zap.NewNop()
	market := sampleMarketConfig()
	cfg := testDefaultConfig()

	// Multi-level depth: L1=0.50 BTC, L2=0.80 BTC
	depth := &redisdepth.DepthSnapshot{
		MarketID:   market.MarketID,
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96000), Quantity: decimal.NewFromFloat(0.50)},
			{Price: decimal.NewFromInt(95900), Quantity: decimal.NewFromFloat(0.80)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(96100), Quantity: decimal.NewFromFloat(0.50)},
			{Price: decimal.NewFromInt(96200), Quantity: decimal.NewFromFloat(0.80)},
		},
	}

	depthReader := &mockDepthReader{snapshot: depth}
	// Submitter returns filledQty = 0.90 BTC (sweeps past L1=0.50 and into L2)
	customSubmitter := &mockCustomFillSubmitter{filledQty: "0.90"}
	selector := engine.NewDirectionSelector()

	worker := engine.NewMarketWorker(market, cfg, customSubmitter, depthReader, selector, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	worker.ExecuteCycle(ctx, config.ProfileHigh)

	cd := worker.AdaptivePostHighCooldown()
	if cd < 25*time.Second || cd > 35*time.Second {
		t.Fatalf("expected MEDIUM severity cooldown in [25s, 35s] for 2-level fill, got %v", cd)
	}
}

// mockCustomFillSubmitter simulates an order filled with a specific quantity.
type mockCustomFillSubmitter struct {
	mu        sync.Mutex
	filledQty string
	orders    []*orderservice.OrderResult
}

func (m *mockCustomFillSubmitter) CreateCrossingOrder(ctx context.Context, marketID, side, priceCap, quantity, idempotencyKey string) (*orderservice.OrderResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := &orderservice.OrderResult{
		OrderID:   "ord-CTS-test-custom-fill",
		Status:    "ORDER_STATUS_FILLED",
		FilledQty: m.filledQty,
	}
	m.orders = append(m.orders, res)
	return res, nil
}

func (m *mockCustomFillSubmitter) GetOrder(ctx context.Context, orderID string) (*orderservice.OrderResult, error) {
	return &orderservice.OrderResult{
		OrderID:   orderID,
		Status:    "ORDER_STATUS_FILLED",
		FilledQty: m.filledQty,
	}, nil
}

func (m *mockCustomFillSubmitter) CancelOrder(ctx context.Context, orderID string) (*orderservice.OrderResult, error) {
	return &orderservice.OrderResult{
		OrderID:   orderID,
		Status:    "ORDER_STATUS_FILLED",
		FilledQty: m.filledQty,
	}, nil
}

func (m *mockCustomFillSubmitter) FindOrderByIdempotencyKey(ctx context.Context, marketID, idempotencyKey string) (*orderservice.OrderResult, error) {
	return nil, nil
}
