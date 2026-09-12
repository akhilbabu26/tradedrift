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

type sagaRepo struct {
	db *pgxpool.Pool
}

var _ repository.SagaRepository = (*sagaRepo)(nil)

// NewSagaRepo constructs a SagaRepository backed by PostgreSQL.
func NewSagaRepo(db *pgxpool.Pool) repository.SagaRepository {
	return &sagaRepo{db: db}
}

// Insert writes a new saga task.
func (r *sagaRepo) Insert(ctx context.Context, task *domain.SagaTask) error {
	query := `
		INSERT INTO admin_saga_tasks
			(id, operation_id, task_type, payload, status,
			 attempt_count, max_attempts, next_attempt_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := r.db.Exec(ctx, query,
		task.ID,
		task.OperationID,
		task.TaskType,
		task.Payload,
		task.Status,
		task.AttemptCount,
		task.MaxAttempts,
		task.NextAttemptAt,
		task.CreatedAt,
		task.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("saga_repo: insert: %w", err)
	}
	return nil
}

// FetchDue returns tasks due for processing, locked exclusively using SKIP LOCKED.
func (r *sagaRepo) FetchDue(ctx context.Context, workerToken string, limit int) ([]*domain.SagaTask, error) {
	query := `
		UPDATE admin_saga_tasks
		SET locked_at  = NOW(),
		    locked_by  = $1,
		    updated_at = NOW()
		WHERE id IN (
		    SELECT id FROM admin_saga_tasks
		    WHERE status IN ('PENDING', 'RETRYING')
		      AND next_attempt_at <= NOW()
		      AND (locked_at IS NULL OR locked_at < NOW() - INTERVAL '5 minutes')
		    ORDER BY next_attempt_at ASC
		    LIMIT $2
		    FOR UPDATE SKIP LOCKED
		)
		RETURNING id, operation_id, task_type, payload, status,
		          attempt_count, max_attempts, next_attempt_at,
		          last_error, locked_at, locked_by, completed_at,
		          created_at, updated_at
	`
	rows, err := r.db.Query(ctx, query, workerToken, limit)
	if err != nil {
		return nil, fmt.Errorf("saga_repo: fetch due: %w", err)
	}
	defer rows.Close()

	var tasks []*domain.SagaTask
	for rows.Next() {
		t, err := scanSagaTask(rows)
		if err != nil {
			return nil, fmt.Errorf("saga_repo: scan: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// UpdateRetry advances the retry schedule after a failed attempt.
// Strictly verifies worker lease ownership (locked_by).
func (r *sagaRepo) UpdateRetry(ctx context.Context, id string, workerToken string, nextAttemptAt time.Time, attemptCount int, lastError string) error {
	query := `
		UPDATE admin_saga_tasks
		SET status          = 'RETRYING',
		    attempt_count   = $2,
		    next_attempt_at = $3,
		    last_error      = $4,
		    locked_at       = NULL,
		    locked_by       = NULL,
		    updated_at      = NOW()
		WHERE id = $1 AND locked_by = $5
	`
	tag, err := r.db.Exec(ctx, query, id, attemptCount, nextAttemptAt, lastError, workerToken)
	if err != nil {
		return fmt.Errorf("saga_repo: update retry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrWorkerLeaseLost
	}
	return nil
}

// MarkCompleted transitions the task to the COMPLETED terminal state.
// Strictly verifies worker lease ownership (locked_by).
func (r *sagaRepo) MarkCompleted(ctx context.Context, id string, workerToken string) error {
	query := `
		UPDATE admin_saga_tasks
		SET status       = 'COMPLETED',
		    completed_at = NOW(),
		    locked_at    = NULL,
		    locked_by    = NULL,
		    updated_at   = NOW()
		WHERE id = $1 AND locked_by = $2
	`
	tag, err := r.db.Exec(ctx, query, id, workerToken)
	if err != nil {
		return fmt.Errorf("saga_repo: mark completed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrWorkerLeaseLost
	}
	return nil
}

// MarkExhausted transitions the task to the EXHAUSTED terminal state.
// Strictly verifies worker lease ownership (locked_by).
func (r *sagaRepo) MarkExhausted(ctx context.Context, id string, workerToken string, lastError string) error {
	query := `
		UPDATE admin_saga_tasks
		SET status      = 'EXHAUSTED',
		    last_error  = $2,
		    locked_at   = NULL,
		    locked_by   = NULL,
		    updated_at  = NOW()
		WHERE id = $1 AND locked_by = $3
	`
	tag, err := r.db.Exec(ctx, query, id, lastError, workerToken)
	if err != nil {
		return fmt.Errorf("saga_repo: mark exhausted: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrWorkerLeaseLost
	}
	return nil
}

// GetQueueStats returns the counts of pending, retrying, and exhausted saga tasks.
func (r *sagaRepo) GetQueueStats(ctx context.Context) (*repository.SagaQueueStats, error) {
	query := `
		SELECT
			COALESCE(SUM(CASE WHEN status = 'PENDING' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'RETRYING' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'EXHAUSTED' THEN 1 ELSE 0 END), 0)
		FROM admin_saga_tasks
	`
	var pending, retrying, exhausted int
	if err := r.db.QueryRow(ctx, query).Scan(&pending, &retrying, &exhausted); err != nil {
		return nil, fmt.Errorf("saga_repo: get queue stats: %w", err)
	}
	return &repository.SagaQueueStats{
		PendingCount:   pending,
		RetryingCount:  retrying,
		ExhaustedCount: exhausted,
	}, nil
}

func scanSagaTask(row interface{ Scan(...any) error }) (*domain.SagaTask, error) {
	var t domain.SagaTask
	var statusStr string
	err := row.Scan(
		&t.ID,
		&t.OperationID,
		&t.TaskType,
		&t.Payload,
		&statusStr,
		&t.AttemptCount,
		&t.MaxAttempts,
		&t.NextAttemptAt,
		&t.LastError,
		&t.LockedAt,
		&t.LockedBy,
		&t.CompletedAt,
		&t.CreatedAt,
		&t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}
		return nil, err
	}
	t.Status = domain.SagaStatus(statusStr)
	return &t, nil
}
