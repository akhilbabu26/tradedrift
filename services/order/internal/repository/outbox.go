package repository

import (
	"context"
	"time"
)

type OutboxEvent struct {
	ID           string
	AggregateID  string
	EventType    string
	Payload      []byte
	PartitionKey string
	PublishedAt  *time.Time
	ProcessingAt *time.Time
	Attempts     int
	LastError    *string
	CreatedAt    time.Time
}

type OutboxRepository interface {
	GetUnpublishedOutboxEvents(ctx context.Context, limit int) ([]*OutboxEvent, error)
	MarkOutboxEventAsPublished(ctx context.Context, id string) error
	RecordOutboxPublishError(ctx context.Context, id string, errMsg string) error
}
