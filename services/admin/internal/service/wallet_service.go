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

type FreezeWalletRequest struct {
	AdminID        string
	IdempotencyKey string
	RequestID      string
	TargetUserID   string
	Asset          string
	Reason         string
	IPAddress      string
	UserAgent      string
}

type UnfreezeWalletRequest struct {
	AdminID        string
	IdempotencyKey string
	RequestID      string
	TargetUserID   string
	Asset          string
	Reason         string
	IPAddress      string
	UserAgent      string
}

// walletMutationParams is the internal parameter bag shared by FreezeWallet and UnfreezeWallet.
type walletMutationParams struct {
	adminID        string
	idempotencyKey string
	requestID      string
	userID         string
	asset          string
	freeze         bool
	reason         string
	ipAddress      string
	userAgent      string
	opType         string
	action         string
	topic          string
}

// ─── Methods ──────────────────────────────────────────────────────────────────

// FreezeWallet handles freezing a user's wallet asset with explicit failure reconciliation.
func (s *AdminService) FreezeWallet(ctx context.Context, req FreezeWalletRequest) (*domain.AdminOperation, error) {
	return s.executeWalletMutation(ctx, walletMutationParams{
		adminID:        req.AdminID,
		idempotencyKey: req.IdempotencyKey,
		requestID:      req.RequestID,
		userID:         req.TargetUserID,
		asset:          req.Asset,
		freeze:         true,
		reason:         req.Reason,
		ipAddress:      req.IPAddress,
		userAgent:      req.UserAgent,
		opType:         domain.OpFreezeWallet,
		action:         domain.ActionFreezeWallet,
		topic:          domain.TopicWalletFrozen,
	})
}

// UnfreezeWallet handles unfreezing a user's wallet asset with explicit failure reconciliation.
func (s *AdminService) UnfreezeWallet(ctx context.Context, req UnfreezeWalletRequest) (*domain.AdminOperation, error) {
	return s.executeWalletMutation(ctx, walletMutationParams{
		adminID:        req.AdminID,
		idempotencyKey: req.IdempotencyKey,
		requestID:      req.RequestID,
		userID:         req.TargetUserID,
		asset:          req.Asset,
		freeze:         false,
		reason:         req.Reason,
		ipAddress:      req.IPAddress,
		userAgent:      req.UserAgent,
		opType:         domain.OpUnfreezeWallet,
		action:         domain.ActionUnfreezeWallet,
		topic:          domain.TopicWalletUnfrozen,
	})
}

// executeWalletMutation is the shared implementation for FreezeWallet and UnfreezeWallet.
// It handles idempotency, DB commit, synchronous Wallet gRPC call, and reconciliation
// of in-flight operations left in PROCESSING by a previous timeout.
func (s *AdminService) executeWalletMutation(ctx context.Context, p walletMutationParams) (op *domain.AdminOperation, err error) {
	// 1. Idempotency & Reconciliation Check
	existing, lookupErr := s.opsRepo.GetByIdempotencyKey(ctx, p.adminID, p.idempotencyKey)
	if lookupErr != nil {
		return nil, fmt.Errorf("executeWalletMutation: idempotency lookup: %w", lookupErr)
	}

	if existing != nil {
		if existing.Status == domain.OperationStatusCompleted {
			return existing, nil
		}
		// Operation is in PROCESSING — previous attempt encountered ambiguous failure (e.g. timeout).
		// Reconcile by re-invoking the idempotent Wallet RPC using the same operation_id.
		s.log.Info("executeWalletMutation: reconciling existing in-flight operation",
			zap.String("operation_id", existing.ID),
			zap.String("user_id", p.userID),
			zap.String("asset", p.asset),
		)
		op = existing
	}

	metrics.RecordOperationStart(p.opType)
	start := time.Now()
	defer func() {
		metrics.RecordOperationComplete(p.opType, err == nil, time.Since(start))
	}()

	if existing == nil {
		// 2. Insert operation (PROCESSING) + audit log + outbox FIRST
		now := time.Now().UTC()
		opID := domain.MustNewV7()
		auditID := domain.MustNewV7()
		eventID := domain.MustNewV7()

		envelope := domain.EventEnvelope{
			EventID:     eventID,
			OperationID: opID,
			RequestID:   p.requestID,
			AdminID:     p.adminID,
			Action:      p.action,
			TargetID:    p.userID,
			Reason:      p.reason,
			Metadata:    map[string]string{"asset": p.asset},
			OccurredAt:  now,
		}
		envelopeBytes, _ := json.Marshal(envelope)

		txReq := repository.AdminOperationTxRequest{
			Operation: &domain.AdminOperation{
				ID:             opID,
				AdminID:        p.adminID,
				IdempotencyKey: p.idempotencyKey,
				RequestID:      p.requestID,
				OperationType:  p.opType,
				TargetID:       p.userID,
				Reason:         p.reason,
				Status:         domain.OperationStatusProcessing,
				CreatedAt:      now,
				UpdatedAt:      now,
			},
			AuditLog: &domain.AuditLog{
				ID:          auditID,
				AdminID:     p.adminID,
				OperationID: &opID,
				RequestID:   p.requestID,
				Action:      p.action,
				TargetType:  domain.TargetTypeWallet,
				TargetID:    p.userID,
				Reason:      p.reason,
				Metadata:    map[string]string{"asset": p.asset, "ip": p.ipAddress},
				IPAddress:   p.ipAddress,
				UserAgent:   p.userAgent,
				CreatedAt:   now,
			},
			OutboxEvent: &domain.OutboxEvent{
				ID:          eventID,
				OperationID: opID,
				Topic:       p.topic,
				Payload:     envelopeBytes,
				CreatedAt:   now,
			},
			SagaTask: nil,
		}

		result, txErr := s.txMgr.ExecAdminOperationTx(ctx, txReq)
		if txErr != nil {
			if isUniqueViolation(txErr) {
				return s.resolveConcurrentConflict(ctx, p.adminID, p.idempotencyKey)
			}
			return nil, fmt.Errorf("executeWalletMutation: transaction failed: %w", txErr)
		}
		op = result.Operation
	}

	// 3. Synchronous Wallet gRPC call
	isFrozen, walletErr := s.walletCli.FreezeWallet(ctx, p.userID, p.asset, p.reason, p.requestID, op.ID, p.freeze)
	if walletErr != nil {
		s.log.Error("executeWalletMutation: downstream wallet gRPC failed",
			zap.String("operation_id", op.ID),
			zap.String("user_id", p.userID),
			zap.String("asset", p.asset),
			zap.Error(walletErr),
		)
		// Keep operation in PROCESSING with diagnostic metadata so client retry reconciles cleanly.
		failBody, _ := json.Marshal(map[string]interface{}{
			"reconciliation_needed": true,
			"last_error":            walletErr.Error(),
			"failed_at":             time.Now().UTC().Format(time.RFC3339),
		})
		_ = s.opsRepo.UpdateStatus(ctx, op.ID, domain.OperationStatusProcessing, failBody)
		return nil, fmt.Errorf("wallet service error: %w", walletErr)
	}

	// 4. Update operation to COMPLETED
	responseBody, _ := json.Marshal(map[string]interface{}{
		"user_id":   p.userID,
		"asset":     p.asset,
		"is_frozen": isFrozen,
	})
	if err := s.opsRepo.UpdateStatus(ctx, op.ID, domain.OperationStatusCompleted, responseBody); err != nil {
		s.log.Error("executeWalletMutation: failed to mark operation completed",
			zap.String("operation_id", op.ID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("executeWalletMutation: mark operation completed: %w", err)
	}
	op.Status = domain.OperationStatusCompleted
	op.ResponseBody = responseBody

	s.log.Info("executeWalletMutation: completed successfully",
		zap.String("operation_id", op.ID),
		zap.String("user_id", p.userID),
		zap.String("asset", p.asset),
		zap.Bool("freeze", p.freeze),
	)
	return op, nil
}
