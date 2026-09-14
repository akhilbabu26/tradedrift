package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/metrics"
	"tradedrift/services/admin/internal/repository"
)

// ─── Request Types ────────────────────────────────────────────────────────────

type SuspendUserRequest struct {
	AdminID        string
	IdempotencyKey string
	RequestID      string
	TargetUserID   string
	Reason         string
	IPAddress      string
	UserAgent      string
}

type UnsuspendUserRequest struct {
	AdminID        string
	IdempotencyKey string
	RequestID      string
	TargetUserID   string
	Reason         string
	IPAddress      string
	UserAgent      string
}

// ─── Methods ──────────────────────────────────────────────────────────────────

// SuspendUser executes the user suspension workflow:
//  1. Checks caller idempotency.
//  2. Atomically commits in DB: operation (PROCESSING) + immutable audit log + outbox event + saga task.
//  3. Catches concurrent duplicate requests via Postgres unique constraint (23505) and returns existing.
//  4. Synchronously attempts Auth session invalidation. If Auth fails, SagaWorker asynchronously completes it.
func (s *AdminService) SuspendUser(ctx context.Context, req SuspendUserRequest) (op *domain.AdminOperation, err error) {
	existing, lookupErr := s.opsRepo.GetByIdempotencyKey(ctx, req.AdminID, req.IdempotencyKey)
	if lookupErr != nil {
		return nil, fmt.Errorf("SuspendUser: idempotency lookup: %w", lookupErr)
	}
	if existing != nil {
		if existing.Status == domain.OperationStatusProcessing {
			return nil, domain.ErrOperationInProgress
		}
		return existing, nil
	}

	metrics.RecordOperationStart(domain.OpSuspendUser)
	start := time.Now()
	defer func() {
		metrics.RecordOperationComplete(domain.OpSuspendUser, err == nil, time.Since(start))
	}()

	now := time.Now().UTC()
	opID := domain.MustNewV7()
	auditID := domain.MustNewV7()
	eventID := domain.MustNewV7()
	sagaID := domain.MustNewV7()

	envelope := domain.EventEnvelope{
		EventID:     eventID,
		OperationID: opID,
		RequestID:   req.RequestID,
		AdminID:     req.AdminID,
		Action:      domain.ActionSuspendUser,
		TargetID:    req.TargetUserID,
		Reason:      req.Reason,
		OccurredAt:  now,
	}
	envelopeBytes, _ := json.Marshal(envelope)

	sagaPayload, _ := json.Marshal(domain.SagaAuthPayload{
		UserID:    req.TargetUserID,
		Reason:    req.Reason,
		RequestID: req.RequestID,
	})

	txReq := repository.AdminOperationTxRequest{
		Operation: &domain.AdminOperation{
			ID:             opID,
			AdminID:        req.AdminID,
			IdempotencyKey: req.IdempotencyKey,
			RequestID:      req.RequestID,
			OperationType:  domain.OpSuspendUser,
			TargetID:       req.TargetUserID,
			Reason:         req.Reason,
			Status:         domain.OperationStatusProcessing,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		AuditLog: &domain.AuditLog{
			ID:          auditID,
			AdminID:     req.AdminID,
			OperationID: &opID,
			RequestID:   req.RequestID,
			Action:      domain.ActionSuspendUser,
			TargetType:  domain.TargetTypeUser,
			TargetID:    req.TargetUserID,
			Reason:      req.Reason,
			Metadata:    map[string]string{"ip": req.IPAddress},
			IPAddress:   req.IPAddress,
			UserAgent:   req.UserAgent,
			CreatedAt:   now,
		},
		OutboxEvent: &domain.OutboxEvent{
			ID:          eventID,
			OperationID: opID,
			Topic:       domain.TopicUserSuspended,
			Payload:     envelopeBytes,
			CreatedAt:   now,
		},
		SagaTask: &domain.SagaTask{
			ID:            sagaID,
			OperationID:   opID,
			TaskType:      domain.SagaTaskAuthInvalidateSessions,
			Payload:       sagaPayload,
			Status:        domain.SagaStatusPending,
			AttemptCount:  0,
			MaxAttempts:   10,
			NextAttemptAt: now,
			CreatedAt:     now,
			UpdatedAt:     now,
		},
	}

	result, err := s.txMgr.ExecAdminOperationTx(ctx, txReq)
	if err != nil {
		if isUniqueViolation(err) {
			return s.resolveConcurrentConflict(ctx, req.AdminID, req.IdempotencyKey)
		}
		return nil, fmt.Errorf("SuspendUser: transaction failed: %w", err)
	}

	// Synchronous Auth suspension attempt
	authErr := s.authCli.SuspendUser(ctx, req.TargetUserID, req.Reason, req.RequestID, opID)
	if authErr != nil {
		s.log.Warn("SuspendUser: immediate auth suspension failed; saga will retry",
			zap.String("operation_id", opID),
			zap.String("user_id", req.TargetUserID),
			zap.Error(authErr),
		)
		// Operation remains in PROCESSING; SagaWorker will retry to completion
		return result.Operation, nil
	}

	// Immediate Auth success -> mark BOTH operation and saga task COMPLETED atomically
	responseBody, _ := json.Marshal(map[string]string{"status": "suspended", "user_id": req.TargetUserID})
	if err := s.txMgr.CompleteAuthSaga(ctx, opID, sagaID, responseBody); err != nil {
		s.log.Error("SuspendUser: failed to mark auth saga and operation completed",
			zap.String("operation_id", opID),
			zap.String("saga_id", sagaID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("SuspendUser: complete saga transaction: %w", err)
	}
	result.Operation.Status = domain.OperationStatusCompleted
	result.Operation.ResponseBody = responseBody

	// Synchronous Redis write with retries
	redisKey := "user:suspended:" + req.TargetUserID
	if setErr := s.SetRedisWithRetry(ctx, redisKey, "1", 3); setErr != nil {
		s.log.Error("SuspendUser: synchronous Redis write failed after retries; reconciler will restore",
			zap.String("operation_id", opID),
			zap.String("user_id", req.TargetUserID),
			zap.Error(setErr),
		)
	} else {
		s.log.Info("SuspendUser: synchronous Redis write succeeded",
			zap.String("key", redisKey),
			zap.String("user_id", req.TargetUserID),
		)
	}

	s.log.Info("SuspendUser: completed successfully",
		zap.String("operation_id", opID),
		zap.String("saga_id", sagaID),
		zap.String("user_id", req.TargetUserID),
	)
	return result.Operation, nil
}

// UnsuspendUser restores a suspended user. Purely event-driven + audit recorded.
func (s *AdminService) UnsuspendUser(ctx context.Context, req UnsuspendUserRequest) (op *domain.AdminOperation, err error) {
	existing, lookupErr := s.opsRepo.GetByIdempotencyKey(ctx, req.AdminID, req.IdempotencyKey)
	if lookupErr != nil {
		return nil, fmt.Errorf("UnsuspendUser: idempotency lookup: %w", lookupErr)
	}
	if existing != nil {
		return existing, nil
	}

	metrics.RecordOperationStart(domain.OpUnsuspendUser)
	start := time.Now()
	defer func() {
		metrics.RecordOperationComplete(domain.OpUnsuspendUser, err == nil, time.Since(start))
	}()

	now := time.Now().UTC()
	opID := domain.MustNewV7()
	auditID := domain.MustNewV7()
	eventID := domain.MustNewV7()

	envelope := domain.EventEnvelope{
		EventID:     eventID,
		OperationID: opID,
		RequestID:   req.RequestID,
		AdminID:     req.AdminID,
		Action:      domain.ActionUnsuspendUser,
		TargetID:    req.TargetUserID,
		Reason:      req.Reason,
		OccurredAt:  now,
	}
	envelopeBytes, _ := json.Marshal(envelope)
	responseBody, _ := json.Marshal(map[string]string{"status": "unsuspended", "user_id": req.TargetUserID})

	txReq := repository.AdminOperationTxRequest{
		Operation: &domain.AdminOperation{
			ID:             opID,
			AdminID:        req.AdminID,
			IdempotencyKey: req.IdempotencyKey,
			RequestID:      req.RequestID,
			OperationType:  domain.OpUnsuspendUser,
			TargetID:       req.TargetUserID,
			Reason:         req.Reason,
			Status:         domain.OperationStatusCompleted,
			ResponseBody:   responseBody,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		AuditLog: &domain.AuditLog{
			ID:          auditID,
			AdminID:     req.AdminID,
			OperationID: &opID,
			RequestID:   req.RequestID,
			Action:      domain.ActionUnsuspendUser,
			TargetType:  domain.TargetTypeUser,
			TargetID:    req.TargetUserID,
			Reason:      req.Reason,
			Metadata:    map[string]string{"ip": req.IPAddress},
			IPAddress:   req.IPAddress,
			UserAgent:   req.UserAgent,
			CreatedAt:   now,
		},
		OutboxEvent: &domain.OutboxEvent{
			ID:          eventID,
			OperationID: opID,
			Topic:       domain.TopicUserUnsuspended,
			Payload:     envelopeBytes,
			CreatedAt:   now,
		},
		SagaTask: nil,
	}

	result, txErr := s.txMgr.ExecAdminOperationTx(ctx, txReq)
	if txErr != nil {
		if isUniqueViolation(txErr) {
			return s.resolveConcurrentConflict(ctx, req.AdminID, req.IdempotencyKey)
		}
		err = fmt.Errorf("UnsuspendUser: transaction failed: %w", txErr)
		return nil, err
	}

	// Synchronous Auth unsuspension call
	if authErr := s.authCli.UnsuspendUser(ctx, req.TargetUserID, req.Reason, req.RequestID, opID); authErr != nil {
		s.log.Error("UnsuspendUser: auth service unsuspend call failed",
			zap.String("operation_id", opID),
			zap.String("user_id", req.TargetUserID),
			zap.Error(authErr),
		)
	}

	// Synchronous Redis DEL with retries
	redisKey := "user:suspended:" + req.TargetUserID
	if delErr := s.DelRedisWithRetry(ctx, redisKey, 3); delErr != nil {
		s.log.Error("UnsuspendUser: synchronous Redis DEL failed after retries; reconciler will heal",
			zap.String("operation_id", opID),
			zap.String("user_id", req.TargetUserID),
			zap.Error(delErr),
		)
	} else {
		s.log.Info("UnsuspendUser: synchronous Redis DEL succeeded",
			zap.String("key", redisKey),
			zap.String("user_id", req.TargetUserID),
		)
	}

	s.log.Info("UnsuspendUser: completed successfully",
		zap.String("operation_id", opID),
		zap.String("user_id", req.TargetUserID),
	)
	return result.Operation, nil
}
