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
}
