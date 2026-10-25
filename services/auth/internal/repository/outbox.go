package repository

import (
	"context"
	"time"
)

// OutboxEvent represents a record in the transactional outbox table.
type OutboxEvent struct {
	ID            string
	AggregateType string
	AggregateID   string
	EventType     string
	Payload       []byte
	Status        string // 'PENDING', 'PROCESSED', 'FAILED'
	FailedReason  string
	CreatedAt     time.Time
	PublishedAt   *time.Time
}

// OutboxRepository defines persistence operations for transactional outbox records.
type OutboxRepository interface {
	// ClaimPendingEvents atomically selects and claims pending events using FOR UPDATE SKIP LOCKED
	// and leases them for in-flight processing by advancing next_attempt_at by 2 minutes.
	ClaimPendingEvents(ctx context.Context, eventType string, limit int) ([]*OutboxEvent, error)

	// GetPendingEvents retrieves up to limit pending events of the specified eventType.
	GetPendingEvents(ctx context.Context, eventType string, limit int) ([]*OutboxEvent, error)

	// MarkProcessed updates the event status to PROCESSED with published_at set to current time.
	MarkProcessed(ctx context.Context, id string) error

	// RecordFailure updates failed_reason while keeping status PENDING.
	RecordFailure(ctx context.Context, id string, reason string) error

	// RecordFailureWithBackoff schedules an exponential backoff retry or marks the event FAILED if maxAttempts is reached.
	RecordFailureWithBackoff(ctx context.Context, id string, attempts int, maxAttempts int, reason string) error
}
