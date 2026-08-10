package postgres

import (
	"context"
	"fmt"

	"tradedrift/services/order/internal/repository"
)

func (r *orderRepository) GetUnpublishedOutboxEvents(ctx context.Context, limit int) ([]*repository.OutboxEvent, error) {
	if limit <= 0 {
		limit = 50
	}

	// Atomic claim query with linear backoff-aware processing_at check (< NOW())
	query := `
		UPDATE outbox
		SET processing_at = NOW(), attempts = attempts + 1
		WHERE id IN (
			SELECT id
			FROM outbox
			WHERE published_at IS NULL
			  AND (processing_at IS NULL OR processing_at < NOW())
			ORDER BY created_at ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, aggregate_id, event_type, payload, partition_key, published_at, processing_at, attempts, last_error, created_at`

	rows, err := r.db.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to claim outbox events: %w", err)
	}
	defer rows.Close()

	var events []*repository.OutboxEvent
	for rows.Next() {
		var e repository.OutboxEvent
		err := rows.Scan(
			&e.ID, &e.AggregateID, &e.EventType, &e.Payload,
			&e.PartitionKey, &e.PublishedAt, &e.ProcessingAt,
			&e.Attempts, &e.LastError, &e.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan outbox event: %w", err)
		}
		events = append(events, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("outbox row iteration error: %w", err)
	}
	return events, nil
}

func (r *orderRepository) MarkOutboxEventAsPublished(ctx context.Context, id string) error {
	query := `
		UPDATE outbox
		SET published_at = NOW(),
		    processing_at = NULL,
		    last_error = NULL
		WHERE id = $1 AND published_at IS NULL`

	res, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to mark outbox event as published: %w", err)
	}
	if res.RowsAffected() != 1 {
		return fmt.Errorf("outbox event %q was not found or already published", id)
	}
	return nil
}

func (r *orderRepository) RecordOutboxPublishError(ctx context.Context, id string, errMsg string) error {
	// Progressive linear backoff delay based on attempts (1s, 2s, 3s... capped at 60s max)
	query := `
		UPDATE outbox
		SET last_error = $2,
		    processing_at = NOW() + (INTERVAL '1 second' * LEAST(attempts, 60))
		WHERE id = $1`

	res, err := r.db.Exec(ctx, query, id, errMsg)
	if err != nil {
		return fmt.Errorf("failed to record outbox publish error: %w", err)
	}
	if res.RowsAffected() != 1 {
		return fmt.Errorf("outbox event %q was not found for error recording", id)
	}
	return nil
}
