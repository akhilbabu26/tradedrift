package repository

import (
	"context"
	"time"
)

// OutboxEvent is a pending event to be published to Kafka.
type OutboxEvent struct {
	ID           string
	AggregateID  string
	EventType    string
	Payload      []byte // Raw JSON
	PartitionKey string
	CreatedAt    time.Time
}

// OutboxRepository defines the persistence contract for the transactional outbox.
type OutboxRepository interface {
	// Insert writes an outbox event within the caller's transaction.
	// Must be called inside an existing DB transaction so the event
	// is committed atomically with the balance changes.
	Insert(ctx context.Context, event *OutboxEvent) error

	// FetchPending returns up to `limit` PENDING outbox events ordered by created_at ASC,
	// locked with FOR UPDATE SKIP LOCKED to prevent concurrent publisher instances
	// from picking the same row.
	FetchPending(ctx context.Context, limit int) ([]*OutboxEvent, error)

	// MarkPublished sets status=PROCESSED and published_at=NOW() for the given event ID.
	// Called after a successful Kafka write.
	MarkPublished(ctx context.Context, id string) error

	// MarkFailed sets status=FAILED and records the failure reason.
	// Called when the Kafka write fails and the event should not be retried.
	MarkFailed(ctx context.Context, id string, reason string) error
}

