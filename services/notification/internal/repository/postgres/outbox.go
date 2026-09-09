package postgres

import (
	"context"
	"fmt"
	"time"

	"tradedrift/services/notification/internal/model"
	"tradedrift/services/notification/internal/repository"
	platformuuid "tradedrift/platform/uuid"
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
// SELECT … FOR UPDATE SKIP LOCKED, then marks them PROCESSING and stamps a fresh claim_token.
//
// The claim_token is a per-claim UUID generated here. Callers must thread it through to
// MarkOutboxPublished and ReleaseOutboxClaims so that only the worker holding the token
// can transition the row — preventing a slow worker from interfering with a second worker
// that has re-claimed the same row after lease expiry.
func (r *Repository) FetchPendingOutbox(ctx context.Context, limit int) ([]*model.OutboxEvent, error) {
	claimToken, err := platformuuid.New()
	if err != nil {
		return nil, fmt.Errorf("generate claim token: %w", err)
	}

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
		SET status = 'PROCESSING', claimed_at = NOW(), claim_token = $2
		FROM claimed
		WHERE o.id = claimed.id
		RETURNING o.id, o.event_type, o.payload, o.target_channel, o.status, o.claim_token, o.retry_count, COALESCE(o.last_error, ''), o.claimed_at, o.created_at, o.published_at
	`
	rows, err := r.db.Query(ctx, query, limit, claimToken)
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
			&ev.ClaimToken,
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
// The claim_token is cleared on recovery so the next worker gets a fresh token.
func (r *Repository) RecoverStaleOutboxClaims(ctx context.Context, timeout time.Duration) (int64, error) {
	threshold := time.Now().UTC().Add(-timeout)
	query := `
		UPDATE notification_outbox
		SET status = 'PENDING', claimed_at = NULL, claim_token = NULL
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
// The WHERE clause requires BOTH status = 'PROCESSING' AND claim_token = $2 so that:
//   - A worker whose 60-second lease has expired cannot mark PROCESSED a row that the
//     recovery path has already re-claimed and handed to a second worker.
//   - A second worker that re-claimed the row gets a fresh claim_token, so the late
//     first worker's token will not match and the UPDATE is a safe no-op.
//
// If RowsAffected == 0 the row was not found, had the wrong status, or the claim token
// did not match — all safe: the owning worker will mark it PROCESSED on its own schedule.
func (r *Repository) MarkOutboxPublished(ctx context.Context, id, claimToken string) error {
	query := `
		UPDATE notification_outbox
		SET status = 'PROCESSED', published_at = NOW(), claimed_at = NULL, claim_token = NULL
		WHERE id = $1 AND status = 'PROCESSING' AND claim_token = $2
	`
	cmdTag, err := r.db.Exec(ctx, query, id, claimToken)
	if err != nil {
		return fmt.Errorf("mark outbox published: %w", err)
	}
	if cmdTag.RowsAffected() == 0 {
		// The row was not found, had wrong status, or the claim token did not match.
		// Return ErrOutboxClaimLost so the publisher can log at Warn rather than Error
		// and avoid treating this normal concurrency condition as a system failure.
		return fmt.Errorf("outbox record %s: %w", id, repository.ErrOutboxClaimLost)
	}
	return nil
}

// IncrementOutboxRetry bumps retry_count and records last_error for an outbox event
// that failed to publish to Redis. The row remains PROCESSING so it will be retried
// on the next publisher poll (or after lease recovery). This is purely observational —
// the retry logic itself is driven by the lease timeout, not this counter.
//
// claimToken is required for the same ownership reason as MarkOutboxPublished: prevents
// a slow/late worker from corrupting the retry metadata of a row that has been re-claimed.
func (r *Repository) IncrementOutboxRetry(ctx context.Context, id, lastError, claimToken string) error {
	query := `
		UPDATE notification_outbox
		SET retry_count = retry_count + 1,
		    last_error  = $2
		WHERE id = $1
		  AND status = 'PROCESSING'
		  AND claim_token = $3
	`
	cmdTag, err := r.db.Exec(ctx, query, id, lastError, claimToken)
	if err != nil {
		return fmt.Errorf("increment outbox retry: %w", err)
	}
	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("outbox record %s: %w", id, repository.ErrOutboxClaimLost)
	}
	return nil
}

// ReleaseOutboxClaims resets in-flight PROCESSING records back to PENDING.
// Called when a batch fails mid-way so remaining events are retried by the next poll.
//
// Both status = 'PROCESSING' AND claim_token = $2 are required:
//   - Prevents a slow worker from resetting a row whose lease has expired and been
//     re-claimed by a second worker (the tokens would not match).
//   - The claim_token is cleared so the next worker gets a fresh token on re-claim.
func (r *Repository) ReleaseOutboxClaims(ctx context.Context, ids []string, claimToken string) error {
	if len(ids) == 0 {
		return nil
	}
	query := `
		UPDATE notification_outbox
		SET status = 'PENDING', claimed_at = NULL, claim_token = NULL
		WHERE id = ANY($1::uuid[])
		  AND status = 'PROCESSING'
		  AND claim_token = $2
	`
	_, err := r.db.Exec(ctx, query, ids, claimToken)
	if err != nil {
		return fmt.Errorf("release outbox claims: %w", err)
	}
	return nil
}

// PurgeProcessedOutbox deletes up to limit PROCESSED outbox records published before cutoff
// matching targetChannelPrefix (e.g. "user:portfolio:", "user:notifications:").
// Uses FOR UPDATE SKIP LOCKED in a CTE to avoid lock contention with active workers.
// Returns the number of rows deleted.
func (r *Repository) PurgeProcessedOutbox(ctx context.Context, targetChannelPrefix string, cutoff time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}
	query := `
		WITH target_rows AS (
			SELECT id
			FROM notification_outbox
			WHERE status = 'PROCESSED'
			  AND published_at < $1
			  AND target_channel LIKE $2 || '%'
			ORDER BY published_at ASC
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		)
		DELETE FROM notification_outbox
		WHERE id IN (SELECT id FROM target_rows);
	`
	cmdTag, err := r.db.Exec(ctx, query, cutoff, targetChannelPrefix, limit)
	if err != nil {
		return 0, fmt.Errorf("purge processed outbox: %w", err)
	}
	return cmdTag.RowsAffected(), nil
}

