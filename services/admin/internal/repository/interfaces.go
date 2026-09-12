package repository

import (
	"context"
	"time"

	"tradedrift/services/admin/internal/domain"
)

// AdminOperationTxRequest carries all records to be atomically committed in a single DB tx.
type AdminOperationTxRequest struct {
	Operation   *domain.AdminOperation
	AuditLog    *domain.AuditLog
	OutboxEvent *domain.OutboxEvent
	SagaTask    *domain.SagaTask // nil for operations that do not require async saga retry
}

// AdminOperationTxResult is the result of ExecAdminOperationTx.
type AdminOperationTxResult struct {
	Operation *domain.AdminOperation
}

// TxManager coordinates multi-table atomic mutations inside a single PostgreSQL transaction.
type TxManager interface {
	ExecAdminOperationTx(ctx context.Context, req AdminOperationTxRequest) (*AdminOperationTxResult, error)
	CompleteAuthSaga(ctx context.Context, opID string, sagaID string, responseBody []byte) error
}

// OperationsRepository manages admin_operations persistence.
type OperationsRepository interface {
	GetByIdempotencyKey(ctx context.Context, adminID, key string) (*domain.AdminOperation, error)
	GetByID(ctx context.Context, id string) (*domain.AdminOperation, error)
	Insert(ctx context.Context, op *domain.AdminOperation) error
	UpdateStatus(ctx context.Context, id string, status domain.OperationStatus, responseBody []byte) error
}

// OutboxBacklogStats provides operational metrics on unpublished events.
type OutboxBacklogStats struct {
	PendingCount int           `json:"pending_count"`
	OldestAge    time.Duration `json:"oldest_age"`
}

// OutboxRepository manages admin_outbox persistence and worker leasing.
type OutboxRepository interface {
	Insert(ctx context.Context, event *domain.OutboxEvent) error
	FetchDue(ctx context.Context, workerToken string, limit int) ([]*domain.OutboxEvent, error)
	MarkPublished(ctx context.Context, id string, workerToken string) error
	UpdateRetry(ctx context.Context, id string, workerToken string, nextAttemptAt time.Time, attemptCount int, lastError string) error
	GetBacklogStats(ctx context.Context) (*OutboxBacklogStats, error)
}

// SagaQueueStats provides operational metrics on the saga task queue.
type SagaQueueStats struct {
	PendingCount   int `json:"pending_count"`
	RetryingCount  int `json:"retrying_count"`
	ExhaustedCount int `json:"exhausted_count"`
}

// SagaRepository manages admin_saga_tasks persistence and worker leasing.
type SagaRepository interface {
	Insert(ctx context.Context, task *domain.SagaTask) error
	FetchDue(ctx context.Context, workerToken string, limit int) ([]*domain.SagaTask, error)
	UpdateRetry(ctx context.Context, id string, workerToken string, nextAttemptAt time.Time, attemptCount int, lastError string) error
	MarkCompleted(ctx context.Context, id string, workerToken string) error
	MarkExhausted(ctx context.Context, id string, workerToken string, lastError string) error
	GetQueueStats(ctx context.Context) (*SagaQueueStats, error)
}

// AuditRepository provides read access to the append-only audit log.
type AuditRepository interface {
	Insert(ctx context.Context, log *domain.AuditLog) error
}
