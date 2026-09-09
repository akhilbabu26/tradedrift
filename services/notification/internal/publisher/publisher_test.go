package publisher_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"tradedrift/services/notification/internal/model"
	"tradedrift/services/notification/internal/publisher"
)

// mockPublisherRepo tracks repository calls from the publisher.
type mockPublisherRepo struct {
	mu sync.Mutex

	pendingEvents   []*model.OutboxEvent
	publishedIDs    []string
	releasedIDs     []string
	recoveredClaims int64
}

func (m *mockPublisherRepo) CreateWithDedupTx(ctx context.Context, notif *model.Notification, sourceEventID string, outbox *model.OutboxEvent) error {
	return nil
}

func (m *mockPublisherRepo) CreateTradeSettledTx(ctx context.Context, buyerNotif, sellerNotif *model.Notification, tradeID string, buyerOutbox, sellerOutbox *model.OutboxEvent) error {
	return nil
}

func (m *mockPublisherRepo) StageOutboxEvent(ctx context.Context, event *model.OutboxEvent) error {
	return nil
}

func (m *mockPublisherRepo) GetByUserID(ctx context.Context, filter model.PaginationFilter) ([]*model.Notification, error) {
	return nil, nil
}

func (m *mockPublisherRepo) MarkAsRead(ctx context.Context, userID, notificationID string) (*model.Notification, error) {
	return nil, nil
}

func (m *mockPublisherRepo) MarkAllAsRead(ctx context.Context, userID string) (int32, error) {
	return 0, nil
}

func (m *mockPublisherRepo) GetUnreadCount(ctx context.Context, userID string) (int32, error) {
	return 0, nil
}

func (m *mockPublisherRepo) FetchPendingOutbox(ctx context.Context, limit int) ([]*model.OutboxEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pendingEvents) == 0 {
		return nil, nil
	}
	n := limit
	if n > len(m.pendingEvents) {
		n = len(m.pendingEvents)
	}
	res := m.pendingEvents[:n]
	m.pendingEvents = m.pendingEvents[n:]
	return res, nil
}

func (m *mockPublisherRepo) RecoverStaleOutboxClaims(ctx context.Context, timeout time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recoveredClaims, nil
}

func (m *mockPublisherRepo) MarkOutboxPublished(ctx context.Context, id, claimToken string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publishedIDs = append(m.publishedIDs, id)
	return nil
}

func (m *mockPublisherRepo) ReleaseOutboxClaims(ctx context.Context, ids []string, claimToken string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releasedIDs = append(m.releasedIDs, ids...)
	return nil
}

func (m *mockPublisherRepo) IncrementOutboxRetry(_ context.Context, _, _, _ string) error {
	return nil
}

func (m *mockPublisherRepo) GetNotificationByID(_ context.Context, _, _ string) (*model.Notification, error) {
	return nil, nil
}

func (m *mockPublisherRepo) GetNotificationIDByEventID(_ context.Context, _ string) (string, error) {
	return "", nil
}

// mockRedisClient records published messages and simulates successes/failures.
type mockRedisClient struct {
	mu           sync.Mutex
	published    map[string][]string // channel -> payloads
	failAttempts int                 // number of attempts to fail before succeeding
	alwaysFail   bool
	callCount    int
}

func newMockRedis() *mockRedisClient {
	return &mockRedisClient{
		published: make(map[string][]string),
	}
}

func (m *mockRedisClient) Publish(ctx context.Context, channel string, message interface{}) *redis.IntCmd {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callCount++

	cmd := redis.NewIntCmd(ctx)
	if m.alwaysFail {
		cmd.SetErr(errors.New("redis connection refused"))
		return cmd
	}

	if m.failAttempts > 0 {
		m.failAttempts--
		cmd.SetErr(errors.New("redis transient publish timeout"))
		return cmd
	}

	var payloadStr string
	switch v := message.(type) {
	case string:
		payloadStr = v
	case []byte:
		payloadStr = string(v)
	}

	m.published[channel] = append(m.published[channel], payloadStr)
	cmd.SetVal(1) // 1 receiver
	return cmd
}

func TestPublisher_ProcessBatch_HappyPath(t *testing.T) {
	repo := &mockPublisherRepo{
		pendingEvents: []*model.OutboxEvent{
			{
				ID:            "outbox-1",
				EventType:     "NotificationCreated",
				TargetChannel: "user:notifications:user-1",
				Payload:       []byte(`{"event_id":"e1","notification_id":"n1"}`),
			},
			{
				ID:            "outbox-2",
				EventType:     "NotificationCreated",
				TargetChannel: "user:notifications:user-2",
				Payload:       []byte(`{"event_id":"e2","notification_id":"n2"}`),
			},
		},
	}
	rdb := newMockRedis()

	pub := publisher.NewPublisher(repo, rdb, zap.NewNop(), publisher.Config{
		BatchSize: 10,
	})

	count, err := pub.ProcessBatch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Verify Redis publishes
	rdb.mu.Lock()
	assert.Len(t, rdb.published["user:notifications:user-1"], 1)
	assert.Len(t, rdb.published["user:notifications:user-2"], 1)
	rdb.mu.Unlock()

	// Verify PostgreSQL mark published
	repo.mu.Lock()
	assert.Equal(t, []string{"outbox-1", "outbox-2"}, repo.publishedIDs)
	assert.Empty(t, repo.releasedIDs)
	repo.mu.Unlock()
}

func TestPublisher_ProcessBatch_TransientFailureAndRecovery(t *testing.T) {
	repo := &mockPublisherRepo{
		pendingEvents: []*model.OutboxEvent{
			{
				ID:            "outbox-retry-1",
				EventType:     "NotificationCreated",
				TargetChannel: "user:notifications:user-retry",
				Payload:       []byte(`{"event_id":"e-retry","notification_id":"n-retry"}`),
			},
		},
	}
	rdb := newMockRedis()
	rdb.failAttempts = 2 // Fail 2 times, succeed on 3rd attempt

	pub := publisher.NewPublisher(repo, rdb, zap.NewNop(), publisher.Config{
		BatchSize:         10,
		MaxPublishRetries: 3,
	})

	count, err := pub.ProcessBatch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	rdb.mu.Lock()
	assert.Equal(t, 3, rdb.callCount) // 2 failures + 1 success
	assert.Len(t, rdb.published["user:notifications:user-retry"], 1)
	rdb.mu.Unlock()

	repo.mu.Lock()
	assert.Equal(t, []string{"outbox-retry-1"}, repo.publishedIDs)
	repo.mu.Unlock()
}

func TestPublisher_ProcessBatch_PersistentRedisFailureReleasesClaims(t *testing.T) {
	repo := &mockPublisherRepo{
		pendingEvents: []*model.OutboxEvent{
			{
				ID:            "outbox-fail-1",
				EventType:     "NotificationCreated",
				TargetChannel: "user:notifications:user-fail",
				Payload:       []byte(`{"event_id":"e-fail","notification_id":"n-fail"}`),
			},
			{
				ID:            "outbox-fail-2",
				EventType:     "NotificationCreated",
				TargetChannel: "user:notifications:user-fail",
				Payload:       []byte(`{"event_id":"e-fail2","notification_id":"n-fail2"}`),
			},
		},
	}
	rdb := newMockRedis()
	rdb.alwaysFail = true // Redis outage

	pub := publisher.NewPublisher(repo, rdb, zap.NewNop(), publisher.Config{
		BatchSize:         10,
		MaxPublishRetries: 2,
	})

	count, err := pub.ProcessBatch(context.Background())
	assert.Error(t, err)
	assert.Equal(t, 0, count)

	// Both items should be immediately released back to PENDING
	repo.mu.Lock()
	assert.Empty(t, repo.publishedIDs)
	assert.ElementsMatch(t, []string{"outbox-fail-1", "outbox-fail-2"}, repo.releasedIDs)
	repo.mu.Unlock()
}

func TestPublisher_DuplicatePublicationHandling(t *testing.T) {
	// Scenario: Worker claims event, publishes to Redis, but simulates crash before MarkOutboxPublished.
	// Recovery loop resets to PENDING, and subsequent publish re-emits to Redis with identical (event_id, notification_id).
	payload := `{"event_id":"018f6749-event-1","notification_id":"018f6749-notif-1"}`
	repo := &mockPublisherRepo{
		pendingEvents: []*model.OutboxEvent{
			{
				ID:            "outbox-dup-1",
				EventType:     "NotificationCreated",
				TargetChannel: "user:notifications:user-dup",
				Payload:       []byte(payload),
			},
		},
	}
	rdb := newMockRedis()

	pub := publisher.NewPublisher(repo, rdb, zap.NewNop(), publisher.Config{
		BatchSize: 10,
	})

	// First run: successfully published
	count, err := pub.ProcessBatch(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Simulate crash/reclaim: event is queued again for re-delivery
	repo.mu.Lock()
	repo.pendingEvents = []*model.OutboxEvent{
		{
			ID:            "outbox-dup-1",
			EventType:     "NotificationCreated",
			TargetChannel: "user:notifications:user-dup",
			Payload:       []byte(payload),
		},
	}
	repo.mu.Unlock()

	// Second run (re-publish)
	count2, err2 := pub.ProcessBatch(context.Background())
	require.NoError(t, err2)
	assert.Equal(t, 1, count2)

	rdb.mu.Lock()
	publishes := rdb.published["user:notifications:user-dup"]
	assert.Len(t, publishes, 2)
	// Both messages contain the exact same duplicate-tolerant (event_id, notification_id)
	assert.Equal(t, payload, publishes[0])
	assert.Equal(t, payload, publishes[1])
	rdb.mu.Unlock()
}
