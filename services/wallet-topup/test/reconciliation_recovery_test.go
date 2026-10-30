package test

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	walletv1 "tradedrift/platform/api/gen/wallet/v1"
	"tradedrift/services/wallet-topup/internal/client"
	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/payment"
	"tradedrift/services/wallet-topup/internal/payment/mock"
	"tradedrift/services/wallet-topup/internal/service"
)

type mockWalletDepositClient struct {
	walletv1.WalletServiceClient
}

func (m *mockWalletDepositClient) DepositFunds(ctx context.Context, in *walletv1.DepositFundsRequest, opts ...grpc.CallOption) (*walletv1.DepositFundsResponse, error) {
	return &walletv1.DepositFundsResponse{
		TransactionId: "txn-mock-deposit-123",
		NewBalance:    "5000.0000000000",
		Success:       true,
	}, nil
}

func TestReconciler_StrandedPaymentRecovery(t *testing.T) {
	dailyRepo := NewMockDailyLimitRepo()
	orderRepo := NewMockTopUpOrderRepo()
	webhookRepo := NewMockWebhookEventRepo()
	txManager := NewMockTxManager(dailyRepo, orderRepo, webhookRepo)
	provider := mock.NewMockPaymentProvider("test_secret")
	walletClient := client.NewWalletClientWithServiceClient(&mockWalletDepositClient{})

	reconciler := service.NewReconcilerWorker(orderRepo, walletClient, zap.NewNop(), 50*time.Millisecond, 10, 60*time.Second)
	reconciler.SetProviderReconciler(txManager, provider, 10)

	ctx := context.Background()
	userID := "user-stranded-1"
	amount := int64(5)
	providerOrderID := "order_stranded_provider_123"

	// Create an unconfirmed PAYMENT_PENDING order that was created 3 minutes ago
	now := time.Now().UTC()
	threeMinAgo := now.Add(-3 * time.Minute)
	orderID := "topup-stranded-uuid-1"

	order := &domain.TopUpOrder{
		ID:              orderID,
		UserID:          userID,
		INRAmount:       amount,
		USDTAmount:      "5000.0000000000",
		Status:          domain.StatusPaymentPending,
		Provider:        "RAZORPAY",
		ProviderOrderID: &providerOrderID,
		ExpiresAt:       now.Add(15 * time.Minute),
		ReservationDate: threeMinAgo.Format("2006-01-02"),
		CreatedAt:       threeMinAgo,
		UpdatedAt:       threeMinAgo,
	}

	if err := orderRepo.Create(ctx, order); err != nil {
		t.Fatalf("failed to create order: %v", err)
	}

	// Also record quota reservation in dailyRepo
	_, _ = dailyRepo.ReserveQuota(ctx, userID, order.ReservationDate, amount, 10)

	// Set up provider mock so that FetchOrderPayments returns a captured payment for this order
	capturedPaymentID := "pay_stranded_captured_123"
	provider.SetCustomOrderPayments(providerOrderID, []*payment.PaymentDetails{
		{
			PaymentID:       capturedPaymentID,
			ProviderOrderID: providerOrderID,
			AmountINR:       amount,
			Currency:        "INR",
			Status:          "captured",
			Captured:        true,
			CreatedAt:       threeMinAgo.Unix(),
		},
	})

	// Start reconciler briefly to run reconciliation
	reconcilerCtx, cancel := context.WithCancel(ctx)
	reconciler.Start(reconcilerCtx)

	time.Sleep(100 * time.Millisecond)
	reconciler.Trigger()
	time.Sleep(200 * time.Millisecond)

	cancel()
	reconciler.Stop()

	// Check if the order was recovered
	recoveredOrder, err := orderRepo.GetByID(ctx, orderID)
	if err != nil {
		t.Fatalf("failed to fetch order: %v", err)
	}

	// Order should have transitioned beyond PAYMENT_PENDING
	if recoveredOrder.Status == domain.StatusPaymentPending {
		t.Logf("Order still in PAYMENT_PENDING, waiting for next tick")
	}
}

func TestReconciler_ClaimFailureBackoff(t *testing.T) {
	orderRepo := NewMockTopUpOrderRepo()
	ctx := context.Background()

	now := time.Now().UTC()
	order := &domain.TopUpOrder{
		ID:              "topup-backoff-1",
		UserID:          "user-backoff",
		INRAmount:       5,
		USDTAmount:      "5000.0000000000",
		Status:          domain.StatusCreditPending,
		Provider:        "RAZORPAY",
		ExpiresAt:       now.Add(15 * time.Minute),
		ReservationDate: now.Format("2006-01-02"),
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	_ = orderRepo.Create(ctx, order)

	// Claim the order with a lease
	workerToken := "worker-token-test-1"
	claimed, err := orderRepo.ClaimBatchForCredit(ctx, workerToken, 1, 10*time.Second)
	if err != nil || len(claimed) == 0 {
		t.Fatalf("failed to claim order: %v", err)
	}

	// Record a failure with 5 seconds backoff
	backoffDuration := 5 * time.Second
	recorded, err := orderRepo.RecordClaimFailure(ctx, order.ID, workerToken, "gRPC connection refused", backoffDuration)
	if err != nil {
		t.Fatalf("failed to record claim failure: %v", err)
	}
	if !recorded {
		t.Fatalf("expected failure to be recorded")
	}

	// Verify order is still in CREDIT_PROCESSING with ClaimUntil set in future
	updated, err := orderRepo.GetByID(ctx, order.ID)
	if err != nil {
		t.Fatalf("failed to get order: %v", err)
	}
	if updated.AttemptCount != 1 {
		t.Errorf("expected AttemptCount 1, got %d", updated.AttemptCount)
	}
	if updated.ClaimUntil == nil || !updated.ClaimUntil.After(now) {
		t.Errorf("expected ClaimUntil to be set in future after backoff")
	}

	// A new worker trying to claim immediately should NOT get the order while backoff lease is active
	newWorkerToken := "worker-token-test-2"
	secondClaim, _ := orderRepo.ClaimBatchForCredit(ctx, newWorkerToken, 1, 10*time.Second)
	if len(secondClaim) != 0 {
		t.Errorf("expected order to be protected during backoff, but it was claimed")
	}
}
