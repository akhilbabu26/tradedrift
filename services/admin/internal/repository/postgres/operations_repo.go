package postgres

import (
	"context"
	"errors"
	"fmt"

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
