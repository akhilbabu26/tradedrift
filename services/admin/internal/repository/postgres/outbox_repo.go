package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/repository"
)

type outboxRepo struct {
	db *pgxpool.Pool
}

var _ repository.OutboxRepository = (*outboxRepo)(nil)

// NewOutboxRepo constructs an OutboxRepository backed by PostgreSQL.
func NewOutboxRepo(db *pgxpool.Pool) repository.OutboxRepository {
	return &outboxRepo{db: db}
}

// Insert writes an outbox event.
func (r *outboxRepo) Insert(ctx context.Context, event *domain.OutboxEvent) error {
	query := `
		INSERT INTO admin_outbox
			(id, operation_id, topic, payload, published, status,
			 attempt_count, max_attempts, next_attempt_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, FALSE, $5, $6, $7, $8, $9, $10)
	`
	_, err := r.db.Exec(ctx, query,
		event.ID,
		event.OperationID,
		event.Topic,
		event.Payload,
		event.Status,
		event.AttemptCount,
		event.MaxAttempts,
		event.NextAttemptAt,
		event.CreatedAt,
		event.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("outbox_repo: insert: %w", err)
	}
	return nil
}

// FetchDue atomically claims due, unpublished outbox events using SKIP LOCKED.
// Durable worker lease ensures multi-instance safety even across long publishing times.
func (r *outboxRepo) FetchDue(ctx context.Context, workerToken string, limit int) ([]*domain.OutboxEvent, error) {
	query := `
		UPDATE admin_outbox
		SET locked_at  = NOW(),
		    locked_by  = $1,
		    status     = 'PROCESSING',
		    updated_at = NOW()
		WHERE id IN (
		    SELECT id FROM admin_outbox
		    WHERE published = FALSE
		      AND next_attempt_at <= NOW()
		      AND (locked_at IS NULL OR locked_at < NOW() - INTERVAL '2 minutes')
		    ORDER BY next_attempt_at ASC
		    LIMIT $2
		    FOR UPDATE SKIP LOCKED
		)
		RETURNING id, operation_id, topic, payload, published, published_at,
		          status, attempt_count, max_attempts, next_attempt_at,
		          last_error, locked_at, locked_by, created_at, updated_at
	`
	rows, err := r.db.Query(ctx, query, workerToken, limit)
	if err != nil {
		return nil, fmt.Errorf("outbox_repo: fetch due: %w", err)
	}
	defer rows.Close()

	var events []*domain.OutboxEvent
	for rows.Next() {
		var e domain.OutboxEvent
		var statusStr string
		if err := rows.Scan(
			&e.ID,
			&e.OperationID,
			&e.Topic,
			&e.Payload,
			&e.Published,
			&e.PublishedAt,
			&statusStr,
			&e.AttemptCount,
			&e.MaxAttempts,
			&e.NextAttemptAt,
			&e.LastError,
			&e.LockedAt,
			&e.LockedBy,
			&e.CreatedAt,
			&e.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("outbox_repo: scan: %w", err)
		}
		e.Status = domain.OutboxStatus(statusStr)
		events = append(events, &e)
	}
	return events, rows.Err()
}

// MarkPublished marks an event as published after verified Kafka ACK.
func (r *outboxRepo) MarkPublished(ctx context.Context, id string) error {
	query := `
		UPDATE admin_outbox
		SET published    = TRUE,
		    published_at = NOW(),
		    status       = 'PUBLISHED',
		    locked_at    = NULL,
		    locked_by    = NULL,
		    updated_at   = NOW()
		WHERE id = $1
	`
	_, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("outbox_repo: mark published: %w", err)
	}
	return nil
}

// UpdateRetry schedules a retry for an outbox event after a transient Kafka failure.
func (r *outboxRepo) UpdateRetry(ctx context.Context, id string, nextAttemptAt time.Time, attemptCount int, lastError string) error {
	query := `
		UPDATE admin_outbox
		SET status          = CASE WHEN $2 >= max_attempts THEN 'FAILED' ELSE 'PENDING' END,
		    attempt_count   = $2,
		    next_attempt_at = $3,
		    last_error      = $4,
		    locked_at       = NULL,
		    locked_by       = NULL,
		    updated_at      = NOW()
		WHERE id = $1
	`
	_, err := r.db.Exec(ctx, query, id, attemptCount, nextAttemptAt, lastError)
	if err != nil {
		return fmt.Errorf("outbox_repo: update retry: %w", err)
	}
	return nil
}

// GetBacklogStats calculates current backlog count and age of the oldest unpublished message.
func (r *outboxRepo) GetBacklogStats(ctx context.Context) (*repository.OutboxBacklogStats, error) {
	query := `
		SELECT COUNT(*),
		       COALESCE(EXTRACT(EPOCH FROM (NOW() - MIN(created_at))), 0)
		FROM admin_outbox
		WHERE published = FALSE
	`
	var count int
	var oldestSeconds float64
	if err := r.db.QueryRow(ctx, query).Scan(&count, &oldestSeconds); err != nil {
		return nil, fmt.Errorf("outbox_repo: get backlog stats: %w", err)
	}
	return &repository.OutboxBacklogStats{
		PendingCount: count,
		OldestAge:    time.Duration(oldestSeconds * float64(time.Second)),
	}, nil
}
