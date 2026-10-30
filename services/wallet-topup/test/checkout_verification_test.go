package test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/payment"
	"tradedrift/services/wallet-topup/internal/payment/mock"
	"tradedrift/services/wallet-topup/internal/service"
)

type mockReconcilerTrigger struct {
	triggered bool
	mu        sync.Mutex
}

func (m *mockReconcilerTrigger) Trigger() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.triggered = true
}

func (m *mockReconcilerTrigger) isTriggered() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.triggered
}

func TestVerifyCheckout_Success(t *testing.T) {
	svc, dailyRepo, _, _, _, provider := setupTopUpService(10)
	trigger := &mockReconcilerTrigger{}
	svc.SetReconciler(trigger)

	ctx := context.Background()
	userID := "user-verify-1"
	amount := int64(5)

	order, err := svc.CreateTopUp(ctx, userID, "idem-verify-1", amount)
	if err != nil {
		t.Fatalf("failed to create top-up: %v", err)
	}

	paymentID := "pay_test_12345"
	sig := provider.GenerateCheckoutSignature(*order.ProviderOrderID, paymentID)

	req := service.VerifyCheckoutRequest{
		TopUpID:           order.ID,
		RazorpayOrderID:   *order.ProviderOrderID,
		RazorpayPaymentID: paymentID,
		RazorpaySignature: sig,
	}

	confirmedOrder, err := svc.VerifyCheckoutPayment(ctx, userID, req)
	if err != nil {
		t.Fatalf("expected verification to succeed, got: %v", err)
	}

	if confirmedOrder.Status != domain.StatusCreditPending {
		t.Errorf("expected status CREDIT_PENDING, got: %s", confirmedOrder.Status)
	}
	if confirmedOrder.PaymentID == nil || *confirmedOrder.PaymentID != paymentID {
		t.Errorf("expected payment ID %s, got %v", paymentID, confirmedOrder.PaymentID)
	}

	// Verify reconciler was triggered
	if !trigger.isTriggered() {
		t.Errorf("expected reconciler Trigger() to be called")
	}

	// Verify daily quota transitioned: reserved -> consumed
	usage, err := dailyRepo.GetDailyUsage(ctx, userID, time.Now().Format("2006-01-02"), 10)
	if err != nil {
		t.Fatalf("failed to get usage: %v", err)
	}
	if usage.ReservedINR != 0 {
		t.Errorf("expected 0 reserved INR, got %d", usage.ReservedINR)
	}
	if usage.ConsumedINR != 5 {
		t.Errorf("expected 5 consumed INR, got %d", usage.ConsumedINR)
	}
}

func TestVerifyCheckout_InvalidSignature(t *testing.T) {
	svc, _, _, _, _, _ := setupTopUpService(10)
	ctx := context.Background()
	userID := "user-verify-sig"

	order, err := svc.CreateTopUp(ctx, userID, "idem-sig-1", 5)
	if err != nil {
		t.Fatalf("failed to create top-up: %v", err)
	}

	req := service.VerifyCheckoutRequest{
		TopUpID:           order.ID,
		RazorpayOrderID:   *order.ProviderOrderID,
		RazorpayPaymentID: "pay_test_bad_sig",
		RazorpaySignature: "invalid_tampered_signature_hex",
	}

	_, err = svc.VerifyCheckoutPayment(ctx, userID, req)
	if !errors.Is(err, domain.ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature, got: %v", err)
	}
}

func TestVerifyCheckout_WrongRazorpayOrderID(t *testing.T) {
	svc, _, _, _, _, provider := setupTopUpService(10)
	ctx := context.Background()
	userID := "user-verify-mismatch"

	order, err := svc.CreateTopUp(ctx, userID, "idem-mismatch-1", 5)
	if err != nil {
		t.Fatalf("failed to create top-up: %v", err)
	}

	paymentID := "pay_test_wrong_order"
	sig := provider.GenerateCheckoutSignature("order_different_999", paymentID)

	req := service.VerifyCheckoutRequest{
		TopUpID:           order.ID,
		RazorpayOrderID:   "order_different_999",
		RazorpayPaymentID: paymentID,
		RazorpaySignature: sig,
	}

	_, err = svc.VerifyCheckoutPayment(ctx, userID, req)
	if !errors.Is(err, domain.ErrMismatchedProviderOrder) {
		t.Fatalf("expected ErrMismatchedProviderOrder, got: %v", err)
	}
}

func TestVerifyCheckout_WrongUserOwnership(t *testing.T) {
	svc, _, _, _, _, provider := setupTopUpService(10)
	ctx := context.Background()
	ownerUser := "user-owner"
	attackerUser := "user-attacker"

	order, err := svc.CreateTopUp(ctx, ownerUser, "idem-owner-1", 5)
	if err != nil {
		t.Fatalf("failed to create top-up: %v", err)
	}

	paymentID := "pay_test_attacker"
	sig := provider.GenerateCheckoutSignature(*order.ProviderOrderID, paymentID)

	req := service.VerifyCheckoutRequest{
		TopUpID:           order.ID,
		RazorpayOrderID:   *order.ProviderOrderID,
		RazorpayPaymentID: paymentID,
		RazorpaySignature: sig,
	}

	_, err = svc.VerifyCheckoutPayment(ctx, attackerUser, req)
	if !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for wrong user, got: %v", err)
	}
}

func TestVerifyCheckout_AmountMismatch(t *testing.T) {
	svc, _, _, _, _, provider := setupTopUpService(10)
	ctx := context.Background()
	userID := "user-amt-mismatch"

	// Create topup for ₹10
	order, err := svc.CreateTopUp(ctx, userID, "idem-amt-1", 10)
	if err != nil {
		t.Fatalf("failed to create top-up: %v", err)
	}

	paymentID := "pay_test_partial"
	sig := provider.GenerateCheckoutSignature(*order.ProviderOrderID, paymentID)

	// Mock provider returns payment for only ₹5 (500 paise)
	provider.SetCustomPayment(paymentID, &payment.PaymentDetails{
		PaymentID:       paymentID,
		ProviderOrderID: *order.ProviderOrderID,
		AmountINR:       5, // ₹5 paid instead of ₹10
		Currency:        "INR",
		Status:          "captured",
		Captured:        true,
	})

	req := service.VerifyCheckoutRequest{
		TopUpID:           order.ID,
		RazorpayOrderID:   *order.ProviderOrderID,
		RazorpayPaymentID: paymentID,
		RazorpaySignature: sig,
	}

	_, err = svc.VerifyCheckoutPayment(ctx, userID, req)
	if !errors.Is(err, domain.ErrPaymentAmountMismatch) {
		t.Fatalf("expected ErrPaymentAmountMismatch, got: %v", err)
	}
}

func TestVerifyCheckout_CurrencyMismatch(t *testing.T) {
	svc, _, _, _, _, provider := setupTopUpService(10)
	ctx := context.Background()
	userID := "user-curr-mismatch"

	order, err := svc.CreateTopUp(ctx, userID, "idem-curr-1", 5)
	if err != nil {
		t.Fatalf("failed to create top-up: %v", err)
	}

	paymentID := "pay_test_usd"
	sig := provider.GenerateCheckoutSignature(*order.ProviderOrderID, paymentID)

	provider.SetCustomPayment(paymentID, &payment.PaymentDetails{
		PaymentID:       paymentID,
		ProviderOrderID: *order.ProviderOrderID,
		AmountINR:       5,
		Currency:        "USD", // Non-INR currency
		Status:          "captured",
		Captured:        true,
	})

	req := service.VerifyCheckoutRequest{
		TopUpID:           order.ID,
		RazorpayOrderID:   *order.ProviderOrderID,
		RazorpayPaymentID: paymentID,
		RazorpaySignature: sig,
	}

	_, err = svc.VerifyCheckoutPayment(ctx, userID, req)
	if !errors.Is(err, domain.ErrPaymentCurrencyMismatch) {
		t.Fatalf("expected ErrPaymentCurrencyMismatch, got: %v", err)
	}
}

func TestVerifyCheckout_PaymentNotCaptured(t *testing.T) {
	svc, _, _, _, _, provider := setupTopUpService(10)
	ctx := context.Background()
	userID := "user-not-captured"

	order, err := svc.CreateTopUp(ctx, userID, "idem-not-cap-1", 5)
	if err != nil {
		t.Fatalf("failed to create top-up: %v", err)
	}

	paymentID := "pay_test_pending_capture"
	sig := provider.GenerateCheckoutSignature(*order.ProviderOrderID, paymentID)

	// Payment is authorized but not captured
	provider.SetCustomPayment(paymentID, &payment.PaymentDetails{
		PaymentID:       paymentID,
		ProviderOrderID: *order.ProviderOrderID,
		AmountINR:       5,
		Currency:        "INR",
		Status:          "authorized",
		Captured:        false,
	})

	req := service.VerifyCheckoutRequest{
		TopUpID:           order.ID,
		RazorpayOrderID:   *order.ProviderOrderID,
		RazorpayPaymentID: paymentID,
		RazorpaySignature: sig,
	}

	_, err = svc.VerifyCheckoutPayment(ctx, userID, req)
	if !errors.Is(err, domain.ErrPaymentNotCaptured) {
		t.Fatalf("expected ErrPaymentNotCaptured, got: %v", err)
	}
}

func TestVerifyCheckout_RepeatedRequests_Idempotent(t *testing.T) {
	svc, _, _, _, _, provider := setupTopUpService(10)
	ctx := context.Background()
	userID := "user-verify-idempotent"

	order, err := svc.CreateTopUp(ctx, userID, "idem-rep-1", 5)
	if err != nil {
		t.Fatalf("failed to create top-up: %v", err)
	}

	paymentID := "pay_test_idem_1"
	sig := provider.GenerateCheckoutSignature(*order.ProviderOrderID, paymentID)

	req := service.VerifyCheckoutRequest{
		TopUpID:           order.ID,
		RazorpayOrderID:   *order.ProviderOrderID,
		RazorpayPaymentID: paymentID,
		RazorpaySignature: sig,
	}

	// First call succeeds
	res1, err := svc.VerifyCheckoutPayment(ctx, userID, req)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if res1.Status != domain.StatusCreditPending {
		t.Errorf("expected CREDIT_PENDING, got %s", res1.Status)
	}

	// Repeated call with same parameters must be safe and idempotent
	res2, err := svc.VerifyCheckoutPayment(ctx, userID, req)
	if err != nil {
		t.Fatalf("repeated call failed: %v", err)
	}
	if res2.Status != domain.StatusCreditPending {
		t.Errorf("expected CREDIT_PENDING on idempotent repeat, got %s", res2.Status)
	}
	if res2.PaymentID == nil || *res2.PaymentID != paymentID {
		t.Errorf("expected payment ID preserved")
	}
}

func TestVerifyCheckout_ProviderAPIFailure(t *testing.T) {
	svc, _, orderRepo, _, _, provider := setupTopUpService(10)
	ctx := context.Background()
	userID := "user-provider-timeout"

	order, err := svc.CreateTopUp(ctx, userID, "idem-timeout-1", 5)
	if err != nil {
		t.Fatalf("failed to create top-up: %v", err)
	}

	paymentID := "pay_test_timeout"
	sig := provider.GenerateCheckoutSignature(*order.ProviderOrderID, paymentID)

	// Inject provider network failure
	provider.SetFailNextFetchPayment(true)

	req := service.VerifyCheckoutRequest{
		TopUpID:           order.ID,
		RazorpayOrderID:   *order.ProviderOrderID,
		RazorpayPaymentID: paymentID,
		RazorpaySignature: sig,
	}

	_, err = svc.VerifyCheckoutPayment(ctx, userID, req)
	if err == nil {
		t.Fatalf("expected error when provider API times out")
	}

	// Verify order status was NOT changed to CREDIT_PENDING
	stored, err := orderRepo.GetByID(ctx, order.ID)
	if err != nil {
		t.Fatalf("failed to get order: %v", err)
	}
	if stored.Status != domain.StatusPaymentPending {
		t.Errorf("expected order to remain PAYMENT_PENDING on timeout, got %s", stored.Status)
	}
}

func TestConcurrent_CheckoutAndWebhook_Convergence(t *testing.T) {
	dailyRepo := NewMockDailyLimitRepo()
	orderRepo := NewMockTopUpOrderRepo()
	webhookRepo := NewMockWebhookEventRepo()
	txManager := NewMockTxManager(dailyRepo, orderRepo, webhookRepo)
	provider := mock.NewMockPaymentProvider("secret")
	topupSvc := service.NewTopUpService(dailyRepo, orderRepo, txManager, provider, 10, zap.NewNop())

	ctx := context.Background()
	userID := "user-concurrent-1"
	amount := int64(5)

	order, err := topupSvc.CreateTopUp(ctx, userID, "idem-conc-1", amount)
	if err != nil {
		t.Fatalf("failed to create topup: %v", err)
	}

	paymentID := "pay_concurrent_123"
	sig := provider.GenerateCheckoutSignature(*order.ProviderOrderID, paymentID)

	var wg sync.WaitGroup
	var checkoutErr, webhookErr error
	var checkoutOrder *domain.TopUpOrder

	// 1. Goroutine for Checkout verification
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := service.VerifyCheckoutRequest{
			TopUpID:           order.ID,
			RazorpayOrderID:   *order.ProviderOrderID,
			RazorpayPaymentID: paymentID,
			RazorpaySignature: sig,
		}
		checkoutOrder, checkoutErr = topupSvc.VerifyCheckoutPayment(ctx, userID, req)
	}()

	// 2. Goroutine for Webhook confirmation
	wg.Add(1)
	go func() {
		defer wg.Done()
		now := time.Now().UTC()
		rawPayload := fmt.Sprintf(`{"event":"payment.captured","payload":{"payment":{"entity":{"id":"%s","order_id":"%s","amount":500,"currency":"INR","status":"captured"}}}}`, paymentID, *order.ProviderOrderID)
		eventID := "evt_concurrent_123"
		paidDate := now.Format("2006-01-02")

		// Process directly via ProcessPaymentConfirmationTx like WebhookService does
		_, webhookErr = txManager.ProcessPaymentConfirmationTx(
			ctx,
			order.ID,
			"RAZORPAY",
			paymentID,
			paidDate,
			[]byte(rawPayload),
			eventID,
			10,
		)
	}()

	wg.Wait()

	// At least one must succeed; if both succeed or one returns already confirmed/duplicate, it must be safe
	if checkoutErr != nil && !errors.Is(checkoutErr, domain.ErrDuplicateWebhook) {
		t.Fatalf("unexpected checkout error: %v", checkoutErr)
	}
	if webhookErr != nil && !errors.Is(webhookErr, domain.ErrDuplicateWebhook) {
		t.Fatalf("unexpected webhook error: %v", webhookErr)
	}

	// Verify the final stored state of the order
	finalOrder, err := orderRepo.GetByID(ctx, order.ID)
	if err != nil {
		t.Fatalf("failed to fetch order: %v", err)
	}
	if finalOrder.Status != domain.StatusCreditPending {
		t.Errorf("expected final status CREDIT_PENDING, got: %s", finalOrder.Status)
	}
	if finalOrder.PaymentID == nil || *finalOrder.PaymentID != paymentID {
		t.Errorf("expected payment ID %s, got %v", paymentID, finalOrder.PaymentID)
	}

	// Check daily quota: consumed must be exactly 5, reserved must be 0 (never 10!)
	usage, err := dailyRepo.GetDailyUsage(ctx, userID, time.Now().Format("2006-01-02"), 10)
	if err != nil {
		t.Fatalf("failed to fetch daily usage: %v", err)
	}
	if usage.ConsumedINR != 5 {
		t.Errorf("expected exactly 5 consumed INR, got %d (double counting defect!)", usage.ConsumedINR)
	}
	if usage.ReservedINR != 0 {
		t.Errorf("expected 0 reserved INR, got %d", usage.ReservedINR)
	}
	_ = checkoutOrder
}
