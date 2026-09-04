package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
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
	// WithTx binds the repository to an active PostgreSQL transaction.
	WithTx(tx pgx.Tx) OutboxRepository

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

	// ReleaseClaim releases an in-flight claimed event back to 'PENDING' status and clears claimed_at.
	// Called when a publisher encounters a transient error and halts batch processing, ensuring
	// the event is immediately retried on the next poll cycle without waiting for lease timeout.
	ReleaseClaim(ctx context.Context, id string) error

	// ReleaseClaims releases multiple in-flight claimed events back to 'PENDING' status and clears claimed_at.
	ReleaseClaims(ctx context.Context, ids []string) error
}


