package kafka_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	notificationkafka "tradedrift/services/notification/internal/kafka"
	"tradedrift/services/notification/internal/service"
)

type mockMessageReader struct {
	mu          sync.Mutex
	messages    []kafka.Message
	committed   []kafka.Message
	fetchIndex  int
	blockChan   chan struct{}
}

func newMockReader(msgs []kafka.Message) *mockMessageReader {
	return &mockMessageReader{
		messages:  msgs,
		blockChan: make(chan struct{}),
	}
}

func (m *mockMessageReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	m.mu.Lock()
	if m.fetchIndex < len(m.messages) {
		msg := m.messages[m.fetchIndex]
		m.fetchIndex++
		m.mu.Unlock()
		return msg, nil
	}
	m.mu.Unlock()

	// Block until context cancelled
	select {
	case <-ctx.Done():
		return kafka.Message{}, ctx.Err()
	case <-m.blockChan:
		return kafka.Message{}, ioEOF()
	}
}

func ioEOF() error {
	return errors.New("EOF")
}

func (m *mockMessageReader) CommitMessages(ctx context.Context, msgs ...kafka.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.committed = append(m.committed, msgs...)
	return nil
}

func (m *mockMessageReader) Close() error {
	close(m.blockChan)
	return nil
}

type mockDLQWriter struct {
	mu       sync.Mutex
	messages []kafka.Message
}

func (m *mockDLQWriter) WriteMessages(ctx context.Context, msgs ...kafka.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, msgs...)
	return nil
}

func (m *mockDLQWriter) Close() error {
	return nil
}

type mockNotificationService struct {
	mu                   sync.Mutex
	tradeSettledCalled   bool
	orderCancelledCalled bool
	portfolioUpdatedCalled bool
	returnErr            error
}

func (m *mockNotificationService) HandleTradeSettled(ctx context.Context, ev *service.TradeSettledEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tradeSettledCalled = true
	return m.returnErr
}

func (m *mockNotificationService) HandleOrderCancelled(ctx context.Context, ev *service.OrderCancelledEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orderCancelledCalled = true
	return m.returnErr
}

func (m *mockNotificationService) HandlePortfolioUpdated(ctx context.Context, ev *service.PortfolioUpdatedEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.portfolioUpdatedCalled = true
	return m.returnErr
}

func TestConsumer_TradeSettled_CommitAfterSuccess(t *testing.T) {
	tradeMsg := kafka.Message{
		Topic:     notificationkafka.TopicTradesSettled,
		Partition: 0,
		Offset:    101,
		Value: []byte(`{
			"trade_id": "018f6749-trade-1",
			"market_id": "BTC-USDT",
			"buyer_user_id": "buyer-1",
			"seller_user_id": "seller-1",
			"price": "96500.00",
			"quantity": "0.1000"
		}`),
	}

	reader := newMockReader([]kafka.Message{tradeMsg})
	dlq := &mockDLQWriter{}
	svc := &mockNotificationService{}

	consumer := notificationkafka.NewConsumerWithMocks(
		reader,
		newMockReader(nil),
		newMockReader(nil),
		dlq,
		svc,
		zap.NewNop(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	consumer.Start(ctx)
	<-ctx.Done()

	svc.mu.Lock()
	assert.True(t, svc.tradeSettledCalled)
	svc.mu.Unlock()

	reader.mu.Lock()
	require.Len(t, reader.committed, 1)
	assert.Equal(t, int64(101), reader.committed[0].Offset)
	reader.mu.Unlock()

	dlq.mu.Lock()
	assert.Empty(t, dlq.messages)
	dlq.mu.Unlock()
}

func TestConsumer_TradeSettled_NoCommitOnServiceError(t *testing.T) {
	tradeMsg := kafka.Message{
		Topic:     notificationkafka.TopicTradesSettled,
		Partition: 0,
		Offset:    202,
		Value: []byte(`{
			"trade_id": "018f6749-trade-2",
			"market_id": "BTC-USDT",
			"buyer_user_id": "buyer-2",
			"seller_user_id": "seller-2"
		}`),
	}

	reader := newMockReader([]kafka.Message{tradeMsg})
	dlq := &mockDLQWriter{}
	svc := &mockNotificationService{returnErr: errors.New("database connection down")}

	consumer := notificationkafka.NewConsumerWithMocks(
		reader,
		newMockReader(nil),
		newMockReader(nil),
		dlq,
		svc,
		zap.NewNop(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	consumer.Start(ctx)
	<-ctx.Done()

	svc.mu.Lock()
	assert.True(t, svc.tradeSettledCalled)
	svc.mu.Unlock()

	// Offset must NOT be committed on service error
	reader.mu.Lock()
	assert.Empty(t, reader.committed)
	reader.mu.Unlock()
}

func TestConsumer_PoisonMessage_RoutesToDLQAndCommits(t *testing.T) {
	poisonMsg := kafka.Message{
		Topic:     notificationkafka.TopicOrdersCancelled,
		Partition: 1,
		Offset:    303,
		Value:     []byte(`{ corrupt-json-not-valid }`),
	}

	reader := newMockReader([]kafka.Message{poisonMsg})
	dlq := &mockDLQWriter{}
	svc := &mockNotificationService{}

	consumer := notificationkafka.NewConsumerWithMocks(
		newMockReader(nil),
		reader,
		newMockReader(nil),
		dlq,
		svc,
		zap.NewNop(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	consumer.Start(ctx)
	<-ctx.Done()

	// Service should not be called with garbage
	svc.mu.Lock()
	assert.False(t, svc.orderCancelledCalled)
	svc.mu.Unlock()

	// DLQ must receive the poisoned message
	dlq.mu.Lock()
	require.Len(t, dlq.messages, 1)
	assert.Equal(t, poisonMsg.Value, dlq.messages[0].Value)
	dlq.mu.Unlock()

	// Offset must be committed so queue doesn't block
	reader.mu.Lock()
	require.Len(t, reader.committed, 1)
	assert.Equal(t, int64(303), reader.committed[0].Offset)
	reader.mu.Unlock()
}
