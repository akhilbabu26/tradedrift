package reconciler_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/config"
	"tradedrift/services/liquidity-engine/internal/meclient"
	"tradedrift/services/liquidity-engine/internal/order"
	"tradedrift/services/liquidity-engine/internal/orderservice"
	"tradedrift/services/liquidity-engine/internal/pricing"
	"tradedrift/services/liquidity-engine/internal/reconciler"
)

// ─── Error sentinel ───────────────────────────────────────────────────────────

var errCancelFailed = errors.New("simulated OS cancel failure")

// ─── Test Doubles ─────────────────────────────────────────────────────────────

type mockCreateCall struct {
	MarketID      string
	Partition     int
	OrderID       string
	ClientOrderID string
	Side          string
	Price         string
	Quantity      string
}

type mockProducer struct {
	createdOrders    []string
	publishedCreates []mockCreateCall
	cancelledOrders  []string
	cancelErr        error
	createErr        error
}

func (p *mockProducer) PublishCreate(ctx context.Context, marketID string, partition int, orderID, clientOrderID, side, price, quantity string) error {
	p.createdOrders = append(p.createdOrders, orderID)
	p.publishedCreates = append(p.publishedCreates, mockCreateCall{
		MarketID:      marketID,
		Partition:     partition,
		OrderID:       orderID,
		ClientOrderID: clientOrderID,
		Side:          side,
		Price:         price,
		Quantity:      quantity,
	})
	return p.createErr
}

func (p *mockProducer) PublishCancel(ctx context.Context, marketID string, partition int, orderID string) error {
	if p.cancelErr != nil {
		return p.cancelErr
	}
	p.cancelledOrders = append(p.cancelledOrders, orderID)
	return nil
}

type mockCreatedOrder struct {
	MarketID      string
	Side          string
	Price         string
	Quantity      string
	ClientOrderID string
}

type mockOrderSvc struct {
	orders         map[string]*orderservice.OrderState
	cancelErr      error
	cancelCalled   []string
	createdOrders  []mockCreatedOrder
	historicOrders map[string]int
}

func newMockOrderSvc() *mockOrderSvc {
	return &mockOrderSvc{
		orders:         make(map[string]*orderservice.OrderState),
		historicOrders: make(map[string]int),
	}
}

func (m *mockOrderSvc) GetOrderByClientID(ctx context.Context, clientOrderID string) (*orderservice.OrderState, error) {
	if o, ok := m.orders[clientOrderID]; ok {
		return o, nil
	}
	return nil, orderservice.ErrOrderNotFound
}

func (m *mockOrderSvc) CancelMMOrder(ctx context.Context, orderID string) error {
	m.cancelCalled = append(m.cancelCalled, orderID)
	if m.cancelErr != nil {
		return m.cancelErr
	}
	return nil
}

func (m *mockOrderSvc) CreateMMOrder(ctx context.Context, marketID, side, price, quantity, clientOrderID string) (*orderservice.OrderState, error) {
	m.createdOrders = append(m.createdOrders, mockCreatedOrder{
		MarketID:      marketID,
		Side:          side,
		Price:         price,
		Quantity:      quantity,
		ClientOrderID: clientOrderID,
	})
	return &orderservice.OrderState{
		OrderID:       "mock-os-order-id-" + clientOrderID,
		ClientOrderID: clientOrderID,
		Status:        "OPEN",
	}, nil
}

func (m *mockOrderSvc) ListMMOrders(ctx context.Context, marketID string) ([]order.OSOrder, error) {
	return nil, nil
}

func (m *mockOrderSvc) RecoverHighestGenerations(ctx context.Context, marketID string) (map[string]int, error) {
	return m.historicOrders, nil
}

type mockMetrics struct {
	rebaseGenFailures int
	expiryGenFailures int
}

func (m *mockMetrics) IncStaleOrders(marketID string)                      {}
func (m *mockMetrics) IncReconcileCreate(marketID string)                  {}
func (m *mockMetrics) IncReconcileCancel(marketID string)                  {}
func (m *mockMetrics) IncReconcileCorrect(marketID string)                 {}
func (m *mockMetrics) IncReconcileNoop(marketID string)                    {}
func (m *mockMetrics) IncOrdersFilled(marketID, side string)               {}
func (m *mockMetrics) IncDuplicateMMLevel(marketID string)                  {}
func (m *mockMetrics) IncRebaseGenerationFailure(marketID, levelID string) { m.rebaseGenFailures++ }
func (m *mockMetrics) IncExpiryGenerationFailure(marketID string)          { m.expiryGenFailures++ }
func (m *mockMetrics) IncQuantityInvariantViolation(marketID string)       {}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func testConfig() *config.Config {
	return &config.Config{
		PendingTimeout:    10 * time.Millisecond,
		CancellingTimeout: 10 * time.Millisecond,
		CancelRetryLimit:  3,
		Markets: []config.MarketConfig{
			{
				MarketID:       "BTC-USDT",
				TickSize:       decimal.RequireFromString("0.01"),
				LotSize:        decimal.RequireFromString("0.00001"),
				LevelCount:     12,
				MinOrderSize:   decimal.RequireFromString("0.00001"),
				ReferencePrice: decimal.RequireFromString("96450.00"),
				SpreadBps:      4,
			},
		},
	}
}

func newInvTestRec(t *testing.T, marketID string) (*reconciler.Reconciler, *order.Tracker, *config.Config, *mockProducer, *mockOrderSvc) {
	t.Helper()
	tracker := order.NewTracker()
	cfg := &config.Config{
		PendingTimeout:    0,
		CancellingTimeout: 0,
		CancelRetryLimit:  3,
		Markets: []config.MarketConfig{
			{
				MarketID:       marketID,
				TickSize:       decimal.RequireFromString("0.01"),
				LotSize:        decimal.RequireFromString("0.00001"),
				LevelCount:     12,
				MinOrderSize:   decimal.RequireFromString("0.00001"),
				ReferencePrice: decimal.RequireFromString("100.00"),
				SpreadBps:      4,
			},
		},
	}
	prod := &mockProducer{}
	osClient := newMockOrderSvc()
	rec := reconciler.NewReconciler(tracker, prod, osClient, nil, cfg, zap.NewNop(), &mockMetrics{})
	return rec, tracker, cfg, prod, osClient
}

func newInvTestRecWithMetrics(t *testing.T, marketID string, metrics *mockMetrics) (*reconciler.Reconciler, *order.Tracker, *config.Config, *mockProducer, *mockOrderSvc) {
	t.Helper()
	tracker := order.NewTracker()
	cfg := &config.Config{
		PendingTimeout:    0,
		CancellingTimeout: 0,
		CancelRetryLimit:  3,
		Markets: []config.MarketConfig{
			{
				MarketID:       marketID,
				TickSize:       decimal.RequireFromString("0.01"),
				LotSize:        decimal.RequireFromString("0.00001"),
				LevelCount:     12,
				MinOrderSize:   decimal.RequireFromString("0.00001"),
				ReferencePrice: decimal.RequireFromString("100.00"),
				SpreadBps:      4,
			},
		},
	}
	prod := &mockProducer{}
	osClient := newMockOrderSvc()
	rec := reconciler.NewReconciler(tracker, prod, osClient, nil, cfg, zap.NewNop(), metrics)
	return rec, tracker, cfg, prod, osClient
}

func invAddResting(t *testing.T, tracker *order.Tracker, levelID, orderID, coid, marketID, side, price string, gen int) {
	t.Helper()
	tracker.SetPending(levelID, orderID, coid, gen, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: marketID,
		Side:     side,
		Price:    decimal.RequireFromString(price),
		Quantity: decimal.RequireFromString("1.0"),
	})
	tracker.SetResting(levelID, orderID, decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
}

func invAddOSRegistered(t *testing.T, tracker *order.Tracker, levelID, orderID, coid, marketID, side, price string, gen int) {
	t.Helper()
	tracker.SetPending(levelID, orderID, coid, gen, pricing.PriceLevel{
		LevelID:  levelID,
		MarketID: marketID,
		Side:     side,
		Price:    decimal.RequireFromString(price),
		Quantity: decimal.RequireFromString("1.0"),
	})
	tracker.SetOSRegistered(levelID, orderID, decimal.RequireFromString("1.0"), decimal.RequireFromString("1.0"))
}

func invEmptySnap(marketID string) *meclient.MarketSnapshot {
	return &meclient.MarketSnapshot{
		MarketID: marketID,
		State:    "LIVE",
		Sequence: 1,
		Orders:   []meclient.MMOrderSummary{},
	}
}
