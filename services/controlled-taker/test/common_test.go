package test

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"tradedrift/services/controlled-taker/internal/clients/orderservice"
	"tradedrift/services/controlled-taker/internal/clients/redisdepth"
	"tradedrift/services/controlled-taker/internal/config"
)

func sampleMarketConfig() config.MarketConfig {
	return config.MarketConfig{
		MarketID:    "BTC-USDT",
		BaseAsset:   "BTC",
		QuoteAsset:  "USDT",
		TickSize:    decimal.NewFromFloat(0.01),
		LotSize:     decimal.NewFromFloat(0.0001),
		MinQuantity: decimal.NewFromFloat(0.0001),
		Partition:   0,
	}
}

func testDefaultConfig() config.Config {
	return config.Config{
		MaxOrderNotionalUSDT:   decimal.NewFromInt(10000),
		MaxHourlyVolumeUSDT:    decimal.NewFromInt(100000),
		MaxDailyVolumeUSDT:     decimal.NewFromInt(500000),
		MaxTradesPerHour:       100,
		MaxSpreadPercent:       decimal.NewFromFloat(0.01),
		MaxSlippageBps:         15,
		CircuitBreakerFailures: 3,
		HighCooldown:           time.Minute,
		CrossingDelay:          10 * time.Millisecond,
		LowInterval:            config.IntervalConfig{MinInterval: 10 * time.Millisecond, BaseInterval: 20 * time.Millisecond, MaxInterval: 30 * time.Millisecond},
		MidInterval:            config.IntervalConfig{MinInterval: 10 * time.Millisecond, BaseInterval: 20 * time.Millisecond, MaxInterval: 30 * time.Millisecond},
		HighInterval:           config.IntervalConfig{MinInterval: 10 * time.Millisecond, BaseInterval: 20 * time.Millisecond, MaxInterval: 30 * time.Millisecond},
	}
}

type mockDepthReader struct {
	mu       sync.Mutex
	snapshot *redisdepth.DepthSnapshot
	err      error
}

func (m *mockDepthReader) GetDepth(ctx context.Context, marketID string) (*redisdepth.DepthSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	return m.snapshot, nil
}

type submittedOrder struct {
	MarketID       string
	Side           string
	PriceCap       string
	Quantity       string
	IdempotencyKey string
}

type mockOrderSubmitter struct {
	mu                             sync.Mutex
	orders                         []submittedOrder
	returnErr                      error
	status                         string
	filledQty                      string
	remainingQty                   string
	cancelledIDs                   []string
	failFirstCreateWithTimeout     bool
	failFirstCreateWithUnavailable bool
	orderCommittedOnTimeout        bool
	createAttempts                 int
	findAttempts                   int
	findOrderErr                   error
}

func (m *mockOrderSubmitter) CreateCrossingOrder(ctx context.Context, marketID, side, priceCap, quantity, idempotencyKey string) (*orderservice.OrderResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.createAttempts++
	if m.failFirstCreateWithTimeout && m.createAttempts == 1 {
		ord := submittedOrder{
			MarketID:       marketID,
			Side:           side,
			PriceCap:       priceCap,
			Quantity:       quantity,
			IdempotencyKey: idempotencyKey,
		}
		if m.orderCommittedOnTimeout {
			m.orders = append(m.orders, ord)
		}
		return nil, context.DeadlineExceeded
	}

	if m.failFirstCreateWithUnavailable && m.createAttempts == 1 {
		ord := submittedOrder{
			MarketID:       marketID,
			Side:           side,
			PriceCap:       priceCap,
			Quantity:       quantity,
			IdempotencyKey: idempotencyKey,
		}
		if m.orderCommittedOnTimeout {
			m.orders = append(m.orders, ord)
		}
		return nil, status.Error(codes.Unavailable, "service unavailable")
	}

	if m.returnErr != nil {
		return nil, m.returnErr
	}
	ord := submittedOrder{
		MarketID:       marketID,
		Side:           side,
		PriceCap:       priceCap,
		Quantity:       quantity,
		IdempotencyKey: idempotencyKey,
	}
	m.orders = append(m.orders, ord)
	s := m.status
	if s == "" {
		s = "ORDER_STATUS_FILLED"
	}
	filled := m.filledQty
	if filled == "" {
		filled = quantity
	}
	remaining := m.remainingQty
	if remaining == "" {
		remaining = "0"
	}
	return &orderservice.OrderResult{
		OrderID:       "ord-" + idempotencyKey,
		ClientOrderID: idempotencyKey,
		Status:        s,
		FilledQty:     filled,
		RemainingQty:  remaining,
	}, nil
}

func (m *mockOrderSubmitter) FindOrderByIdempotencyKey(ctx context.Context, marketID, idempotencyKey string) (*orderservice.OrderResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.findAttempts++
	if m.findOrderErr != nil {
		return nil, m.findOrderErr
	}

	for _, o := range m.orders {
		if o.IdempotencyKey == idempotencyKey || (o.IdempotencyKey == "" && len(m.orders) == 1) {
			s := m.status
			if s == "" {
				s = "ORDER_STATUS_FILLED"
			}
			filled := m.filledQty
			if filled == "" {
				filled = o.Quantity
			}
			remaining := m.remainingQty
			if remaining == "" {
				remaining = "0"
			}
			return &orderservice.OrderResult{
				OrderID:       "ord-" + idempotencyKey,
				ClientOrderID: idempotencyKey,
				Status:        s,
				FilledQty:     filled,
				RemainingQty:  remaining,
			}, nil
		}
	}

	return nil, nil
}

func (m *mockOrderSubmitter) GetOrder(ctx context.Context, orderID string) (*orderservice.OrderResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.status
	if s == "" {
		s = "ORDER_STATUS_FILLED"
	}
	filled := m.filledQty
	if filled == "" && len(m.orders) > 0 {
		filled = m.orders[len(m.orders)-1].Quantity
	}
	remaining := m.remainingQty
	if remaining == "" {
		remaining = "0"
	}
	return &orderservice.OrderResult{
		OrderID:      orderID,
		Status:       s,
		FilledQty:    filled,
		RemainingQty: remaining,
	}, nil
}

func (m *mockOrderSubmitter) CancelOrder(ctx context.Context, orderID string) (*orderservice.OrderResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelledIDs = append(m.cancelledIDs, orderID)
	m.status = "ORDER_STATUS_CANCELLED"
	m.remainingQty = "0"
	return &orderservice.OrderResult{
		OrderID:      orderID,
		Status:       "ORDER_STATUS_CANCELLED",
		FilledQty:    m.filledQty,
		RemainingQty: "0",
	}, nil
}

type mockRedisReader struct {
	pingErr  error
	depths   map[string]*redisdepth.DepthSnapshot
	depthErr error
}

func (m *mockRedisReader) Ping(ctx context.Context) error {
	return m.pingErr
}

func (m *mockRedisReader) GetDepth(ctx context.Context, marketID string) (*redisdepth.DepthSnapshot, error) {
	if m.depthErr != nil {
		return nil, m.depthErr
	}
	if d, ok := m.depths[marketID]; ok {
		return d, nil
	}
	return nil, errors.New("depth not found")
}

type mockOrderPinger struct {
	pingErr error
}

func (m *mockOrderPinger) Ping(ctx context.Context) error {
	return m.pingErr
}

func freshDepth(marketID string) *redisdepth.DepthSnapshot {
	return &redisdepth.DepthSnapshot{
		MarketID:   marketID,
		SnapshotAt: time.Now(),
		Bids: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(100), Quantity: decimal.NewFromFloat(1.0)},
		},
		Asks: []redisdepth.DepthLevel{
			{Price: decimal.NewFromInt(101), Quantity: decimal.NewFromFloat(1.0)},
		},
	}
}
