package test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/repository"
)

// MockDailyLimitRepo provides thread-safe in-memory quota tracking with exact SQL invariant semantics.
type MockDailyLimitRepo struct {
	mu     sync.Mutex
	limits map[string]*domain.DailyTopUpLimit // key: userID:usageDate
}

var _ repository.DailyLimitRepository = (*MockDailyLimitRepo)(nil)

func NewMockDailyLimitRepo() *MockDailyLimitRepo {
	return &MockDailyLimitRepo{
		limits: make(map[string]*domain.DailyTopUpLimit),
	}
}

func (m *MockDailyLimitRepo) key(userID, usageDate string) string {
	return fmt.Sprintf("%s:%s", userID, usageDate)
}

func (m *MockDailyLimitRepo) EnsureDailyLimit(ctx context.Context, userID, usageDate string, limitINR int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.key(userID, usageDate)
	if _, ok := m.limits[k]; !ok {
		m.limits[k] = &domain.DailyTopUpLimit{
			UserID:    userID,
			UsageDate: usageDate,
			LimitINR:  limitINR,
		}
	}
	return nil
}

func (m *MockDailyLimitRepo) ReserveQuota(ctx context.Context, userID, usageDate string, amountINR, limitINR int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.key(userID, usageDate)
	limit, ok := m.limits[k]
	if !ok {
		limit = &domain.DailyTopUpLimit{
			UserID:    userID,
			UsageDate: usageDate,
			LimitINR:  limitINR,
		}
		m.limits[k] = limit
	}

	if limit.ReservedINR+limit.ConsumedINR+amountINR > limit.LimitINR {
		return false, nil
	}

	limit.ReservedINR += amountINR
	return true, nil
}

func (m *MockDailyLimitRepo) ReleaseReservedQuota(ctx context.Context, userID, usageDate string, amountINR int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.key(userID, usageDate)
	limit, ok := m.limits[k]
	if !ok || limit.ReservedINR < amountINR {
		return fmt.Errorf("invariant violation: reserved_inr (%d) < amount (%d)", limit.ReservedINR, amountINR)
	}
	limit.ReservedINR -= amountINR
	return nil
}

func (m *MockDailyLimitRepo) ConsumeReservedQuota(ctx context.Context, userID, usageDate string, amountINR int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.key(userID, usageDate)
	limit, ok := m.limits[k]
	if !ok || limit.ReservedINR < amountINR {
		return fmt.Errorf("invariant violation: reserved_inr (%d) < amount (%d)", limit.ReservedINR, amountINR)
	}
	limit.ReservedINR -= amountINR
	limit.ConsumedINR += amountINR
	return nil
}

func (m *MockDailyLimitRepo) AttemptDirectConsumption(ctx context.Context, userID, usageDate string, amountINR, limitINR int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.key(userID, usageDate)
	limit, ok := m.limits[k]
	if !ok {
		limit = &domain.DailyTopUpLimit{
			UserID:    userID,
			UsageDate: usageDate,
			LimitINR:  limitINR,
		}
		m.limits[k] = limit
	}

	if limit.ReservedINR+limit.ConsumedINR+amountINR > limit.LimitINR {
		return false, nil
	}

	limit.ConsumedINR += amountINR
	return true, nil
}

func (m *MockDailyLimitRepo) GetDailyUsage(ctx context.Context, userID, usageDate string, limitINR int64) (*domain.DailyTopUpLimit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.key(userID, usageDate)
	limit, ok := m.limits[k]
	if !ok {
		return &domain.DailyTopUpLimit{
			UserID:    userID,
			UsageDate: usageDate,
			LimitINR:  limitINR,
		}, nil
	}
	copy := *limit
	return &copy, nil
}

// MockTopUpOrderRepo provides in-memory order persistence with tokenized lease fencing and idempotency.
type MockTopUpOrderRepo struct {
	mu          sync.Mutex
	orders      map[string]*domain.TopUpOrder // by ID
	idempotency map[string]string             // key: userID:idempotencyKey -> orderID
}

var _ repository.TopUpOrderRepository = (*MockTopUpOrderRepo)(nil)

func NewMockTopUpOrderRepo() *MockTopUpOrderRepo {
	return &MockTopUpOrderRepo{
		orders:      make(map[string]*domain.TopUpOrder),
		idempotency: make(map[string]string),
	}
}

func (m *MockTopUpOrderRepo) Create(ctx context.Context, order *domain.TopUpOrder) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ik := fmt.Sprintf("%s:%s", order.UserID, order.IdempotencyKey)
	if _, ok := m.idempotency[ik]; ok {
		return domain.ErrIdempotencyConflict
	}

	copy := *order
	m.orders[order.ID] = &copy
	m.idempotency[ik] = order.ID
	return nil
}

func (m *MockTopUpOrderRepo) GetByID(ctx context.Context, id string) (*domain.TopUpOrder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	order, ok := m.orders[id]
	if !ok {
		return nil, domain.ErrOrderNotFound
	}
	copy := *order
	return &copy, nil
}

func (m *MockTopUpOrderRepo) GetByIdempotencyKey(ctx context.Context, userID, key string) (*domain.TopUpOrder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ik := fmt.Sprintf("%s:%s", userID, key)
	orderID, ok := m.idempotency[ik]
	if !ok {
		return nil, nil
	}
	order, ok := m.orders[orderID]
	if !ok {
		return nil, nil
	}
	copy := *order
	return &copy, nil
}

func (m *MockTopUpOrderRepo) GetByProviderOrderID(ctx context.Context, provider, providerOrderID string) (*domain.TopUpOrder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, o := range m.orders {
		if o.Provider == provider && o.ProviderOrderID != nil && *o.ProviderOrderID == providerOrderID {
			copy := *o
			return &copy, nil
		}
	}
	return nil, domain.ErrOrderNotFound
}

func (m *MockTopUpOrderRepo) GetPendingExpired(ctx context.Context, now time.Time, limit int) ([]*domain.TopUpOrder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result []*domain.TopUpOrder
	for _, o := range m.orders {
		if (o.Status == domain.StatusPaymentPending || o.Status == domain.StatusInitiated) && o.ExpiresAt.Before(now) {
			copy := *o
			result = append(result, &copy)
			if len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MockTopUpOrderRepo) ExpireOrder(ctx context.Context, orderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[orderID]
	if !ok || o.Status != domain.StatusPaymentPending {
		return domain.ErrOrderTerminalStatus
	}
	o.Status = domain.StatusExpired
	return nil
}

func (m *MockTopUpOrderRepo) ClaimBatchForCredit(ctx context.Context, workerToken string, batchSize int, leaseDuration time.Duration) ([]*domain.TopUpOrder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	var claimed []*domain.TopUpOrder

	for _, o := range m.orders {
		isEligible := o.Status == domain.StatusCreditPending ||
			(o.Status == domain.StatusCreditProcessing && o.ClaimUntil != nil && o.ClaimUntil.Before(now))

		if isEligible {
			o.Status = domain.StatusCreditProcessing
			o.ClaimToken = &workerToken
			claimedAt := now
			o.ClaimedAt = &claimedAt
			claimUntil := now.Add(leaseDuration)
			o.ClaimUntil = &claimUntil
			o.AttemptCount++

			copy := *o
			claimed = append(claimed, &copy)
			if len(claimed) >= batchSize {
				break
			}
		}
	}
	return claimed, nil
}

func (m *MockTopUpOrderRepo) CompleteOrder(ctx context.Context, orderID, workerToken string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[orderID]
	if !ok {
		return false, nil
	}
	if o.Status == domain.StatusCreditProcessing && o.ClaimToken != nil && *o.ClaimToken == workerToken {
		o.Status = domain.StatusCompleted
		now := time.Now().UTC()
		o.CompletedAt = &now
		return true, nil
	}
	return false, nil
}

func (m *MockTopUpOrderRepo) RecordClaimFailure(ctx context.Context, orderID, workerToken, errMsg string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[orderID]
	if !ok {
		return false, nil
	}
	if o.Status == domain.StatusCreditProcessing && o.ClaimToken != nil && *o.ClaimToken == workerToken {
		o.LastError = &errMsg
		now := time.Now().UTC()
		o.ClaimUntil = &now
		return true, nil
	}
	return false, nil
}

func (m *MockTopUpOrderRepo) TransitionToCreditPending(ctx context.Context, orderID, provider, paymentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[orderID]
	if !ok || o.Status != domain.StatusPaymentPending {
		return domain.ErrOrderTerminalStatus
	}
	o.Status = domain.StatusCreditPending
	o.PaymentID = &paymentID
	return nil
}

func (m *MockTopUpOrderRepo) TransitionToRefundRequired(ctx context.Context, orderID, provider, paymentID, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[orderID]
	if !ok || o.Status != domain.StatusPaymentPending {
		return domain.ErrOrderTerminalStatus
	}
	o.Status = domain.StatusRefundRequired
	o.PaymentID = &paymentID
	o.LastError = &reason
	return nil
}

// MockWebhookEventRepo provides in-memory verified deduplication.
type MockWebhookEventRepo struct {
	mu     sync.Mutex
	events map[string]*domain.WebhookEvent // by ID
	dedup  map[string]bool                 // key: provider:eventID (verified only)
}

var _ repository.WebhookEventRepository = (*MockWebhookEventRepo)(nil)

func NewMockWebhookEventRepo() *MockWebhookEventRepo {
	return &MockWebhookEventRepo{
		events: make(map[string]*domain.WebhookEvent),
		dedup:  make(map[string]bool),
	}
}

func (m *MockWebhookEventRepo) RecordVerifiedEvent(ctx context.Context, event *domain.WebhookEvent) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := fmt.Sprintf("%s:%s", event.Provider, event.EventID)
	if m.dedup[k] {
		return true, nil
	}
	m.dedup[k] = true
	copy := *event
	m.events[event.ID] = &copy
	return false, nil
}

func (m *MockWebhookEventRepo) RecordFailedSignatureEvent(ctx context.Context, event *domain.WebhookEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	copy := *event
	m.events[event.ID] = &copy
	return nil
}

func (m *MockWebhookEventRepo) RecordEvent(ctx context.Context, event *domain.WebhookEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	copy := *event
	m.events[event.ID] = &copy
	if event.EventID != "" && event.SignatureValid {
		k := fmt.Sprintf("%s:%s", event.Provider, event.EventID)
		m.dedup[k] = true
	}
	return nil
}

func (m *MockWebhookEventRepo) GetAllEvents() []*domain.WebhookEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []*domain.WebhookEvent
	for _, e := range m.events {
		copy := *e
		result = append(result, &copy)
	}
	return result
}

func (m *MockWebhookEventRepo) UpdateEventStatus(ctx context.Context, eventID, status string, errMsg *string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.events[eventID]
	if !ok {
		return domain.ErrOrderNotFound
	}
	e.Status = status
	e.ErrorMessage = errMsg
	now := time.Now().UTC()
	e.ProcessedAt = &now
	return nil
}

// MockTxManager coordinates multi-entity operations in memory with strict transaction rollback semantics.
type MockTxManager struct {
	dailyRepo   *MockDailyLimitRepo
	orderRepo   *MockTopUpOrderRepo
	webhookRepo *MockWebhookEventRepo
	mu          sync.Mutex

	// Injected failure toggles for regression testing
	SimulateConfirmError bool
	SimulateExpiryError  bool
}

var _ repository.TransactionManager = (*MockTxManager)(nil)

func NewMockTxManager(
	dailyRepo *MockDailyLimitRepo,
	orderRepo *MockTopUpOrderRepo,
	webhookRepo *MockWebhookEventRepo,
) *MockTxManager {
	return &MockTxManager{
		dailyRepo:   dailyRepo,
		orderRepo:   orderRepo,
		webhookRepo: webhookRepo,
	}
}

func (m *MockTxManager) ProcessPaymentConfirmationTx(
	ctx context.Context,
	orderID string,
	provider string,
	paymentID string,
	paidDate string,
	rawPayload []byte,
	eventID string,
	dailyLimitINR int64,
) (*repository.PaymentConfirmationResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.SimulateConfirmError {
		return nil, errors.New("simulated database transaction failure during confirmation")
	}

	// 1. Check if already processed
	dedupKey := fmt.Sprintf("%s:%s", provider, eventID)
	if m.webhookRepo.dedup[dedupKey] {
		return &repository.PaymentConfirmationResult{AlreadyProcessed: true}, nil
	}

	order, ok := m.orderRepo.orders[orderID]
	if !ok {
		return nil, domain.ErrOrderNotFound
	}

	if order.Status == domain.StatusCreditPending ||
		order.Status == domain.StatusCreditProcessing ||
		order.Status == domain.StatusCompleted {
		m.webhookRepo.dedup[dedupKey] = true
		copy := *order
		return &repository.PaymentConfirmationResult{AlreadyProcessed: true, Order: &copy}, nil
	}

	if order.Status == domain.StatusExpired {
		order.Status = domain.StatusRefundRequired
		order.PaymentID = &paymentID
		reason := "payment captured after order expiration"
		order.LastError = &reason
		m.webhookRepo.dedup[dedupKey] = true
		copy := *order
		return &repository.PaymentConfirmationResult{AlreadyProcessed: false, Order: &copy}, nil
	}

	if order.Status != domain.StatusPaymentPending {
		return nil, domain.ErrOrderTerminalStatus
	}

	// 2. Quota accounting
	if order.ReservationDate == paidDate {
		// Same-day: shift reserved -> consumed
		if err := m.dailyRepo.ConsumeReservedQuota(ctx, order.UserID, order.ReservationDate, order.INRAmount); err != nil {
			return nil, err
		}
		order.Status = domain.StatusCreditPending
		order.PaymentID = &paymentID
	} else {
		// Cross-midnight: release Day 1, attempt Day 2
		_ = m.dailyRepo.ReleaseReservedQuota(ctx, order.UserID, order.ReservationDate, order.INRAmount)
		day2Avail, err := m.dailyRepo.AttemptDirectConsumption(ctx, order.UserID, paidDate, order.INRAmount, dailyLimitINR)
		if err != nil {
			return nil, err
		}
		if day2Avail {
			order.Status = domain.StatusCreditPending
			order.PaymentID = &paymentID
		} else {
			order.Status = domain.StatusRefundRequired
			order.PaymentID = &paymentID
			reason := "Day 2 daily limit exhausted"
			order.LastError = &reason
		}
	}

	// Commit deduplication in same atomic transaction
	m.webhookRepo.dedup[dedupKey] = true

	copy := *order
	return &repository.PaymentConfirmationResult{
		AlreadyProcessed: false,
		Order:            &copy,
	}, nil
}

func (m *MockTxManager) ExpireOrderAndReleaseQuotaTx(
	ctx context.Context,
	orderID string,
	userID string,
	usageDate string,
	amountINR int64,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.SimulateExpiryError {
		return errors.New("simulated database failure during expiry")
	}

	order, ok := m.orderRepo.orders[orderID]
	if !ok || (order.Status != domain.StatusPaymentPending && order.Status != domain.StatusInitiated) {
		return domain.ErrOrderTerminalStatus
	}

	// In single transaction, decrement quota first
	if err := m.dailyRepo.ReleaseReservedQuota(ctx, userID, usageDate, amountINR); err != nil {
		return err
	}

	order.Status = domain.StatusExpired
	return nil
}

func (m *MockTxManager) InitiateTopUpTx(
	ctx context.Context,
	order *domain.TopUpOrder,
	dailyLimitINR int64,
) (*domain.TopUpOrder, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ik := fmt.Sprintf("%s:%s", order.UserID, order.IdempotencyKey)
	if existingID, exists := m.orderRepo.idempotency[ik]; exists {
		existing := m.orderRepo.orders[existingID]
		if existing.INRAmount == order.INRAmount {
			copy := *existing
			return &copy, nil
		}
		return nil, domain.ErrIdempotencyConflict
	}

	reserved, err := m.dailyRepo.ReserveQuota(ctx, order.UserID, order.ReservationDate, order.INRAmount, dailyLimitINR)
	if err != nil {
		return nil, err
	}
	if !reserved {
		return nil, domain.ErrDailyLimitExceeded
	}

	copy := *order
	copy.Status = domain.StatusInitiated
	m.orderRepo.orders[order.ID] = &copy
	m.orderRepo.idempotency[ik] = order.ID

	return nil, nil // proceed to provider
}

func (m *MockTxManager) ActivatePaymentPending(ctx context.Context, orderID, providerOrderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	order, ok := m.orderRepo.orders[orderID]
	if !ok || order.Status != domain.StatusInitiated {
		return domain.ErrOrderTerminalStatus
	}

	order.Status = domain.StatusPaymentPending
	order.ProviderOrderID = &providerOrderID
	return nil
}

func (m *MockTxManager) CancelInitiatedOrderTx(
	ctx context.Context,
	orderID string,
	userID string,
	usageDate string,
	amountINR int64,
	errMsg string,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	order, ok := m.orderRepo.orders[orderID]
	if !ok {
		return domain.ErrOrderNotFound
	}
	if order.Status != domain.StatusInitiated {
		return fmt.Errorf("order %s was not in INITIATED state; aborting quota release", orderID)
	}

	_ = m.dailyRepo.ReleaseReservedQuota(ctx, userID, usageDate, amountINR)
	order.Status = domain.StatusFailed
	order.LastError = &errMsg
	return nil
}
