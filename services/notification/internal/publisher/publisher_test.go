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
	purgeCalls      []purgeCall
	purgeReturns    []int64
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

type purgeCall struct {
	targetChannelPrefix string
	cutoff              time.Time
	limit               int
}

func (m *mockPublisherRepo) PurgeProcessedOutbox(ctx context.Context, targetChannelPrefix string, cutoff time.Time, limit int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purgeCalls = append(m.purgeCalls, purgeCall{
		targetChannelPrefix: targetChannelPrefix,
		cutoff:              cutoff,
		limit:               limit,
	})
	if m.purgeReturns != nil {
		if len(m.purgeReturns) > 0 {
			ret := m.purgeReturns[0]
			m.purgeReturns = m.purgeReturns[1:]
			return ret, nil
		}
	}
	return 0, nil
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

func TestPublisher_RetentionCleanupExecution(t *testing.T) {
	repo := &mockPublisherRepo{
		// Simulate first portfolio batch returning 1000 (batchSize -> triggers loop), second returning 150 (done)
		// Then notification batch returning 50 (done)
		purgeReturns: []int64{1000, 150, 50},
	}
	rdb := newMockRedis()

	pub := publisher.NewPublisher(repo, rdb, zap.NewNop(), publisher.Config{
		CleanupInterval:    50 * time.Millisecond,
		PortfolioRetention: 24 * time.Hour,
		NotifRetention:     7 * 24 * time.Hour,
		CleanupBatchSize:   1000,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Run publisher in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- pub.Start(ctx)
	}()

	// Wait briefly for initial startup retention cleanup to run
	time.Sleep(50 * time.Millisecond)
	cancel()

	err := <-errCh
	require.ErrorIs(t, err, context.Canceled)

	repo.mu.Lock()
	defer repo.mu.Unlock()

	// Should have executed at least:
	// 1. portfolio chunk 1
	// 2. portfolio chunk 2
	// 3. notification chunk 1
	require.GreaterOrEqual(t, len(repo.purgeCalls), 3)

	// First call was portfolio prefix with 24h cutoff
	assert.Equal(t, "user:portfolio:", repo.purgeCalls[0].targetChannelPrefix)
	assert.Equal(t, 1000, repo.purgeCalls[0].limit)
	assert.WithinDuration(t, time.Now().Add(-24*time.Hour), repo.purgeCalls[0].cutoff, 5*time.Second)

	// Second call was portfolio prefix continuing drain
	assert.Equal(t, "user:portfolio:", repo.purgeCalls[1].targetChannelPrefix)

	// Third call was notification prefix with 7-day cutoff
	assert.Equal(t, "user:notifications:", repo.purgeCalls[2].targetChannelPrefix)
	assert.WithinDuration(t, time.Now().Add(-7*24*time.Hour), repo.purgeCalls[2].cutoff, 5*time.Second)
}

func TestPublisher_StartAndCancelCleanShutdown(t *testing.T) {
	repo := &mockPublisherRepo{}
	rdb := newMockRedis()

	pub := publisher.NewPublisher(repo, rdb, zap.NewNop(), publisher.Config{
		PollInterval:     10 * time.Millisecond,
		IdleInterval:     10 * time.Millisecond,
		RecoveryInterval: 20 * time.Millisecond,
		CleanupInterval:  20 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- pub.Start(ctx)
	}()

	// Let tickers fire
	time.Sleep(30 * time.Millisecond)

	// Cancel and measure prompt exit
	start := time.Now()
	cancel()

	select {
	case err := <-errCh:
		assert.ErrorIs(t, err, context.Canceled)
		assert.Less(t, time.Since(start), 200*time.Millisecond, "Start must return promptly on cancellation")
	case <-time.After(1 * time.Second):
		t.Fatal("Publisher did not stop within 1 second after context cancellation")
	}
}

