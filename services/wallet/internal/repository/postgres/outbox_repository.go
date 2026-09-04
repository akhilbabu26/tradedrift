package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"tradedrift/services/wallet/internal/repository"
)

type OutboxRepository struct {
	db repository.DBTX
}

func NewOutboxRepository(db repository.DBTX) *OutboxRepository {
	return &OutboxRepository{db: db}
}

func (r *OutboxRepository) WithTx(tx pgx.Tx) repository.OutboxRepository {
	return &OutboxRepository{db: tx}
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

// FetchPending atomically claims up to limit unhandled outbox events using an atomic CTE
// transition to 'PROCESSING' with lease timeout recovery (1 minute) and FOR UPDATE SKIP LOCKED.
// Multi-worker safe: prevents concurrent publisher instances from claiming or publishing the same row.
// Note: This provides at-least-once delivery semantics (not once-and-only-once across worker or broker
// crashes before MarkPublished). Downstream consumers MUST remain idempotent.
func (r *OutboxRepository) FetchPending(ctx context.Context, limit int) ([]*repository.OutboxEvent, error) {
	const q = `
		WITH claimable AS (
			SELECT id
			FROM outbox
			WHERE status = 'PENDING'
			   OR (
				   status = 'PROCESSING'
				   AND (claimed_at IS NULL OR claimed_at < NOW() - INTERVAL '1 minute')
			   )
			ORDER BY created_at ASC, id ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		),
		updated AS (
			UPDATE outbox
			SET status = 'PROCESSING', claimed_at = NOW()
			WHERE id IN (SELECT id FROM claimable)
			RETURNING id, aggregate_id, event_type, payload, partition_key, created_at
		)
		SELECT id, aggregate_id, event_type, payload, partition_key, created_at
		FROM updated
		ORDER BY created_at ASC, id ASC;
	`

	rows, err := r.db.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("fetch and claim pending outbox: %w", err)
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

// MarkPublished sets status=PROCESSED, records published_at, and clears claimed_at for an event
// that is currently in 'PROCESSING' status. Returns error if not currently in PROCESSING.
func (r *OutboxRepository) MarkPublished(ctx context.Context, id string) error {
	const q = `
		UPDATE outbox
		SET status = 'PROCESSED', published_at = NOW(), claimed_at = NULL
		WHERE id = $1 AND status = 'PROCESSING'
	`
	res, err := r.db.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("mark outbox published %s: %w", id, err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("outbox event %s not found or not in PROCESSING status", id)
	}
	return nil
}

// MarkFailed sets status=FAILED, records the failure reason, and clears claimed_at for an event
// that is currently in 'PROCESSING' status.
func (r *OutboxRepository) MarkFailed(ctx context.Context, id string, reason string) error {
	const q = `
		UPDATE outbox
		SET status = 'FAILED', failed_reason = $2, claimed_at = NULL
		WHERE id = $1 AND status = 'PROCESSING'
	`
	res, err := r.db.Exec(ctx, q, id, reason)
	if err != nil {
		return fmt.Errorf("mark outbox failed %s: %w", id, err)
	}
	if res.RowsAffected() == 0 {
		return fmt.Errorf("outbox event %s not found or not in PROCESSING status", id)
	}
	return nil
}

// ReleaseClaim resets a PROCESSING event back to 'PENDING' and clears claimed_at.
func (r *OutboxRepository) ReleaseClaim(ctx context.Context, id string) error {
	return r.ReleaseClaims(ctx, []string{id})
}

// ReleaseClaims resets multiple PROCESSING events back to 'PENDING' and clears claimed_at.
func (r *OutboxRepository) ReleaseClaims(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	const q = `
		UPDATE outbox
		SET status = 'PENDING', claimed_at = NULL
		WHERE id = ANY($1) AND status = 'PROCESSING'
	`
	_, err := r.db.Exec(ctx, q, ids)
	if err != nil {
		return fmt.Errorf("release outbox claims: %w", err)
	}
	return nil
}

// Compile-time check.
var _ repository.OutboxRepository = (*OutboxRepository)(nil)
