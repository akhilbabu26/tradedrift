package test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/payment/mock"
	"tradedrift/services/wallet-topup/internal/service"
	"tradedrift/services/wallet-topup/internal/webhook"
)

func setupWebhookTest(secret string, dailyLimit int64) (
	*service.TopUpService,
	*service.WebhookService,
	*MockDailyLimitRepo,
	*MockTopUpOrderRepo,
	*MockWebhookEventRepo,
	*MockTxManager,
	*mock.MockPaymentProvider,
) {
	dailyRepo := NewMockDailyLimitRepo()
	orderRepo := NewMockTopUpOrderRepo()
	webhookRepo := NewMockWebhookEventRepo()
	txManager := NewMockTxManager(dailyRepo, orderRepo, webhookRepo)
	provider := mock.NewMockPaymentProvider(secret)
	razorpay := mock.NewNamedMockPaymentProvider("RAZORPAY", secret)
	verifier := webhook.NewVerifier(provider, razorpay)

	topupSvc := service.NewTopUpService(dailyRepo, orderRepo, txManager, provider, dailyLimit, zap.NewNop())
	webhookSvc := service.NewWebhookService(verifier, webhookRepo, orderRepo, txManager, dailyLimit, zap.NewNop())

	return topupSvc, webhookSvc, dailyRepo, orderRepo, webhookRepo, txManager, provider
}

func TestWebhook_InvalidSignature(t *testing.T) {
	_, webhookSvc, _, _, _, _, _ := setupWebhookTest("secret", 10)

	payload := []byte(`{"event_id":"evt_1","inr_amount":5,"currency":"INR"}`)
	err := webhookSvc.ProcessWebhook(context.Background(), "MOCK", payload, "bad_signature", time.Now().Unix())
	if !errors.Is(err, domain.ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature, got %v", err)
	}
}

func TestWebhook_ReplayExpired(t *testing.T) {
	_, webhookSvc, _, _, _, _, provider := setupWebhookTest("secret", 10)

	payload := []byte(`{"event_id":"evt_1","inr_amount":5,"currency":"INR"}`)
	oldTimestamp := time.Now().Unix() - 305 // > 300s old
	sig := provider.GenerateSignature(payload, oldTimestamp)

	err := webhookSvc.ProcessWebhook(context.Background(), "MOCK", payload, sig, oldTimestamp)
	if !errors.Is(err, domain.ErrWebhookReplay) {
		t.Fatalf("expected ErrWebhookReplay, got %v", err)
	}
}

func TestWebhook_InvalidEventType(t *testing.T) {
	topupSvc, webhookSvc, _, orderRepo, _, _, provider := setupWebhookTest("secret", 10)
	ctx := context.Background()

	order, err := topupSvc.CreateTopUp(ctx, "user-wh-evt", "wh-evt-key", 5)
	if err != nil {
		t.Fatalf("CreateTopUp failed: %v", err)
	}

	now := time.Now().Unix()
	whPayload := service.PaymentWebhookPayload{
		EventID:   "evt_failed_payment",
		EventType: "payment.failed", // NOT payment.captured
		PaymentID: "pay_fail_1",
		OrderID:   order.ID,
		INRAmount: 5,
		Currency:  "INR",
		Timestamp: now,
	}
	body, _ := json.Marshal(whPayload)
	sig := provider.GenerateSignature(body, now)

	err = webhookSvc.ProcessWebhook(ctx, "MOCK", body, sig, now)
	if !errors.Is(err, domain.ErrInvalidEventType) {
		t.Fatalf("expected ErrInvalidEventType, got %v", err)
	}

	// Order must still be PAYMENT_PENDING, not CREDIT_PENDING
	o, _ := orderRepo.GetByID(ctx, order.ID)
	if o.Status != domain.StatusPaymentPending {
		t.Errorf("expected order to remain PAYMENT_PENDING, got %s", o.Status)
	}
}

func TestWebhook_ProviderMismatch(t *testing.T) {
	topupSvc, webhookSvc, _, _, _, _, provider := setupWebhookTest("secret", 10)
	ctx := context.Background()

	order, err := topupSvc.CreateTopUp(ctx, "user-wh-pm", "wh-pm-key", 5)
	if err != nil {
		t.Fatalf("CreateTopUp failed: %v", err)
	}

	now := time.Now().Unix()
	whPayload := service.PaymentWebhookPayload{
		EventID:   "evt_pm_1",
		EventType: "payment.captured",
		PaymentID: "pay_pm_1",
		OrderID:   order.ID,
		INRAmount: 5,
		Currency:  "INR",
		PaidAt:    now,
		Timestamp: now,
	}
	body, _ := json.Marshal(whPayload)
	sig := provider.GenerateSignature(body, now)

	// Webhook claims to be RAZORPAY when order was created for MOCK
	err = webhookSvc.ProcessWebhook(ctx, "RAZORPAY", body, sig, now)
	if !errors.Is(err, domain.ErrProviderMismatch) {
		t.Fatalf("expected ErrProviderMismatch, got %v", err)
	}
}

func TestWebhook_SameDaySuccess(t *testing.T) {
	topupSvc, webhookSvc, dailyRepo, orderRepo, _, _, provider := setupWebhookTest("test_secret", 10)

	ctx := context.Background()
	userID := "user-webhook-success"

	// 1. Create Order: ₹5
	order, err := topupSvc.CreateTopUp(ctx, userID, "key-wh-1", 5)
	if err != nil {
		t.Fatalf("CreateTopUp failed: %v", err)
	}

	// 2. Prepare Webhook Payload
	now := time.Now().Unix()
	whPayload := service.PaymentWebhookPayload{
		EventID:   "evt_pay_1",
		EventType: "payment.captured",
		PaymentID: "pay_123456",
		OrderID:   order.ID,
		INRAmount: 5,
		Currency:  "INR",
		PaidAt:    now,
		Timestamp: now,
	}
	body, _ := json.Marshal(whPayload)
	sig := provider.GenerateSignature(body, now)

	// 3. Process Webhook
	if err := webhookSvc.ProcessWebhook(ctx, "MOCK", body, sig, now); err != nil {
		t.Fatalf("ProcessWebhook failed: %v", err)
	}

	// 4. Assert Order moved to CREDIT_PENDING
	updatedOrder, _ := orderRepo.GetByID(ctx, order.ID)
	if updatedOrder.Status != domain.StatusCreditPending {
		t.Errorf("expected CREDIT_PENDING, got %s", updatedOrder.Status)
	}

	// 5. Assert Quota shifted: reserved=0, consumed=5
	usage, _ := dailyRepo.GetDailyUsage(ctx, userID, order.ReservationDate, 10)
	if usage.ReservedINR != 0 {
		t.Errorf("expected reserved 0, got %d", usage.ReservedINR)
	}
	if usage.ConsumedINR != 5 {
		t.Errorf("expected consumed 5, got %d", usage.ConsumedINR)
	}
}

func TestWebhook_CrossMidnight_QuotaExhausted_RefundRequired(t *testing.T) {
	_, webhookSvc, dailyRepo, orderRepo, _, _, provider := setupWebhookTest("test_secret", 10)

	ctx := context.Background()
	userID := "user-cross-midnight"
	day1 := "2026-09-08"
	day2 := time.Now().Format("2006-01-02") // current day

	// 1. Order was reserved on Day 1 for ₹8
	orderID := "order-cross-1"
	orderRepo.orders[orderID] = &domain.TopUpOrder{
		ID:              orderID,
		UserID:          userID,
		INRAmount:       8,
		USDTAmount:      "8000.0000000000",
		ReservationDate: day1,
		Provider:        "MOCK",
		Status:          domain.StatusPaymentPending,
	}
	// Day 1 has 8 reserved
	dailyRepo.limits[userID+":"+day1] = &domain.DailyTopUpLimit{
		UserID:      userID,
		UsageDate:   day1,
		LimitINR:    10,
		ReservedINR: 8,
		ConsumedINR: 0,
	}

	// 2. But user already consumed ₹5 on Day 2! So only ₹5 capacity left on Day 2 (< ₹8 required)
	dailyRepo.limits[userID+":"+day2] = &domain.DailyTopUpLimit{
		UserID:      userID,
		UsageDate:   day2,
		LimitINR:    10,
		ReservedINR: 0,
		ConsumedINR: 5,
	}

	// 3. Payment arrives on Day 2
	now := time.Now().Unix()
	whPayload := service.PaymentWebhookPayload{
		EventID:   "evt_cross_exhausted",
		EventType: "payment.captured",
		PaymentID: "pay_cross_999",
		OrderID:   orderID,
		INRAmount: 8,
		Currency:  "INR",
		PaidAt:    now,
		Timestamp: now,
	}
	body, _ := json.Marshal(whPayload)
	sig := provider.GenerateSignature(body, now)

	// 4. Process Webhook
	if err := webhookSvc.ProcessWebhook(ctx, "MOCK", body, sig, now); err != nil {
		t.Fatalf("ProcessWebhook failed: %v", err)
	}

	// 5. Assert: Day 1 reservation released (reserved=0)
	day1Usage, _ := dailyRepo.GetDailyUsage(ctx, userID, day1, 10)
	if day1Usage.ReservedINR != 0 {
		t.Errorf("expected Day 1 reserved to be released to 0, got %d", day1Usage.ReservedINR)
	}

	// 6. Assert: Order transitioned to REFUND_REQUIRED!
	updatedOrder, _ := orderRepo.GetByID(ctx, orderID)
	if updatedOrder.Status != domain.StatusRefundRequired {
		t.Errorf("expected REFUND_REQUIRED, got %s", updatedOrder.Status)
	}

	// 7. CRITICAL INVARIANT: Day 2 consumed must remain 5, NOT incremented to 13!
	day2Usage, _ := dailyRepo.GetDailyUsage(ctx, userID, day2, 10)
	if day2Usage.ConsumedINR != 5 {
		t.Errorf("CRITICAL INVARIANT VIOLATION: Day 2 consumed must remain 5, got %d", day2Usage.ConsumedINR)
	}
}

func TestWebhook_DelayedCrossMidnight_CaptureDateRespected(t *testing.T) {
	_, webhookSvc, dailyRepo, orderRepo, _, _, provider := setupWebhookTest("test_secret", 10)
	ctx := context.Background()
	userID := "user-delayed-cross"
	day1 := "2026-09-10"
	day2 := "2026-09-11"

	orderID, _ := platformuuid.New()
	provOrderID := "mock_order_" + orderID
	order := &domain.TopUpOrder{
		ID:              orderID,
		UserID:          userID,
		IdempotencyKey:  "key-delayed-1",
		INRAmount:       5,
		USDTAmount:      "5000.0000000000",
		ReservationDate: day1,
		Provider:        "MOCK",
		ProviderOrderID: &provOrderID,
		Status:          domain.StatusPaymentPending,
		ExpiresAt:       time.Now().UTC().Add(1 * time.Hour),
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}
	_ = orderRepo.Create(ctx, order)

	// User has ₹5 reserved on Day 1
	dailyRepo.limits[userID+":"+day1] = &domain.DailyTopUpLimit{
		UserID:      userID,
		UsageDate:   day1,
		LimitINR:    10,
		ReservedINR: 5,
		ConsumedINR: 0,
	}
	// Day 2 has 0 usage so far
	dailyRepo.limits[userID+":"+day2] = &domain.DailyTopUpLimit{
		UserID:      userID,
		UsageDate:   day2,
		LimitINR:    10,
		ReservedINR: 0,
		ConsumedINR: 0,
	}

	// Capture happened on Day 1 at 23:59:50 IST (17:29:50 UTC)
	locIST, _ := time.LoadLocation("Asia/Kolkata")
	if locIST == nil {
		locIST = time.FixedZone("IST", 5*3600+30*60)
	}
	day1Capture := time.Date(2026, 9, 10, 23, 59, 50, 0, locIST).Unix()
	nowHeader := time.Now().Unix() // Header timestamp is when webhook arrives

	whPayload := service.PaymentWebhookPayload{
		EventID:         "evt_delayed_1",
		EventType:       "payment.captured",
		PaymentID:       "pay_delayed_99",
		ProviderOrderID: provOrderID,
		INRAmount:       5,
		Currency:        "INR",
		PaidAt:          day1Capture, // Explicit payment capture timestamp
		Timestamp:       nowHeader,
	}
	body, _ := json.Marshal(whPayload)
	sig := provider.GenerateSignature(body, nowHeader)

	if err := webhookSvc.ProcessWebhook(ctx, "MOCK", body, sig, nowHeader); err != nil {
		t.Fatalf("ProcessWebhook failed: %v", err)
	}

	// Assert: Payment was credited to Day 1 quota, NOT Day 2!
	day1Usage, _ := dailyRepo.GetDailyUsage(ctx, userID, day1, 10)
	if day1Usage.ReservedINR != 0 || day1Usage.ConsumedINR != 5 {
		t.Errorf("expected Day 1 reserved=0, consumed=5; got reserved=%d, consumed=%d",
			day1Usage.ReservedINR, day1Usage.ConsumedINR)
	}

	day2Usage, _ := dailyRepo.GetDailyUsage(ctx, userID, day2, 10)
	if day2Usage.ConsumedINR != 0 || day2Usage.ReservedINR != 0 {
		t.Errorf("expected Day 2 to remain completely untouched; got reserved=%d, consumed=%d",
			day2Usage.ReservedINR, day2Usage.ConsumedINR)
	}

	updatedOrder, _ := orderRepo.GetByID(ctx, orderID)
	if updatedOrder.Status != domain.StatusCreditPending {
		t.Errorf("expected order status CREDIT_PENDING, got %s", updatedOrder.Status)
	}
}

func TestWebhook_MandatoryFieldsValidation(t *testing.T) {
	_, webhookSvc, _, _, _, _, provider := setupWebhookTest("test_secret", 10)
	ctx := context.Background()
	now := time.Now().Unix()

	tests := []struct {
		name    string
		payload service.PaymentWebhookPayload
		wantErr string
	}{
		{
			name: "missing event_id",
			payload: service.PaymentWebhookPayload{
				EventID:   "",
				EventType: "payment.captured",
				PaymentID: "pay_1",
				OrderID:   "ord_1",
				INRAmount: 5,
				Currency:  "INR",
				PaidAt:    now,
				Timestamp: now,
			},
			wantErr: "missing mandatory event_id",
		},
		{
			name: "missing payment_id",
			payload: service.PaymentWebhookPayload{
				EventID:   "evt_1",
				EventType: "payment.captured",
				PaymentID: "",
				OrderID:   "ord_1",
				INRAmount: 5,
				Currency:  "INR",
				PaidAt:    now,
				Timestamp: now,
			},
			wantErr: "missing mandatory payment_id",
		},
		{
			name: "missing order identity",
			payload: service.PaymentWebhookPayload{
				EventID:         "evt_1",
				EventType:       "payment.captured",
				PaymentID:       "pay_1",
				OrderID:         "",
				ProviderOrderID: "",
				INRAmount:       5,
				Currency:        "INR",
				PaidAt:          now,
				Timestamp:       now,
			},
			wantErr: "missing mandatory order reference",
		},
		{
			name: "empty event_type",
			payload: service.PaymentWebhookPayload{
				EventID:   "evt_1",
				EventType: "",
				PaymentID: "pay_1",
				OrderID:   "ord_1",
				INRAmount: 5,
				Currency:  "INR",
				PaidAt:    now,
				Timestamp: now,
			},
			wantErr: "invalid event type",
		},
		{
			name: "missing paid_at",
			payload: service.PaymentWebhookPayload{
				EventID:   "evt_1",
				EventType: "payment.captured",
				PaymentID: "pay_1",
				OrderID:   "ord_1",
				INRAmount: 5,
				Currency:  "INR",
				PaidAt:    0,
				Timestamp: now,
			},
			wantErr: "missing mandatory paid_at timestamp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.payload)
			sig := provider.GenerateSignature(body, now)
			err := webhookSvc.ProcessWebhook(ctx, "MOCK", body, sig, now)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
		})
	}
}

func TestWebhook_ProviderOrderID_Lookup(t *testing.T) {
	topupSvc, webhookSvc, _, orderRepo, _, _, provider := setupWebhookTest("test_secret", 10)
	ctx := context.Background()

	// 1. Create order
	order, err := topupSvc.CreateTopUp(ctx, "user-porder", "porder-key-1", 5)
	if err != nil {
		t.Fatalf("CreateTopUp failed: %v", err)
	}

	now := time.Now().Unix()

	// 2. Lookup by ProviderOrderID only (OrderID field omitted)
	whPayload := service.PaymentWebhookPayload{
		EventID:         "evt_prov_1",
		EventType:       "payment.captured",
		PaymentID:       "pay_prov_1",
		ProviderOrderID: *order.ProviderOrderID,
		INRAmount:       5,
		Currency:        "INR",
		PaidAt:          now,
		Timestamp:       now,
	}
	body, _ := json.Marshal(whPayload)
	sig := provider.GenerateSignature(body, now)

	err = webhookSvc.ProcessWebhook(ctx, "MOCK", body, sig, now)
	if err != nil {
		t.Fatalf("ProcessWebhook with ProviderOrderID failed: %v", err)
	}

	updated, _ := orderRepo.GetByID(ctx, order.ID)
	if updated.Status != domain.StatusCreditPending {
		t.Fatalf("expected CREDIT_PENDING, got %s", updated.Status)
	}

	// 3. Mismatched ProviderOrderID rejected
	whPayloadMismatch := service.PaymentWebhookPayload{
		EventID:         "evt_prov_2",
		EventType:       "payment.captured",
		PaymentID:       "pay_prov_2",
		OrderID:         order.ID,
		ProviderOrderID: "mock_order_wrong_id",
		INRAmount:       5,
		Currency:        "INR",
		PaidAt:          now,
		Timestamp:       now,
	}
	bodyMis, _ := json.Marshal(whPayloadMismatch)
	sigMis := provider.GenerateSignature(bodyMis, now)

	err = webhookSvc.ProcessWebhook(ctx, "MOCK", bodyMis, sigMis, now)
	if err == nil {
		t.Fatalf("expected error for mismatched provider order ID, got nil")
	}
}

func TestWebhook_ValidSignatureMalformedJSON(t *testing.T) {
	_, webhookSvc, _, _, webhookRepo, _, provider := setupWebhookTest("secret", 10)
	ctx := context.Background()
	now := time.Now().Unix()

	malformedJSON := []byte(`{"event_id": "evt_broken", invalid_json`)
	sig := provider.GenerateSignature(malformedJSON, now)

	err := webhookSvc.ProcessWebhook(ctx, "MOCK", malformedJSON, sig, now)
	if err == nil {
		t.Fatalf("expected error for malformed json, got nil")
	}

	events := webhookRepo.GetAllEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 audited webhook event, got %d", len(events))
	}
	ev := events[0]
	if !ev.SignatureValid {
		t.Errorf("expected SignatureValid == true, got false")
	}
	if ev.Status != domain.WebhookStatusFailed {
		t.Errorf("expected Status == FAILED, got %s", ev.Status)
	}
	if ev.ErrorMessage == nil || !strings.Contains(*ev.ErrorMessage, "malformed webhook payload") {
		t.Errorf("expected error message mentioning malformed webhook payload, got %v", ev.ErrorMessage)
	}
}

func TestWebhook_ValidSignatureMissingPaidAt(t *testing.T) {
	topupSvc, webhookSvc, _, _, webhookRepo, _, provider := setupWebhookTest("secret", 10)
	ctx := context.Background()
	now := time.Now().Unix()

	order, err := topupSvc.CreateTopUp(ctx, "user-paidat-test", "key-paidat-1", 5)
	if err != nil {
		t.Fatalf("CreateTopUp failed: %v", err)
	}

	whPayload := service.PaymentWebhookPayload{
		EventID:   "evt_no_paidat",
		EventType: "payment.captured",
		PaymentID: "pay_no_paidat_1",
		OrderID:   order.ID,
		INRAmount: 5,
		Currency:  "INR",
		PaidAt:    0, // missing/zero
		Timestamp: now,
	}
	body, _ := json.Marshal(whPayload)
	sig := provider.GenerateSignature(body, now)

	err = webhookSvc.ProcessWebhook(ctx, "MOCK", body, sig, now)
	if err == nil {
		t.Fatalf("expected error for missing paid_at, got nil")
	}
	if !strings.Contains(err.Error(), "missing mandatory paid_at timestamp") {
		t.Fatalf("unexpected error message: %v", err)
	}

	events := webhookRepo.GetAllEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 audited webhook event, got %d", len(events))
	}
	ev := events[0]
	if !ev.SignatureValid {
		t.Errorf("expected SignatureValid == true, got false")
	}
	if ev.Status != domain.WebhookStatusFailed {
		t.Errorf("expected Status == FAILED, got %s", ev.Status)
	}
	if ev.ErrorMessage == nil || !strings.Contains(*ev.ErrorMessage, "paid_at") {
		t.Errorf("expected error message mentioning paid_at, got %v", ev.ErrorMessage)
	}
}

func TestWebhook_ValidSignatureAmountMismatch_AuditRecorded(t *testing.T) {
	topupSvc, webhookSvc, _, _, webhookRepo, _, provider := setupWebhookTest("secret", 10)
	ctx := context.Background()
	now := time.Now().Unix()

	order, err := topupSvc.CreateTopUp(ctx, "user-mismatch-test", "key-mismatch-1", 5)
	if err != nil {
		t.Fatalf("CreateTopUp failed: %v", err)
	}

	whPayload := service.PaymentWebhookPayload{
		EventID:   "evt_mismatch_1",
		EventType: "payment.captured",
		PaymentID: "pay_mismatch_1",
		OrderID:   order.ID,
		INRAmount: 8, // order was ₹5!
		Currency:  "INR",
		PaidAt:    now,
		Timestamp: now,
	}
	body, _ := json.Marshal(whPayload)
	sig := provider.GenerateSignature(body, now)

	err = webhookSvc.ProcessWebhook(ctx, "MOCK", body, sig, now)
	if err == nil {
		t.Fatalf("expected error for amount mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "amount mismatch") {
		t.Fatalf("unexpected error message: %v", err)
	}

	events := webhookRepo.GetAllEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 audited webhook event, got %d", len(events))
	}
	ev := events[0]
	if !ev.SignatureValid {
		t.Errorf("expected SignatureValid == true, got false")
	}
	if ev.Status != domain.WebhookStatusFailed {
		t.Errorf("expected Status == FAILED, got %s", ev.Status)
	}
	if ev.ErrorMessage == nil || !strings.Contains(*ev.ErrorMessage, "amount mismatch") {
		t.Errorf("expected error message mentioning amount mismatch, got %v", ev.ErrorMessage)
	}
}

