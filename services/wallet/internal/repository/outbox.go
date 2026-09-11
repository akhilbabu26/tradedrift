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
	ClaimToken   string
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
	// Strictly verifies claim_token to fence against stale worker completions.
	MarkPublished(ctx context.Context, id string, claimToken string) error

	// MarkFailed sets status=FAILED and records the failure reason.
	// Strictly verifies claim_token to fence against stale worker failures.
	MarkFailed(ctx context.Context, id string, reason string, claimToken string) error

	// ReleaseClaim releases an in-flight claimed event back to 'PENDING' status and clears claimed_at.
	// Strictly verifies claim_token to fence against stale worker releases.
	ReleaseClaim(ctx context.Context, id string, claimToken string) error

	// ReleaseClaims releases multiple in-flight claimed events back to 'PENDING' status and clears claimed_at.
	// Strictly verifies claim_token to fence against stale worker releases.
	ReleaseClaims(ctx context.Context, ids []string, claimToken string) error
}


