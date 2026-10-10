package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"tradedrift/services/portfolio/internal/repository"
)

// RecordSnapshot persists an immutable portfolio valuation snapshot into PostgreSQL.
// Includes a 1-second debounce per user to prevent micro-duplicate write amplification.
func (r *Repository) RecordSnapshot(ctx context.Context, s repository.PortfolioSnapshot) error {
	if s.UserID == "" {
		return fmt.Errorf("user_id cannot be empty")
	}
	if s.SnapshotAt.IsZero() {
		s.SnapshotAt = time.Now().UTC()
	}

	// 1. Debounce check: avoid inserting if an identical snapshot exists within 1 second
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM portfolio_snapshots
			WHERE user_id = $1
			  AND snapshot_at >= $2 - INTERVAL '1 second'
			  AND snapshot_at <= $2 + INTERVAL '1 second'
		)
	`, s.UserID, s.SnapshotAt).Scan(&exists)
	if err == nil && exists {
		// Harmless duplicate absorbed cleanly
		return nil
	}

	id := s.ID
	if id == "" {
		id = uuid.New().String()
	}

	query := `
		INSERT INTO portfolio_snapshots (
			id, user_id, total_equity, cash_balance, holdings_value,
			unrealized_pnl, realized_pnl, trigger_type, snapshot_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())
	`

	_, err = r.pool.Exec(ctx, query,
		id,
		s.UserID,
		s.TotalEquity,
		s.CashBalance,
		s.HoldingsValue,
		s.UnrealizedPnL,
		s.RealizedPnL,
		s.TriggerType,
		s.SnapshotAt,
	)
	if err != nil {
		return fmt.Errorf("insert portfolio snapshot: %w", err)
	}

	return nil
}

// GetSnapshots retrieves strictly ascending, sampled portfolio snapshots for a user since startTime.
// Uses time-bucket sampling to keep the payload size bounded (max limit points) while preserving chronological order.
func (r *Repository) GetSnapshots(ctx context.Context, userID string, startTime time.Time, bucketDuration time.Duration, limit int) ([]repository.PortfolioSnapshot, error) {
	if limit <= 0 || limit > 500 {
		limit = 150
	}

	var rows pgx.Rows
	var err error

	if bucketDuration <= 0 {
		query := `
			SELECT id, user_id, total_equity, cash_balance, holdings_value,
			       unrealized_pnl, realized_pnl, trigger_type, snapshot_at, created_at
			FROM portfolio_snapshots
			WHERE user_id = $1 AND snapshot_at >= $2
			ORDER BY snapshot_at ASC
			LIMIT $3
		`
		rows, err = r.pool.Query(ctx, query, userID, startTime, limit)
	} else {
		// Sample one snapshot per time bucket using DISTINCT ON or ROW_NUMBER
		bucketSeconds := int64(bucketDuration.Seconds())
		if bucketSeconds <= 0 {
			bucketSeconds = 60
		}

		query := `
			WITH bucketed AS (
				SELECT id, user_id, total_equity, cash_balance, holdings_value,
				       unrealized_pnl, realized_pnl, trigger_type, snapshot_at, created_at,
				       ROW_NUMBER() OVER (
				           PARTITION BY (EXTRACT(EPOCH FROM snapshot_at)::BIGINT / $3)
				           ORDER BY snapshot_at DESC
				       ) as rn
				FROM portfolio_snapshots
				WHERE user_id = $1 AND snapshot_at >= $2
			)
			SELECT id, user_id, total_equity, cash_balance, holdings_value,
			       unrealized_pnl, realized_pnl, trigger_type, snapshot_at, created_at
			FROM bucketed
			WHERE rn = 1
			ORDER BY snapshot_at ASC
			LIMIT $4
		`
		rows, err = r.pool.Query(ctx, query, userID, startTime, bucketSeconds, limit)
	}

	if err != nil {
		return nil, fmt.Errorf("query portfolio snapshots: %w", err)
	}
	defer rows.Close()

	var results []repository.PortfolioSnapshot
	for rows.Next() {
		var s repository.PortfolioSnapshot
		if err := rows.Scan(
			&s.ID,
			&s.UserID,
			&s.TotalEquity,
			&s.CashBalance,
			&s.HoldingsValue,
			&s.UnrealizedPnL,
			&s.RealizedPnL,
			&s.TriggerType,
			&s.SnapshotAt,
			&s.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan portfolio snapshot: %w", err)
		}
		results = append(results, s)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate portfolio snapshots: %w", err)
	}

	return results, nil
}

// GetActiveUserIDsWithHoldings returns distinct user IDs that have open non-zero holdings or recent trades.
func (r *Repository) GetActiveUserIDsWithHoldings(ctx context.Context) ([]string, error) {
	query := `
		SELECT DISTINCT user_id FROM holdings WHERE quantity > 0
		UNION
		SELECT DISTINCT user_id FROM portfolio_snapshots WHERE snapshot_at >= NOW() - INTERVAL '24 hours'
		LIMIT 100
	`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query active user IDs: %w", err)
	}
	defer rows.Close()

	var userIDs []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("scan active user ID: %w", err)
		}
		userIDs = append(userIDs, uid)
	}

	return userIDs, rows.Err()
}
