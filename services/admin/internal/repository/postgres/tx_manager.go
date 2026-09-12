package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/repository"
)

type txManager struct {
	db *pgxpool.Pool
}

var _ repository.TxManager = (*txManager)(nil)

// NewTxManager constructs the transactional coordinator.
func NewTxManager(db *pgxpool.Pool) repository.TxManager {
	return &txManager{db: db}
}

// ExecAdminOperationTx performs the atomic admin mutation:
//
//	BEGIN
//	  INSERT admin_audit_log
//	  INSERT admin_operations
//	  INSERT admin_outbox
//	  INSERT admin_saga_tasks   (optional — only if req.SagaTask != nil)
//	COMMIT
//
// On any failure the transaction is rolled back and the error is returned.
func (t *txManager) ExecAdminOperationTx(ctx context.Context, req repository.AdminOperationTxRequest) (*repository.AdminOperationTxResult, error) {
	tx, err := t.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("tx_manager: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Insert audit log (strictly append-only)
	if err := insertAuditLogTx(ctx, tx, req.AuditLog); err != nil {
		return nil, fmt.Errorf("tx_manager: audit log: %w", err)
	}

	// 2. Insert operation record (enforces unique idempotency constraint)
	if err := insertOperationTx(ctx, tx, req.Operation); err != nil {
		return nil, fmt.Errorf("tx_manager: operation: %w", err)
	}

	// 3. Insert outbox event (with leasing defaults)
	if err := insertOutboxEventTx(ctx, tx, req.OutboxEvent); err != nil {
		return nil, fmt.Errorf("tx_manager: outbox event: %w", err)
	}

	// 4. Insert saga task (conditional)
	if req.SagaTask != nil {
		if err := insertSagaTaskTx(ctx, tx, req.SagaTask); err != nil {
			return nil, fmt.Errorf("tx_manager: saga task: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tx_manager: commit: %w", err)
	}

	return &repository.AdminOperationTxResult{Operation: req.Operation}, nil
}

// CompleteAuthSaga atomically transitions both the admin_operation and its associated
// admin_saga_task to COMPLETED in a single PostgreSQL transaction. This eliminates race
// conditions where the background SagaWorker re-runs an already-succeeded Auth invalidation.
func (t *txManager) CompleteAuthSaga(ctx context.Context, opID string, sagaID string, responseBody []byte) error {
	tx, err := t.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("tx_manager: begin complete auth saga tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Mark admin operation completed
	_, err = tx.Exec(ctx, `
		UPDATE admin_operations
		SET status        = 'COMPLETED',
		    response_body = $1,
		    updated_at    = NOW()
		WHERE id = $2`,
		responseBody, opID,
	)
	if err != nil {
		return fmt.Errorf("tx_manager: update operation completed: %w", err)
	}

	// 2. Mark saga task completed
	_, err = tx.Exec(ctx, `
		UPDATE admin_saga_tasks
		SET status       = 'COMPLETED',
		    completed_at = NOW(),
		    locked_at    = NULL,
		    locked_by    = NULL,
		    updated_at   = NOW()
		WHERE id = $1`,
		sagaID,
	)
	if err != nil {
		return fmt.Errorf("tx_manager: update saga task completed: %w", err)
	}

	return tx.Commit(ctx)
}

// ─── Transactional helpers ────────────────────────────────────────────────────

func insertAuditLogTx(ctx context.Context, tx pgx.Tx, log *domain.AuditLog) error {
	meta, err := json.Marshal(log.Metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO admin_audit_log
			(id, admin_id, operation_id, request_id, action, target_type, target_id,
			 reason, metadata, ip_address, user_agent, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		log.ID, log.AdminID, log.OperationID, log.RequestID,
		log.Action, log.TargetType, log.TargetID, log.Reason,
		meta, log.IPAddress, log.UserAgent, log.CreatedAt,
	)
	return err
}

func insertOperationTx(ctx context.Context, tx pgx.Tx, op *domain.AdminOperation) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO admin_operations
			(id, admin_id, idempotency_key, request_id, operation_type,
			 target_id, reason, status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		op.ID, op.AdminID, op.IdempotencyKey, op.RequestID,
		op.OperationType, op.TargetID, op.Reason, op.Status,
		op.CreatedAt, op.UpdatedAt,
	)
	return err
}

func insertOutboxEventTx(ctx context.Context, tx pgx.Tx, event *domain.OutboxEvent) error {
	status := domain.OutboxStatusPending
	maxAttempts := 10
	if event.MaxAttempts > 0 {
		maxAttempts = event.MaxAttempts
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO admin_outbox
			(id, operation_id, topic, payload, published, status,
			 attempt_count, max_attempts, next_attempt_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,FALSE,$5,0,$6,$7,$8,$9)`,
		event.ID, event.OperationID, event.Topic, event.Payload, status,
		maxAttempts, event.CreatedAt, event.CreatedAt, event.CreatedAt,
	)
	return err
}

func insertSagaTaskTx(ctx context.Context, tx pgx.Tx, task *domain.SagaTask) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO admin_saga_tasks
			(id, operation_id, task_type, payload, status,
			 attempt_count, max_attempts, next_attempt_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		task.ID, task.OperationID, task.TaskType, task.Payload,
		task.Status, task.AttemptCount, task.MaxAttempts,
		task.NextAttemptAt, task.CreatedAt, task.UpdatedAt,
	)
	return err
}
