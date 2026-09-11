package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/repository"
)

type PostgresTxManager struct {
	db *pgxpool.Pool
}

var _ repository.TransactionManager = (*PostgresTxManager)(nil)

func NewPostgresTxManager(db *pgxpool.Pool) *PostgresTxManager {
	return &PostgresTxManager{db: db}
}

// ProcessPaymentConfirmationTx executes the complete webhook confirmation inside a single atomic transaction.
func (m *PostgresTxManager) ProcessPaymentConfirmationTx(
	ctx context.Context,
	orderID string,
	provider string,
	paymentID string,
	paidDate string,
	rawPayload []byte,
	eventID string,
	dailyLimitINR int64,
) (*repository.PaymentConfirmationResult, error) {
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin webhook transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// ── 1. Check/Lock Webhook Event Deduplication ──────────────────────────────
	var existingWebhookID string
	var existingWebhookStatus string
	err = tx.QueryRow(ctx, `
		SELECT id, status FROM webhook_events
		WHERE provider = $1 AND event_id = $2
		FOR UPDATE;
	`, provider, eventID).Scan(&existingWebhookID, &existingWebhookStatus)

	webhookRowID := existingWebhookID
	if err == nil {
		if existingWebhookStatus == domain.WebhookStatusProcessed {
			// Already completely processed -> idempotent 200 OK
			return &repository.PaymentConfirmationResult{AlreadyProcessed: true}, nil
		}
	} else if errors.Is(err, pgx.ErrNoRows) {
		// New event -> insert with status = RECEIVED
		webhookRowID, err = platformuuid.New()
		if err != nil {
			return nil, fmt.Errorf("failed to generate webhook event ID: %w", err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO webhook_events (
				id, provider, event_id, payment_id, signature_valid, payload, status, received_at
			) VALUES ($1, $2, $3, $4, TRUE, $5, $6, NOW());
		`, webhookRowID, provider, eventID, paymentID, rawPayload, domain.WebhookStatusReceived)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return &repository.PaymentConfirmationResult{AlreadyProcessed: true}, nil
			}
			return nil, fmt.Errorf("failed to record webhook event in tx: %w", err)
		}
	} else {
		return nil, fmt.Errorf("failed to query webhook event: %w", err)
	}

	// ── 2. Lock Order Row Deterministically (FOR UPDATE) ──────────────────────
	var order domain.TopUpOrder
	var rDate time.Time
	err = tx.QueryRow(ctx, `
		SELECT id, user_id, idempotency_key, inr_amount, usdt_amount, reservation_date,
		       provider, provider_order_id, payment_id, status
		FROM topup_orders
		WHERE id = $1
		FOR UPDATE;
	`, orderID).Scan(
		&order.ID, &order.UserID, &order.IdempotencyKey, &order.INRAmount, &order.USDTAmount,
		&rDate, &order.Provider, &order.ProviderOrderID, &order.PaymentID, &order.Status,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrOrderNotFound
		}
		return nil, fmt.Errorf("failed to lock order row: %w", err)
	}
	order.ReservationDate = rDate.Format("2006-01-02")

	// Check existing order status
	if order.Status == domain.StatusCreditPending ||
		order.Status == domain.StatusCreditProcessing ||
		order.Status == domain.StatusCompleted {
		// Mark webhook PROCESSED and return success
		_, _ = tx.Exec(ctx, `
			UPDATE webhook_events SET status = $1, processed_at = NOW() WHERE id = $2;
		`, domain.WebhookStatusProcessed, webhookRowID)
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &repository.PaymentConfirmationResult{AlreadyProcessed: true, Order: &order}, nil
	}

	if order.Status == domain.StatusExpired {
		// Payment captured after order expired -> REFUND_REQUIRED
		_, err = tx.Exec(ctx, `
			UPDATE topup_orders
			SET status = $1, payment_id = $2, last_error = 'payment captured after order expiration', updated_at = NOW()
			WHERE id = $3;
		`, domain.StatusRefundRequired, paymentID, orderID)
		if err != nil {
			return nil, fmt.Errorf("failed to mark expired order as refund required: %w", err)
		}
		_, _ = tx.Exec(ctx, `
			UPDATE webhook_events SET status = $1, processed_at = NOW() WHERE id = $2;
		`, domain.WebhookStatusProcessed, webhookRowID)
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		order.Status = domain.StatusRefundRequired
		return &repository.PaymentConfirmationResult{AlreadyProcessed: false, Order: &order}, nil
	}

	if order.Status != domain.StatusPaymentPending {
		return nil, domain.ErrOrderTerminalStatus
	}

	// ── 3. Quota Accounting & Order State Transition ──────────────────────────
	if order.ReservationDate == paidDate {
		// Same-day: shift reserved -> consumed atomically
		tag, err := tx.Exec(ctx, `
			UPDATE daily_topup_limits
			SET reserved_inr = reserved_inr - $1,
			    consumed_inr = consumed_inr + $1,
			    updated_at   = NOW()
			WHERE user_id    = $2
			  AND usage_date = $3
			  AND reserved_inr >= $1;
		`, order.INRAmount, order.UserID, order.ReservationDate)
		if err != nil {
			return nil, fmt.Errorf("failed to consume reserved quota: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil, fmt.Errorf("invariant violation: cannot consume %d INR reserved quota", order.INRAmount)
		}

		// Transition order to CREDIT_PENDING
		_, err = tx.Exec(ctx, `
			UPDATE topup_orders
			SET status     = $1,
			    payment_id = $2,
			    updated_at = NOW()
			WHERE id = $3;
		`, domain.StatusCreditPending, paymentID, orderID)
		if err != nil {
			return nil, fmt.Errorf("failed to transition order to CREDIT_PENDING: %w", err)
		}
		order.Status = domain.StatusCreditPending
	} else {
		// Cross-midnight:
		// 1. Release Day 1 reserved
		tag, err := tx.Exec(ctx, `
			UPDATE daily_topup_limits
			SET reserved_inr = reserved_inr - $1,
			    updated_at   = NOW()
			WHERE user_id    = $2
			  AND usage_date = $3
			  AND reserved_inr >= $1;
		`, order.INRAmount, order.UserID, order.ReservationDate)
		if err != nil {
			return nil, fmt.Errorf("failed to release Day 1 quota: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return nil, fmt.Errorf("invariant violation: Day 1 reserved quota < %d", order.INRAmount)
		}

		// 2. Ensure Day 2 record exists
		_, err = tx.Exec(ctx, `
			INSERT INTO daily_topup_limits (user_id, usage_date, limit_inr, reserved_inr, consumed_inr)
			VALUES ($1, $2, $3, 0, 0)
			ON CONFLICT (user_id, usage_date) DO NOTHING;
		`, order.UserID, paidDate, dailyLimitINR)
		if err != nil {
			return nil, fmt.Errorf("failed to ensure Day 2 limit record: %w", err)
		}

		// 3. Attempt Day 2 consumption
		tag, err = tx.Exec(ctx, `
			UPDATE daily_topup_limits
			SET consumed_inr = consumed_inr + $1,
			    updated_at   = NOW()
			WHERE user_id    = $2
			  AND usage_date = $3
			  AND (reserved_inr + consumed_inr + $1) <= limit_inr;
		`, order.INRAmount, order.UserID, paidDate)
		if err != nil {
			return nil, fmt.Errorf("failed to attempt Day 2 consumption: %w", err)
		}

		if tag.RowsAffected() == 1 {
			// Day 2 quota available -> CREDIT_PENDING
			_, err = tx.Exec(ctx, `
				UPDATE topup_orders
				SET status     = $1,
				    payment_id = $2,
				    updated_at = NOW()
				WHERE id = $3;
			`, domain.StatusCreditPending, paymentID, orderID)
			if err != nil {
				return nil, fmt.Errorf("failed to update order to CREDIT_PENDING: %w", err)
			}
			order.Status = domain.StatusCreditPending
		} else {
			// Day 2 quota exhausted -> REFUND_REQUIRED (consumed is NOT incremented!)
			_, err = tx.Exec(ctx, `
				UPDATE topup_orders
				SET status     = $1,
				    payment_id = $2,
				    last_error = 'Day 2 daily limit exhausted',
				    updated_at = NOW()
				WHERE id = $3;
			`, domain.StatusRefundRequired, paymentID, orderID)
			if err != nil {
				return nil, fmt.Errorf("failed to update order to REFUND_REQUIRED: %w", err)
			}
			order.Status = domain.StatusRefundRequired
		}
	}

	// ── 4. Mark Webhook Event PROCESSED in Same Transaction ───────────────────
	_, err = tx.Exec(ctx, `
		UPDATE webhook_events
		SET status       = $1,
		    processed_at = NOW()
		WHERE id = $2;
	`, domain.WebhookStatusProcessed, webhookRowID)
	if err != nil {
		return nil, fmt.Errorf("failed to update webhook event status: %w", err)
	}

	// ── 5. Commit Transaction ─────────────────────────────────────────────────
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit webhook transaction: %w", err)
	}

	return &repository.PaymentConfirmationResult{
		AlreadyProcessed: false,
		Order:            &order,
	}, nil
}

// ExpireOrderAndReleaseQuotaTx atomically expires an order and releases its reserved quota.
func (m *PostgresTxManager) ExpireOrderAndReleaseQuotaTx(
	ctx context.Context,
	orderID string,
	userID string,
	usageDate string,
	amountINR int64,
) error {
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin expiry transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Mark order EXPIRED
	orderTag, err := tx.Exec(ctx, `
		UPDATE topup_orders
		SET status = $1, updated_at = NOW()
		WHERE id = $2 AND status IN ('PAYMENT_PENDING', 'INITIATED');
	`, domain.StatusExpired, orderID)
	if err != nil {
		return fmt.Errorf("failed to expire order: %w", err)
	}
	if orderTag.RowsAffected() == 0 {
		return domain.ErrOrderTerminalStatus
	}

	// 2. Release reserved quota
	quotaTag, err := tx.Exec(ctx, `
		UPDATE daily_topup_limits
		SET reserved_inr = reserved_inr - $1,
		    updated_at   = NOW()
		WHERE user_id    = $2
		  AND usage_date = $3
		  AND reserved_inr >= $1;
	`, amountINR, userID, usageDate)
	if err != nil {
		return fmt.Errorf("failed to release reserved quota in expiry tx: %w", err)
	}
	if quotaTag.RowsAffected() == 0 {
		return fmt.Errorf("invariant violation: cannot release %d INR reserved quota for user %s on %s", amountINR, userID, usageDate)
	}

	return tx.Commit(ctx)
}

// InitiateTopUpTx creates order in INITIATED status and reserves quota atomically.
func (m *PostgresTxManager) InitiateTopUpTx(
	ctx context.Context,
	order *domain.TopUpOrder,
	dailyLimitINR int64,
) (*domain.TopUpOrder, error) {
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin initiate topup tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Check idempotency key under transaction lock
	var existing domain.TopUpOrder
	err = tx.QueryRow(ctx, `
		SELECT id, inr_amount, status
		FROM topup_orders
		WHERE user_id = $1 AND idempotency_key = $2
		FOR UPDATE;
	`, order.UserID, order.IdempotencyKey).Scan(&existing.ID, &existing.INRAmount, &existing.Status)

	if err == nil {
		if existing.INRAmount == order.INRAmount {
			_ = tx.Rollback(ctx)
			return &existing, nil
		}
		return nil, domain.ErrIdempotencyConflict
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to check idempotency key: %w", err)
	}

	// 2. Ensure daily limit record exists
	_, err = tx.Exec(ctx, `
		INSERT INTO daily_topup_limits (user_id, usage_date, limit_inr, reserved_inr, consumed_inr)
		VALUES ($1, $2, $3, 0, 0)
		ON CONFLICT (user_id, usage_date) DO NOTHING;
	`, order.UserID, order.ReservationDate, dailyLimitINR)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure daily limit record: %w", err)
	}

	// 3. Atomically reserve quota
	quotaTag, err := tx.Exec(ctx, `
		UPDATE daily_topup_limits
		SET reserved_inr = reserved_inr + $1,
		    updated_at   = NOW()
		WHERE user_id    = $2
		  AND usage_date = $3
		  AND (reserved_inr + consumed_inr + $1) <= limit_inr;
	`, order.INRAmount, order.UserID, order.ReservationDate)
	if err != nil {
		return nil, fmt.Errorf("failed to reserve daily quota: %w", err)
	}
	if quotaTag.RowsAffected() == 0 {
		return nil, domain.ErrDailyLimitExceeded
	}

	// 4. Insert order in INITIATED status
	_, err = tx.Exec(ctx, `
		INSERT INTO topup_orders (
			id, user_id, idempotency_key, inr_amount, usdt_amount, reservation_date,
			provider, status, expires_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);
	`, order.ID, order.UserID, order.IdempotencyKey, order.INRAmount, order.USDTAmount,
		order.ReservationDate, order.Provider, domain.StatusInitiated,
		order.ExpiresAt, order.CreatedAt, order.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, domain.ErrIdempotencyConflict
		}
		return nil, fmt.Errorf("failed to insert initiated topup order: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit initiate topup tx: %w", err)
	}

	return nil, nil // proceed to provider
}

// ActivatePaymentPending moves an INITIATED order to PAYMENT_PENDING with provider order ID.
func (m *PostgresTxManager) ActivatePaymentPending(ctx context.Context, orderID, providerOrderID string) error {
	query := `
		UPDATE topup_orders
		SET status            = $1,
		    provider_order_id = $2,
		    updated_at        = NOW()
		WHERE id = $3 AND status = 'INITIATED';
	`
	tag, err := m.db.Exec(ctx, query, domain.StatusPaymentPending, providerOrderID, orderID)
	if err != nil {
		return fmt.Errorf("failed to activate payment pending: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrOrderTerminalStatus
	}
	return nil
}

// CancelInitiatedOrderTx cancels an INITIATED order and releases reserved quota atomically.
func (m *PostgresTxManager) CancelInitiatedOrderTx(
	ctx context.Context,
	orderID string,
	userID string,
	usageDate string,
	amountINR int64,
	errMsg string,
) error {
	tx, err := m.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin cancel order tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Mark order FAILED
	orderTag, err := tx.Exec(ctx, `
		UPDATE topup_orders
		SET status = $1, last_error = $2, updated_at = NOW()
		WHERE id = $3 AND status = 'INITIATED';
	`, domain.StatusFailed, errMsg, orderID)
	if err != nil {
		return fmt.Errorf("failed to mark initiated order failed: %w", err)
	}
	if orderTag.RowsAffected() == 0 {
		return fmt.Errorf("order %s was not in INITIATED state; aborting quota release", orderID)
	}

	// 2. Release reserved quota
	quotaTag, err := tx.Exec(ctx, `
		UPDATE daily_topup_limits
		SET reserved_inr = reserved_inr - $1,
		    updated_at   = NOW()
		WHERE user_id    = $2
		  AND usage_date = $3
		  AND reserved_inr >= $1;
	`, amountINR, userID, usageDate)
	if err != nil {
		return fmt.Errorf("failed to release quota on cancel: %w", err)
	}
	if quotaTag.RowsAffected() == 0 {
		return fmt.Errorf("invariant violation: reserved_inr < %d on cancel", amountINR)
	}

	return tx.Commit(ctx)
}
