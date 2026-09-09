package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"tradedrift/services/notification/internal/model"
	"tradedrift/services/notification/internal/repository"
)

// CreateWithDedupTx atomically inserts:
//  1. A deduplication row in processed_events (keyed by sourceEventID).
//  2. The notification row.
//  3. An outbox row (PENDING) so the publisher can deliver it to Redis.
//
// Returns repository.ErrAlreadyProcessed if sourceEventID was already processed.
func (r *Repository) CreateWithDedupTx(
	ctx context.Context,
	notif *model.Notification,
	sourceEventID string,
	outbox *model.OutboxEvent,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Deduplication check on source event ID.
	// We also store the notification_id so that idempotent callers can recover
	// the original notification on a retry without creating a duplicate.
	if sourceEventID != "" {
		dedupQuery := `INSERT INTO processed_events (event_id, user_id, notification_id) VALUES ($1, $2, $3)`
		var uid *string
		if notif.UserID != "" {
			uid = &notif.UserID
		}
		var nid *string
		if notif.ID != "" {
			nid = &notif.ID
		}
		if _, err := tx.Exec(ctx, dedupQuery, sourceEventID, uid, nid); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
				return repository.ErrAlreadyProcessed
			}
			return fmt.Errorf("insert processed_events: %w", err)
		}
	}

	// 2. Insert notification
	notifQuery := `
		INSERT INTO notifications (id, user_id, title, message, type, reference_id, reference_type, is_read, created_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::uuid, NULLIF($7, ''), $8, $9)
	`
	if _, err := tx.Exec(ctx, notifQuery,
		notif.ID,
		notif.UserID,
		notif.Title,
		notif.Message,
		notif.Type,
		notif.ReferenceID,
		notif.ReferenceType,
		notif.IsRead,
		notif.CreatedAt,
	); err != nil {
		return fmt.Errorf("insert notification: %w", err)
	}

	// 3. Stage outbox record
	if outbox != nil {
		outboxQuery := `
			INSERT INTO notification_outbox (id, event_type, payload, target_channel, status, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
		`
		if _, err := tx.Exec(ctx, outboxQuery,
			outbox.ID,
			outbox.EventType,
			outbox.Payload,
			outbox.TargetChannel,
			model.OutboxStatusPending,
			outbox.CreatedAt,
		); err != nil {
			return fmt.Errorf("insert notification_outbox: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// CreateTradeSettledTx atomically inserts deduplication, both buyer and seller notifications,
// and both outbox events in a single transaction.
// One TradeSettled source event produces two independent notification rows (one per counterparty).
func (r *Repository) CreateTradeSettledTx(
	ctx context.Context,
	buyerNotif *model.Notification,
	sellerNotif *model.Notification,
	sourceEventID string,
	buyerOutbox *model.OutboxEvent,
	sellerOutbox *model.OutboxEvent,
) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Deduplication on source TradeSettled event ID
	if sourceEventID != "" {
		dedupQuery := `INSERT INTO processed_events (event_id, user_id) VALUES ($1, NULL)`
		if _, err := tx.Exec(ctx, dedupQuery, sourceEventID); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return repository.ErrAlreadyProcessed
			}
			return fmt.Errorf("insert processed_events: %w", err)
		}
	}

	notifQuery := `
		INSERT INTO notifications (id, user_id, title, message, type, reference_id, reference_type, is_read, created_at)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::uuid, NULLIF($7, ''), $8, $9)
	`
	outboxQuery := `
		INSERT INTO notification_outbox (id, event_type, payload, target_channel, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`

	// 2. Buyer notification and outbox
	if buyerNotif != nil {
		if _, err := tx.Exec(ctx, notifQuery,
			buyerNotif.ID, buyerNotif.UserID, buyerNotif.Title, buyerNotif.Message,
			buyerNotif.Type, buyerNotif.ReferenceID, buyerNotif.ReferenceType,
			buyerNotif.IsRead, buyerNotif.CreatedAt,
		); err != nil {
			return fmt.Errorf("insert buyer notification: %w", err)
		}
	}
	if buyerOutbox != nil {
		if _, err := tx.Exec(ctx, outboxQuery,
			buyerOutbox.ID, buyerOutbox.EventType, buyerOutbox.Payload,
			buyerOutbox.TargetChannel, model.OutboxStatusPending, buyerOutbox.CreatedAt,
		); err != nil {
			return fmt.Errorf("insert buyer outbox: %w", err)
		}
	}

	// 3. Seller notification and outbox
	if sellerNotif != nil {
		if _, err := tx.Exec(ctx, notifQuery,
			sellerNotif.ID, sellerNotif.UserID, sellerNotif.Title, sellerNotif.Message,
			sellerNotif.Type, sellerNotif.ReferenceID, sellerNotif.ReferenceType,
			sellerNotif.IsRead, sellerNotif.CreatedAt,
		); err != nil {
			return fmt.Errorf("insert seller notification: %w", err)
		}
	}
	if sellerOutbox != nil {
		if _, err := tx.Exec(ctx, outboxQuery,
			sellerOutbox.ID, sellerOutbox.EventType, sellerOutbox.Payload,
			sellerOutbox.TargetChannel, model.OutboxStatusPending, sellerOutbox.CreatedAt,
		); err != nil {
			return fmt.Errorf("insert seller outbox: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// GetByUserID retrieves notifications using deterministic keyset pagination (created_at DESC, id DESC).
// It queries LIMIT+1 rows so the caller can determine has_more exactly without a separate COUNT query.
// The returned slice is always at most limit items; the caller must pass the slice length and the
// extra-row presence back to the client.
func (r *Repository) GetByUserID(ctx context.Context, filter model.PaginationFilter) ([]*model.Notification, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	// Fetch one extra row to determine whether a next page exists without a COUNT(*) query.
	query := `
		SELECT id, user_id, title, message, type, COALESCE(reference_id::text, ''), COALESCE(reference_type, ''), is_read, read_at, created_at
		FROM notifications
		WHERE user_id = $1
		  AND ($2::timestamptz IS NULL OR (created_at < $2 OR (created_at = $2 AND id < $3::uuid)))
		  AND ($4 = '' OR type = $4)
		ORDER BY created_at DESC, id DESC
		LIMIT $5
	`

	var cursorTime *time.Time
	var cursorID *string
	if filter.CursorTime != nil && filter.CursorID != "" {
		cursorTime = filter.CursorTime
		cursorID = &filter.CursorID
	}

	// Pass limit+1 so the service layer can detect has_more without a separate COUNT.
	rows, err := r.db.Query(ctx, query, filter.UserID, cursorTime, cursorID, filter.TypeFilter, limit+1)
	if err != nil {
		return nil, fmt.Errorf("query notifications: %w", err)
	}
	defer rows.Close()

	var results []*model.Notification
	for rows.Next() {
		n := &model.Notification{}
		if err := rows.Scan(
			&n.ID,
			&n.UserID,
			&n.Title,
			&n.Message,
			&n.Type,
			&n.ReferenceID,
			&n.ReferenceType,
			&n.IsRead,
			&n.ReadAt,
			&n.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}
		results = append(results, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// MarkAsRead marks a specific notification as read, enforcing user ownership.
// COALESCE preserves the original read_at timestamp on repeated calls — the first
// read wins and subsequent calls are idempotent with respect to the timestamp.
func (r *Repository) MarkAsRead(ctx context.Context, userID, notificationID string) (*model.Notification, error) {
	query := `
		UPDATE notifications
		SET is_read = TRUE, read_at = COALESCE(read_at, NOW())
		WHERE id = $1 AND user_id = $2
		RETURNING id, user_id, title, message, type, COALESCE(reference_id::text, ''), COALESCE(reference_type, ''), is_read, read_at, created_at
	`
	n := &model.Notification{}
	err := r.db.QueryRow(ctx, query, notificationID, userID).Scan(
		&n.ID,
		&n.UserID,
		&n.Title,
		&n.Message,
		&n.Type,
		&n.ReferenceID,
		&n.ReferenceType,
		&n.IsRead,
		&n.ReadAt,
		&n.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotificationNotFound
		}
		return nil, fmt.Errorf("update notification read: %w", err)
	}
	return n, nil
}

// MarkAllAsRead marks all unread notifications for a user as read and returns the updated count.
func (r *Repository) MarkAllAsRead(ctx context.Context, userID string) (int32, error) {
	query := `
		UPDATE notifications
		SET is_read = TRUE, read_at = NOW()
		WHERE user_id = $1 AND is_read = FALSE
	`
	cmdTag, err := r.db.Exec(ctx, query, userID)
	if err != nil {
		return 0, fmt.Errorf("mark all read: %w", err)
	}
	return int32(cmdTag.RowsAffected()), nil
}

// GetUnreadCount returns the number of unread notifications for a user.
func (r *Repository) GetUnreadCount(ctx context.Context, userID string) (int32, error) {
	query := `SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND is_read = FALSE`
	var count int64
	if err := r.db.QueryRow(ctx, query, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("get unread count: %w", err)
	}
	return int32(count), nil
}

// GetNotificationByID fetches a single notification by its primary key, enforcing user ownership.
// Used by CreateNotification to return the original notification when an idempotency key is reused.
func (r *Repository) GetNotificationByID(ctx context.Context, userID, notificationID string) (*model.Notification, error) {
	query := `
		SELECT id, user_id, title, message, type, COALESCE(reference_id::text, ''), COALESCE(reference_type, ''), is_read, read_at, created_at
		FROM notifications
		WHERE id = $1 AND user_id = $2
	`
	n := &model.Notification{}
	err := r.db.QueryRow(ctx, query, notificationID, userID).Scan(
		&n.ID,
		&n.UserID,
		&n.Title,
		&n.Message,
		&n.Type,
		&n.ReferenceID,
		&n.ReferenceType,
		&n.IsRead,
		&n.ReadAt,
		&n.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrNotificationNotFound
		}
		return nil, fmt.Errorf("get notification by id: %w", err)
	}
	return n, nil
}

// GetNotificationIDByEventID looks up the notification_id stored alongside an event in
// processed_events. Used by the idempotency path in CreateNotification to find the
// original notification when a caller retries with the same idempotency_key.
func (r *Repository) GetNotificationIDByEventID(ctx context.Context, eventID string) (string, error) {
	query := `SELECT COALESCE(notification_id::text, '') FROM processed_events WHERE event_id = $1`
	var notifID string
	if err := r.db.QueryRow(ctx, query, eventID).Scan(&notifID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", repository.ErrNotificationNotFound
		}
		return "", fmt.Errorf("lookup notification id by event: %w", err)
	}
	if notifID == "" {
		return "", fmt.Errorf("processed_events row for event %s has no notification_id", eventID)
	}
	return notifID, nil
}
