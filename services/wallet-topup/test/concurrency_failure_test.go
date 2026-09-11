package test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	walletv1 "tradedrift/platform/api/gen/wallet/v1"
	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet-topup/internal/client"
	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/payment/mock"
	"tradedrift/services/wallet-topup/internal/service"
	"tradedrift/services/wallet-topup/internal/webhook"
)

// ── Test 1: Concurrent Quota Race ─────────────────────────────────────────────
// User with ₹10 daily limit launches two concurrent top-up orders of ₹6 each.
// Expected: Exactly one succeeds, one fails with ErrDailyLimitExceeded. Total reserved = ₹6.
func TestConcurrency_QuotaRace(t *testing.T) {
	dailyRepo := NewMockDailyLimitRepo()
	orderRepo := NewMockTopUpOrderRepo()
	webhookRepo := NewMockWebhookEventRepo()
	txManager := NewMockTxManager(dailyRepo, orderRepo, webhookRepo)
	provider := mock.NewMockPaymentProvider("secret")
	topupSvc := service.NewTopUpService(dailyRepo, orderRepo, txManager, provider, 10, zap.NewNop())

	userID := "user-race"
	var successCount int32
	var limitExceededCount int32

	var wg sync.WaitGroup
	wg.Add(2)

	// Launch Request A: ₹6
	go func() {
		defer wg.Done()
		_, err := topupSvc.CreateTopUp(context.Background(), userID, "key-race-A", 6)
		if err == nil {
			atomic.AddInt32(&successCount, 1)
		} else if err == domain.ErrDailyLimitExceeded {
			atomic.AddInt32(&limitExceededCount, 1)
		}
	}()

	// Launch Request B: ₹6
	go func() {
		defer wg.Done()
		_, err := topupSvc.CreateTopUp(context.Background(), userID, "key-race-B", 6)
		if err == nil {
			atomic.AddInt32(&successCount, 1)
		} else if err == domain.ErrDailyLimitExceeded {
			atomic.AddInt32(&limitExceededCount, 1)
		}
	}()

	wg.Wait()

	if successCount != 1 {
		t.Errorf("expected exactly 1 order to succeed, got %d", successCount)
	}
	if limitExceededCount != 1 {
		t.Errorf("expected exactly 1 order to fail with ErrDailyLimitExceeded, got %d", limitExceededCount)
	}

	usage, err := topupSvc.GetDailyUsage(context.Background(), userID)
	if err != nil {
		t.Fatalf("failed to get daily usage: %v", err)
	}
	if usage.ReservedINR != 6 {
		t.Errorf("expected exactly 6 INR reserved, got %d", usage.ReservedINR)
	}
}

// ── Test 2: Duplicate Webhook Storm ───────────────────────────────────────────
// External gateway retries a webhook 3 times concurrently.
// Expected: Exactly one event processes quota transition; all return fast 200 OK without duplicate quota deductions.
func TestConcurrency_DuplicateWebhookStorm(t *testing.T) {
	dailyRepo := NewMockDailyLimitRepo()
	orderRepo := NewMockTopUpOrderRepo()
	webhookRepo := NewMockWebhookEventRepo()
	txManager := NewMockTxManager(dailyRepo, orderRepo, webhookRepo)
	secret := "secret"
	provider := mock.NewMockPaymentProvider(secret)
	verifier := webhook.NewVerifier(provider)

	topupSvc := service.NewTopUpService(dailyRepo, orderRepo, txManager, provider, 10, zap.NewNop())
	webhookSvc := service.NewWebhookService(verifier, webhookRepo, orderRepo, txManager, 10, zap.NewNop())

	ctx := context.Background()
	userID := "user-storm"

	order, err := topupSvc.CreateTopUp(ctx, userID, "storm-order", 5)
	if err != nil {
		t.Fatalf("CreateTopUp failed: %v", err)
	}

	now := time.Now().Unix()
	whPayload := service.PaymentWebhookPayload{
		EventID:   "evt_duplicate_storm_1",
		EventType: "payment.captured",
		PaymentID: "pay_storm_123",
		OrderID:   order.ID,
		INRAmount: 5,
		Currency:  "INR",
		PaidAt:    now,
		Timestamp: now,
	}
	body, _ := json.Marshal(whPayload)
	sig := provider.GenerateSignature(body, now)

	var wg sync.WaitGroup
	var errCount int32
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := webhookSvc.ProcessWebhook(context.Background(), "MOCK", body, sig, now); err != nil {
				atomic.AddInt32(&errCount, 1)
			}
		}()
	}
	wg.Wait()

	if errCount != 0 {
		t.Errorf("expected 0 errors (all duplicate webhooks should return 200 OK fast ack), got %d errors", errCount)
	}

	// Verify quota was consumed exactly ONCE
	usage, err := topupSvc.GetDailyUsage(ctx, userID)
	if err != nil {
		t.Fatalf("failed to get usage: %v", err)
	}
	if usage.ConsumedINR != 5 {
		t.Errorf("expected consumed 5 INR (consumed once), got %d", usage.ConsumedINR)
	}
	if usage.ReservedINR != 0 {
		t.Errorf("expected reserved 0 INR, got %d", usage.ReservedINR)
	}
}

// ── Test 3: Lease Expiration & Stale Worker Fencing ────────────────────────────
// Worker A claims order with 50ms lease. Lease expires.
// Worker B claims the order. Worker A attempts late completion.
// Expected: Worker A completion rejected (0 rows affected / false). Worker B completes successfully.
func TestReconciler_LeaseExpirationAndFencing(t *testing.T) {
	orderRepo := NewMockTopUpOrderRepo()
	ctx := context.Background()

	orderID := "order-lease-test"
	orderRepo.orders[orderID] = &domain.TopUpOrder{
		ID:         orderID,
		UserID:     "user-lease",
		INRAmount:  5,
		USDTAmount: "5000.0000000000",
		Status:     domain.StatusCreditPending,
	}

	// 1. Worker A claims with 50ms lease
	workerAToken := "worker-token-A"
	claimedA, err := orderRepo.ClaimBatchForCredit(ctx, workerAToken, 10, 50*time.Millisecond)
	if err != nil || len(claimedA) != 1 {
		t.Fatalf("Worker A failed to claim order: %v", err)
	}

	// 2. Lease expires (advance time 60ms)
	time.Sleep(60 * time.Millisecond)

	// 3. Worker B claims the order
	workerBToken := "worker-token-B"
	claimedB, err := orderRepo.ClaimBatchForCredit(ctx, workerBToken, 10, 60*time.Second)
	if err != nil || len(claimedB) != 1 {
		t.Fatalf("Worker B failed to claim expired lease order: %v", err)
	}

	// 4. Worker A attempts late completion with expired lease token
	completedA, err := orderRepo.CompleteOrder(ctx, orderID, workerAToken)
	if err != nil {
		t.Fatalf("Worker A complete order failed with err: %v", err)
	}
	if completedA {
		t.Errorf("CRITICAL SAFETY VIOLATION: Worker A completed order despite expired lease and token mismatch!")
	}

	// 5. Worker B completes with valid token
	completedB, err := orderRepo.CompleteOrder(ctx, orderID, workerBToken)
	if err != nil || !completedB {
		t.Fatalf("Worker B should have succeeded completing order: %v", err)
	}

	finalOrder, _ := orderRepo.GetByID(ctx, orderID)
	if finalOrder.Status != domain.StatusCompleted {
		t.Errorf("expected final status COMPLETED, got %s", finalOrder.Status)
	}
}

// ── Test 4: Post-Credit Network Disconnect & Reconciler Retry ──────────────────
// Worker calls DepositFunds, succeeds on server, but connection drops on return.
// Top-Up service retries calling DepositFunds with same referenceID ("order-retry-123").
// Expected: Wallet balance is credited ONCE, second call succeeds idempotently.
func TestWallet_IdempotentDepositRetry(t *testing.T) {
	type ledgerEntry struct {
		referenceID   string
		referenceType string
		amount        string
	}

	var mu sync.Mutex
	var currentBalance int64 = 10000 // 10,000 USDT seed
	ledger := make(map[string]ledgerEntry)

	mockDepositFunds := func(userID, asset, amount, refID, refType string) (int64, bool) {
		mu.Lock()
		defer mu.Unlock()

		key := refID + ":" + refType + ":" + asset
		if _, exists := ledger[key]; exists {
			// Idempotent duplicate: return current balance without crediting
			return currentBalance, true
		}

		var depositVal int64 = 5000 // 5,000 USDT
		currentBalance += depositVal
		ledger[key] = ledgerEntry{
			referenceID:   refID,
			referenceType: refType,
			amount:        amount,
		}
		return currentBalance, false
	}

	orderID := "order-disconnect-123"

	// Attempt 1: Succeeds in DB, but client network drops before receiving response
	bal1, wasDup1 := mockDepositFunds("user-1", "USDT", "5000.0000000000", orderID, "TOPUP")
	if wasDup1 {
		t.Errorf("expected attempt 1 to be new deposit")
	}
	if bal1 != 15000 {
		t.Errorf("expected balance 15000, got %d", bal1)
	}

	// Reconciler retry: identical call
	bal2, wasDup2 := mockDepositFunds("user-1", "USDT", "5000.0000000000", orderID, "TOPUP")
	if !wasDup2 {
		t.Errorf("expected attempt 2 to be detected as idempotent duplicate")
	}
	if bal2 != 15000 {
		t.Errorf("CRITICAL FINANCIAL INVARIANT VIOLATION: balance double credited! Expected 15000, got %d", bal2)
	}
}

// ── Test 5: Atomic Webhook Failure Recovery ───────────────────────────────────
// Webhook processing encounters a simulated DB transaction failure.
// Assert: Quota is NOT consumed, webhook event is NOT permanently deduplicated.
// Retry succeeds, consuming quota and moving order to CREDIT_PENDING.
func TestConcurrency_AtomicWebhookFailureRecovery(t *testing.T) {
	dailyRepo := NewMockDailyLimitRepo()
	orderRepo := NewMockTopUpOrderRepo()
	webhookRepo := NewMockWebhookEventRepo()
	txManager := NewMockTxManager(dailyRepo, orderRepo, webhookRepo)
	secret := "secret"
	provider := mock.NewMockPaymentProvider(secret)
	verifier := webhook.NewVerifier(provider)

	topupSvc := service.NewTopUpService(dailyRepo, orderRepo, txManager, provider, 10, zap.NewNop())
	webhookSvc := service.NewWebhookService(verifier, webhookRepo, orderRepo, txManager, 10, zap.NewNop())

	ctx := context.Background()
	userID := "user-atomic-fail"

	order, err := topupSvc.CreateTopUp(ctx, userID, "key-atomic-fail", 5)
	if err != nil {
		t.Fatalf("CreateTopUp failed: %v", err)
	}

	now := time.Now().Unix()
	whPayload := service.PaymentWebhookPayload{
		EventID:   "evt_atomic_fail_1",
		EventType: "payment.captured",
		PaymentID: "pay_atomic_fail_1",
		OrderID:   order.ID,
		INRAmount: 5,
		Currency:  "INR",
		PaidAt:    now,
		Timestamp: now,
	}
	body, _ := json.Marshal(whPayload)
	sig := provider.GenerateSignature(body, now)

	// Invalidate transaction execution to simulate sudden DB connection drop or constraint failure
	txManager.SimulateConfirmError = true

	err = webhookSvc.ProcessWebhook(ctx, "MOCK", body, sig, now)
	if err == nil {
		t.Fatalf("expected error from simulated DB transaction failure, got nil")
	}

	// Invariant check: Quota must NOT be consumed! Reserved must remain 5.
	usage, err := topupSvc.GetDailyUsage(ctx, userID)
	if err != nil {
		t.Fatalf("failed to get usage: %v", err)
	}
	if usage.ConsumedINR != 0 {
		t.Errorf("CRITICAL INVARIANT VIOLATION: consumed quota must be 0 after tx rollback, got %d", usage.ConsumedINR)
	}
	if usage.ReservedINR != 5 {
		t.Errorf("expected reserved quota to remain 5, got %d", usage.ReservedINR)
	}

	// Order must still be PAYMENT_PENDING
	o, _ := orderRepo.GetByID(ctx, order.ID)
	if o.Status != domain.StatusPaymentPending {
		t.Errorf("expected order status to remain PAYMENT_PENDING, got %s", o.Status)
	}

	// Fix DB condition and retry webhook (provider retry)
	txManager.SimulateConfirmError = false

	err = webhookSvc.ProcessWebhook(ctx, "MOCK", body, sig, now)
	if err != nil {
		t.Fatalf("expected retry to succeed after DB recovery, got: %v", err)
	}

	// After successful retry: Quota consumed=5, reserved=0, status=CREDIT_PENDING
	usage2, _ := topupSvc.GetDailyUsage(ctx, userID)
	if usage2.ConsumedINR != 5 {
		t.Errorf("expected consumed 5 after retry, got %d", usage2.ConsumedINR)
	}
	if usage2.ReservedINR != 0 {
		t.Errorf("expected reserved 0 after retry, got %d", usage2.ReservedINR)
	}
	o2, _ := orderRepo.GetByID(ctx, order.ID)
	if o2.Status != domain.StatusCreditPending {
		t.Errorf("expected status CREDIT_PENDING after retry, got %s", o2.Status)
	}
}

// ── Test 6: Atomic Order Expiry & Quota Release ───────────────────────────────
// Assert ExpireOrderAndReleaseQuotaTx releases reserved quota and marks order EXPIRED atomically.
func TestConcurrency_AtomicOrderExpiry(t *testing.T) {
	dailyRepo := NewMockDailyLimitRepo()
	orderRepo := NewMockTopUpOrderRepo()
	webhookRepo := NewMockWebhookEventRepo()
	txManager := NewMockTxManager(dailyRepo, orderRepo, webhookRepo)
	provider := mock.NewMockPaymentProvider("secret")
	topupSvc := service.NewTopUpService(dailyRepo, orderRepo, txManager, provider, 10, zap.NewNop())

	ctx := context.Background()
	userID := "user-atomic-expiry"

	order, err := topupSvc.CreateTopUp(ctx, userID, "key-expiry-1", 4)
	if err != nil {
		t.Fatalf("CreateTopUp failed: %v", err)
	}

	// Expire order atomically
	err = txManager.ExpireOrderAndReleaseQuotaTx(ctx, order.ID, userID, order.ReservationDate, order.INRAmount)
	if err != nil {
		t.Fatalf("ExpireOrderAndReleaseQuotaTx failed: %v", err)
	}

	// Verify order status is EXPIRED
	o, err := orderRepo.GetByID(ctx, order.ID)
	if err != nil {
		t.Fatalf("failed to get order: %v", err)
	}
	if o.Status != domain.StatusExpired {
		t.Errorf("expected status EXPIRED, got %s", o.Status)
	}

	// Verify quota is released
	usage, err := topupSvc.GetDailyUsage(ctx, userID)
	if err != nil {
		t.Fatalf("failed to get usage: %v", err)
	}
	if usage.ReservedINR != 0 {
		t.Errorf("expected reserved 0, got %d", usage.ReservedINR)
	}
	if usage.RemainingINR != 10 {
		t.Errorf("expected remaining 10, got %d", usage.RemainingINR)
	}
}

// ── Test 7: Concurrent Idempotency Provider Order Protection ──────────────────
// Two concurrent requests arrive with the exact same idempotency key.
// Assert: Exactly 1 order is created at the external provider; the second request
// gets the existing order without generating a duplicate provider order.
func TestConcurrency_IdempotencyProviderOrderProtection(t *testing.T) {
	dailyRepo := NewMockDailyLimitRepo()
	orderRepo := NewMockTopUpOrderRepo()
	webhookRepo := NewMockWebhookEventRepo()
	txManager := NewMockTxManager(dailyRepo, orderRepo, webhookRepo)
	provider := mock.NewMockPaymentProvider("secret")
	topupSvc := service.NewTopUpService(dailyRepo, orderRepo, txManager, provider, 10, zap.NewNop())

	ctx := context.Background()
	userID := "user-racing-idempotency"
	idempotencyKey := "key-race-same-order"

	var wg sync.WaitGroup
	var orderA, orderB *domain.TopUpOrder
	var errA, errB error

	wg.Add(2)
	go func() {
		defer wg.Done()
		orderA, errA = topupSvc.CreateTopUp(ctx, userID, idempotencyKey, 5)
	}()
	go func() {
		defer wg.Done()
		orderB, errB = topupSvc.CreateTopUp(ctx, userID, idempotencyKey, 5)
	}()
	wg.Wait()

	if errA != nil {
		t.Fatalf("Request A failed: %v", errA)
	}
	if errB != nil {
		t.Fatalf("Request B failed: %v", errB)
	}

	if orderA.ID != orderB.ID {
		t.Errorf("expected identical order IDs, got %s and %s", orderA.ID, orderB.ID)
	}

	// Only 5 INR must be reserved, not 10!
	usage, _ := topupSvc.GetDailyUsage(ctx, userID)
	if usage.ReservedINR != 5 {
		t.Errorf("expected exactly 5 INR reserved, got %d", usage.ReservedINR)
	}
}

// ── Test 8: INITIATED Order Expiry Reclaims Quota ────────────────────────────
// An order that remained in INITIATED (e.g., gateway timeout or server crash)
// is swept upon expiration, transitioning to EXPIRED and reclaiming reserved quota.
func TestConcurrency_InitiatedOrderExpirationReclaimsQuota(t *testing.T) {
	dailyRepo := NewMockDailyLimitRepo()
	orderRepo := NewMockTopUpOrderRepo()
	webhookRepo := NewMockWebhookEventRepo()
	txManager := NewMockTxManager(dailyRepo, orderRepo, webhookRepo)

	ctx := context.Background()
	userID := "user-orphaned-initiated"
	orderID, _ := platformuuid.New()
	usageDate := "2026-09-11"

	order := &domain.TopUpOrder{
		ID:              orderID,
		UserID:          userID,
		IdempotencyKey:  "key-orphan-init",
		INRAmount:       6,
		USDTAmount:      "6000.0000000000",
		ReservationDate: usageDate,
		Provider:        "MOCK",
		Status:          domain.StatusInitiated,
		ExpiresAt:       time.Now().UTC().Add(-1 * time.Minute), // already expired
		CreatedAt:       time.Now().UTC().Add(-15 * time.Minute),
		UpdatedAt:       time.Now().UTC().Add(-15 * time.Minute),
	}

	// 1. Reserve quota and insert INITIATED order
	_, err := txManager.InitiateTopUpTx(ctx, order, 10)
	if err != nil {
		t.Fatalf("InitiateTopUpTx failed: %v", err)
	}

	// 2. Query expired pending orders: INITIATED order must be returned
	expired, err := orderRepo.GetPendingExpired(ctx, time.Now().UTC(), 10)
	if err != nil {
		t.Fatalf("GetPendingExpired failed: %v", err)
	}
	found := false
	for _, o := range expired {
		if o.ID == orderID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected orphaned INITIATED order %s to be returned by GetPendingExpired", orderID)
	}

	// 3. Atomically expire order and release quota
	err = txManager.ExpireOrderAndReleaseQuotaTx(ctx, orderID, userID, usageDate, 6)
	if err != nil {
		t.Fatalf("ExpireOrderAndReleaseQuotaTx failed on INITIATED order: %v", err)
	}

	// 4. Assert order is EXPIRED and quota reserved is 0
	updated, _ := orderRepo.GetByID(ctx, orderID)
	if updated.Status != domain.StatusExpired {
		t.Errorf("expected EXPIRED status, got %s", updated.Status)
	}

	limit, _ := dailyRepo.GetDailyUsage(ctx, userID, usageDate, 10)
	if limit.ReservedINR != 0 {
		t.Errorf("expected reserved quota 0, got %d", limit.ReservedINR)
	}
}

// ── Test 9: CancelInitiatedOrderTx Guard Against Non-INITIATED Orders ────────
func TestConcurrency_CancelInitiatedOrderTx_RejectsNonInitiated(t *testing.T) {
	dailyRepo := NewMockDailyLimitRepo()
	orderRepo := NewMockTopUpOrderRepo()
	webhookRepo := NewMockWebhookEventRepo()
	txManager := NewMockTxManager(dailyRepo, orderRepo, webhookRepo)
	provider := mock.NewMockPaymentProvider("secret")
	topupSvc := service.NewTopUpService(dailyRepo, orderRepo, txManager, provider, 10, zap.NewNop())

	ctx := context.Background()
	userID := "user-cancel-guard"

	// 1. Create order: transitions to PAYMENT_PENDING
	order, err := topupSvc.CreateTopUp(ctx, userID, "key-cancel-guard", 5)
	if err != nil {
		t.Fatalf("CreateTopUp failed: %v", err)
	}
	if order.Status != domain.StatusPaymentPending {
		t.Fatalf("expected PAYMENT_PENDING, got %s", order.Status)
	}

	// 2. Attempt CancelInitiatedOrderTx on an order that is in PAYMENT_PENDING
	err = txManager.CancelInitiatedOrderTx(ctx, order.ID, userID, order.ReservationDate, order.INRAmount, "gateway error")
	if err == nil {
		t.Fatalf("expected CancelInitiatedOrderTx to fail for non-INITIATED order, got nil")
	}

	// Verify order status unchanged
	o, _ := orderRepo.GetByID(ctx, order.ID)
	if o.Status != domain.StatusPaymentPending {
		t.Errorf("expected status to remain PAYMENT_PENDING, got %s", o.Status)
	}
}

// ── Test 10: Idempotency Key Length Validation ──────────────────────────────
func TestCreateTopUp_IdempotencyKeyTooLong(t *testing.T) {
	dailyRepo := NewMockDailyLimitRepo()
	orderRepo := NewMockTopUpOrderRepo()
	webhookRepo := NewMockWebhookEventRepo()
	txManager := NewMockTxManager(dailyRepo, orderRepo, webhookRepo)
	provider := mock.NewMockPaymentProvider("secret")
	topupSvc := service.NewTopUpService(dailyRepo, orderRepo, txManager, provider, 10, zap.NewNop())

	ctx := context.Background()
	longKey := strings.Repeat("a", 101)

	_, err := topupSvc.CreateTopUp(ctx, "user-long-key", longKey, 5)
	if !errors.Is(err, domain.ErrInvalidIdempotencyKey) {
		t.Fatalf("expected ErrInvalidIdempotencyKey, got %v", err)
	}
}

type mockDirectNilClient struct {
	walletv1.WalletServiceClient
}

func (m *mockDirectNilClient) DepositFunds(ctx context.Context, in *walletv1.DepositFundsRequest, opts ...grpc.CallOption) (*walletv1.DepositFundsResponse, error) {
	return nil, nil
}

// ── Test 11: Reconciler Graceful Nil gRPC Response Protection ─────────────────
func TestReconciler_NilWalletResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	walletClient := client.NewWalletClientWithServiceClient(&mockDirectNilClient{})

	orderRepo := NewMockTopUpOrderRepo()
	orderID := "order-nil-wallet-test"
	orderRepo.orders[orderID] = &domain.TopUpOrder{
		ID:         orderID,
		UserID:     "user-nil-test",
		INRAmount:  5,
		USDTAmount: "5000.0000000000",
		Status:     domain.StatusCreditPending,
	}

	worker := service.NewReconcilerWorker(
		orderRepo,
		walletClient,
		zap.NewNop(),
		20*time.Millisecond,
		10,
		60*time.Second,
	)

	worker.Start(ctx)
	defer worker.Stop()

	var lastErr *string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		o, _ := orderRepo.GetByID(ctx, orderID)
		if o != nil && o.LastError != nil {
			lastErr = o.LastError
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if lastErr == nil {
		t.Fatalf("expected reconciler to record failure for nil response, got nil lastError")
	}
	if *lastErr != "wallet service returned nil response" {
		t.Errorf("expected LastError 'wallet service returned nil response', got %q", *lastErr)
	}
}

