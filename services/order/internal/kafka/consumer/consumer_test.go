package consumer_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"tradedrift/services/order/internal/kafka/consumer"
	"tradedrift/services/order/internal/repository"
	"tradedrift/services/order/internal/wallet"
)

type mockTradeRepo struct {
	repository.OrderRepository
	mu              sync.Mutex
	processedTrades map[string]bool
	invalidOrders   map[string]bool
	releases        map[string]*repository.WalletRelease
	enqueueCalls    int
	applyTradeCalls int
}

func newMockTradeRepo() *mockTradeRepo {
	return &mockTradeRepo{
		processedTrades: make(map[string]bool),
		invalidOrders:   make(map[string]bool),
		releases:        make(map[string]*repository.WalletRelease),
	}
}

func (m *mockTradeRepo) ApplyTradeFill(ctx context.Context, tradeID, buyOrderID, sellOrderID, fillQty string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applyTradeCalls++

	// Check if already processed (Idempotency)
	if m.processedTrades[tradeID] {
		// Idempotent duplicate: no-op, do NOT enqueue release or transition orders again
		return nil, nil
	}

	// Validate referenced orders BEFORE recording processed_trades
	if m.invalidOrders[buyOrderID] || m.invalidOrders[sellOrderID] {
		return nil, errors.New("semantic order validation failure: invalid order relationship")
	}

	// Record processed trade ONLY after validation passes
	m.processedTrades[tradeID] = true

	var filledOrderIDs []string
	if buyOrderID != "" {
		filledOrderIDs = append(filledOrderIDs, buyOrderID)
		m.releases[buyOrderID] = &repository.WalletRelease{
			ID:      "rel-" + buyOrderID,
			OrderID: buyOrderID,
			Status:  repository.WalletReleasePending,
		}
		m.enqueueCalls++
	}

	return filledOrderIDs, nil
}

func (m *mockTradeRepo) ClaimPendingWalletReleases(ctx context.Context, limit int) ([]*repository.WalletRelease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var pending []*repository.WalletRelease
	for _, r := range m.releases {
		if r.Status == repository.WalletReleasePending {
			pending = append(pending, r)
		}
	}
	return pending, nil
}

func (m *mockTradeRepo) MarkWalletReleaseCompleted(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, r := range m.releases {
		if r.ID == id {
			r.Status = repository.WalletReleaseCompleted
			return nil
		}
	}
	return nil
}

func (m *mockTradeRepo) MarkWalletReleaseCompletedByOrderID(ctx context.Context, orderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if r, ok := m.releases[orderID]; ok {
		r.Status = repository.WalletReleaseCompleted
	}
	return nil
}

func (m *mockTradeRepo) RecordWalletReleaseError(ctx context.Context, id string, errMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, r := range m.releases {
		if r.ID == id {
			r.Attempts++
			r.LastError = &errMsg
			return nil
		}
	}
	return nil
}

type configurableWalletClient struct {
	mu         sync.Mutex
	failOrders map[string]error
	calls      []string
}

func (c *configurableWalletClient) ReleaseFunds(ctx context.Context, orderID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.calls = append(c.calls, orderID)
	if c.failOrders != nil {
		if err, ok := c.failOrders[orderID]; ok {
			return err
		}
	}
	return nil
}

func TestTradeConsumer_WalletReleaseFailure_RetriedByWorkerUntilCompleted(t *testing.T) {
	repo := newMockTradeRepo()
	walletClient := &configurableWalletClient{
		failOrders: make(map[string]error),
	}
	logger := zap.NewNop()

	orderID := uuid.New().String()
	tradeID := uuid.New().String()

	// STEP 1: Simulate Wallet Service failure during initial consumer processing
	walletClient.failOrders[orderID] = errors.New("wallet service connection refused")

	c := consumer.NewConsumer([]string{"localhost:9092"}, "test-group", "trades.settled", repo, walletClient, logger)

	event := consumer.TradeSettledEvent{
		TradeID:    tradeID,
		BuyOrderID: orderID,
		Quantity:   "1.000",
	}
	payload, err := json.Marshal(event)
	require.NoError(t, err)

	msg := kafkago.Message{
		Topic: "trades.settled",
		Value: payload,
	}

	// Consumer processes the message
	err = c.ProcessMessage(context.Background(), msg)
	require.NoError(t, err)

	// INVARIANT 1: ApplyTradeFill enqueued durable wallet release in the same transaction
	repo.mu.Lock()
	releaseRecord, exists := repo.releases[orderID]
	repo.mu.Unlock()
	require.True(t, exists, "wallet release must be enqueued even if immediate ReleaseFunds fails")
	assert.Equal(t, repository.WalletReleasePending, releaseRecord.Status)

	// INVARIANT 2: Initial ReleaseFunds was called once and failed
	walletClient.mu.Lock()
	assert.Len(t, walletClient.calls, 1)
	walletClient.mu.Unlock()

	// STEP 2: Wallet Service recovers. Background worker runs.
	walletClient.mu.Lock()
	delete(walletClient.failOrders, orderID)
	walletClient.mu.Unlock()

	worker := wallet.NewWalletReleaseWorker(repo, walletClient, logger, 50*time.Millisecond)
	worker.ProcessPendingReleases(context.Background())

	// INVARIANT 3: Worker retried ReleaseFunds and marked release COMPLETED
	walletClient.mu.Lock()
	assert.Len(t, walletClient.calls, 2, "worker must retry ReleaseFunds")
	walletClient.mu.Unlock()

	repo.mu.Lock()
	assert.Equal(t, repository.WalletReleaseCompleted, repo.releases[orderID].Status)
	repo.mu.Unlock()
}

func TestTradeConsumer_DuplicateTradeSettled_DoesNotDuplicateWalletRelease(t *testing.T) {
	repo := newMockTradeRepo()
	walletClient := &configurableWalletClient{}
	logger := zap.NewNop()

	orderID := uuid.New().String()
	tradeID := uuid.New().String()

	c := consumer.NewConsumer([]string{"localhost:9092"}, "test-group", "trades.settled", repo, walletClient, logger)

	event := consumer.TradeSettledEvent{
		TradeID:    tradeID,
		BuyOrderID: orderID,
		Quantity:   "1.000",
	}
	payload, err := json.Marshal(event)
	require.NoError(t, err)

	msg := kafkago.Message{
		Topic: "trades.settled",
		Value: payload,
	}

	// First delivery: succeeds and enqueues release
	err = c.ProcessMessage(context.Background(), msg)
	require.NoError(t, err)
	assert.Equal(t, 1, repo.enqueueCalls)

	// Second delivery (duplicate TradeSettled from Kafka):
	err = c.ProcessMessage(context.Background(), msg)
	require.NoError(t, err)

	// INVARIANT: processed_trades conflict must NOT trigger another wallet release enqueue!
	assert.Equal(t, 1, repo.enqueueCalls, "duplicate trade message must not enqueue duplicate wallet releases")
}

func TestTradeConsumer_MalformedEvents_SkippedGracefully(t *testing.T) {
	repo := newMockTradeRepo()
	walletClient := &configurableWalletClient{}
	logger := zap.NewNop()
	c := consumer.NewConsumer([]string{"localhost:9092"}, "test-group", "trades.settled", repo, walletClient, logger)

	validUUID := uuid.New().String()

	testCases := []struct {
		name  string
		event consumer.TradeSettledEvent
	}{
		{
			name: "missing trade_id",
			event: consumer.TradeSettledEvent{
				TradeID:    "",
				BuyOrderID: validUUID,
				Quantity:   "1.000",
			},
		},
		{
			name: "invalid trade_id non-uuid",
			event: consumer.TradeSettledEvent{
				TradeID:    "not-a-uuid",
				BuyOrderID: validUUID,
				Quantity:   "1.000",
			},
		},
		{
			name: "both order IDs empty",
			event: consumer.TradeSettledEvent{
				TradeID:    validUUID,
				BuyOrderID: "",
				SellOrderID: "",
				Quantity:   "1.000",
			},
		},
		{
			name: "invalid buy order ID non-uuid",
			event: consumer.TradeSettledEvent{
				TradeID:    validUUID,
				BuyOrderID: "invalid-buy-uuid",
				Quantity:   "1.000",
			},
		},
		{
			name: "invalid sell order ID non-uuid",
			event: consumer.TradeSettledEvent{
				TradeID:     validUUID,
				SellOrderID: "invalid-sell-uuid",
				Quantity:    "1.000",
			},
		},
		{
			name: "zero quantity",
			event: consumer.TradeSettledEvent{
				TradeID:    validUUID,
				BuyOrderID: validUUID,
				Quantity:   "0.000",
			},
		},
		{
			name: "negative quantity",
			event: consumer.TradeSettledEvent{
				TradeID:    validUUID,
				BuyOrderID: validUUID,
				Quantity:   "-1.500",
			},
		},
		{
			name: "non-numeric quantity",
			event: consumer.TradeSettledEvent{
				TradeID:    validUUID,
				BuyOrderID: validUUID,
				Quantity:   "abc",
			},
		},
		{
			name: "self-match buy_order_id equals sell_order_id",
			event: consumer.TradeSettledEvent{
				TradeID:     validUUID,
				BuyOrderID:  validUUID,
				SellOrderID: validUUID,
				Quantity:    "1.000",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := json.Marshal(tc.event)
			require.NoError(t, err)

			msg := kafkago.Message{
				Topic: "trades.settled",
				Value: payload,
			}

			// Malformed events should return nil (committing offset) and NOT call ApplyTradeFill
			err = c.ProcessMessage(context.Background(), msg)
			require.NoError(t, err)
			assert.Equal(t, 0, repo.applyTradeCalls, "malformed event must not call ApplyTradeFill")
		})
	}
}

func TestTradeConsumer_PoisoningPrevention_InvalidTradeRollsBack_AllowsCorrectedTrade(t *testing.T) {
	repo := newMockTradeRepo()
	walletClient := &configurableWalletClient{}
	logger := zap.NewNop()
	c := consumer.NewConsumer([]string{"localhost:9092"}, "test-group", "trades.settled", repo, walletClient, logger)

	tradeID := uuid.New().String()
	invalidOrderID := uuid.New().String()
	validOrderID := uuid.New().String()

	repo.invalidOrders[invalidOrderID] = true

	// Trade A: trade_id = X with invalid order relationship -> ApplyTradeFill fails and rolls back
	eventA := consumer.TradeSettledEvent{
		TradeID:    tradeID,
		BuyOrderID: invalidOrderID,
		Quantity:   "1.000",
	}
	payloadA, err := json.Marshal(eventA)
	require.NoError(t, err)

	err = c.ProcessMessage(context.Background(), kafkago.Message{Topic: "trades.settled", Value: payloadA})
	require.Error(t, err, "trade with invalid order must fail and trigger consumer error")

	// INVARIANT: processed_trades was NOT recorded for tradeID (no poisoning)
	repo.mu.Lock()
	assert.False(t, repo.processedTrades[tradeID], "processed_trades must NOT be recorded when trade validation rolls back")
	assert.Equal(t, 0, repo.enqueueCalls, "no wallet releases enqueued on failed trade")
	repo.mu.Unlock()

	// Trade B: same trade_id = X, but with corrected valid relationship -> must be accepted!
	eventB := consumer.TradeSettledEvent{
		TradeID:    tradeID,
		BuyOrderID: validOrderID,
		Quantity:   "1.000",
	}
	payloadB, err := json.Marshal(eventB)
	require.NoError(t, err)

	err = c.ProcessMessage(context.Background(), kafkago.Message{Topic: "trades.settled", Value: payloadB})
	require.NoError(t, err, "corrected trade with same trade_id must be accepted successfully")

	repo.mu.Lock()
	assert.True(t, repo.processedTrades[tradeID], "processed_trades must now be recorded")
	assert.Equal(t, 1, repo.enqueueCalls, "wallet release enqueued for valid trade")
	repo.mu.Unlock()

	// Inverse invariant: duplicate Trade B arrives -> idempotent no-op, no second fill or release
	err = c.ProcessMessage(context.Background(), kafkago.Message{Topic: "trades.settled", Value: payloadB})
	require.NoError(t, err, "duplicate trade must be treated as idempotent no-op")

	repo.mu.Lock()
	assert.Equal(t, 1, repo.enqueueCalls, "duplicate trade must NOT enqueue a second wallet release")
	repo.mu.Unlock()
}
