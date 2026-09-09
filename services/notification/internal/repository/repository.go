package repository

import (
	"context"
	"errors"
	"time"

	"tradedrift/services/notification/internal/model"
)

var (
	ErrAlreadyProcessed     = errors.New("event has already been processed")
	ErrNotificationNotFound = errors.New("notification not found")
)

// NotificationRepository defines the data access contract for inboxes and outbox events.
type NotificationRepository interface {
	// CreateWithDedupTx atomically inserts an event deduplication row, a notification, and an outbox record.
	CreateWithDedupTx(ctx context.Context, notif *model.Notification, sourceEventID string, outbox *model.OutboxEvent) error

	// CreateTradeSettledTx atomically inserts deduplication for sourceEventID, dual counterparty notifications, and dual outbox records.
	CreateTradeSettledTx(
		ctx context.Context,
		buyerNotif *model.Notification,
		sellerNotif *model.Notification,
		sourceEventID string,
		buyerOutbox *model.OutboxEvent,
		sellerOutbox *model.OutboxEvent,
	) error

	// StageOutboxEvent persists an outbox record without modifying the notifications inbox (e.g. ephemeral portfolio updates).
	StageOutboxEvent(ctx context.Context, outbox *model.OutboxEvent) error

	// GetByUserID fetches notifications using deterministic keyset pagination (created_at DESC, id DESC).
	GetByUserID(ctx context.Context, filter model.PaginationFilter) ([]*model.Notification, error)

	// MarkAsRead marks a specific notification as read, enforcing user ownership.
	MarkAsRead(ctx context.Context, userID, notificationID string) (*model.Notification, error)

	// MarkAllAsRead marks all notifications for a user as read and returns the updated count.
	MarkAllAsRead(ctx context.Context, userID string) (int32, error)

	// GetUnreadCount returns the number of unread notifications for a user.
	GetUnreadCount(ctx context.Context, userID string) (int32, error)

	// FetchPendingOutbox claims up to limit PENDING or stale PROCESSING outbox records via FOR UPDATE SKIP LOCKED.
	FetchPendingOutbox(ctx context.Context, limit int) ([]*model.OutboxEvent, error)

	// RecoverStaleOutboxClaims resets PROCESSING records older than lease timeout back to PENDING.
	RecoverStaleOutboxClaims(ctx context.Context, timeout time.Duration) (int64, error)

	// MarkOutboxPublished transitions an outbox record to PROCESSED.
	MarkOutboxPublished(ctx context.Context, id string) error

	// IncrementOutboxRetry increments retry_count and records last_error for a failed outbox event.
	// Called when a Redis PUBLISH fails after all retries so the failure is visible in the DB.
	IncrementOutboxRetry(ctx context.Context, id, lastError string) error

	// ReleaseOutboxClaims unclaims in-flight outbox records back to PENDING.
	ReleaseOutboxClaims(ctx context.Context, ids []string) error
}
