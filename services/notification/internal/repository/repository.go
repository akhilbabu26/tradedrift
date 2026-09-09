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

	// ErrOutboxClaimLost is returned by MarkOutboxPublished when the claim token does not
	// match the stored token, meaning the row has been re-claimed by another worker after
	// this worker's lease expired. This is a normal concurrency condition, not a system
	// failure — the owning worker will mark the row PROCESSED on its own schedule.
	ErrOutboxClaimLost = errors.New("outbox claim lost: claim token mismatch")
)

// NotificationRepository defines the data access contract for inboxes and outbox events.
type NotificationRepository interface {
	// CreateWithDedupTx atomically inserts an event deduplication row, a notification, and an outbox record.
	// notificationID is stored in processed_events so that callers can retrieve the original notification
	// on an idempotent retry (sourceEventID unique violation).
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
	// Returns limit+1 rows so the caller can detect has_more without a COUNT query.
	GetByUserID(ctx context.Context, filter model.PaginationFilter) ([]*model.Notification, error)

	// GetNotificationByID fetches a single notification by its ID, enforcing user ownership.
	// Used by CreateNotification to return the original notification on an idempotent retry.
	GetNotificationByID(ctx context.Context, userID, notificationID string) (*model.Notification, error)

	// GetNotificationIDByEventID looks up the notification_id stored in processed_events for a
	// given event/idempotency key. Used by the idempotency path in CreateNotification.
	GetNotificationIDByEventID(ctx context.Context, eventID string) (string, error)

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

	// MarkOutboxPublished transitions an outbox record from PROCESSING → PROCESSED.
	// claimToken must match the token stored when the row was claimed — prevents a
	// slow worker from marking a row that another worker has already re-claimed.
	// Returns ErrOutboxClaimLost when RowsAffected == 0 due to a token mismatch.
	MarkOutboxPublished(ctx context.Context, id, claimToken string) error

	// IncrementOutboxRetry increments retry_count and records last_error for a failed outbox event.
	// claimToken must match so that a late worker cannot corrupt the retry metadata of a re-claimed row.
	IncrementOutboxRetry(ctx context.Context, id, lastError, claimToken string) error

	// ReleaseOutboxClaims unclaims in-flight outbox records back to PENDING.
	// claimToken must match the token stored when rows were claimed — prevents a
	// slow worker from releasing rows that another worker has already re-claimed.
	ReleaseOutboxClaims(ctx context.Context, ids []string, claimToken string) error
}
