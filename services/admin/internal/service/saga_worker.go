package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/admin/internal/client"
	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/metrics"
	"tradedrift/services/admin/internal/repository"
)

// SagaWorker polls admin_saga_tasks for pending/retrying tasks and processes them.
// Uses explicit retry schedule with jitter and smart gRPC error classification.
// Lifecycle contract: Start must be called at most once. Stop must be called after Start.
// Workers cannot be restarted after Stop.
type SagaWorker struct {
	txMgr     repository.TxManager
	sagaRepo  repository.SagaRepository
	opsRepo   repository.OperationsRepository
	authCli   *client.AuthClient
	log       *zap.Logger
	interval  time.Duration
	done      chan struct{}
	wg        sync.WaitGroup
	startOnce sync.Once
	stopOnce  sync.Once
}

// NewSagaWorker constructs the SagaWorker background engine.
func NewSagaWorker(
	txMgr repository.TxManager,
	sagaRepo repository.SagaRepository,
	opsRepo repository.OperationsRepository,
	authCli *client.AuthClient,
	log *zap.Logger,
	interval time.Duration,
) *SagaWorker {
	return &SagaWorker{
		txMgr:    txMgr,
		sagaRepo: sagaRepo,
		opsRepo:  opsRepo,
		authCli:  authCli,
		log:      log,
		interval: interval,
		done:     make(chan struct{}),
	}
}

// Start launches the SagaWorker poll loop in the background. It is idempotent.
func (w *SagaWorker) Start(ctx context.Context) {
	w.startOnce.Do(func() {
		w.wg.Add(1)
		go func() {
			defer w.wg.Done()
			ticker := time.NewTicker(w.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					w.poll(ctx)
				case <-ctx.Done():
					return
				case <-w.done:
					return
				}
			}
		}()
	})
}

// Stop signals the SagaWorker to stop and waits for any in-flight task processing to complete. It is idempotent.
func (w *SagaWorker) Stop() {
	w.stopOnce.Do(func() {
		close(w.done)
	})
	w.wg.Wait()
}

func (w *SagaWorker) poll(ctx context.Context) {
	workerToken := domain.MustNewV7()
	tasks, err := w.sagaRepo.FetchDue(ctx, workerToken, 10)
	if err != nil {
		w.log.Error("SagaWorker: fetch due failed", zap.Error(err))
		metrics.SagaWorkerErrorsTotal.Inc()
		return
	}

	for _, task := range tasks {
		w.processTask(ctx, task, workerToken)
	}
}

func (w *SagaWorker) processTask(ctx context.Context, task *domain.SagaTask, workerToken string) {
	taskCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	switch task.TaskType {
	case domain.SagaTaskAuthInvalidateSessions:
		w.handleAuthInvalidation(taskCtx, task, workerToken)
	default:
		w.log.Error("SagaWorker: unknown task type",
			zap.String("task_id", task.ID),
			zap.String("task_type", task.TaskType),
		)
	}
}

func (w *SagaWorker) handleAuthInvalidation(ctx context.Context, task *domain.SagaTask, workerToken string) {
	var payload domain.SagaAuthPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		w.log.Error("SagaWorker: unmarshal payload failed",
			zap.String("task_id", task.ID),
			zap.Error(err),
		)
		return
	}

	start := time.Now()
	err := w.authCli.InvalidateUserSessions(ctx, payload.UserID, payload.Reason, payload.RequestID, task.OperationID)
	if err == nil {
		// ── Success: Atomically complete both saga task and operation in one transaction ──
		responseBody, _ := json.Marshal(map[string]string{"status": "suspended", "user_id": payload.UserID})
		if completeErr := w.txMgr.CompleteAuthSaga(ctx, task.OperationID, task.ID, responseBody); completeErr != nil {
			metrics.SagaWorkerErrorsTotal.Inc()
			w.log.Error("SagaWorker: CompleteAuthSaga failed",
				zap.String("task_id", task.ID),
				zap.String("operation_id", task.OperationID),
				zap.Error(completeErr),
			)
			return
		}
		metrics.RecordSagaComplete(task.TaskType, time.Since(start))
		w.log.Info("SagaWorker: AUTH_INVALIDATE_SESSIONS completed atomically",
			zap.String("task_id", task.ID),
			zap.String("operation_id", task.OperationID),
			zap.String("user_id", payload.UserID),
			zap.Int("attempt", task.AttemptCount+1),
		)
		return
	}

	metrics.SagaWorkerErrorsTotal.Inc()

	// ── Failure Handling ──────────────────────────────────────────────────────
	nextAttempt := task.AttemptCount + 1
	errMsg := fmt.Sprintf("attempt %d: %s", nextAttempt, err.Error())

	// If error is non-retryable (e.g. InvalidArgument, NotFound), terminate immediately without wasting 8 hours
	isRetryable := client.IsRetryableGRPCError(err)
	if !isRetryable || nextAttempt >= task.MaxAttempts {
		if markErr := w.sagaRepo.MarkExhausted(ctx, task.ID, workerToken, errMsg); markErr != nil {
			metrics.SagaWorkerErrorsTotal.Inc()
			if errors.Is(markErr, domain.ErrWorkerLeaseLost) {
				w.log.Warn("SagaWorker: lease lost before marking exhausted", zap.String("task_id", task.ID))
				return
			}
			w.log.Error("SagaWorker: MarkExhausted failed", zap.String("task_id", task.ID), zap.Error(markErr))
			return
		}
		metrics.RecordSagaExhausted(task.TaskType, time.Since(start))
		if updateErr := w.opsRepo.UpdateStatus(ctx, task.OperationID, domain.OperationStatusFailed, nil); updateErr != nil {
			metrics.SagaWorkerErrorsTotal.Inc()
			w.log.Error("SagaWorker: failed to mark operation FAILED",
				zap.String("operation_id", task.OperationID),
				zap.Error(updateErr),
			)
		}
		w.log.Error("CRITICAL_ALERT: SAGA_AUTH_REVOCATION_EXHAUSTED",
			zap.String("task_id", task.ID),
			zap.String("operation_id", task.OperationID),
			zap.String("user_id", payload.UserID),
			zap.Int("max_attempts", task.MaxAttempts),
			zap.Bool("is_retryable", isRetryable),
			zap.String("last_error", errMsg),
		)
		return
	}

	// Retryable error: schedule next attempt using exponential schedule + jitter
	baseDelay := domain.NextDelay(nextAttempt - 1)
	jitter := time.Duration(float64(baseDelay) * (0.1 * (2*rand.Float64() - 1)))
	nextAt := time.Now().UTC().Add(baseDelay + jitter)

	if updateErr := w.sagaRepo.UpdateRetry(ctx, task.ID, workerToken, nextAt, nextAttempt, errMsg); updateErr != nil {
		metrics.SagaWorkerErrorsTotal.Inc()
		if errors.Is(updateErr, domain.ErrWorkerLeaseLost) {
			w.log.Warn("SagaWorker: lease lost before scheduling retry", zap.String("task_id", task.ID))
			return
		}
		w.log.Error("SagaWorker: UpdateRetry failed", zap.String("task_id", task.ID), zap.Error(updateErr))
		return
	}
	w.log.Warn("SagaWorker: AUTH_INVALIDATE_SESSIONS retry scheduled",
		zap.String("task_id", task.ID),
		zap.String("user_id", payload.UserID),
		zap.Int("attempt", nextAttempt),
		zap.Time("next_attempt_at", nextAt),
		zap.Error(err),
	)
}
