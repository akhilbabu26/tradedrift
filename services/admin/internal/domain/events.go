package domain

import "time"

// OutboxStatus represents the delivery state of an outbox event.
type OutboxStatus string

const (
	OutboxStatusPending    OutboxStatus = "PENDING"
	OutboxStatusProcessing OutboxStatus = "PROCESSING"
	OutboxStatusPublished  OutboxStatus = "PUBLISHED"
	OutboxStatusFailed     OutboxStatus = "FAILED"
)

// OutboxEvent is a row in admin_outbox waiting to be published to Kafka.
// It is written atomically inside the same DB transaction as admin_operations.
type OutboxEvent struct {
	ID            string       `json:"id"`           // UUIDv7 event_id
	OperationID   string       `json:"operation_id"`
	Topic         string       `json:"topic"`
	Payload       []byte       `json:"payload"`      // JSON-encoded Kafka message value
	Published     bool         `json:"published"`
	PublishedAt   *time.Time   `json:"published_at,omitempty"`
	Status        OutboxStatus `json:"status"`
	AttemptCount  int          `json:"attempt_count"`
	MaxAttempts   int          `json:"max_attempts"`
	NextAttemptAt time.Time    `json:"next_attempt_at"`
	LastError     *string      `json:"last_error,omitempty"`
	LockedAt      *time.Time   `json:"locked_at,omitempty"`
	LockedBy      *string      `json:"locked_by,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

// Kafka topic constants for admin events.
const (
	TopicUserSuspended   = "admin.user-suspended.v1"
	TopicUserUnsuspended = "admin.user-unsuspended.v1"
	TopicWalletFrozen    = "admin.wallet-frozen.v1"
	TopicWalletUnfrozen  = "admin.wallet-unfrozen.v1"
	TopicMarketHalted    = "admin.market-halted.v1"
	TopicMarketResumed   = "admin.market-resumed.v1"
)

// EventEnvelope is the structured Kafka message value for all admin events.
// Every field is mandatory for distributed tracing correlation.
type EventEnvelope struct {
	EventID     string            `json:"event_id"`     // UUIDv7 unique per event
	OperationID string            `json:"operation_id"`
	RequestID   string            `json:"request_id"`
	AdminID     string            `json:"admin_id"`
	Action      string            `json:"action"`       // matches AuditLog.Action
	TargetID    string            `json:"target_id"`
	Reason      string            `json:"reason"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	OccurredAt  time.Time         `json:"occurred_at"`
}
