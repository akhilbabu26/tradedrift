package model

import (
	"time"
)

// Standard notification types
const (
	TypeInfo      = "INFO"
	TypeTradeFill = "TRADE_FILL"
	TypeSystem    = "SYSTEM"
	TypeAccount   = "ACCOUNT"
)

// Standard reference types
const (
	RefTypeTrade   = "TRADE"
	RefTypeOrder   = "ORDER"
	RefTypeDeposit = "DEPOSIT"
)

// Outbox lifecycle states
const (
	OutboxStatusPending    = "PENDING"
	OutboxStatusProcessing = "PROCESSING"
	OutboxStatusProcessed  = "PROCESSED"
	OutboxStatusFailed      = "FAILED"
)

// Notification represents a durable user inbox record in PostgreSQL.
type Notification struct {
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	Title         string     `json:"title"`
	Message       string     `json:"message"`
	Type          string     `json:"type"`
	ReferenceID   string     `json:"reference_id,omitempty"`
	ReferenceType string     `json:"reference_type,omitempty"`
	IsRead        bool       `json:"is_read"`
	ReadAt        *time.Time `json:"read_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// CreateNotificationInput contains the fields needed to persist a notification.
type CreateNotificationInput struct {
	UserID        string
	Title         string
	Message       string
	Type          string
	ReferenceID   string
	ReferenceType string
}

// OutboxEvent represents a staged message awaiting publication to Redis Pub/Sub.
type OutboxEvent struct {
	ID            string
	EventType     string
	Payload       []byte
	TargetChannel string // e.g. "user:notifications:{user_id}" or "user:portfolio:{user_id}"
	Status        string
	RetryCount    int
	LastError     string
	ClaimedAt     *time.Time
	CreatedAt     time.Time
	PublishedAt   *time.Time
}

// RedisEnvelope is the standard envelope published to Redis Pub/Sub for duplicate tolerance.
type RedisEnvelope struct {
	EventID        string    `json:"event_id"`        // Source Kafka/domain event ID
	NotificationID string    `json:"notification_id"` // Individual notification ID
	Type           string    `json:"type"`            // e.g. "notification.created", "portfolio.updated"
	Channel        string    `json:"channel"`
	Timestamp      time.Time `json:"timestamp"`
	Data           any       `json:"data"`
}

// PaginationFilter specifies deterministic keyset cursor parameters.
type PaginationFilter struct {
	UserID     string
	CursorTime *time.Time
	CursorID   string
	Limit      int
	TypeFilter string
}
