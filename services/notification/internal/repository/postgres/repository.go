package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/notification/internal/model"
	"tradedrift/services/notification/internal/repository"
)

type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// CreateWithDedupTx atomically inserts deduplication for sourceEventID, the notification, and an outbox event.
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

	// 1. Deduplication check on source event ID
	if sourceEventID != "" {
		dedupQuery := `INSERT INTO processed_events (event_id, user_id) VALUES ($1, $2)`
		var uid *string
		if notif.UserID != "" {
			uid = &notif.UserID
		}
		if _, err := tx.Exec(ctx, dedupQuery, sourceEventID, uid); err != nil {
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

// CreateTradeSettledTx atomically inserts deduplication for sourceEventID, both buyer & seller notifications, and both outbox events.
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

// StageOutboxEvent persists an outbox record directly (e.g. ephemeral portfolio updates).
func (r *Repository) StageOutboxEvent(ctx context.Context, outbox *model.OutboxEvent) error {
	query := `
		INSERT INTO notification_outbox (id, event_type, payload, target_channel, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := r.db.Exec(ctx, query,
		outbox.ID,
		outbox.EventType,
		outbox.Payload,
		outbox.TargetChannel,
		model.OutboxStatusPending,
		outbox.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("stage outbox: %w", err)
	}
	return nil
}

// GetByUserID retrieves notifications using deterministic keyset pagination (created_at DESC, id DESC).
func (r *Repository) GetByUserID(ctx context.Context, filter model.PaginationFilter) ([]*model.Notification, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}

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

	rows, err := r.db.Query(ctx, query, filter.UserID, cursorTime, cursorID, filter.TypeFilter, limit)
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

	return results, rows.Err()
}

// MarkAsRead marks a specific notification as read, enforcing user ownership.
func (r *Repository) MarkAsRead(ctx context.Context, userID, notificationID string) (*model.Notification, error) {
	query := `
		UPDATE notifications
		SET is_read = TRUE, read_at = NOW()
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

// FetchPendingOutbox claims up to limit PENDING or stale PROCESSING outbox records via FOR UPDATE SKIP LOCKED.
func (r *Repository) FetchPendingOutbox(ctx context.Context, limit int) ([]*model.OutboxEvent, error) {
	query := `
		WITH claimed AS (
			SELECT id
			FROM notification_outbox
			WHERE status = 'PENDING'
			   OR (status = 'PROCESSING' AND claimed_at < NOW() - INTERVAL '60 seconds')
			ORDER BY created_at ASC, id ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE notification_outbox o
		SET status = 'PROCESSING', claimed_at = NOW()
		FROM claimed
		WHERE o.id = claimed.id
		RETURNING o.id, o.event_type, o.payload, o.target_channel, o.status, o.retry_count, COALESCE(o.last_error, ''), o.claimed_at, o.created_at, o.published_at
	`
	rows, err := r.db.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("claim outbox records: %w", err)
	}
	defer rows.Close()

	var events []*model.OutboxEvent
	for rows.Next() {
		ev := &model.OutboxEvent{}
		if err := rows.Scan(
			&ev.ID,
			&ev.EventType,
			&ev.Payload,
			&ev.TargetChannel,
			&ev.Status,
			&ev.RetryCount,
			&ev.LastError,
			&ev.ClaimedAt,
			&ev.CreatedAt,
			&ev.PublishedAt,
		); err != nil {
			return nil, fmt.Errorf("scan outbox event: %w", err)
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

// RecoverStaleOutboxClaims resets PROCESSING records older than lease timeout back to PENDING.
func (r *Repository) RecoverStaleOutboxClaims(ctx context.Context, timeout time.Duration) (int64, error) {
	threshold := time.Now().UTC().Add(-timeout)
	query := `
		UPDATE notification_outbox
		SET status = 'PENDING', claimed_at = NULL
		WHERE status = 'PROCESSING' AND claimed_at < $1
	`
	cmdTag, err := r.db.Exec(ctx, query, threshold)
	if err != nil {
		return 0, fmt.Errorf("recover stale outbox claims: %w", err)
	}
	return cmdTag.RowsAffected(), nil
}

// MarkOutboxPublished transitions an outbox record to PROCESSED.
func (r *Repository) MarkOutboxPublished(ctx context.Context, id string) error {
	query := `
		UPDATE notification_outbox
		SET status = 'PROCESSED', published_at = NOW(), claimed_at = NULL
		WHERE id = $1
	`
	cmdTag, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("mark outbox published: %w", err)
	}
	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("outbox record %s not found", id)
	}
	return nil
}

// ReleaseOutboxClaims unclaims in-flight outbox records back to PENDING.
func (r *Repository) ReleaseOutboxClaims(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	query := `
		UPDATE notification_outbox
		SET status = 'PENDING', claimed_at = NULL
		WHERE id = ANY($1::uuid[])
	`
	_, err := r.db.Exec(ctx, query, ids)
	if err != nil {
		return fmt.Errorf("release outbox claims: %w", err)
	}
	return nil
}
