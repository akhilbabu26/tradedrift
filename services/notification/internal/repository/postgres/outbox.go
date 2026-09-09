package postgres

import (
	"context"
	"fmt"
	"time"

	"tradedrift/services/notification/internal/model"
)

// StageOutboxEvent persists a standalone outbox record (e.g. ephemeral portfolio updates
// that do not create a persistent notification row).
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

// FetchPendingOutbox claims up to limit PENDING (or stale PROCESSING) outbox records using
// SELECT … FOR UPDATE SKIP LOCKED, then marks them PROCESSING so no other worker picks them up.
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

// RecoverStaleOutboxClaims resets PROCESSING records older than the lease timeout back to PENDING
// so that a crashed publisher worker does not permanently block those rows.
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

// MarkOutboxPublished transitions an outbox record from PROCESSING → PROCESSED.
//
// The WHERE clause requires status = 'PROCESSING' so that a worker whose 60-second
// lease has expired cannot mark PROCESSED a row that the recovery path has already
// re-claimed and handed to a second worker. If RowsAffected == 0 the row was either
// not found or had already been re-claimed; the caller logs this but it is safe
// because the new owner will publish and mark it PROCESSED on its own schedule.
func (r *Repository) MarkOutboxPublished(ctx context.Context, id string) error {
	query := `
		UPDATE notification_outbox
		SET status = 'PROCESSED', published_at = NOW(), claimed_at = NULL
		WHERE id = $1 AND status = 'PROCESSING'
	`
	cmdTag, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("mark outbox published: %w", err)
	}
	if cmdTag.RowsAffected() == 0 {
		// Row not found or lease already expired and re-claimed — safe to ignore.
		return fmt.Errorf("outbox record %s not found or lease expired (status was not PROCESSING)", id)
	}
	return nil
}

// ReleaseOutboxClaims resets in-flight PROCESSING records back to PENDING.
// Called when a batch fails mid-way so remaining events are retried by the next poll.
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
