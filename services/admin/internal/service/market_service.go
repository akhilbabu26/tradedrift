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

// ─── Methods ──────────────────────────────────────────────────────────────────

// HaltMarket publishes a halt event via transactional outbox. Matching Engine consumes it.
func (s *AdminService) HaltMarket(ctx context.Context, req HaltMarketRequest) (op *domain.AdminOperation, err error) {
	existing, lookupErr := s.opsRepo.GetByIdempotencyKey(ctx, req.AdminID, req.IdempotencyKey)
	if lookupErr != nil {
		return nil, fmt.Errorf("HaltMarket: idempotency lookup: %w", lookupErr)
	}
	if existing != nil {
		return existing, nil
	}

	metrics.RecordOperationStart(domain.OpHaltMarket)
	start := time.Now()
	defer func() {
		metrics.RecordOperationComplete(domain.OpHaltMarket, err == nil, time.Since(start))
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

	result, txErr := s.txMgr.ExecAdminOperationTx(ctx, txReq)
	if txErr != nil {
		if isUniqueViolation(txErr) {
			return s.resolveConcurrentConflict(ctx, req.AdminID, req.IdempotencyKey)
		}
		err = fmt.Errorf("HaltMarket: transaction failed: %w", txErr)
		return nil, err
	}

	// Synchronous Redis write with 3 retries (50ms, 150ms, 300ms)
	redisKey := "market:halted:" + req.MarketID
	if setErr := s.SetRedisWithRetry(ctx, redisKey, "1", 3); setErr != nil {
		s.log.Error("HaltMarket: synchronous Redis write failed after retries; reconciler will restore",
			zap.String("operation_id", opID),
			zap.String("market_id", req.MarketID),
			zap.Error(setErr),
		)
	} else {
		s.log.Info("HaltMarket: synchronous Redis write succeeded",
			zap.String("key", redisKey),
			zap.String("operation_id", opID),
		)
	}

	// Phase 3 Metric: Record stateful market halt and start timestamp
	metrics.RecordMarketHalted(req.MarketID, now)

	s.log.Info("HaltMarket: completed successfully",
		zap.String("operation_id", opID),
		zap.String("market_id", req.MarketID),
	)
	return result.Operation, nil
}

// ResumeMarket publishes a resume event via transactional outbox. Matching Engine consumes it.
func (s *AdminService) ResumeMarket(ctx context.Context, req ResumeMarketRequest) (op *domain.AdminOperation, err error) {
	existing, lookupErr := s.opsRepo.GetByIdempotencyKey(ctx, req.AdminID, req.IdempotencyKey)
	if lookupErr != nil {
		return nil, fmt.Errorf("ResumeMarket: idempotency lookup: %w", lookupErr)
	}
	if existing != nil {
		return existing, nil
	}

	metrics.RecordOperationStart(domain.OpResumeMarket)
	start := time.Now()
	defer func() {
		metrics.RecordOperationComplete(domain.OpResumeMarket, err == nil, time.Since(start))
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

	result, txErr := s.txMgr.ExecAdminOperationTx(ctx, txReq)
	if txErr != nil {
		if isUniqueViolation(txErr) {
			return s.resolveConcurrentConflict(ctx, req.AdminID, req.IdempotencyKey)
		}
		err = fmt.Errorf("ResumeMarket: transaction failed: %w", txErr)
		return nil, err
	}

	// Synchronous Redis DEL with 3 retries (50ms, 150ms, 300ms)
	redisKey := "market:halted:" + req.MarketID
	if delErr := s.DelRedisWithRetry(ctx, redisKey, 3); delErr != nil {
		s.log.Error("ResumeMarket: synchronous Redis DEL failed after retries; reconciler will heal",
			zap.String("operation_id", opID),
			zap.String("market_id", req.MarketID),
			zap.Error(delErr),
		)
	} else {
		s.log.Info("ResumeMarket: synchronous Redis DEL succeeded",
			zap.String("key", redisKey),
			zap.String("operation_id", opID),
		)
	}

	// Phase 3 Metric: Reset stateful market halt gauges
	metrics.RecordMarketResumed(req.MarketID)

	s.log.Info("ResumeMarket: completed successfully",
		zap.String("operation_id", opID),
		zap.String("market_id", req.MarketID),
	)
	return result.Operation, nil
}

// ReconstructMarketState queries persistent operations history and repopulates Prometheus market halt gauges.
// This guarantees that after an Admin service restart or crash, extended market halts are accurately tracked.
func (s *AdminService) ReconstructMarketState(ctx context.Context) error {
	states, err := s.opsRepo.GetLatestMarketStates(ctx)
	if err != nil {
		return fmt.Errorf("admin_service: reconstruct market state: %w", err)
	}

	haltedCount := 0
	for _, st := range states {
		if st.IsHalted {
			metrics.RecordMarketHalted(st.MarketID, st.UpdatedAt)
			haltedCount++
			s.log.Warn("admin_service: reconstructed halted market state on startup",
				zap.String("market_id", st.MarketID),
				zap.Time("halted_at", st.UpdatedAt),
			)
		} else {
			metrics.RecordMarketResumed(st.MarketID)
		}
	}

	s.log.Info("admin_service: market halt state reconstruction complete",
		zap.Int("total_markets_evaluated", len(states)),
		zap.Int("actively_halted", haltedCount),
	)
	return nil
}
