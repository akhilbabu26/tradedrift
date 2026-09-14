package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/repository"
)

type operationsRepo struct {
	db *pgxpool.Pool
}

var _ repository.OperationsRepository = (*operationsRepo)(nil)

// NewOperationsRepo constructs an OperationsRepository backed by PostgreSQL.
func NewOperationsRepo(db *pgxpool.Pool) repository.OperationsRepository {
	return &operationsRepo{db: db}
}

// GetByIdempotencyKey returns the existing operation for (adminID, idempotencyKey), or nil if none exists.
func (r *operationsRepo) GetByIdempotencyKey(ctx context.Context, adminID, key string) (*domain.AdminOperation, error) {
	query := `
		SELECT id, admin_id, idempotency_key, request_id, operation_type,
		       target_id, reason, status, response_body, created_at, updated_at
		FROM admin_operations
		WHERE admin_id = $1 AND idempotency_key = $2
	`
	row := r.db.QueryRow(ctx, query, adminID, key)
	op, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("operations_repo: get by idempotency key: %w", err)
	}
	return op, nil
}

// GetByID returns the operation for the given ID, or nil if none exists.
func (r *operationsRepo) GetByID(ctx context.Context, id string) (*domain.AdminOperation, error) {
	query := `
		SELECT id, admin_id, idempotency_key, request_id, operation_type,
		       target_id, reason, status, response_body, created_at, updated_at
		FROM admin_operations
		WHERE id = $1
	`
	row := r.db.QueryRow(ctx, query, id)
	op, err := scanOperation(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("operations_repo: get by id: %w", err)
	}
	return op, nil
}

// Insert writes a new operation record.
func (r *operationsRepo) Insert(ctx context.Context, op *domain.AdminOperation) error {
	query := `
		INSERT INTO admin_operations
			(id, admin_id, idempotency_key, request_id, operation_type,
			 target_id, reason, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := r.db.Exec(ctx, query,
		op.ID,
		op.AdminID,
		op.IdempotencyKey,
		op.RequestID,
		op.OperationType,
		op.TargetID,
		op.Reason,
		op.Status,
		op.CreatedAt,
		op.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("operations_repo: insert: %w", err)
	}
	return nil
}

// UpdateStatus transitions the operation status and optionally stores the response body.
func (r *operationsRepo) UpdateStatus(ctx context.Context, id string, status domain.OperationStatus, responseBody []byte) error {
	query := `
		UPDATE admin_operations
		SET status        = $2,
		    response_body = $3,
		    updated_at    = NOW()
		WHERE id = $1
	`
	_, err := r.db.Exec(ctx, query, id, status, responseBody)
	if err != nil {
		return fmt.Errorf("operations_repo: update status: %w", err)
	}
	return nil
}

// GetOperationsSummary aggregates counts by status and by operation_type since a given timestamp.
func (r *operationsRepo) GetOperationsSummary(ctx context.Context, since time.Time) (*repository.OperationsSummaryStats, error) {
	stats := &repository.OperationsSummaryStats{
		ByType: make(map[string]int),
	}

	// 1. Status Aggregation (separating PENDING vs PROCESSING, and finding oldest PROCESSING age)
	statusQuery := `
		SELECT
			COUNT(*) AS total_ops,
			COUNT(*) FILTER (WHERE status = 'COMPLETED') AS completed_ops,
			COUNT(*) FILTER (WHERE status = 'FAILED') AS failed_ops,
			COUNT(*) FILTER (WHERE status = 'PENDING') AS pending_ops,
			COUNT(*) FILTER (WHERE status = 'PROCESSING') AS processing_ops,
			COALESCE(EXTRACT(EPOCH FROM (NOW() - MIN(created_at) FILTER (WHERE status = 'PROCESSING'))), 0.0) AS oldest_processing_age
		FROM admin_operations
		WHERE created_at >= $1
	`
	row := r.db.QueryRow(ctx, statusQuery, since)
	err := row.Scan(
		&stats.TotalOperations,
		&stats.CompletedOperations,
		&stats.FailedOperations,
		&stats.PendingOperations,
		&stats.ProcessingOperations,
		&stats.OldestProcessingAgeSeconds,
	)
	if err != nil {
		return nil, fmt.Errorf("operations_repo: status summary: %w", err)
	}

	// 2. Breakdown by Operation Type
	typeQuery := `
		SELECT operation_type, COUNT(*)
		FROM admin_operations
		WHERE created_at >= $1
		GROUP BY operation_type
	`
	rows, err := r.db.Query(ctx, typeQuery, since)
	if err != nil {
		return nil, fmt.Errorf("operations_repo: type summary: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var opType string
		var count int
		if err := rows.Scan(&opType, &count); err != nil {
			return nil, err
		}
		stats.ByType[opType] = count
	}

	return stats, nil
}

// GetLatestMarketStates queries PostgreSQL to reconstruct the operational state of all markets.
func (r *operationsRepo) GetLatestMarketStates(ctx context.Context) ([]repository.MarketStateSnapshot, error) {
	query := `
		SELECT DISTINCT ON (target_id) target_id, operation_type, created_at
		FROM admin_operations
		WHERE operation_type IN ('HALT_MARKET', 'RESUME_MARKET')
		  AND status = 'COMPLETED'
		ORDER BY target_id, created_at DESC
	`
	rows, err := r.db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("operations_repo: get latest market states: %w", err)
	}
	defer rows.Close()

	var states []repository.MarketStateSnapshot
	for rows.Next() {
		var marketID string
		var opType string
		var createdAt time.Time
		if err := rows.Scan(&marketID, &opType, &createdAt); err != nil {
			return nil, fmt.Errorf("operations_repo: scan market state: %w", err)
		}
		states = append(states, repository.MarketStateSnapshot{
			MarketID:  marketID,
			IsHalted:  opType == domain.OpHaltMarket,
			UpdatedAt: createdAt,
		})
	}
	return states, nil
}

func scanOperation(row pgx.Row) (*domain.AdminOperation, error) {
	var op domain.AdminOperation
	var statusStr string
	err := row.Scan(
		&op.ID,
		&op.AdminID,
		&op.IdempotencyKey,
		&op.RequestID,
		&op.OperationType,
		&op.TargetID,
		&op.Reason,
		&statusStr,
		&op.ResponseBody,
		&op.CreatedAt,
		&op.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	op.Status = domain.OperationStatus(statusStr)
	return &op, nil
}
