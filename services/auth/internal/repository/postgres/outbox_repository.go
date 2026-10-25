package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/auth/internal/repository"
)

type OutboxRepository struct {
	db *pgxpool.Pool
}

func NewOutboxRepository(db *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{db: db}
}

func (r *OutboxRepository) ClaimPendingEvents(ctx context.Context, eventType string, limit int) ([]*repository.OutboxEvent, error) {
	query := `
		WITH claimable AS (
			SELECT id
			FROM outbox
			WHERE event_type = $1
			  AND status = 'PENDING'
			  AND (
			    payload->>'next_attempt_at' IS NULL
			    OR (payload->>'next_attempt_at')::timestamptz <= NOW()
			  )
			ORDER BY created_at ASC
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE outbox o
		SET payload = jsonb_set(
			COALESCE(o.payload, '{}'::jsonb),
			'{next_attempt_at}',
			to_jsonb((NOW() + interval '2 minutes')::text)
		)
		FROM claimable c
		WHERE o.id = c.id
		RETURNING o.id, o.aggregate_type, o.aggregate_id, o.event_type, o.payload, o.status, COALESCE(o.failed_reason, ''), o.created_at, o.published_at;
	`
	rows, err := r.db.Query(ctx, query, eventType, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to claim pending outbox events: %w", err)
	}
	defer rows.Close()

	var events []*repository.OutboxEvent
	for rows.Next() {
		var e repository.OutboxEvent
		if err := rows.Scan(
			&e.ID, &e.AggregateType, &e.AggregateID, &e.EventType, &e.Payload,
			&e.Status, &e.FailedReason, &e.CreatedAt, &e.PublishedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan claimed outbox event: %w", err)
		}
		events = append(events, &e)
	}
	return events, nil
}

func (r *OutboxRepository) GetPendingEvents(ctx context.Context, eventType string, limit int) ([]*repository.OutboxEvent, error) {
	query := `
		SELECT id, aggregate_type, aggregate_id, event_type, payload, status, COALESCE(failed_reason, ''), created_at, published_at
		FROM outbox
		WHERE event_type = $1 AND status = 'PENDING'
		ORDER BY created_at ASC
		LIMIT $2
	`
	rows, err := r.db.Query(ctx, query, eventType, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending outbox events: %w", err)
	}
	defer rows.Close()

	var events []*repository.OutboxEvent
	for rows.Next() {
		var e repository.OutboxEvent
		if err := rows.Scan(
			&e.ID, &e.AggregateType, &e.AggregateID, &e.EventType, &e.Payload,
			&e.Status, &e.FailedReason, &e.CreatedAt, &e.PublishedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan outbox event: %w", err)
		}
		events = append(events, &e)
	}
	return events, nil
}

func (r *OutboxRepository) MarkProcessed(ctx context.Context, id string) error {
	query := `
		UPDATE outbox
		SET status = 'PROCESSED', published_at = NOW(), failed_reason = NULL
		WHERE id = $1
	`
	_, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to mark outbox event processed: %w", err)
	}
	return nil
}

func (r *OutboxRepository) RecordFailure(ctx context.Context, id string, reason string) error {
	query := `
		UPDATE outbox
		SET failed_reason = $2
		WHERE id = $1
	`
	_, err := r.db.Exec(ctx, query, id, reason)
	if err != nil {
		return fmt.Errorf("failed to record outbox event failure: %w", err)
	}
	return nil
}

func (r *OutboxRepository) RecordFailureWithBackoff(ctx context.Context, id string, attempts int, maxAttempts int, reason string) error {
	if attempts >= maxAttempts {
		query := `
			UPDATE outbox
			SET status = 'FAILED',
			    failed_reason = $2,
			    payload = jsonb_set(
			        COALESCE(payload, '{}'::jsonb),
			        '{attempts}',
			        to_jsonb($3::int)
			    )
			WHERE id = $1
		`
		failedReason := fmt.Sprintf("permanently failed after %d attempts: %s", attempts, reason)
		_, err := r.db.Exec(ctx, query, id, failedReason, attempts)
		if err != nil {
			return fmt.Errorf("failed to mark outbox event failed: %w", err)
		}
		return nil
	}

	// Calculate exponential backoff duration: 30s * 2^(attempts-1) -> 30s, 60s, 120s, 240s
	backoffSecs := (1 << (attempts - 1)) * 30
	if backoffSecs > 900 {
		backoffSecs = 900 // Cap maximum backoff at 15 minutes
	}

	query := `
		UPDATE outbox
		SET failed_reason = $2,
		    payload = jsonb_set(
		        jsonb_set(
		            COALESCE(payload, '{}'::jsonb),
		            '{attempts}',
		            to_jsonb($3::int)
		        ),
		        '{next_attempt_at}',
		        to_jsonb((NOW() + ($4 || ' seconds')::interval)::text)
		    )
		WHERE id = $1
	`
	failedMsg := fmt.Sprintf("attempt %d failed: %s", attempts, reason)
	_, err := r.db.Exec(ctx, query, id, failedMsg, attempts, fmt.Sprintf("%d", backoffSecs))
	if err != nil {
		return fmt.Errorf("failed to schedule outbox event retry: %w", err)
	}
	return nil
}
