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

// OperationsSummaryStats contains aggregated business metrics on administrative operations.
type OperationsSummaryStats struct {
	TotalOperations            int            `json:"total_operations"`
	CompletedOperations        int            `json:"completed_operations"`
	FailedOperations           int            `json:"failed_operations"`
	PendingOperations          int            `json:"pending_operations"`
	ProcessingOperations       int            `json:"processing_operations"`
	OldestProcessingAgeSeconds float64        `json:"oldest_processing_age_seconds"`
	ByType                     map[string]int `json:"by_type"`
}

// MarketStateSnapshot captures the current operational halt state of a trading pair.
type MarketStateSnapshot struct {
	MarketID  string    `json:"market_id"`
	IsHalted  bool      `json:"is_halted"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OperationsRepository manages admin_operations persistence.
type OperationsRepository interface {
	GetByIdempotencyKey(ctx context.Context, adminID, key string) (*domain.AdminOperation, error)
	GetByID(ctx context.Context, id string) (*domain.AdminOperation, error)
	Insert(ctx context.Context, op *domain.AdminOperation) error
	UpdateStatus(ctx context.Context, id string, status domain.OperationStatus, responseBody []byte) error
	GetOperationsSummary(ctx context.Context, since time.Time) (*OperationsSummaryStats, error)
	GetLatestMarketStates(ctx context.Context) ([]MarketStateSnapshot, error)
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

// AuditAnalyticsStats provides aggregated forensic statistics on administrative actions.
type AuditAnalyticsStats struct {
	TotalAuditEvents int            `json:"total_audit_events"`
	ByAction         map[string]int `json:"by_action"`
	ByTargetType     map[string]int `json:"by_target_type"`
	TopAdmins        map[string]int `json:"top_admins"`
}

// AuditRepository provides read access and forensic analytics on the append-only audit log.
type AuditRepository interface {
	Insert(ctx context.Context, log *domain.AuditLog) error
	GetAuditLogsInWindow(ctx context.Context, start, end time.Time, limit int) ([]domain.AuditLog, error)
	GetAuditStats(ctx context.Context, since time.Time) (*AuditAnalyticsStats, error)
}

// IncidentFilter defines query parameters for incident listing.
type IncidentFilter struct {
	ServiceName string
	Status      domain.IncidentStatus
	Severity    domain.IncidentSeverity
	Since       *time.Time
	Limit       int
	Offset      int
}

// IncidentStats captures incident counts and MTTD/MTTR performance metrics.
type IncidentStats struct {
	TotalIncidents     int      `json:"total_incidents"`
	OpenIncidents      int      `json:"open_incidents"`
	ResolvedIncidents  int      `json:"resolved_incidents"`
	AverageMTTDSeconds *float64 `json:"average_mttd_seconds,omitempty"` // Omitted for autonomous polling probe detections
	AverageMTTRSeconds float64  `json:"average_mttr_seconds"`
}

// IncidentRepository manages admin_incidents persistence and lifecycle state transitions.
type IncidentRepository interface {
	Create(ctx context.Context, incident *domain.Incident) error
	GetActiveByService(ctx context.Context, serviceName string) (*domain.Incident, error)
	GetByID(ctx context.Context, id string) (*domain.Incident, error)
	UpdateHeartbeat(ctx context.Context, id string, lastSeenAt time.Time, failureCount int) error
	Resolve(ctx context.Context, id string, resolvedAt time.Time, mttrSeconds float64, rootCause string) error
	List(ctx context.Context, filter IncidentFilter) ([]*domain.Incident, error)
	GetStats(ctx context.Context, since time.Time) (*IncidentStats, error)
}
