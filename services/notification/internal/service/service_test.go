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
	for _, id := range m.savedDedupEvents {
		if id == sourceEventID {
			return repository.ErrAlreadyProcessed
		}
	}
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

func (m *mockRepo) MarkOutboxPublished(ctx context.Context, id, claimToken string) error {
	return nil
}

func (m *mockRepo) ReleaseOutboxClaims(ctx context.Context, eventIDs []string, claimToken string) error {
	return nil
}

func (m *mockRepo) PurgeProcessedOutbox(ctx context.Context, targetChannelPrefix string, cutoff time.Time, limit int) (int64, error) {
	return 0, nil
}

func (m *mockRepo) IncrementOutboxRetry(_ context.Context, _, _, _ string) error {
	return nil
}

func (m *mockRepo) GetNotificationByID(ctx context.Context, userID, notificationID string) (*model.Notification, error) {
	for _, n := range m.savedNotifications {
		if n.ID == notificationID && n.UserID == userID {
			return n, nil
		}
	}
	return nil, repository.ErrNotificationNotFound
}

func (m *mockRepo) GetNotificationIDByEventID(ctx context.Context, eventID string) (string, error) {
	for i, key := range m.savedDedupEvents {
		if key == eventID && i < len(m.savedNotifications) {
			return m.savedNotifications[i].ID, nil
		}
	}
	return "", repository.ErrNotificationNotFound
}

func TestService_HandleTradeSettled_CounterpartyPrivacyIsolation(t *testing.T) {
	repo := &mockRepo{}
	svc := service.NewService(repo, zap.NewNop())

	eventID := "018f6749-0000-7000-8000-000000000000"
	buyerID := "018f6749-0001-7000-8000-000000000001"
	sellerID := "018f6749-0002-7000-8000-000000000002"
	tradeID := "018f6749-0003-7000-8000-000000000003"

	buyOrderID  := "018f6749-0004-7000-8000-000000000004"
	sellOrderID := "018f6749-0005-7000-8000-000000000005"

	ev := &service.TradeSettledEvent{
		EventID:      eventID,
		TradeID:      tradeID,
		MarketID:     "BTC-USDT",
		BaseAsset:    "BTC",
		QuoteAsset:   "USDT",
		BuyerUserID:  buyerID,
		SellerUserID: sellerID,
		BuyOrderID:   buyOrderID,
		SellOrderID:  sellOrderID,
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
	assert.False(t, strings.Contains(buyerNotif.Message, sellOrderID), "buyer message leaked sell order ID")

	// Check Seller Notification Invariants
	assert.Equal(t, sellerID, sellerNotif.UserID)
	assert.Equal(t, model.TypeTradeFill, sellerNotif.Type)
	assert.Contains(t, sellerNotif.Message, "SELL order of 0.0500 BTC on BTC-USDT filled at 96500.00 USDT")
	// Must NOT leak Buyer's ID or Buy Order ID
	assert.False(t, strings.Contains(sellerNotif.Message, buyerID), "seller message leaked buyer user ID")
	assert.False(t, strings.Contains(sellerNotif.Message, buyOrderID), "seller message leaked buy order ID")

	// Check Outbox Events and Target Channels
	// EventID in the Redis envelope must be ev.EventID (the source domain event ID),
	// not tradeID — one source event creates two notifications with different notification_ids.
	buyerOutbox := repo.savedOutbox[0]
	assert.Equal(t, "user:notifications:"+buyerID, buyerOutbox.TargetChannel)
	var buyerEnv model.RedisEnvelope
	require.NoError(t, json.Unmarshal(buyerOutbox.Payload, &buyerEnv))
	assert.Equal(t, eventID, buyerEnv.EventID) // source event ID
	assert.Equal(t, buyerNotif.ID, buyerEnv.NotificationID)

	sellerOutbox := repo.savedOutbox[1]
	assert.Equal(t, "user:notifications:"+sellerID, sellerOutbox.TargetChannel)
	var sellerEnv model.RedisEnvelope
	require.NoError(t, json.Unmarshal(sellerOutbox.Payload, &sellerEnv))
	assert.Equal(t, eventID, sellerEnv.EventID) // same source event ID
	assert.Equal(t, sellerNotif.ID, sellerEnv.NotificationID)
}

func TestService_HandleTradeSettled_IdempotentSkip(t *testing.T) {
	repo := &mockRepo{returnAlreadyProcessed: true}
	svc := service.NewService(repo, zap.NewNop())

	ev := &service.TradeSettledEvent{
		EventID:      "018f6749-aaaa-7000-8000-000000000000",
		TradeID:      "018f6749-bbbb-7000-8000-000000000000",
		MarketID:     "BTC-USDT",
		Price:        "64000.00",
		Quantity:     "1.0",
		BuyerUserID:  "018f6749-0001-7000-8000-000000000001",
		SellerUserID: "018f6749-0002-7000-8000-000000000002",
	}

	// Should not return an error when repo returns ErrAlreadyProcessed
	err := svc.HandleTradeSettled(context.Background(), ev)
	assert.NoError(t, err)
}

func TestService_EventPayloadValidation(t *testing.T) {
	baseEvent := func() service.TradeSettledEvent {
		return service.TradeSettledEvent{
			EventID:      "018f6749-aaaa-7000-8000-000000000000",
			TradeID:      "018f6749-bbbb-7000-8000-000000000000",
			MarketID:     "BTC-USDT",
			Price:        "64000.00",
			Quantity:     "1.50",
			BuyerUserID:  "018f6749-0001-7000-8000-000000000001",
			SellerUserID: "018f6749-0002-7000-8000-000000000002",
		}
	}

	t.Run("valid event passes", func(t *testing.T) {
		ev := baseEvent()
		assert.NoError(t, ev.Validate())
	})

	t.Run("market_id structural validation", func(t *testing.T) {
		invalidMarkets := []string{"BTC", "-BTC", "BTC-", "BTC-USDT-PERP", "btc-usdt", ""}
		for _, m := range invalidMarkets {
			ev := baseEvent()
			ev.MarketID = m
			assert.Error(t, ev.Validate(), "expected error for invalid market_id %q", m)
		}
	})

	t.Run("price validation preserves decimal string and rejects non-positive", func(t *testing.T) {
		validPrices := []string{"100", "0.00001", "96500.50"}
		for _, p := range validPrices {
			ev := baseEvent()
			ev.Price = p
			assert.NoError(t, ev.Validate(), "expected valid price for %q", p)
		}

		invalidPrices := []string{"0", "0.0", "0.0000", "-100", "-0.01", "abc", "1e5", ""}
		for _, p := range invalidPrices {
			ev := baseEvent()
			ev.Price = p
			assert.Error(t, ev.Validate(), "expected error for invalid price %q", p)
		}
	})

	t.Run("quantity validation preserves decimal string and rejects non-positive", func(t *testing.T) {
		validQuantities := []string{"1", "0.001", "100.5"}
		for _, q := range validQuantities {
			ev := baseEvent()
			ev.Quantity = q
			assert.NoError(t, ev.Validate(), "expected valid quantity for %q", q)
		}

		invalidQuantities := []string{"0", "0.00", "-1", "xyz", ""}
		for _, q := range invalidQuantities {
			ev := baseEvent()
			ev.Quantity = q
			assert.Error(t, ev.Validate(), "expected error for invalid quantity %q", q)
		}
	})
}

func TestService_HandleOrderCancelled(t *testing.T) {
	repo := &mockRepo{}
	svc := service.NewService(repo, zap.NewNop())

	cancelEventID := "018f6749-cccc-7000-8000-000000000001"
	cancelOrderID := "018f6749-cccc-7000-8000-000000000002"
	cancelUserID  := "018f6749-0010-7000-8000-000000000010"

	ev := &service.OrderCancelledEvent{
		EventID:  cancelEventID,
		OrderID:  cancelOrderID,
		UserID:   cancelUserID,
		MarketID: "ETH-USDT",
		Reason:   "insufficient balance for fee",
	}

	err := svc.HandleOrderCancelled(context.Background(), ev)
	require.NoError(t, err)

	assert.True(t, repo.createWithDedupTxCalled)
	require.Len(t, repo.savedNotifications, 1)
	notif := repo.savedNotifications[0]
	assert.Equal(t, cancelUserID, notif.UserID)
	assert.Equal(t, model.TypeSystem, notif.Type)
	assert.Contains(t, notif.Message, "insufficient balance for fee")

	require.Len(t, repo.savedOutbox, 1)
	outbox := repo.savedOutbox[0]
	assert.Equal(t, "user:notifications:"+cancelUserID, outbox.TargetChannel)

	var env model.RedisEnvelope
	require.NoError(t, json.Unmarshal(outbox.Payload, &env))
	assert.Equal(t, cancelEventID, env.EventID) // source event ID, not order ID
	assert.Equal(t, notif.ID, env.NotificationID)
}

func TestService_HandlePortfolioUpdated_EphemeralNoInbox(t *testing.T) {
	repo := &mockRepo{}
	svc := service.NewService(repo, zap.NewNop())

	pfEventID := "018f6749-eeee-7000-8000-000000000100"
	pfUserID  := "018f6749-0020-7000-8000-000000000020"
	ev := &service.PortfolioUpdatedEvent{
		EventID:     pfEventID,
		UserID:      pfUserID,
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
	assert.Equal(t, "user:portfolio:"+pfUserID, outbox.TargetChannel)

	var env model.RedisEnvelope
	require.NoError(t, json.Unmarshal(outbox.Payload, &env))
	assert.Equal(t, pfEventID, env.EventID)
	assert.Empty(t, env.NotificationID) // Empty because there is no persistent notification row
	assert.Equal(t, "portfolio.updated", env.Type)
}

func TestService_CreateNotification_Validation(t *testing.T) {
	repo := &mockRepo{}
	svc := service.NewService(repo, zap.NewNop())
	ctx := context.Background()

	// Empty user ID
	_, err := svc.CreateNotification(ctx, model.CreateNotificationInput{})
	assert.ErrorIs(t, err, service.ErrInvalidUserID)

	// Non-UUID user ID is also rejected (validateUUID)
	_, err = svc.CreateNotification(ctx, model.CreateNotificationInput{UserID: "not-a-uuid"})
	assert.Error(t, err)

	// Valid UUID but missing title
	validUID := "018f6749-0030-7000-8000-000000000030"
	_, err = svc.CreateNotification(ctx, model.CreateNotificationInput{UserID: validUID})
	assert.ErrorIs(t, err, service.ErrEmptyTitle)

	_, err = svc.CreateNotification(ctx, model.CreateNotificationInput{UserID: validUID, Title: "Notice"})
	assert.ErrorIs(t, err, service.ErrEmptyMessage)

	// Invalid notification type
	_, err = svc.CreateNotification(ctx, model.CreateNotificationInput{
		UserID:  validUID,
		Title:   "Notice",
		Message: "Account verified",
		Type:    "UNKNOWN_TYPE",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid notification type")

	// Invalid reference_id (non-UUID)
	_, err = svc.CreateNotification(ctx, model.CreateNotificationInput{
		UserID:      validUID,
		Title:       "Notice",
		Message:     "Account verified",
		ReferenceID: "not-a-uuid",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reference_id")

	// Invalid reference_type
	_, err = svc.CreateNotification(ctx, model.CreateNotificationInput{
		UserID:        validUID,
		Title:         "Notice",
		Message:       "Account verified",
		ReferenceType: "INVALID_REF_TYPE",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid reference_type")

	notif, err := svc.CreateNotification(ctx, model.CreateNotificationInput{
		UserID:  validUID,
		Title:   "Notice",
		Message: "Account verified",
		Type:    model.TypeAccount,
	})
	require.NoError(t, err)
	assert.Equal(t, validUID, notif.UserID)
	assert.Equal(t, model.TypeAccount, notif.Type)
	assert.Equal(t, "Notice", notif.Title)
}

func TestService_CreateNotification_IdempotencyRetry(t *testing.T) {
	repo := &mockRepo{}
	svc := service.NewService(repo, zap.NewNop())
	ctx := context.Background()

	validUID := "018f6749-0030-7000-8000-000000000030"
	idempotencyKey := "018f6749-aaaa-7000-8000-000000000099"

	input := model.CreateNotificationInput{
		UserID:         validUID,
		Title:          "Deposit Confirmed",
		Message:        "Your 500 USDT deposit was successful.",
		Type:           model.TypeAccount,
		IdempotencyKey: idempotencyKey,
	}

	// 1. First call: creates the notification
	notif1, err := svc.CreateNotification(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, notif1)
	assert.Equal(t, validUID, notif1.UserID)
	assert.Equal(t, "Deposit Confirmed", notif1.Title)
	assert.NotEmpty(t, notif1.ID)

	// 2. Second call with exact same idempotency_key: returns the existing notification
	notif2, err := svc.CreateNotification(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, notif2)
	assert.Equal(t, notif1.ID, notif2.ID, "idempotent retry must return identical notification ID")
	assert.Equal(t, notif1.Title, notif2.Title)
	assert.Equal(t, notif1.Message, notif2.Message)

	// In-memory repo must still only have 1 notification saved
	assert.Len(t, repo.savedNotifications, 1)

	// 3. Invalid idempotency key (non-UUID) should fail validation immediately
	invalidInput := input
	invalidInput.IdempotencyKey = "invalid-uuid-format"
	_, err = svc.CreateNotification(ctx, invalidInput)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "idempotency_key")
}
