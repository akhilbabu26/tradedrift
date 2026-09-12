package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"tradedrift/services/admin/internal/client"
	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/repository"
)

// AdminService orchestrates all administrative mutations.
// It enforces atomic audit consistency, caller idempotency, event publication via outbox,
// and safe reconciliation across downstream services (Auth, Wallet).
type AdminService struct {
	txMgr     repository.TxManager
	opsRepo   repository.OperationsRepository
	authCli   *client.AuthClient
	walletCli *client.WalletClient
	log       *zap.Logger
}

// NewAdminService constructs the core admin service.
func NewAdminService(
	txMgr repository.TxManager,
	opsRepo repository.OperationsRepository,
	authCli *client.AuthClient,
	walletCli *client.WalletClient,
	log *zap.Logger,
) *AdminService {
	return &AdminService{
		txMgr:     txMgr,
		opsRepo:   opsRepo,
		authCli:   authCli,
		walletCli: walletCli,
		log:       log,
	}
}

// ─── Request Structs ─────────────────────────────────────────────────────────

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

type HaltMarketRequest struct {
	AdminID        string
	IdempotencyKey string
	RequestID      string
	MarketID       string
	Reason         string
	IPAddress      string
	UserAgent      string
}

type ResumeMarketRequest struct {
	AdminID        string
	IdempotencyKey string
	RequestID      string
	MarketID       string
	Reason         string
	IPAddress      string
	UserAgent      string
}

// ─── 1. Suspend User ─────────────────────────────────────────────────────────

// SuspendUser executes the user suspension workflow:
// 1. Checks caller idempotency.
// 2. Atomically commits in DB: operation (PROCESSING) + immutable audit log + outbox event + saga task.
// 3. Catches concurrent duplicate requests via Postgres unique constraint (23505) and returns existing.
// 4. Synchronously attempts Auth session invalidation. If Auth fails, SagaWorker asynchronously completes it.
func (s *AdminService) SuspendUser(ctx context.Context, req SuspendUserRequest) (*domain.AdminOperation, error) {
	existing, err := s.opsRepo.GetByIdempotencyKey(ctx, req.AdminID, req.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("SuspendUser: idempotency lookup: %w", err)
	}
	if existing != nil {
		if existing.Status == domain.OperationStatusProcessing {
			return nil, domain.ErrOperationInProgress
		}
		return existing, nil
	}

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

	// Synchronous Auth invalidation attempt
	authErr := s.authCli.InvalidateUserSessions(ctx, req.TargetUserID, req.Reason, req.RequestID, opID)
	if authErr != nil {
		s.log.Warn("SuspendUser: immediate auth session invalidation failed; saga will retry",
			zap.String("operation_id", opID),
			zap.String("user_id", req.TargetUserID),
			zap.Error(authErr),
		)
		// Operation remains in PROCESSING; SagaWorker will retry to completion
		return result.Operation, nil
	}

	// Immediate Auth success -> mark operation COMPLETED
	responseBody, _ := json.Marshal(map[string]string{"status": "suspended", "user_id": req.TargetUserID})
	_ = s.opsRepo.UpdateStatus(ctx, opID, domain.OperationStatusCompleted, responseBody)
	result.Operation.Status = domain.OperationStatusCompleted
	result.Operation.ResponseBody = responseBody

	s.log.Info("SuspendUser: completed successfully",
		zap.String("operation_id", opID),
		zap.String("user_id", req.TargetUserID),
	)
	return result.Operation, nil
}

// ─── 2. Unsuspend User ───────────────────────────────────────────────────────

// UnsuspendUser restores a suspended user. Purely event-driven + audit recorded.
func (s *AdminService) UnsuspendUser(ctx context.Context, req UnsuspendUserRequest) (*domain.AdminOperation, error) {
	existing, err := s.opsRepo.GetByIdempotencyKey(ctx, req.AdminID, req.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("UnsuspendUser: idempotency lookup: %w", err)
	}
	if existing != nil {
		return existing, nil
	}

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

	result, err := s.txMgr.ExecAdminOperationTx(ctx, txReq)
	if err != nil {
		if isUniqueViolation(err) {
			return s.resolveConcurrentConflict(ctx, req.AdminID, req.IdempotencyKey)
		}
		return nil, fmt.Errorf("UnsuspendUser: transaction failed: %w", err)
	}

	s.log.Info("UnsuspendUser: completed successfully",
		zap.String("operation_id", opID),
		zap.String("user_id", req.TargetUserID),
	)
	return result.Operation, nil
}

// ─── 3. Freeze Wallet ────────────────────────────────────────────────────────

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

// ─── 4. Unfreeze Wallet ──────────────────────────────────────────────────────

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

func (s *AdminService) executeWalletMutation(ctx context.Context, p walletMutationParams) (*domain.AdminOperation, error) {
	// 1. Idempotency & Reconciliation Check
	existing, err := s.opsRepo.GetByIdempotencyKey(ctx, p.adminID, p.idempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("executeWalletMutation: idempotency lookup: %w", err)
	}

	var op *domain.AdminOperation
	if existing != nil {
		if existing.Status == domain.OperationStatusCompleted {
			return existing, nil
		}
		// Operation is in PROCESSING — previous attempt encountered ambiguous failure (e.g. timeout)
		// Reconcile by re-invoking the idempotent Wallet RPC using the same operation_id
		s.log.Info("executeWalletMutation: reconciling existing in-flight operation",
			zap.String("operation_id", existing.ID),
			zap.String("user_id", p.userID),
			zap.String("asset", p.asset),
		)
		op = existing
	} else {
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
		// Keep operation in PROCESSING status with error info so client retry reconciles cleanly
		return nil, fmt.Errorf("wallet service error: %w", walletErr)
	}

	// 4. Update operation to COMPLETED
	responseBody, _ := json.Marshal(map[string]interface{}{
		"user_id":   p.userID,
		"asset":     p.asset,
		"is_frozen": isFrozen,
	})
	_ = s.opsRepo.UpdateStatus(ctx, op.ID, domain.OperationStatusCompleted, responseBody)
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

// ─── 5. Halt Market ──────────────────────────────────────────────────────────

// HaltMarket publishes a halt event via transactional outbox. Matching Engine consumes it.
func (s *AdminService) HaltMarket(ctx context.Context, req HaltMarketRequest) (*domain.AdminOperation, error) {
	existing, err := s.opsRepo.GetByIdempotencyKey(ctx, req.AdminID, req.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("HaltMarket: idempotency lookup: %w", err)
	}
	if existing != nil {
		return existing, nil
	}

	now := time.Now().UTC()
	opID := domain.MustNewV7()
	auditID := domain.MustNewV7()
	eventID := domain.MustNewV7()

	envelope := domain.EventEnvelope{
		EventID:     eventID,
		OperationID: opID,
		RequestID:   req.RequestID,
		AdminID:     req.AdminID,
		Action:      domain.ActionHaltMarket,
		TargetID:    req.MarketID,
		Reason:      req.Reason,
		OccurredAt:  now,
	}
	envelopeBytes, _ := json.Marshal(envelope)
	responseBody, _ := json.Marshal(map[string]string{"status": "halted", "market_id": req.MarketID})

	txReq := repository.AdminOperationTxRequest{
		Operation: &domain.AdminOperation{
			ID:             opID,
			AdminID:        req.AdminID,
			IdempotencyKey: req.IdempotencyKey,
			RequestID:      req.RequestID,
			OperationType:  domain.OpHaltMarket,
			TargetID:       req.MarketID,
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
			Action:      domain.ActionHaltMarket,
			TargetType:  domain.TargetTypeMarket,
			TargetID:    req.MarketID,
			Reason:      req.Reason,
			Metadata:    map[string]string{"market_id": req.MarketID, "ip": req.IPAddress},
			IPAddress:   req.IPAddress,
			UserAgent:   req.UserAgent,
			CreatedAt:   now,
		},
		OutboxEvent: &domain.OutboxEvent{
			ID:          eventID,
			OperationID: opID,
			Topic:       domain.TopicMarketHalted,
			Payload:     envelopeBytes,
			CreatedAt:   now,
		},
		SagaTask: nil,
	}

	result, err := s.txMgr.ExecAdminOperationTx(ctx, txReq)
	if err != nil {
		if isUniqueViolation(err) {
			return s.resolveConcurrentConflict(ctx, req.AdminID, req.IdempotencyKey)
		}
		return nil, fmt.Errorf("HaltMarket: transaction failed: %w", err)
	}

	s.log.Info("HaltMarket: completed successfully",
		zap.String("operation_id", opID),
		zap.String("market_id", req.MarketID),
	)
	return result.Operation, nil
}

// ─── 6. Resume Market ────────────────────────────────────────────────────────

// ResumeMarket publishes a resume event via transactional outbox. Matching Engine consumes it.
func (s *AdminService) ResumeMarket(ctx context.Context, req ResumeMarketRequest) (*domain.AdminOperation, error) {
	existing, err := s.opsRepo.GetByIdempotencyKey(ctx, req.AdminID, req.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("ResumeMarket: idempotency lookup: %w", err)
	}
	if existing != nil {
		return existing, nil
	}

	now := time.Now().UTC()
	opID := domain.MustNewV7()
	auditID := domain.MustNewV7()
	eventID := domain.MustNewV7()

	envelope := domain.EventEnvelope{
		EventID:     eventID,
		OperationID: opID,
		RequestID:   req.RequestID,
		AdminID:     req.AdminID,
		Action:      domain.ActionResumeMarket,
		TargetID:    req.MarketID,
		Reason:      req.Reason,
		OccurredAt:  now,
	}
	envelopeBytes, _ := json.Marshal(envelope)
	responseBody, _ := json.Marshal(map[string]string{"status": "resumed", "market_id": req.MarketID})

	txReq := repository.AdminOperationTxRequest{
		Operation: &domain.AdminOperation{
			ID:             opID,
			AdminID:        req.AdminID,
			IdempotencyKey: req.IdempotencyKey,
			RequestID:      req.RequestID,
			OperationType:  domain.OpResumeMarket,
			TargetID:       req.MarketID,
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
			Action:      domain.ActionResumeMarket,
			TargetType:  domain.TargetTypeMarket,
			TargetID:    req.MarketID,
			Reason:      req.Reason,
			Metadata:    map[string]string{"market_id": req.MarketID, "ip": req.IPAddress},
			IPAddress:   req.IPAddress,
			UserAgent:   req.UserAgent,
			CreatedAt:   now,
		},
		OutboxEvent: &domain.OutboxEvent{
			ID:          eventID,
			OperationID: opID,
			Topic:       domain.TopicMarketResumed,
			Payload:     envelopeBytes,
			CreatedAt:   now,
		},
		SagaTask: nil,
	}

	result, err := s.txMgr.ExecAdminOperationTx(ctx, txReq)
	if err != nil {
		if isUniqueViolation(err) {
			return s.resolveConcurrentConflict(ctx, req.AdminID, req.IdempotencyKey)
		}
		return nil, fmt.Errorf("ResumeMarket: transaction failed: %w", err)
	}

	s.log.Info("ResumeMarket: completed successfully",
		zap.String("operation_id", opID),
		zap.String("market_id", req.MarketID),
	)
	return result.Operation, nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func (s *AdminService) resolveConcurrentConflict(ctx context.Context, adminID, key string) (*domain.AdminOperation, error) {
	existing, err := s.opsRepo.GetByIdempotencyKey(ctx, adminID, key)
	if err != nil {
		return nil, fmt.Errorf("resolveConcurrentConflict: lookup failed: %w", err)
	}
	if existing != nil {
		if existing.Status == domain.OperationStatusProcessing {
			return nil, domain.ErrOperationInProgress
		}
		return existing, nil
	}
	return nil, domain.ErrOperationInProgress
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
