package test

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/payment/mock"
	"tradedrift/services/wallet-topup/internal/service"
)

func setupTopUpService(dailyLimit int64) (*service.TopUpService, *MockDailyLimitRepo, *MockTopUpOrderRepo, *MockWebhookEventRepo, *MockTxManager, *mock.MockPaymentProvider) {
	dailyRepo := NewMockDailyLimitRepo()
	orderRepo := NewMockTopUpOrderRepo()
	webhookRepo := NewMockWebhookEventRepo()
	txManager := NewMockTxManager(dailyRepo, orderRepo, webhookRepo)
	provider := mock.NewMockPaymentProvider("secret")
	svc := service.NewTopUpService(dailyRepo, orderRepo, txManager, provider, dailyLimit, zap.NewNop())
	return svc, dailyRepo, orderRepo, webhookRepo, txManager, provider
}

func TestCreateTopUp_Success(t *testing.T) {
	svc, _, _, _, _, _ := setupTopUpService(10)

	ctx := context.Background()
	userID := "user-123"
	key := "idem-key-1"
	amount := int64(5)

	order, err := svc.CreateTopUp(ctx, userID, key, amount)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if order.INRAmount != 5 {
		t.Errorf("expected 5 INR, got %d", order.INRAmount)
	}
	if order.USDTAmount != "5000.0000000000" {
		t.Errorf("expected 5000.0000000000 USDT, got %s", order.USDTAmount)
	}
	if order.Status != domain.StatusPaymentPending {
		t.Errorf("expected status PAYMENT_PENDING, got %s", order.Status)
	}

	// Verify daily quota reflects 5 INR reserved
	usage, err := svc.GetDailyUsage(ctx, userID)
	if err != nil {
		t.Fatalf("failed to get daily usage: %v", err)
	}
	if usage.ReservedINR != 5 {
		t.Errorf("expected 5 INR reserved, got %d", usage.ReservedINR)
	}
	if usage.RemainingINR != 5 {
		t.Errorf("expected 5 INR remaining, got %d", usage.RemainingINR)
	}
}

func TestCreateTopUp_AmountBounds(t *testing.T) {
	svc, _, _, _, _, _ := setupTopUpService(10)

	ctx := context.Background()
	invalidAmounts := []int64{0, -1, 11, 50}

	for _, amt := range invalidAmounts {
		_, err := svc.CreateTopUp(ctx, "user-bounds", "k", amt)
		if !errors.Is(err, domain.ErrInvalidAmount) {
			t.Errorf("expected ErrInvalidAmount for %d INR, got %v", amt, err)
		}
	}
}

func TestCreateTopUp_ConfigurableLimit(t *testing.T) {
	// Set dynamic daily limit to 25
	svc, _, _, _, _, _ := setupTopUpService(25)

	ctx := context.Background()
	userID := "user-custom-limit"

	// Should be able to reserve ₹10 (within ₹25 limit, even though default is ₹10)
	order, err := svc.CreateTopUp(ctx, userID, "key-custom-1", 10)
	if err != nil {
		t.Fatalf("expected order creation to succeed with ₹25 limit, got: %v", err)
	}
	if order.Status != domain.StatusPaymentPending {
		t.Errorf("expected status PAYMENT_PENDING, got %s", order.Status)
	}

	usage, err := svc.GetDailyUsage(ctx, userID)
	if err != nil {
		t.Fatalf("failed to get daily usage: %v", err)
	}
	if usage.LimitINR != 25 {
		t.Errorf("expected LimitINR 25, got %d", usage.LimitINR)
	}
	if usage.RemainingINR != 15 {
		t.Errorf("expected RemainingINR 15, got %d", usage.RemainingINR)
	}
}

func TestCreateTopUp_Idempotency(t *testing.T) {
	svc, _, _, _, _, _ := setupTopUpService(10)

	ctx := context.Background()
	userID := "user-idem"
	key := "shared-key"

	// 1. Initial Order: ₹4
	order1, err := svc.CreateTopUp(ctx, userID, key, 4)
	if err != nil {
		t.Fatalf("first order failed: %v", err)
	}

	// 2. Same Key, Same Amount: returns existing order
	order2, err := svc.CreateTopUp(ctx, userID, key, 4)
	if err != nil {
		t.Fatalf("second order failed: %v", err)
	}
	if order1.ID != order2.ID {
		t.Errorf("expected same order ID %s, got %s", order1.ID, order2.ID)
	}

	// 3. Same Key, Different Amount (₹6): returns 409 conflict
	_, err = svc.CreateTopUp(ctx, userID, key, 6)
	if !errors.Is(err, domain.ErrIdempotencyConflict) {
		t.Errorf("expected ErrIdempotencyConflict, got %v", err)
	}
}

func TestCreateTopUp_ProviderFailureRollback(t *testing.T) {
	svc, _, _, _, _, provider := setupTopUpService(10)

	ctx := context.Background()
	userID := "user-fail"

	// Simulate payment provider network failure
	provider.SetFailNextCreateOrder(true)

	_, err := svc.CreateTopUp(ctx, userID, "fail-key", 7)
	if err == nil {
		t.Fatalf("expected provider error, got nil")
	}

	// CRITICAL ASSERTION: Reserved quota must be 0 (immediately rolled back)!
	usage, err := svc.GetDailyUsage(ctx, userID)
	if err != nil {
		t.Fatalf("failed to get usage: %v", err)
	}
	if usage.ReservedINR != 0 {
		t.Errorf("expected 0 reserved quota after provider rollback, got %d", usage.ReservedINR)
	}
	if usage.RemainingINR != 10 {
		t.Errorf("expected 10 remaining quota, got %d", usage.RemainingINR)
	}
}
