package test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/payment/mock"
	"tradedrift/services/wallet-topup/internal/service"
	"tradedrift/services/wallet-topup/internal/webhook"
)

// GenerateRazorpaySignature computes the standard Razorpay HMAC-SHA256 signature
func GenerateRazorpaySignature(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestSimulator_RazorpayPaymentCaptured_IdempotentReconciliation(t *testing.T) {
	const secret = "staging_rzp_webhook_secret_key"
	const dailyLimit = int64(10)

	dailyRepo := NewMockDailyLimitRepo()
	orderRepo := NewMockTopUpOrderRepo()
	webhookRepo := NewMockWebhookEventRepo()
	txManager := NewMockTxManager(dailyRepo, orderRepo, webhookRepo)

	rzpProvider := mock.NewNamedMockPaymentProvider("RAZORPAY", secret)
	verifier := webhook.NewVerifier(rzpProvider)

	topupSvc := service.NewTopUpService(dailyRepo, orderRepo, txManager, rzpProvider, dailyLimit, zap.NewNop())
	webhookSvc := service.NewWebhookService(verifier, webhookRepo, orderRepo, txManager, dailyLimit, zap.NewNop())

	ctx := context.Background()
	userID := "usr_staging_trader_1"

	// 1. Create a top-up order
	order, err := topupSvc.CreateTopUp(ctx, userID, "idem_key_stag_1", 5)
	if err != nil {
		t.Fatalf("failed to create topup order: %v", err)
	}

	rzpOrderID := *order.ProviderOrderID

	// 2. Build authentic Razorpay payment.captured webhook payload
	rzpPayload := map[string]interface{}{
		"entity":     "event",
		"account_id": "acc_staging_001",
		"event":      "payment.captured",
		"created_at": time.Now().Unix(),
		"payload": map[string]interface{}{
			"payment": map[string]interface{}{
				"entity": map[string]interface{}{
					"id":         "pay_stag_001",
					"amount":     500, // ₹5 in paise
					"currency":   "INR",
					"status":     "captured",
					"order_id":   rzpOrderID,
					"created_at": time.Now().Unix(),
					"notes": map[string]string{
						"topup_order_id": order.ID,
					},
				},
			},
		},
	}

	body, err := json.Marshal(rzpPayload)
	if err != nil {
		t.Fatalf("failed to marshal payload: %v", err)
	}

	validSig := GenerateRazorpaySignature(body, secret)
	now := time.Now().Unix()

	// 3. First Delivery — should successfully reconcile and advance order to CREDIT_PENDING
	err = webhookSvc.ProcessWebhook(ctx, "RAZORPAY", body, validSig, now)
	if err != nil {
		t.Fatalf("expected first webhook delivery to succeed, got: %v", err)
	}

	reconciledOrder, err := orderRepo.GetByID(ctx, order.ID)
	if err != nil {
		t.Fatalf("failed to load reconciled order: %v", err)
	}
	if reconciledOrder.Status != domain.StatusCreditPending {
		t.Fatalf("expected order status %s, got %s", domain.StatusCreditPending, reconciledOrder.Status)
	}

	// 4. Second Delivery (Duplicate / Replay) — Must be idempotent without duplicate crediting
	err = webhookSvc.ProcessWebhook(ctx, "RAZORPAY", body, validSig, now)
	if err != nil {
		t.Fatalf("expected duplicate webhook delivery to be idempotently accepted, got: %v", err)
	}

	orderAfterReplay, _ := orderRepo.GetByID(ctx, order.ID)
	if orderAfterReplay.Status != domain.StatusCreditPending {
		t.Fatalf("expected order status to remain %s, got: %s", domain.StatusCreditPending, orderAfterReplay.Status)
	}

	// 5. Tampered Signature Delivery — Must be rejected with ErrInvalidSignature
	badSig := "0000000000000000000000000000000000000000000000000000000000000000"
	err = webhookSvc.ProcessWebhook(ctx, "RAZORPAY", body, badSig, now)
	if err == nil {
		t.Fatalf("expected tampered signature to be rejected, but succeeded")
	}
}

func TestSimulator_RazorpayOrderPaid_Succeeds(t *testing.T) {
	const secret = "staging_rzp_webhook_secret_key"
	const dailyLimit = int64(10)

	dailyRepo := NewMockDailyLimitRepo()
	orderRepo := NewMockTopUpOrderRepo()
	webhookRepo := NewMockWebhookEventRepo()
	txManager := NewMockTxManager(dailyRepo, orderRepo, webhookRepo)

	rzpProvider := mock.NewNamedMockPaymentProvider("RAZORPAY", secret)
	verifier := webhook.NewVerifier(rzpProvider)

	topupSvc := service.NewTopUpService(dailyRepo, orderRepo, txManager, rzpProvider, dailyLimit, zap.NewNop())
	webhookSvc := service.NewWebhookService(verifier, webhookRepo, orderRepo, txManager, dailyLimit, zap.NewNop())

	ctx := context.Background()
	userID := "usr_staging_trader_2"

	order, err := topupSvc.CreateTopUp(ctx, userID, "idem_key_order_paid", 2)
	if err != nil {
		t.Fatalf("failed to create topup order: %v", err)
	}

	rzpOrderID := *order.ProviderOrderID

	// Razorpay order.paid event payload
	rzpPayload := map[string]interface{}{
		"entity":     "event",
		"account_id": "acc_staging_001",
		"event":      "order.paid",
		"created_at": time.Now().Unix(),
		"payload": map[string]interface{}{
			"order": map[string]interface{}{
				"entity": map[string]interface{}{
					"id":         rzpOrderID,
					"amount":     200, // ₹2 in paise
					"currency":   "INR",
					"status":     "paid",
					"created_at": time.Now().Unix(),
					"notes": map[string]string{
						"topup_order_id": order.ID,
					},
				},
			},
		},
	}

	body, _ := json.Marshal(rzpPayload)
	validSig := GenerateRazorpaySignature(body, secret)
	now := time.Now().Unix()

	err = webhookSvc.ProcessWebhook(ctx, "RAZORPAY", body, validSig, now)
	if err != nil {
		t.Fatalf("expected order.paid webhook to succeed, got: %v", err)
	}

	confirmedOrder, _ := orderRepo.GetByID(ctx, order.ID)
	if confirmedOrder.Status != domain.StatusCreditPending {
		t.Fatalf("expected order status %s, got: %s", domain.StatusCreditPending, confirmedOrder.Status)
	}
}
