package service_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"

	"tradedrift/services/admin/internal/domain"
	"tradedrift/services/admin/internal/repository"
	"tradedrift/services/admin/internal/service"
)

// Mock TxManager
type mockTxManager struct {
	execFunc func(ctx context.Context, req repository.AdminOperationTxRequest) (*repository.AdminOperationTxResult, error)
}

func (m *mockTxManager) ExecAdminOperationTx(ctx context.Context, req repository.AdminOperationTxRequest) (*repository.AdminOperationTxResult, error) {
	if m.execFunc != nil {
		return m.execFunc(ctx, req)
	}
	return &repository.AdminOperationTxResult{Operation: req.Operation}, nil
}

// Mock OperationsRepository
type mockOpsRepo struct {
	operations map[string]*domain.AdminOperation
}

func newMockOpsRepo() *mockOpsRepo {
	return &mockOpsRepo{operations: make(map[string]*domain.AdminOperation)}
}

func (m *mockOpsRepo) GetByIdempotencyKey(ctx context.Context, adminID, key string) (*domain.AdminOperation, error) {
	composite := adminID + ":" + key
	if op, ok := m.operations[composite]; ok {
		return op, nil
	}
	return nil, nil
}

func (m *mockOpsRepo) GetByID(ctx context.Context, id string) (*domain.AdminOperation, error) {
	for _, op := range m.operations {
		if op.ID == id {
			return op, nil
		}
	}
	return nil, nil
}

func (m *mockOpsRepo) Insert(ctx context.Context, op *domain.AdminOperation) error {
	composite := op.AdminID + ":" + op.IdempotencyKey
	m.operations[composite] = op
	return nil
}

func (m *mockOpsRepo) UpdateStatus(ctx context.Context, id string, status domain.OperationStatus, responseBody []byte) error {
	for _, op := range m.operations {
		if op.ID == id {
			op.Status = status
			op.ResponseBody = responseBody
			return nil
		}
	}
	return nil
}

func TestAdminService_HaltMarket_Idempotent(t *testing.T) {
	opsRepo := newMockOpsRepo()
	txMgr := &mockTxManager{
		execFunc: func(ctx context.Context, req repository.AdminOperationTxRequest) (*repository.AdminOperationTxResult, error) {
			_ = opsRepo.Insert(ctx, req.Operation)
			return &repository.AdminOperationTxResult{Operation: req.Operation}, nil
		},
	}

	svc := service.NewAdminService(txMgr, opsRepo, nil, nil, zap.NewNop())
	ctx := context.Background()

	req := service.HaltMarketRequest{
		AdminID:        "admin-1",
		IdempotencyKey: "halt-btc-inr-001",
		RequestID:      "req-1",
		MarketID:       "BTC-INR",
		Reason:         "Emergency circuit breaker triggered",
	}

	// First execution
	op1, err := svc.HaltMarket(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error on first call: %v", err)
	}
	if op1.Status != domain.OperationStatusCompleted {
		t.Fatalf("expected status COMPLETED, got: %s", op1.Status)
	}

	// Duplicate execution with same Idempotency-Key
	op2, err := svc.HaltMarket(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}
	if op2.ID != op1.ID {
		t.Fatalf("expected cached operation ID %s, got: %s", op1.ID, op2.ID)
	}
}

func TestAdminService_ResumeMarket_Idempotent(t *testing.T) {
	opsRepo := newMockOpsRepo()
	txMgr := &mockTxManager{
		execFunc: func(ctx context.Context, req repository.AdminOperationTxRequest) (*repository.AdminOperationTxResult, error) {
			_ = opsRepo.Insert(ctx, req.Operation)
			return &repository.AdminOperationTxResult{Operation: req.Operation}, nil
		},
	}

	svc := service.NewAdminService(txMgr, opsRepo, nil, nil, zap.NewNop())
	ctx := context.Background()

	req := service.ResumeMarketRequest{
		AdminID:        "admin-1",
		IdempotencyKey: "resume-btc-inr-001",
		RequestID:      "req-2",
		MarketID:       "BTC-INR",
		Reason:         "Market conditions stabilized",
	}

	op1, err := svc.ResumeMarket(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error on resume: %v", err)
	}
	if op1.OperationType != domain.OpResumeMarket {
		t.Fatalf("expected OpResumeMarket, got: %s", op1.OperationType)
	}

	// Repeated call returns existing operation
	op2, err := svc.ResumeMarket(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error on duplicate resume: %v", err)
	}
	if op2.ID != op1.ID {
		t.Fatalf("expected identical operation ID")
	}
}

func TestAdminService_UnsuspendUser_Idempotent(t *testing.T) {
	opsRepo := newMockOpsRepo()
	txMgr := &mockTxManager{
		execFunc: func(ctx context.Context, req repository.AdminOperationTxRequest) (*repository.AdminOperationTxResult, error) {
			_ = opsRepo.Insert(ctx, req.Operation)
			return &repository.AdminOperationTxResult{Operation: req.Operation}, nil
		},
	}

	svc := service.NewAdminService(txMgr, opsRepo, nil, nil, zap.NewNop())
	ctx := context.Background()

	req := service.UnsuspendUserRequest{
		AdminID:        "admin-1",
		IdempotencyKey: "unsuspend-user-001",
		RequestID:      "req-3",
		TargetUserID:   "1f85afe9-e866-4629-bf51-c8dc8a72d7aa",
		Reason:         "Compliance verification passed",
	}

	op1, err := svc.UnsuspendUser(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error on unsuspend: %v", err)
	}
	if op1.OperationType != domain.OpUnsuspendUser {
		t.Fatalf("expected OpUnsuspendUser, got: %s", op1.OperationType)
	}

	op2, err := svc.UnsuspendUser(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error on duplicate unsuspend: %v", err)
	}
	if op2.ID != op1.ID {
		t.Fatalf("expected identical operation ID")
	}
}

func TestAdminService_ConcurrentConflictRecovery(t *testing.T) {
	opsRepo := newMockOpsRepo()
	now := time.Now().UTC()
	opID := domain.MustNewV7()

	existingOp := &domain.AdminOperation{
		ID:             opID,
		AdminID:        "admin-1",
		IdempotencyKey: "concurrent-race-key",
		RequestID:      "req-race",
		OperationType:  domain.OpHaltMarket,
		TargetID:       "BTC-INR",
		Reason:         "Circuit breaker",
		Status:         domain.OperationStatusCompleted,
		ResponseBody:   json.RawMessage(`{"status":"halted"}`),
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	// Simulate concurrent insert: ExecAdminOperationTx returns Postgres unique constraint violation 23505
	txMgr := &mockTxManager{
		execFunc: func(ctx context.Context, req repository.AdminOperationTxRequest) (*repository.AdminOperationTxResult, error) {
			// Prepopulate existing record as if the competing goroutine committed first
			_ = opsRepo.Insert(ctx, existingOp)
			return nil, &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"}
		},
	}

	svc := service.NewAdminService(txMgr, opsRepo, nil, nil, zap.NewNop())
	ctx := context.Background()

	req := service.HaltMarketRequest{
		AdminID:        "admin-1",
		IdempotencyKey: "concurrent-race-key",
		RequestID:      "req-competing",
		MarketID:       "BTC-INR",
		Reason:         "Circuit breaker",
	}

	// Should cleanly catch 23505, query opsRepo, and return the existing operation without a 500 error!
	op, err := svc.HaltMarket(ctx, req)
	if err != nil {
		t.Fatalf("expected clean conflict recovery, got error: %v", err)
	}
	if op.ID != opID {
		t.Fatalf("expected recovered operation ID %s, got: %s", opID, op.ID)
	}
	if op.Status != domain.OperationStatusCompleted {
		t.Fatalf("expected status COMPLETED, got: %s", op.Status)
	}
}
