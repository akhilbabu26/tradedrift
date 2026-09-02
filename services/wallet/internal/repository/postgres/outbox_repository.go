package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/wallet/internal/repository"
)

type OutboxRepository struct {
	db *pgxpool.Pool
}

func NewOutboxRepository(db *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{db: db}
}

func (r *OutboxRepository) Insert(ctx context.Context, event *repository.OutboxEvent) error {
	query := `
		INSERT INTO outbox (
			id, aggregate_id, event_type, payload,
			partition_key, status, created_at
		) VALUES ($1, $2, $3, $4, $5, 'PENDING', $6)
	`
	_, err := r.db.Exec(ctx, query,
		event.ID, event.AggregateID, event.EventType, event.Payload,
		event.PartitionKey, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("failed to insert outbox event: %w", err)
	}
	return nil
}

// FetchPending selects up to limit PENDING outbox events, oldest-first.
// FOR UPDATE SKIP LOCKED prevents two publisher instances from claiming the same row.
// The caller MUST commit or rollback the enclosing transaction to release the lock.
func (r *OutboxRepository) FetchPending(ctx context.Context, limit int) ([]*repository.OutboxEvent, error) {
	const q = `
		SELECT id, aggregate_id, event_type, payload, partition_key, created_at
		FROM outbox
		WHERE status = 'PENDING'
		ORDER BY created_at ASC
		LIMIT $1
		FOR UPDATE SKIP LOCKED`

	rows, err := r.db.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("fetch pending outbox: %w", err)
	}
	defer rows.Close()

	var events []*repository.OutboxEvent
	for rows.Next() {
		e := &repository.OutboxEvent{}
		if err := rows.Scan(&e.ID, &e.AggregateID, &e.EventType, &e.Payload, &e.PartitionKey, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan outbox row: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// MarkPublished sets status=PROCESSED and records published_at for a successfully published event.
func (r *OutboxRepository) MarkPublished(ctx context.Context, id string) error {
	const q = `UPDATE outbox SET status = 'PROCESSED', published_at = NOW() WHERE id = $1`
	_, err := r.db.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("mark outbox published %s: %w", id, err)
	}
	return nil
}

// MarkFailed sets status=FAILED and records the failure reason for an event that
// could not be published after retries. The event is preserved for investigation.
func (r *OutboxRepository) MarkFailed(ctx context.Context, id string, reason string) error {
	const q = `UPDATE outbox SET status = 'FAILED', failed_reason = $2 WHERE id = $1`
	_, err := r.db.Exec(ctx, q, id, reason)
	if err != nil {
		return fmt.Errorf("mark outbox failed %s: %w", id, err)
	}
	return nil
}

// Compile-time check.
var _ repository.OutboxRepository = (*OutboxRepository)(nil)

