package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"tradedrift/services/wallet-topup/internal/domain"
)

// DBTX is an interface satisfied by both *pgxpool.Pool and pgx.Tx.
type DBTX interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconnCommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type pgconnCommandTag interface {
	RowsAffected() int64
}

// DailyLimitRepository manages quota tracking and capacity checks.
type DailyLimitRepository interface {
	EnsureDailyLimit(ctx context.Context, userID, usageDate string, limitINR int64) error
	ReserveQuota(ctx context.Context, userID, usageDate string, amountINR, limitINR int64) (bool, error)
	ReleaseReservedQuota(ctx context.Context, userID, usageDate string, amountINR int64) error
	ConsumeReservedQuota(ctx context.Context, userID, usageDate string, amountINR int64) error
	AttemptDirectConsumption(ctx context.Context, userID, usageDate string, amountINR, limitINR int64) (bool, error)
	GetDailyUsage(ctx context.Context, userID, usageDate string, limitINR int64) (*domain.DailyTopUpLimit, error)
}

// TopUpOrderRepository manages order lifecycle persistence and tokenized lease claiming.
type TopUpOrderRepository interface {
	Create(ctx context.Context, order *domain.TopUpOrder) error
	GetByID(ctx context.Context, id string) (*domain.TopUpOrder, error)
	GetByProviderOrderID(ctx context.Context, provider, providerOrderID string) (*domain.TopUpOrder, error)
	GetByIdempotencyKey(ctx context.Context, userID, key string) (*domain.TopUpOrder, error)
	GetPendingExpired(ctx context.Context, now time.Time, limit int) ([]*domain.TopUpOrder, error)
	ExpireOrder(ctx context.Context, orderID string) error
	ClaimBatchForCredit(ctx context.Context, workerToken string, batchSize int, leaseDuration time.Duration) ([]*domain.TopUpOrder, error)
	CompleteOrder(ctx context.Context, orderID, workerToken string) (bool, error)
	RecordClaimFailure(ctx context.Context, orderID, workerToken, errMsg string) (bool, error)
	TransitionToCreditPending(ctx context.Context, orderID, provider, paymentID string) error
	TransitionToRefundRequired(ctx context.Context, orderID, provider, paymentID, reason string) error
}

// WebhookEventRepository manages immutable webhook audit logs and deduplication.
type WebhookEventRepository interface {
	RecordVerifiedEvent(ctx context.Context, event *domain.WebhookEvent) (alreadyProcessed bool, err error)
	RecordFailedSignatureEvent(ctx context.Context, event *domain.WebhookEvent) error
	RecordEvent(ctx context.Context, event *domain.WebhookEvent) error
	UpdateEventStatus(ctx context.Context, eventID, status string, errMsg *string) error
}

// PaymentConfirmationResult holds the result of atomic webhook processing.
type PaymentConfirmationResult struct {
	AlreadyProcessed bool
	Order            *domain.TopUpOrder
}

// TransactionManager coordinates multi-entity operations requiring atomic PostgreSQL transactions.
type TransactionManager interface {
	ProcessPaymentConfirmationTx(
		ctx context.Context,
		orderID string,
		provider string,
		paymentID string,
		paidDate string,
		rawPayload []byte,
		eventID string,
		dailyLimitINR int64,
	) (*PaymentConfirmationResult, error)

	ExpireOrderAndReleaseQuotaTx(
		ctx context.Context,
		orderID string,
		userID string,
		usageDate string,
		amountINR int64,
	) error

	InitiateTopUpTx(
		ctx context.Context,
		order *domain.TopUpOrder,
		dailyLimitINR int64,
	) (existing *domain.TopUpOrder, err error)

	CancelInitiatedOrderTx(
		ctx context.Context,
		orderID string,
		userID string,
		usageDate string,
		amountINR int64,
		errMsg string,
	) error

	ActivatePaymentPending(
		ctx context.Context,
		orderID string,
		providerOrderID string,
	) error
}
