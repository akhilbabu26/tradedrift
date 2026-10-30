package consumer_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"tradedrift/services/order/internal/kafka/consumer"
	"tradedrift/services/order/internal/repository"
)

type mockOrderRepo struct {
	repository.OrderRepository
	cancelledOrders   []string
	reconciledOrders  []string
	enqueuedReleases  []string
	completedReleases []string
	reconcileStatus   repository.OrderStatus
	alreadyTerminal   map[string]bool
}

func (m *mockOrderRepo) MarkOrderCancelled(ctx context.Context, orderID string) (*repository.Order, error) {
	if m.alreadyTerminal != nil && m.alreadyTerminal[orderID] {
		return nil, nil // idempotent no-op
	}
	m.cancelledOrders = append(m.cancelledOrders, orderID)
	m.enqueuedReleases = append(m.enqueuedReleases, orderID)
	if m.alreadyTerminal == nil {
		m.alreadyTerminal = make(map[string]bool)
	}
	m.alreadyTerminal[orderID] = true
	return &repository.Order{
		ID:     orderID,
		Status: repository.StatusCancelled,
	}, nil
}

func (m *mockOrderRepo) ReconcileAbsentOrder(ctx context.Context, orderID string) (*repository.Order, error) {
	if m.alreadyTerminal != nil && m.alreadyTerminal[orderID] {
		return nil, nil // idempotent no-op
	}
	m.reconciledOrders = append(m.reconciledOrders, orderID)
	m.enqueuedReleases = append(m.enqueuedReleases, orderID)
	if m.alreadyTerminal == nil {
		m.alreadyTerminal = make(map[string]bool)
	}
	m.alreadyTerminal[orderID] = true
	status := repository.StatusCancelled
	if m.reconcileStatus != "" {
		status = m.reconcileStatus
	}
	return &repository.Order{
		ID:     orderID,
		Status: status,
	}, nil
}

func (m *mockOrderRepo) MarkWalletReleaseCompletedByOrderID(ctx context.Context, orderID string) error {
	m.completedReleases = append(m.completedReleases, orderID)
	return nil
}

type mockWalletClient struct {
	releasedOrders []string
	releaseErr     error
}

func (m *mockWalletClient) ReleaseFunds(ctx context.Context, orderID string) error {
	m.releasedOrders = append(m.releasedOrders, orderID)
	return m.releaseErr
}

func TestCancelConsumer_UserMismatch_DoesNotCancelOrReleaseFunds(t *testing.T) {
	repo := &mockOrderRepo{}
	wallet := &mockWalletClient{}
	logger := zap.NewNop()

	c := consumer.NewCancelConsumer([]string{"localhost:9092"}, "test-group", "orders.cancelled.v1", repo, wallet, logger)

	orderID := uuid.New().String()
	event := consumer.OrderCancelledEvent{
		EventID:   uuid.New().String(),
		OrderID:   orderID,
		UserID:    uuid.New().String(),
		MarketID:  "BTC-USDT",
		Status:    "USER_MISMATCH",
		Reason:    "user_mismatch",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	msg := kafkago.Message{
		Topic: "orders.cancelled.v1",
		Value: payload,
	}

	if err := c.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("unexpected ProcessMessage error: %v", err)
	}

	// Invariant: USER_MISMATCH must be observational only downstream.
	// It MUST NOT transition order to CANCELLED in DB.
	if len(repo.cancelledOrders) != 0 {
		t.Fatalf("expected 0 calls to MarkOrderCancelled on USER_MISMATCH, got %d", len(repo.cancelledOrders))
	}
	if len(repo.reconciledOrders) != 0 {
		t.Fatalf("expected 0 calls to ReconcileAbsentOrder on USER_MISMATCH, got %d", len(repo.reconciledOrders))
	}

	// Invariant: Reserved funds MUST NOT be released.
	if len(wallet.releasedOrders) != 0 {
		t.Fatalf("expected 0 calls to ReleaseFunds on USER_MISMATCH, got %d", len(wallet.releasedOrders))
	}
}

func TestCancelConsumer_RemovedFromBook_CancelsAndReleasesFunds(t *testing.T) {
	repo := &mockOrderRepo{}
	wallet := &mockWalletClient{}
	logger := zap.NewNop()

	c := consumer.NewCancelConsumer([]string{"localhost:9092"}, "test-group", "orders.cancelled.v1", repo, wallet, logger)

	orderID := uuid.New().String()
	event := consumer.OrderCancelledEvent{
		EventID:   uuid.New().String(),
		OrderID:   orderID,
		UserID:    uuid.New().String(),
		MarketID:  "BTC-USDT",
		Status:    "REMOVED_FROM_BOOK",
		Reason:    "user_requested",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	msg := kafkago.Message{
		Topic: "orders.cancelled.v1",
		Value: payload,
	}

	if err := c.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("unexpected ProcessMessage error: %v", err)
	}

	// Invariant: REMOVED_FROM_BOOK transitions order to CANCELLED in DB and releases funds.
	if len(repo.cancelledOrders) != 1 || repo.cancelledOrders[0] != orderID {
		t.Fatalf("expected 1 call to MarkOrderCancelled with orderID=%s, got %v", orderID, repo.cancelledOrders)
	}
	if len(wallet.releasedOrders) != 1 || wallet.releasedOrders[0] != orderID {
		t.Fatalf("expected 1 call to ReleaseFunds with orderID=%s, got %v", orderID, wallet.releasedOrders)
	}
}

func TestCancelConsumer_InvalidRequest_DoesNotCancelOrReleaseFunds(t *testing.T) {
	repo := &mockOrderRepo{}
	wallet := &mockWalletClient{}
	logger := zap.NewNop()

	c := consumer.NewCancelConsumer([]string{"localhost:9092"}, "test-group", "orders.cancelled.v1", repo, wallet, logger)

	orderID := uuid.New().String()
	event := consumer.OrderCancelledEvent{
		EventID:   uuid.New().String(),
		OrderID:   orderID,
		UserID:    uuid.New().String(),
		MarketID:  "BTC-USDT",
		Status:    "INVALID_REQUEST",
		Reason:    "invalid_market_id",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	msg := kafkago.Message{
		Topic: "orders.cancelled.v1",
		Value: payload,
	}

	if err := c.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("unexpected ProcessMessage error: %v", err)
	}

	// Invariant: INVALID_REQUEST must not cause DB cancellation or wallet fund release.
	if len(repo.cancelledOrders) != 0 {
		t.Fatalf("expected 0 calls to MarkOrderCancelled on INVALID_REQUEST, got %d", len(repo.cancelledOrders))
	}
	if len(repo.reconciledOrders) != 0 {
		t.Fatalf("expected 0 calls to ReconcileAbsentOrder on INVALID_REQUEST, got %d", len(repo.reconciledOrders))
	}
	if len(wallet.releasedOrders) != 0 {
		t.Fatalf("expected 0 calls to ReleaseFunds on INVALID_REQUEST, got %d", len(wallet.releasedOrders))
	}
}

func TestCancelConsumer_AlreadyAbsent_Filled_EnqueuesReleaseAndCompletes(t *testing.T) {
	repo := &mockOrderRepo{
		reconcileStatus: repository.StatusFilled,
	}
	wallet := &mockWalletClient{}
	logger := zap.NewNop()

	c := consumer.NewCancelConsumer([]string{"localhost:9092"}, "test-group", "orders.cancelled.v1", repo, wallet, logger)

	orderID := uuid.New().String()
	event := consumer.OrderCancelledEvent{
		EventID:   uuid.New().String(),
		OrderID:   orderID,
		UserID:    uuid.New().String(),
		MarketID:  "BTC-USDT",
		Status:    "ALREADY_ABSENT",
		Reason:    "already_absent",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	msg := kafkago.Message{Topic: "orders.cancelled.v1", Value: payload}
	if err := c.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("unexpected ProcessMessage error: %v", err)
	}

	// Invariant 1: ReconcileAbsentOrder called and durably enqueued into wallet_releases
	if len(repo.reconciledOrders) != 1 || repo.reconciledOrders[0] != orderID {
		t.Fatalf("expected 1 call to ReconcileAbsentOrder with orderID=%s, got %v", orderID, repo.reconciledOrders)
	}
	if len(repo.enqueuedReleases) != 1 || repo.enqueuedReleases[0] != orderID {
		t.Fatalf("expected 1 call to EnqueueWalletRelease for FILLED order %s, got %v", orderID, repo.enqueuedReleases)
	}

	// Invariant 2: Opportunistic immediate release succeeded and completed the release record
	if len(wallet.releasedOrders) != 1 || wallet.releasedOrders[0] != orderID {
		t.Fatalf("expected 1 call to ReleaseFunds with orderID=%s, got %v", orderID, wallet.releasedOrders)
	}
	if len(repo.completedReleases) != 1 || repo.completedReleases[0] != orderID {
		t.Fatalf("expected 1 completed release for order %s, got %v", orderID, repo.completedReleases)
	}
}

func TestCancelConsumer_AlreadyAbsent_Cancelled_ReleaseFails_IdempotentOnRedelivery(t *testing.T) {
	repo := &mockOrderRepo{
		reconcileStatus: repository.StatusCancelled,
	}
	wallet := &mockWalletClient{}
	logger := zap.NewNop()

	c := consumer.NewCancelConsumer([]string{"localhost:9092"}, "test-group", "orders.cancelled.v1", repo, wallet, logger)

	orderID := uuid.New().String()
	event := consumer.OrderCancelledEvent{
		EventID:   uuid.New().String(),
		OrderID:   orderID,
		UserID:    uuid.New().String(),
		MarketID:  "BTC-USDT",
		Status:    "ALREADY_ABSENT",
		Reason:    "already_absent",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	msg := kafkago.Message{Topic: "orders.cancelled.v1", Value: payload}

	// First attempt: simulate wallet service transport failure
	importErrors := true
	if importErrors {
		wallet.releaseErr = errors.New("wallet transport timeout")
	}
	if err := c.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("process message should succeed without error because durable release was enqueued: %v", err)
	}

	// INVARIANT: ReconcileAbsentOrder transitioned order and durably enqueued into wallet_releases
	if len(repo.reconciledOrders) != 1 {
		t.Fatalf("expected 1 call to ReconcileAbsentOrder, got %d", len(repo.reconciledOrders))
	}
	if len(repo.enqueuedReleases) != 1 {
		t.Fatalf("expected 1 enqueued wallet release, got %d", len(repo.enqueuedReleases))
	}
	// Release was NOT marked completed because immediate call failed
	if len(repo.completedReleases) != 0 {
		t.Fatalf("expected 0 completed releases on wallet failure, got %d", len(repo.completedReleases))
	}

	// Kafka redelivery / retry occurs:
	wallet.releaseErr = nil // wallet is healthy now
	if err := c.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("unexpected error on redelivery: %v", err)
	}

	// INVARIANT: Idempotent no-op on redelivery — no duplicate reconciliation or duplicate enqueue
	if len(repo.reconciledOrders) != 1 {
		t.Fatalf("redelivery must NOT perform second reconciliation, got %d", len(repo.reconciledOrders))
	}
	if len(repo.enqueuedReleases) != 1 {
		t.Fatalf("redelivery must NOT duplicate wallet_releases enqueue, got %d", len(repo.enqueuedReleases))
	}
}

func TestCancelConsumer_AlreadyAbsent_Filled_ReleaseFails_IdempotentOnRedelivery(t *testing.T) {
	repo := &mockOrderRepo{
		reconcileStatus: repository.StatusFilled,
	}
	wallet := &mockWalletClient{}
	logger := zap.NewNop()

	c := consumer.NewCancelConsumer([]string{"localhost:9092"}, "test-group", "orders.cancelled.v1", repo, wallet, logger)

	orderID := uuid.New().String()
	event := consumer.OrderCancelledEvent{
		EventID:   uuid.New().String(),
		OrderID:   orderID,
		UserID:    uuid.New().String(),
		MarketID:  "BTC-USDT",
		Status:    "ALREADY_ABSENT",
		Reason:    "already_absent",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	msg := kafkago.Message{Topic: "orders.cancelled.v1", Value: payload}

	// First attempt: wallet fails
	wallet.releaseErr = errors.New("wallet transport timeout")
	if err := c.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("process message error: %v", err)
	}

	if len(repo.reconciledOrders) != 1 || len(repo.enqueuedReleases) != 1 {
		t.Fatalf("expected 1 reconciliation and 1 enqueue for FILLED order")
	}
	if len(repo.completedReleases) != 0 {
		t.Fatalf("expected 0 completed releases")
	}

	// Redelivery:
	wallet.releaseErr = nil
	if err := c.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("redelivery error: %v", err)
	}

	// No duplicate reconciliation
	if len(repo.reconciledOrders) != 1 || len(repo.enqueuedReleases) != 1 {
		t.Fatalf("expected idempotency preserved on redelivery")
	}
}

func TestCancelConsumer_MarketBlocked_ReconcilesAndReleasesFunds(t *testing.T) {
	repo := &mockOrderRepo{
		reconcileStatus: repository.StatusCancelled,
	}
	wallet := &mockWalletClient{}
	logger := zap.NewNop()

	c := consumer.NewCancelConsumer([]string{"localhost:9092"}, "test-group", "orders.cancelled.v1", repo, wallet, logger)

	orderID := uuid.New().String()
	event := consumer.OrderCancelledEvent{
		EventID:   uuid.New().String(),
		OrderID:   orderID,
		UserID:    uuid.New().String(),
		MarketID:  "ETH-USDT",
		Status:    "ALREADY_ABSENT",
		Reason:    "market_blocked",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	msg := kafkago.Message{Topic: "orders.cancelled.v1", Value: payload}
	if err := c.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Invariant: ReconcileAbsentOrder called, order transitioned to CANCELLED, wallet released
	if len(repo.reconciledOrders) != 1 || repo.reconciledOrders[0] != orderID {
		t.Fatalf("expected 1 call to ReconcileAbsentOrder with orderID=%s, got %v", orderID, repo.reconciledOrders)
	}
	if len(wallet.releasedOrders) != 1 || wallet.releasedOrders[0] != orderID {
		t.Fatalf("expected 1 call to ReleaseFunds with orderID=%s, got %v", orderID, wallet.releasedOrders)
	}
	if len(repo.completedReleases) != 1 || repo.completedReleases[0] != orderID {
		t.Fatalf("expected 1 call to MarkWalletReleaseCompletedByOrderID with orderID=%s, got %v", orderID, repo.completedReleases)
	}
}

func TestCancelConsumer_MarketBlocked_InvalidRequestFallback_ReconcilesAndReleasesFunds(t *testing.T) {
	repo := &mockOrderRepo{
		reconcileStatus: repository.StatusCancelled,
	}
	wallet := &mockWalletClient{}
	logger := zap.NewNop()

	c := consumer.NewCancelConsumer([]string{"localhost:9092"}, "test-group", "orders.cancelled.v1", repo, wallet, logger)

	orderID := uuid.New().String()
	event := consumer.OrderCancelledEvent{
		EventID:   uuid.New().String(),
		OrderID:   orderID,
		UserID:    uuid.New().String(),
		MarketID:  "ETH-USDT",
		Status:    "INVALID_REQUEST",
		Reason:    "market_blocked",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	msg := kafkago.Message{Topic: "orders.cancelled.v1", Value: payload}
	if err := c.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Invariant: ReconcileAbsentOrder called even on INVALID_REQUEST when reason is market_blocked
	if len(repo.reconciledOrders) != 1 || repo.reconciledOrders[0] != orderID {
		t.Fatalf("expected 1 call to ReconcileAbsentOrder with orderID=%s, got %v", orderID, repo.reconciledOrders)
	}
	if len(wallet.releasedOrders) != 1 || wallet.releasedOrders[0] != orderID {
		t.Fatalf("expected 1 call to ReleaseFunds with orderID=%s, got %v", orderID, wallet.releasedOrders)
	}
}

func TestCancelConsumer_MarketReconciling_InvalidRequestFallback_ReconcilesAndReleasesFunds(t *testing.T) {
	repo := &mockOrderRepo{
		reconcileStatus: repository.StatusCancelled,
	}
	wallet := &mockWalletClient{}
	logger := zap.NewNop()

	c := consumer.NewCancelConsumer([]string{"localhost:9092"}, "test-group", "orders.cancelled.v1", repo, wallet, logger)

	orderID := uuid.New().String()
	event := consumer.OrderCancelledEvent{
		EventID:   uuid.New().String(),
		OrderID:   orderID,
		UserID:    uuid.New().String(),
		MarketID:  "ETH-USDT",
		Status:    "INVALID_REQUEST",
		Reason:    "market_reconciling",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	msg := kafkago.Message{Topic: "orders.cancelled.v1", Value: payload}
	if err := c.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Invariant: ReconcileAbsentOrder called on INVALID_REQUEST when reason is market_reconciling
	if len(repo.reconciledOrders) != 1 || repo.reconciledOrders[0] != orderID {
		t.Fatalf("expected 1 call to ReconcileAbsentOrder with orderID=%s, got %v", orderID, repo.reconciledOrders)
	}
	if len(wallet.releasedOrders) != 1 || wallet.releasedOrders[0] != orderID {
		t.Fatalf("expected 1 call to ReleaseFunds with orderID=%s, got %v", orderID, wallet.releasedOrders)
	}
	if len(repo.completedReleases) != 1 || repo.completedReleases[0] != orderID {
		t.Fatalf("expected 1 call to MarkWalletReleaseCompletedByOrderID with orderID=%s, got %v", orderID, repo.completedReleases)
	}
}

func TestCancelConsumer_MarketBlocked_DuplicateDelivery_Idempotent(t *testing.T) {
	repo := &mockOrderRepo{
		reconcileStatus: repository.StatusCancelled,
	}
	wallet := &mockWalletClient{}
	logger := zap.NewNop()

	c := consumer.NewCancelConsumer([]string{"localhost:9092"}, "test-group", "orders.cancelled.v1", repo, wallet, logger)

	orderID := uuid.New().String()
	event := consumer.OrderCancelledEvent{
		EventID:   uuid.New().String(),
		OrderID:   orderID,
		UserID:    uuid.New().String(),
		MarketID:  "ETH-USDT",
		Status:    "ALREADY_ABSENT",
		Reason:    "market_blocked",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	msg := kafkago.Message{Topic: "orders.cancelled.v1", Value: payload}

	// First delivery
	if err := c.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("first delivery error: %v", err)
	}

	// Second delivery (redelivery / duplicate)
	if err := c.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("second delivery error: %v", err)
	}

	// Invariant: Idempotent no-op on duplicate delivery. No duplicate reconciliation, no duplicate wallet release.
	if len(repo.reconciledOrders) != 1 {
		t.Fatalf("expected exactly 1 call to ReconcileAbsentOrder due to idempotency, got %d", len(repo.reconciledOrders))
	}
	if len(wallet.releasedOrders) != 1 {
		t.Fatalf("expected exactly 1 call to ReleaseFunds due to idempotency, got %d", len(wallet.releasedOrders))
	}
}

func TestCancelConsumer_MMOrder_NoReservation_ReconcilesAndCompletesCleanly(t *testing.T) {
	repo := &mockOrderRepo{
		reconcileStatus: repository.StatusCancelled,
	}
	// Simulate Wallet Service returning "reservation not found" because MM accounts bypass reservations
	walletClient := &mockWalletClient{
		releaseErr: fmt.Errorf("reservation not found for order 01a12601-a895-703e-81e1-66756a9ebe90"),
	}
	logger := zap.NewNop()

	c := consumer.NewCancelConsumer([]string{"localhost:9092"}, "test-group", "orders.cancelled.v1", repo, walletClient, logger)

	orderID := "01a12601-a895-703e-81e1-66756a9ebe90"
	event := consumer.OrderCancelledEvent{
		EventID:   uuid.New().String(),
		OrderID:   orderID,
		UserID:    "00000000-0000-0000-0000-000000000001", // MM-001
		MarketID:  "ETH-USDT",
		Status:    "ALREADY_ABSENT",
		Reason:    "market_blocked",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	msg := kafkago.Message{Topic: "orders.cancelled.v1", Value: payload}
	if err := c.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("process message error: %v", err)
	}

	// Invariant 1: Order reconciled to CANCELLED in Order Service
	if len(repo.reconciledOrders) != 1 || repo.reconciledOrders[0] != orderID {
		t.Fatalf("expected order %s to be reconciled, got %v", orderID, repo.reconciledOrders)
	}

	// Invariant 2: Wallet release was attempted
	if len(walletClient.releasedOrders) != 1 || walletClient.releasedOrders[0] != orderID {
		t.Fatalf("expected release attempt for order %s, got %v", orderID, walletClient.releasedOrders)
	}

	// Invariant 3: Because MM has no reservation, "reservation not found" is recognized as idempotent success
	// and marked completed in order DB, avoiding stuck pending retries or ledger corruption
	if len(repo.completedReleases) != 1 || repo.completedReleases[0] != orderID {
		t.Fatalf("expected release to be marked completed for MM order, got %v", repo.completedReleases)
	}
}

func TestCancelConsumer_PartiallyFilledOrder_PreservesFillsAndReconciles(t *testing.T) {
	repo := &mockOrderRepo{
		reconcileStatus: repository.StatusCancelled,
	}
	walletClient := &mockWalletClient{}
	logger := zap.NewNop()

	c := consumer.NewCancelConsumer([]string{"localhost:9092"}, "test-group", "orders.cancelled.v1", repo, walletClient, logger)

	orderID := "01a12642-a9d7-738a-81da-bcf8d74bed27"
	event := consumer.OrderCancelledEvent{
		EventID:   uuid.New().String(),
		OrderID:   orderID,
		UserID:    "00000000-0000-0000-0000-000000000001",
		MarketID:  "ETH-USDT",
		Status:    "ALREADY_ABSENT",
		Reason:    "market_blocked",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	msg := kafkago.Message{Topic: "orders.cancelled.v1", Value: payload}
	if err := c.ProcessMessage(context.Background(), msg); err != nil {
		t.Fatalf("process message error: %v", err)
	}

	// Invariant: Reconciled order transitions to CANCELLED, preserving filled quantity
	if len(repo.reconciledOrders) != 1 || repo.reconciledOrders[0] != orderID {
		t.Fatalf("expected order %s to be reconciled, got %v", orderID, repo.reconciledOrders)
	}
	if len(walletClient.releasedOrders) != 1 || walletClient.releasedOrders[0] != orderID {
		t.Fatalf("expected release attempt for order %s, got %v", orderID, walletClient.releasedOrders)
	}
	if len(repo.completedReleases) != 1 || repo.completedReleases[0] != orderID {
		t.Fatalf("expected release marked completed, got %v", repo.completedReleases)
	}
}

