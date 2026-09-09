package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"tradedrift/services/notification/internal/model"
	"tradedrift/services/notification/internal/repository"
	"tradedrift/services/notification/internal/service"
)

// mockRepo is a lightweight in-memory mock for testing the service layer logic.
type mockRepo struct {
	createWithDedupTxCalled   bool
	createTradeSettledTxCalled bool
	stageOutboxEventCalled    bool

	savedNotifications []*model.Notification
	savedOutbox        []*model.OutboxEvent
	savedDedupEvents   []string

	returnAlreadyProcessed bool
	returnErr              error
	unreadCount            int32
	markAllCount           int32
}

func (m *mockRepo) CreateWithDedupTx(ctx context.Context, notif *model.Notification, sourceEventID string, outbox *model.OutboxEvent) error {
	if m.returnAlreadyProcessed {
		return repository.ErrAlreadyProcessed
	}
	if m.returnErr != nil {
		return m.returnErr
	}
	m.createWithDedupTxCalled = true
	m.savedNotifications = append(m.savedNotifications, notif)
	m.savedOutbox = append(m.savedOutbox, outbox)
	m.savedDedupEvents = append(m.savedDedupEvents, sourceEventID)
	return nil
}

func (m *mockRepo) CreateTradeSettledTx(ctx context.Context, buyerNotif, sellerNotif *model.Notification, tradeID string, buyerOutbox, sellerOutbox *model.OutboxEvent) error {
	if m.returnAlreadyProcessed {
		return repository.ErrAlreadyProcessed
	}
	if m.returnErr != nil {
		return m.returnErr
	}
	m.createTradeSettledTxCalled = true
	m.savedNotifications = append(m.savedNotifications, buyerNotif, sellerNotif)
	m.savedOutbox = append(m.savedOutbox, buyerOutbox, sellerOutbox)
	m.savedDedupEvents = append(m.savedDedupEvents, tradeID)
	return nil
}

func (m *mockRepo) StageOutboxEvent(ctx context.Context, event *model.OutboxEvent) error {
	if m.returnErr != nil {
		return m.returnErr
	}
	m.stageOutboxEventCalled = true
	m.savedOutbox = append(m.savedOutbox, event)
	return nil
}

func (m *mockRepo) GetByUserID(ctx context.Context, filter model.PaginationFilter) ([]*model.Notification, error) {
	if m.returnErr != nil {
		return nil, m.returnErr
	}
	var res []*model.Notification
	for _, n := range m.savedNotifications {
		if n.UserID == filter.UserID {
			res = append(res, n)
		}
	}
	return res, nil
}

func (m *mockRepo) MarkAsRead(ctx context.Context, userID, notificationID string) (*model.Notification, error) {
	if m.returnErr != nil {
		return nil, m.returnErr
	}
	for _, n := range m.savedNotifications {
		if n.ID == notificationID && n.UserID == userID {
			n.IsRead = true
			return n, nil
		}
	}
	return nil, repository.ErrNotificationNotFound
}

func (m *mockRepo) MarkAllAsRead(ctx context.Context, userID string) (int32, error) {
	if m.returnErr != nil {
		return 0, m.returnErr
	}
	return m.markAllCount, nil
}

func (m *mockRepo) GetUnreadCount(ctx context.Context, userID string) (int32, error) {
	if m.returnErr != nil {
		return 0, m.returnErr
	}
	return m.unreadCount, nil
}

func (m *mockRepo) FetchPendingOutbox(ctx context.Context, batchSize int) ([]*model.OutboxEvent, error) {
	return nil, nil
}

func (m *mockRepo) RecoverStaleOutboxClaims(ctx context.Context, leaseTimeout time.Duration) (int64, error) {
	return 0, nil
}

func (m *mockRepo) MarkOutboxPublished(ctx context.Context, id string) error {
	return nil
}

func (m *mockRepo) ReleaseOutboxClaims(ctx context.Context, eventIDs []string) error {
	return nil
}

func TestService_HandleTradeSettled_CounterpartyPrivacyIsolation(t *testing.T) {
	repo := &mockRepo{}
	svc := service.NewService(repo, zap.NewNop())

	buyerID := "018f6749-0001-7000-8000-000000000001"
	sellerID := "018f6749-0002-7000-8000-000000000002"
	tradeID := "018f6749-0003-7000-8000-000000000003"

	ev := &service.TradeSettledEvent{
		TradeID:      tradeID,
		MarketID:     "BTC-USDT",
		BaseAsset:    "BTC",
		QuoteAsset:   "USDT",
		BuyerUserID:  buyerID,
		SellerUserID: sellerID,
		BuyOrderID:   "buy-order-123",
		SellOrderID:  "sell-order-456",
		Price:        "96500.00",
		Quantity:     "0.0500",
		ExecutedAt:   "2026-09-08T14:30:00Z",
	}

	err := svc.HandleTradeSettled(context.Background(), ev)
	require.NoError(t, err)

	assert.True(t, repo.createTradeSettledTxCalled)
	require.Len(t, repo.savedNotifications, 2)
	require.Len(t, repo.savedOutbox, 2)

	buyerNotif := repo.savedNotifications[0]
	sellerNotif := repo.savedNotifications[1]

	// Check Buyer Notification Invariants
	assert.Equal(t, buyerID, buyerNotif.UserID)
	assert.Equal(t, model.TypeTradeFill, buyerNotif.Type)
	assert.Contains(t, buyerNotif.Message, "BUY order of 0.0500 BTC on BTC-USDT filled at 96500.00 USDT")
	// Must NOT leak Seller's ID or Sell Order ID
	assert.False(t, strings.Contains(buyerNotif.Message, sellerID), "buyer message leaked seller user ID")
	assert.False(t, strings.Contains(buyerNotif.Message, "sell-order-456"), "buyer message leaked sell order ID")

	// Check Seller Notification Invariants
	assert.Equal(t, sellerID, sellerNotif.UserID)
	assert.Equal(t, model.TypeTradeFill, sellerNotif.Type)
	assert.Contains(t, sellerNotif.Message, "SELL order of 0.0500 BTC on BTC-USDT filled at 96500.00 USDT")
	// Must NOT leak Buyer's ID or Buy Order ID
	assert.False(t, strings.Contains(sellerNotif.Message, buyerID), "seller message leaked buyer user ID")
	assert.False(t, strings.Contains(sellerNotif.Message, "buy-order-123"), "seller message leaked buy order ID")

	// Check Outbox Events and Target Channels
	buyerOutbox := repo.savedOutbox[0]
	assert.Equal(t, "user:notifications:"+buyerID, buyerOutbox.TargetChannel)
	var buyerEnv model.RedisEnvelope
	require.NoError(t, json.Unmarshal(buyerOutbox.Payload, &buyerEnv))
	assert.Equal(t, tradeID, buyerEnv.EventID)
	assert.Equal(t, buyerNotif.ID, buyerEnv.NotificationID)

	sellerOutbox := repo.savedOutbox[1]
	assert.Equal(t, "user:notifications:"+sellerID, sellerOutbox.TargetChannel)
	var sellerEnv model.RedisEnvelope
	require.NoError(t, json.Unmarshal(sellerOutbox.Payload, &sellerEnv))
	assert.Equal(t, tradeID, sellerEnv.EventID)
	assert.Equal(t, sellerNotif.ID, sellerEnv.NotificationID)
}

func TestService_HandleTradeSettled_IdempotentSkip(t *testing.T) {
	repo := &mockRepo{returnAlreadyProcessed: true}
	svc := service.NewService(repo, zap.NewNop())

	ev := &service.TradeSettledEvent{
		TradeID:      "trade-dup-123",
		MarketID:     "BTC-USDT",
		BuyerUserID:  "buyer-1",
		SellerUserID: "seller-1",
	}

	// Should not return an error when repo returns ErrAlreadyProcessed
	err := svc.HandleTradeSettled(context.Background(), ev)
	assert.NoError(t, err)
}

func TestService_HandleOrderCancelled(t *testing.T) {
	repo := &mockRepo{}
	svc := service.NewService(repo, zap.NewNop())

	ev := &service.OrderCancelledEvent{
		EventID:  "cancel-event-1",
		OrderID:  "order-999",
		UserID:   "user-cancelled-1",
		MarketID: "ETH-USDT",
		Reason:   "insufficient balance for fee",
	}

	err := svc.HandleOrderCancelled(context.Background(), ev)
	require.NoError(t, err)

	assert.True(t, repo.createWithDedupTxCalled)
	require.Len(t, repo.savedNotifications, 1)
	notif := repo.savedNotifications[0]
	assert.Equal(t, "user-cancelled-1", notif.UserID)
	assert.Equal(t, model.TypeSystem, notif.Type)
	assert.Contains(t, notif.Message, "insufficient balance for fee")

	require.Len(t, repo.savedOutbox, 1)
	outbox := repo.savedOutbox[0]
	assert.Equal(t, "user:notifications:user-cancelled-1", outbox.TargetChannel)

	var env model.RedisEnvelope
	require.NoError(t, json.Unmarshal(outbox.Payload, &env))
	assert.Equal(t, "cancel-event-1", env.EventID)
	assert.Equal(t, notif.ID, env.NotificationID)
}

func TestService_HandlePortfolioUpdated_EphemeralNoInbox(t *testing.T) {
	repo := &mockRepo{}
	svc := service.NewService(repo, zap.NewNop())

	ev := &service.PortfolioUpdatedEvent{
		EventID:     "pf-event-100",
		UserID:      "user-pf-1",
		TotalValue:  "50000.00",
		CashBalance: "12000.00",
	}

	err := svc.HandlePortfolioUpdated(context.Background(), ev)
	require.NoError(t, err)

	assert.True(t, repo.stageOutboxEventCalled)
	// Must NOT insert into notifications inbox
	assert.Empty(t, repo.savedNotifications)
	require.Len(t, repo.savedOutbox, 1)

	outbox := repo.savedOutbox[0]
	assert.Equal(t, "user:portfolio:user-pf-1", outbox.TargetChannel)

	var env model.RedisEnvelope
	require.NoError(t, json.Unmarshal(outbox.Payload, &env))
	assert.Equal(t, "pf-event-100", env.EventID)
	assert.Empty(t, env.NotificationID) // Empty because there is no persistent notification row
	assert.Equal(t, "portfolio.updated", env.Type)
}

func TestService_CreateNotification_Validation(t *testing.T) {
	repo := &mockRepo{}
	svc := service.NewService(repo, zap.NewNop())
	ctx := context.Background()

	_, err := svc.CreateNotification(ctx, model.CreateNotificationInput{})
	assert.ErrorIs(t, err, service.ErrInvalidUserID)

	_, err = svc.CreateNotification(ctx, model.CreateNotificationInput{UserID: "u1"})
	assert.ErrorIs(t, err, service.ErrEmptyTitle)

	_, err = svc.CreateNotification(ctx, model.CreateNotificationInput{UserID: "u1", Title: "Notice"})
	assert.ErrorIs(t, err, service.ErrEmptyMessage)

	notif, err := svc.CreateNotification(ctx, model.CreateNotificationInput{
		UserID:  "u1",
		Title:   "Notice",
		Message: "Account verified",
		Type:    model.TypeAccount,
	})
	require.NoError(t, err)
	assert.Equal(t, "u1", notif.UserID)
	assert.Equal(t, model.TypeAccount, notif.Type)
	assert.Equal(t, "Notice", notif.Title)
}
