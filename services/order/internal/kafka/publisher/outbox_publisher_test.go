package publisher_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"tradedrift/services/order/internal/kafka/publisher"
	"tradedrift/services/order/internal/repository"
)

type mockOutboxRepo struct {
	mu           sync.Mutex
	events       []*repository.OutboxEvent
	publishedIDs []string
	prunedCount  int64
	prunedBefore time.Time
}

func (m *mockOutboxRepo) GetUnpublishedOutboxEvents(ctx context.Context, limit int) ([]*repository.OutboxEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var pending []*repository.OutboxEvent
	for _, e := range m.events {
		if e.PublishedAt == nil {
			pending = append(pending, e)
			if len(pending) >= limit {
				break
			}
		}
	}
	return pending, nil
}

func (m *mockOutboxRepo) MarkOutboxEventAsPublished(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.events {
		if e.ID == id {
			now := time.Now().UTC()
			e.PublishedAt = &now
			m.publishedIDs = append(m.publishedIDs, id)
			return nil
		}
	}
	return nil
}

func (m *mockOutboxRepo) RecordOutboxPublishError(ctx context.Context, id string, errMsg string) error {
	return nil
}

func (m *mockOutboxRepo) PrunePublishedOutboxEvents(ctx context.Context, olderThan time.Duration, limit int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().UTC().Add(-olderThan)
	m.prunedBefore = cutoff

	var remaining []*repository.OutboxEvent
	var deleted int64
	for _, e := range m.events {
		if e.PublishedAt != nil && e.PublishedAt.Before(cutoff) {
			deleted++
		} else {
			remaining = append(remaining, e)
		}
	}
	m.events = remaining
	m.prunedCount += deleted
	return deleted, nil
}

type mockProducer struct {
	mu        sync.Mutex
	published []string
}

func (p *mockProducer) Publish(ctx context.Context, topic, partitionKey string, payload []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.published = append(p.published, topic+":"+partitionKey)
	return nil
}

func (p *mockProducer) Close() error {
	return nil
}

func TestOutboxPublisher_PublishAndPrune(t *testing.T) {
	now := time.Now().UTC()
	oldTime := now.Add(-48 * time.Hour) // Published 48h ago

	repo := &mockOutboxRepo{
		events: []*repository.OutboxEvent{
			{
				ID:           "event-1-unpub",
				EventType:    "OrderCreated",
				PartitionKey: "BTC-USDT",
				Payload:      []byte(`{"order_id":"1"}`),
				PublishedAt:  nil,
				CreatedAt:    now,
			},
			{
				ID:           "event-2-old-pub",
				EventType:    "OrderCreated",
				PartitionKey: "BTC-USDT",
				Payload:      []byte(`{"order_id":"2"}`),
				PublishedAt:  &oldTime,
				CreatedAt:    oldTime,
			},
		},
	}

	prod := &mockProducer{}
	logger := zap.NewNop()

	pub := publisher.NewOutboxPublisherWithRetention(
		repo,
		prod,
		logger,
		50*time.Millisecond,
		24*time.Hour, // retain 24 hours
		10*time.Minute,
	)

	// Test on-demand pruning
	deleted, err := pub.PrunePublishedEvents(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted, "must prune the 48-hour old published event")
	assert.Equal(t, int64(1), repo.prunedCount)

	// Verify remaining events
	assert.Len(t, repo.events, 1)
	assert.Equal(t, "event-1-unpub", repo.events[0].ID)
}
