package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/admin/internal/client"
	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/repository"
)

// SagaWorker polls admin_saga_tasks for pending/retrying tasks and processes them.
// Uses explicit retry schedule with jitter and smart gRPC error classification.
type SagaWorker struct {
	sagaRepo repository.SagaRepository
	opsRepo  repository.OperationsRepository
	authCli  *client.AuthClient
	log      *zap.Logger
	interval time.Duration
	done     chan struct{}
}

// NewSagaWorker constructs the SagaWorker background engine.
func NewSagaWorker(
	sagaRepo repository.SagaRepository,
	opsRepo repository.OperationsRepository,
	authCli *client.AuthClient,
	log *zap.Logger,
	interval time.Duration,
) *SagaWorker {
	return &SagaWorker{
		sagaRepo: sagaRepo,
		opsRepo:  opsRepo,
		authCli:  authCli,
		log:      log,
		interval: interval,
		done:     make(chan struct{}),
	}
}

// Start launches the SagaWorker poll loop in the background.
func (w *SagaWorker) Start(ctx context.Context) {
	go func() {
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
}

// Stop signals the SagaWorker to stop.
func (w *SagaWorker) Stop() {
	close(w.done)
}

func (w *SagaWorker) poll(ctx context.Context) {
	workerToken := domain.MustNewV7()
	tasks, err := w.sagaRepo.FetchDue(ctx, workerToken, 10)
	if err != nil {
		w.log.Error("SagaWorker: fetch due failed", zap.Error(err))
		return
	}

	for _, task := range tasks {
		w.processTask(ctx, task)
	}
}

func (w *SagaWorker) processTask(ctx context.Context, task *domain.SagaTask) {
	taskCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	switch task.TaskType {
	case domain.SagaTaskAuthInvalidateSessions:
		w.handleAuthInvalidation(taskCtx, task)
	default:
		w.log.Error("SagaWorker: unknown task type",
			zap.String("task_id", task.ID),
			zap.String("task_type", task.TaskType),
		)
	}
}

func (w *SagaWorker) handleAuthInvalidation(ctx context.Context, task *domain.SagaTask) {
	var payload domain.SagaAuthPayload
	if err := json.Unmarshal(task.Payload, &payload); err != nil {
		w.log.Error("SagaWorker: unmarshal payload failed",
			zap.String("task_id", task.ID),
			zap.Error(err),
		)
		return
	}

	err := w.authCli.InvalidateUserSessions(ctx, payload.UserID, payload.Reason, payload.RequestID, task.OperationID)
	if err == nil {
		// ── Success ───────────────────────────────────────────────────────────
		_ = w.sagaRepo.MarkCompleted(ctx, task.ID)
		responseBody, _ := json.Marshal(map[string]string{"status": "suspended", "user_id": payload.UserID})
		_ = w.opsRepo.UpdateStatus(ctx, task.OperationID, domain.OperationStatusCompleted, responseBody)
		w.log.Info("SagaWorker: AUTH_INVALIDATE_SESSIONS completed",
			zap.String("task_id", task.ID),
			zap.String("user_id", payload.UserID),
			zap.Int("attempt", task.AttemptCount+1),
		)
		return
	}

	// ── Failure Handling ──────────────────────────────────────────────────────
	nextAttempt := task.AttemptCount + 1
	errMsg := fmt.Sprintf("attempt %d: %s", nextAttempt, err.Error())

	// If error is non-retryable (e.g. InvalidArgument, NotFound), terminate immediately without wasting 8 hours
	isRetryable := client.IsRetryableGRPCError(err)
	if !isRetryable || nextAttempt >= task.MaxAttempts {
		_ = w.sagaRepo.MarkExhausted(ctx, task.ID, errMsg)
		_ = w.opsRepo.UpdateStatus(ctx, task.OperationID, domain.OperationStatusFailed, nil)
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

	if updateErr := w.sagaRepo.UpdateRetry(ctx, task.ID, nextAt, nextAttempt, errMsg); updateErr != nil {
		w.log.Error("SagaWorker: UpdateRetry failed", zap.String("task_id", task.ID), zap.Error(updateErr))
	}
	w.log.Warn("SagaWorker: AUTH_INVALIDATE_SESSIONS retry scheduled",
		zap.String("task_id", task.ID),
		zap.String("user_id", payload.UserID),
		zap.Int("attempt", nextAttempt),
		zap.Time("next_attempt_at", nextAt),
		zap.Error(err),
	)
}
