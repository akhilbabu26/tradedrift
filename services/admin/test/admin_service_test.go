package test

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
	execFunc         func(ctx context.Context, req repository.AdminOperationTxRequest) (*repository.AdminOperationTxResult, error)
	completeSagaFunc func(ctx context.Context, opID string, sagaID string, responseBody []byte) error
}

func (m *mockTxManager) ExecAdminOperationTx(ctx context.Context, req repository.AdminOperationTxRequest) (*repository.AdminOperationTxResult, error) {
	if m.execFunc != nil {
		return m.execFunc(ctx, req)
	}
	return &repository.AdminOperationTxResult{Operation: req.Operation}, nil
}

func (m *mockTxManager) CompleteAuthSaga(ctx context.Context, opID string, sagaID string, responseBody []byte) error {
	if m.completeSagaFunc != nil {
		return m.completeSagaFunc(ctx, opID, sagaID, responseBody)
	}
	return nil
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

	txMgr := &mockTxManager{
		execFunc: func(ctx context.Context, req repository.AdminOperationTxRequest) (*repository.AdminOperationTxResult, error) {
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

func TestAdminService_SuspendUser_AuthFailure_RemainsProcessing(t *testing.T) {
	opsRepo := newMockOpsRepo()
	var sagaTaskInserted *domain.SagaTask
	completeSagaCalled := false

	txMgr := &mockTxManager{
		execFunc: func(ctx context.Context, req repository.AdminOperationTxRequest) (*repository.AdminOperationTxResult, error) {
			_ = opsRepo.Insert(ctx, req.Operation)
			sagaTaskInserted = req.SagaTask
			return &repository.AdminOperationTxResult{Operation: req.Operation}, nil
		},
		completeSagaFunc: func(ctx context.Context, opID string, sagaID string, responseBody []byte) error {
			completeSagaCalled = true
			return nil
		},
	}

	// nil authCli will cause immediate Auth invalidation to fail
	svc := service.NewAdminService(txMgr, opsRepo, nil, nil, zap.NewNop())
	ctx := context.Background()

	req := service.SuspendUserRequest{
		AdminID:        "admin-1",
		IdempotencyKey: "suspend-user-saga-test",
		RequestID:      "req-suspend-1",
		TargetUserID:   "user-123",
		Reason:         "Suspicious fraudulent activity detected",
	}

	op, err := svc.SuspendUser(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Operation must remain PROCESSING so the background SagaWorker can retry it
	if op.Status != domain.OperationStatusProcessing {
		t.Fatalf("expected status PROCESSING when Auth fails, got: %s", op.Status)
	}

	// Saga task must have been inserted into DB transaction
	if sagaTaskInserted == nil {
		t.Fatalf("expected SagaTask to be created in transaction")
	}
	if sagaTaskInserted.TaskType != domain.SagaTaskAuthInvalidateSessions {
		t.Fatalf("expected task type %s, got: %s", domain.SagaTaskAuthInvalidateSessions, sagaTaskInserted.TaskType)
	}

	// CompleteAuthSaga must NOT have been called because Auth failed
	if completeSagaCalled {
		t.Fatalf("CompleteAuthSaga must not be called when immediate Auth fails")
	}
}

func TestAdminService_FreezeWallet_FailureRecordsDiagnosticState(t *testing.T) {
	opsRepo := newMockOpsRepo()
	txMgr := &mockTxManager{
		execFunc: func(ctx context.Context, req repository.AdminOperationTxRequest) (*repository.AdminOperationTxResult, error) {
			_ = opsRepo.Insert(ctx, req.Operation)
			return &repository.AdminOperationTxResult{Operation: req.Operation}, nil
		},
	}

	// nil walletCli will cause immediate downstream FreezeWallet RPC to fail
	svc := service.NewAdminService(txMgr, opsRepo, nil, nil, zap.NewNop())
	ctx := context.Background()

	req := service.FreezeWalletRequest{
		AdminID:        "admin-1",
		IdempotencyKey: "freeze-wallet-fail-test",
		RequestID:      "req-freeze-1",
		TargetUserID:   "user-123",
		Asset:          "BTC",
		Reason:         "Suspicious withdrawal attempt",
	}

	_, err := svc.FreezeWallet(ctx, req)
	if err == nil {
		t.Fatalf("expected wallet error when wallet client is nil")
	}

	// Fetch operation from repo to verify failure metadata was recorded
	op, getErr := opsRepo.GetByIdempotencyKey(ctx, "admin-1", "freeze-wallet-fail-test")
	if getErr != nil || op == nil {
		t.Fatalf("expected operation to exist in repo, got err: %v", getErr)
	}

	// Must remain in PROCESSING so client can retry and reconcile
	if op.Status != domain.OperationStatusProcessing {
		t.Fatalf("expected status PROCESSING, got: %s", op.Status)
	}

	// Diagnostic metadata must be present in response_body
	var failData map[string]interface{}
	if err := json.Unmarshal(op.ResponseBody, &failData); err != nil {
		t.Fatalf("failed to unmarshal diagnostic response body: %v", err)
	}
	if failData["reconciliation_needed"] != true {
		t.Errorf("expected reconciliation_needed: true, got: %v", failData["reconciliation_needed"])
	}
	if failData["last_error"] == nil || failData["last_error"] == "" {
		t.Errorf("expected last_error to be recorded in response body")
	}
}

func TestAdminService_WorkerLeaseLost(t *testing.T) {
	// Verify ErrWorkerLeaseLost sentinel error is defined and distinct
	if domain.ErrWorkerLeaseLost == nil {
		t.Fatalf("domain.ErrWorkerLeaseLost must not be nil")
	}
	if domain.ErrWorkerLeaseLost.Error() == "" {
		t.Fatalf("domain.ErrWorkerLeaseLost must have descriptive error message")
	}
}
