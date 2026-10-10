package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"tradedrift/services/order/internal/repository"
	"tradedrift/services/order/internal/service"
)

type mockRepo struct {
	repository.OrderRepository
	mu            sync.Mutex
	orders        map[string]*repository.Order
	idempMap      map[string]*repository.Order
	findCallCount int
	simulateRace  bool
	failOnCreate  error
	enqueueCalls  []string
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		orders:   make(map[string]*repository.Order),
		idempMap: make(map[string]*repository.Order),
	}
}

func (m *mockRepo) EnqueueWalletRelease(ctx context.Context, orderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.enqueueCalls = append(m.enqueueCalls, orderID)
	return nil
}

func (m *mockRepo) FindByIdempotencyKey(ctx context.Context, key string) (*repository.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.findCallCount++
	if m.simulateRace && m.findCallCount == 1 {
		return nil, nil // simulate competing uncommitted transaction
	}
	return m.idempMap[key], nil
}

func (m *mockRepo) CreateOrder(ctx context.Context, o *repository.Order, outboxPayload []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failOnCreate != nil {
		return m.failOnCreate
	}
	if o.IdempotencyKey != nil {
		if _, exists := m.idempMap[*o.IdempotencyKey]; exists {
			return repository.ErrDuplicateIdempotencyKey
		}
		m.idempMap[*o.IdempotencyKey] = o
	}
	m.orders[o.ID] = o
	return nil
}

func (m *mockRepo) GetByID(ctx context.Context, orderID, userID string) (*repository.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, exists := m.orders[orderID]
	if !exists || o.UserID != userID {
		return nil, repository.ErrOrderNotFound
	}
	return o, nil
}

func (m *mockRepo) UpdateStatusToCancelling(ctx context.Context, o *repository.Order, outboxPayload []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, exists := m.orders[o.ID]
	if !exists {
		return repository.ErrOrderNotFound
	}
	stored.Status = repository.StatusCancelling
	return nil
}

type mockWalletClient struct {
	mu            sync.Mutex
	reserved      map[string]string
	releaseCalls  []string
	failOnRelease bool
}

func newMockWalletClient() *mockWalletClient {
	return &mockWalletClient{
		reserved: make(map[string]string),
	}
}

func (w *mockWalletClient) ReserveFunds(ctx context.Context, userID, orderID, asset, amount string) (string, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reserved[orderID] = amount
	return "res-" + orderID, false, nil
}

func (w *mockWalletClient) ReleaseFunds(ctx context.Context, orderID string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.releaseCalls = append(w.releaseCalls, orderID)
	if w.failOnRelease {
		return errors.New("wallet service connection error")
	}
	delete(w.reserved, orderID)
	return nil
}

type mockMarketGuard struct {
	halted bool
	err    error
}

func (g *mockMarketGuard) IsHalted(ctx context.Context, marketID string) (bool, error) {
	return g.halted, g.err
}

func setupTestService(repo repository.OrderRepository, wallet service.WalletClient, guard service.MarketGuard) service.Service {
	logger := zap.NewNop()
	return service.NewService(repo, wallet, nil, guard, "00000000-0000-0000-0000-000000000001", logger)
}

func TestService_CreateOrder_Invariants(t *testing.T) {
	validUserID := uuid.New().String()

	t.Run("invalid user ID non-uuid", func(t *testing.T) {
		svc := setupTestService(newMockRepo(), newMockWalletClient(), &mockMarketGuard{})
		_, err := svc.CreateOrder(context.Background(), &service.CreateOrderParams{
			UserID:    "not-a-uuid",
			MarketID:  "BTC-USDT",
			Side:      repository.SideBuy,
			OrderType: repository.TypeLimit,
			Price:     "60000.00",
			Quantity:  "0.10",
		})
		require.ErrorIs(t, err, service.ErrInvalidUserID)
	})

	t.Run("invalid side", func(t *testing.T) {
		svc := setupTestService(newMockRepo(), newMockWalletClient(), &mockMarketGuard{})
		_, err := svc.CreateOrder(context.Background(), &service.CreateOrderParams{
			UserID:    validUserID,
			MarketID:  "BTC-USDT",
			Side:      "HOLD",
			OrderType: repository.TypeLimit,
			Price:     "60000.00",
			Quantity:  "0.10",
		})
		require.ErrorIs(t, err, service.ErrInvalidSide)
	})

	t.Run("invalid type", func(t *testing.T) {
		svc := setupTestService(newMockRepo(), newMockWalletClient(), &mockMarketGuard{})
		_, err := svc.CreateOrder(context.Background(), &service.CreateOrderParams{
			UserID:    validUserID,
			MarketID:  "BTC-USDT",
			Side:      repository.SideBuy,
			OrderType: "STOP_LOSS",
			Price:     "60000.00",
			Quantity:  "0.10",
		})
		require.ErrorIs(t, err, service.ErrInvalidType)
	})

	t.Run("unsupported market", func(t *testing.T) {
		svc := setupTestService(newMockRepo(), newMockWalletClient(), &mockMarketGuard{})
		_, err := svc.CreateOrder(context.Background(), &service.CreateOrderParams{
			UserID:    validUserID,
			MarketID:  "DOGE-USDT",
			Side:      repository.SideBuy,
			OrderType: repository.TypeLimit,
			Price:     "0.10",
			Quantity:  "10.0",
		})
		require.ErrorIs(t, err, service.ErrInvalidMarket)
	})

	t.Run("quantity non-positive or invalid", func(t *testing.T) {
		svc := setupTestService(newMockRepo(), newMockWalletClient(), &mockMarketGuard{})
		_, err := svc.CreateOrder(context.Background(), &service.CreateOrderParams{
			UserID:    validUserID,
			MarketID:  "BTC-USDT",
			Side:      repository.SideBuy,
			OrderType: repository.TypeLimit,
			Price:     "60000.00",
			Quantity:  "-0.10",
		})
		require.ErrorIs(t, err, service.ErrInvalidQuantity)
	})

	t.Run("quantity exceeds base asset precision", func(t *testing.T) {
		svc := setupTestService(newMockRepo(), newMockWalletClient(), &mockMarketGuard{})
		// BTC supports 8 decimals. 9 decimals must be rejected.
		_, err := svc.CreateOrder(context.Background(), &service.CreateOrderParams{
			UserID:    validUserID,
			MarketID:  "BTC-USDT",
			Side:      repository.SideBuy,
			OrderType: repository.TypeLimit,
			Price:     "60000.00",
			Quantity:  "0.123456789", // 9 decimals
		})
		require.ErrorIs(t, err, service.ErrInvalidQuantityPrecision)
	})

	t.Run("price exceeds maximum supported decimal precision", func(t *testing.T) {
		svc := setupTestService(newMockRepo(), newMockWalletClient(), &mockMarketGuard{})
		_, err := svc.CreateOrder(context.Background(), &service.CreateOrderParams{
			UserID:    validUserID,
			MarketID:  "BTC-USDT",
			Side:      repository.SideBuy,
			OrderType: repository.TypeLimit,
			Price:     "60000.12345678901", // 11 decimals
			Quantity:  "0.10",
		})
		require.ErrorIs(t, err, service.ErrInvalidPricePrecision)
	})

	t.Run("market ID canonical normalization to uppercase", func(t *testing.T) {
		repo := newMockRepo()
		wallet := newMockWalletClient()
		svc := setupTestService(repo, wallet, &mockMarketGuard{})
		order, err := svc.CreateOrder(context.Background(), &service.CreateOrderParams{
			UserID:    validUserID,
			MarketID:  "btc-usdt", // lowercase input
			Side:      repository.SideBuy,
			OrderType: repository.TypeLimit,
			Price:     "60000.00",
			Quantity:  "0.10",
		})
		require.NoError(t, err)
		assert.Equal(t, "BTC-USDT", order.MarketID)
	})
}

func TestService_ConcurrentIdempotencyReplay(t *testing.T) {
	validUserID := uuid.New().String()
	idempKey := "unique-idemp-" + uuid.New().String()

	t.Run("race condition: DB duplicate constraint returns committed order and releases redundant reservation", func(t *testing.T) {
		repo := newMockRepo()
		wallet := newMockWalletClient()
		svc := setupTestService(repo, wallet, &mockMarketGuard{})

		// Simulate pre-existing order committed by competing concurrent goroutine
		existingOrder := &repository.Order{
			ID:                uuid.New().String(),
			UserID:            validUserID,
			MarketID:          "BTC-USDT",
			Side:              repository.SideBuy,
			OrderType:         repository.TypeLimit,
			Price:             strPtr("60000.0000000000"),
			Quantity:          "0.1000000000",
			RemainingQuantity: "0.1000000000",
			Status:            repository.StatusOpen,
			IdempotencyKey:    &idempKey,
			CreatedAt:         time.Now().UTC(),
			UpdatedAt:         time.Now().UTC(),
		}
		repo.idempMap[idempKey] = existingOrder
		repo.orders[existingOrder.ID] = existingOrder
		repo.simulateRace = true

		// To simulate the race condition where FindByIdempotencyKey returned nil initially,
		// force CreateOrder to return repository.ErrDuplicateIdempotencyKey
		repo.failOnCreate = repository.ErrDuplicateIdempotencyKey

		// Call CreateOrder with identical parameters
		order, err := svc.CreateOrder(context.Background(), &service.CreateOrderParams{
			UserID:         validUserID,
			MarketID:       "BTC-USDT",
			Side:           repository.SideBuy,
			OrderType:      repository.TypeLimit,
			Price:          "60000.00",
			Quantity:       "0.10",
			IdempotencyKey: idempKey,
		})

		require.NoError(t, err, "matching concurrent idempotent request must succeed")
		assert.Equal(t, existingOrder.ID, order.ID)
		assert.Equal(t, 1, len(wallet.releaseCalls), "redundant temporary reservation from lost race must be released")
	})

	t.Run("race condition: DB duplicate constraint with conflicting parameters returns ErrDuplicateIdempotencyKey", func(t *testing.T) {
		repo := newMockRepo()
		wallet := newMockWalletClient()
		svc := setupTestService(repo, wallet, &mockMarketGuard{})

		existingOrder := &repository.Order{
			ID:                uuid.New().String(),
			UserID:            validUserID,
			MarketID:          "BTC-USDT",
			Side:              repository.SideBuy,
			OrderType:         repository.TypeLimit,
			Price:             strPtr("60000.0000000000"),
			Quantity:          "0.1000000000",
			Status:            repository.StatusOpen,
			IdempotencyKey:    &idempKey,
		}
		repo.idempMap[idempKey] = existingOrder
		repo.orders[existingOrder.ID] = existingOrder
		repo.simulateRace = true
		repo.failOnCreate = repository.ErrDuplicateIdempotencyKey

		// Call with different price
		_, err := svc.CreateOrder(context.Background(), &service.CreateOrderParams{
			UserID:         validUserID,
			MarketID:       "BTC-USDT",
			Side:           repository.SideBuy,
			OrderType:      repository.TypeLimit,
			Price:          "50000.00", // conflicting price
			Quantity:       "0.10",
			IdempotencyKey: idempKey,
		})

		require.ErrorIs(t, err, service.ErrDuplicateIdempotencyKey)
	})
}

func TestService_CancelAndGetOrder_Validations(t *testing.T) {
	svc := setupTestService(newMockRepo(), newMockWalletClient(), &mockMarketGuard{})
	validUUID := uuid.New().String()

	t.Run("cancel invalid order ID non-uuid", func(t *testing.T) {
		_, err := svc.CancelOrder(context.Background(), "invalid-order-id", validUUID)
		require.ErrorIs(t, err, service.ErrOrderNotFound)
	})

	t.Run("cancel invalid user ID non-uuid", func(t *testing.T) {
		_, err := svc.CancelOrder(context.Background(), validUUID, "invalid-user-id")
		require.ErrorIs(t, err, service.ErrInvalidUserID)
	})

	t.Run("get invalid order ID non-uuid", func(t *testing.T) {
		_, err := svc.GetOrder(context.Background(), "invalid-order-id", validUUID)
		require.ErrorIs(t, err, service.ErrOrderNotFound)
	})

	t.Run("get invalid user ID non-uuid", func(t *testing.T) {
		_, err := svc.GetOrder(context.Background(), validUUID, "invalid-user-id")
		require.ErrorIs(t, err, service.ErrInvalidUserID)
	})

	t.Run("list invalid user ID non-uuid", func(t *testing.T) {
		_, err := svc.ListOrders(context.Background(), "invalid-user-id", "BTC-USDT", "", "", "", nil, nil, 10)
		require.ErrorIs(t, err, service.ErrInvalidUserID)
	})

	t.Run("list invalid market ID", func(t *testing.T) {
		_, err := svc.ListOrders(context.Background(), validUUID, "INVALID-MARKET", "", "", "", nil, nil, 10)
		require.ErrorIs(t, err, service.ErrInvalidMarket)
	})
}

func TestService_CreateOrder_SagaCompensation_DurableEnqueueOnWalletFailure(t *testing.T) {
	repo := newMockRepo()
	repo.failOnCreate = errors.New("postgres disk full")

	walletClient := newMockWalletClient()
	walletClient.failOnRelease = true // simulate network transport partition to Wallet Service

	svc := setupTestService(repo, walletClient, &mockMarketGuard{})

	params := &service.CreateOrderParams{
		UserID:    uuid.New().String(),
		MarketID:  "BTC-USDT",
		Side:      repository.SideBuy,
		OrderType: repository.TypeLimit,
		Price:     "50000.00",
		Quantity:  "0.10000000",
	}

	_, err := svc.CreateOrder(context.Background(), params)
	require.Error(t, err)

	walletClient.mu.Lock()
	require.Len(t, walletClient.releaseCalls, 1, "immediate ReleaseFunds must be attempted")
	orderID := walletClient.releaseCalls[0]
	walletClient.mu.Unlock()

	repo.mu.Lock()
	require.Len(t, repo.enqueueCalls, 1, "EnqueueWalletRelease must be called for durable worker compensation")
	assert.Equal(t, orderID, repo.enqueueCalls[0])
	repo.mu.Unlock()
}

func TestService_CreateOrder_StrictAssetPrecision_UnknownAssetRejected(t *testing.T) {
	repo := newMockRepo()
	walletClient := newMockWalletClient()
	svc := setupTestService(repo, walletClient, &mockMarketGuard{})

	params := &service.CreateOrderParams{
		UserID:    uuid.New().String(),
		MarketID:  "DOGE-USDT",
		Side:      repository.SideBuy,
		OrderType: repository.TypeLimit,
		Price:     "0.10",
		Quantity:  "100.00",
	}

	_, err := svc.CreateOrder(context.Background(), params)
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrInvalidMarket)
}

func strPtr(s string) *string {
	return &s
}
