package postgres

import (
	"context"
	"fmt"

	"tradedrift/services/order/internal/repository"
)

func (r *orderRepository) ClaimPendingWalletReleases(ctx context.Context, limit int) ([]*repository.WalletRelease, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
		UPDATE wallet_releases
		SET processed_at = NOW() + INTERVAL '30 seconds', attempts = attempts + 1
		WHERE id IN (
			SELECT id
			FROM wallet_releases
			WHERE status = 'PENDING'
			  AND (processed_at IS NULL OR processed_at < NOW())
			ORDER BY created_at ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, order_id, status, attempts, last_error, created_at, processed_at`

	rows, err := r.db.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to claim pending wallet releases: %w", err)
	}
	defer rows.Close()

	var releases []*repository.WalletRelease
	for rows.Next() {
		var wr repository.WalletRelease
		var statusStr string
		err := rows.Scan(
			&wr.ID, &wr.OrderID, &statusStr,
			&wr.Attempts, &wr.LastError, &wr.CreatedAt, &wr.ProcessedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan wallet release: %w", err)
		}
		wr.Status = repository.WalletReleaseStatus(statusStr)
		releases = append(releases, &wr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("wallet release row iteration error: %w", err)
	}
	return releases, nil
}

func (r *orderRepository) MarkWalletReleaseCompleted(ctx context.Context, id string) error {
	query := `
		UPDATE wallet_releases
		SET status = 'COMPLETED',
		    processed_at = NOW(),
		    last_error = NULL
		WHERE id = $1 AND status != 'COMPLETED'`

	_, err := r.db.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to mark wallet release as completed: %w", err)
	}
	return nil
}

func (r *orderRepository) MarkWalletReleaseCompletedByOrderID(ctx context.Context, orderID string) error {
	query := `
		UPDATE wallet_releases
		SET status = 'COMPLETED',
		    processed_at = NOW(),
		    last_error = NULL
		WHERE order_id = $1 AND status != 'COMPLETED'`

	_, err := r.db.Exec(ctx, query, orderID)
	if err != nil {
		return fmt.Errorf("failed to mark wallet release as completed for order %s: %w", orderID, err)
	}
	return nil
}

func (r *orderRepository) RecordWalletReleaseError(ctx context.Context, id string, errMsg string) error {
	query := `
		UPDATE wallet_releases
		SET last_error = $2,
		    processed_at = NOW() + (INTERVAL '1 second' * LEAST(attempts, 60))
		WHERE id = $1`

	_, err := r.db.Exec(ctx, query, id, errMsg)
	if err != nil {
		return fmt.Errorf("failed to record wallet release error: %w", err)
	}
	return nil
}

func (r *orderRepository) EnqueueWalletRelease(ctx context.Context, orderID string) error {
	query := `
		INSERT INTO wallet_releases (order_id, status)
		VALUES ($1, 'PENDING')
		ON CONFLICT (order_id) DO NOTHING`

	_, err := r.db.Exec(ctx, query, orderID)
	if err != nil {
		return fmt.Errorf("failed to enqueue wallet release for order %s: %w", orderID, err)
	}
	return nil
}
