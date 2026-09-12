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

// ProcessPaymentConfirmationTx executes the complete webhook payment confirmation
// inside a single unified atomic PostgreSQL transaction.
//
// WHY THIS EXISTS / PROBLEM SOLVED:
// Payment providers (like Razorpay) send webhooks over the public internet. Webhooks can be:
//  1. Retried multiple times (at-least-once delivery, network timeouts).
//  2. Delayed across midnight (order created at 23:59 Day 1, paid at 00:02 Day 2).
//  3. Delayed past order expiration (user completed payment on UPI after our 15m timer expired).
//
// This method handles ALL these edge cases atomically:
//  - Prevents double crediting via row locks (SELECT ... FOR UPDATE).
//  - Transitions daily limits from 'reserved_inr' to 'consumed_inr'.
//  - Handles cross-midnight daily limit accounting without exceeding caps.
//  - Flags expired orders that received late payments as 'REFUND_REQUIRED'.
//  - Marks the webhook event as 'PROCESSED' all within the exact same COMMIT.
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

	// ── STEP 1: Webhook Event Deduplication (Idempotency) ──────────────────────
	// Webhook providers frequently retry the same webhook if an ACK is delayed.
	// We query the webhook_events table for this specific (provider, event_id).
	// Locking with FOR UPDATE ensures concurrent duplicate webhooks queue sequentially.
	var existingWebhookID string
	var existingWebhookStatus string
	err = tx.QueryRow(ctx, `
		SELECT id, status FROM webhook_events
		WHERE provider = $1 AND event_id = $2
		FOR UPDATE;
	`, provider, eventID).Scan(&existingWebhookID, &existingWebhookStatus)

	webhookRowID := existingWebhookID
	if err == nil {
		// If this exact webhook was already completely processed in the past,
		// we exit immediately and return AlreadyProcessed=true.
		// The caller will respond with HTTP 200 OK without re-running financial mutations.
		if existingWebhookStatus == domain.WebhookStatusProcessed {
			return &repository.PaymentConfirmationResult{AlreadyProcessed: true}, nil
		}
	} else if errors.Is(err, pgx.ErrNoRows) {
		// This is a brand new webhook event: record it in state 'RECEIVED'.
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
			// PostgreSQL Error 23505 = unique_violation.
			// Protects against simultaneous race conditions where two identical webhooks hit at the exact same millisecond.
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return &repository.PaymentConfirmationResult{AlreadyProcessed: true}, nil
			}
			return nil, fmt.Errorf("failed to record webhook event in tx: %w", err)
		}
	} else {
		return nil, fmt.Errorf("failed to query webhook event: %w", err)
	}

	// ── STEP 2: Lock Order Row Deterministically (Pessimistic Row Locking) ────
	// We lock the specific topup_order row using SELECT ... FOR UPDATE.
	// This serializes all operations on this order, preventing concurrent status transitions.
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

	// ── STEP 2A: Check if Order is Already Completed or Being Credited ─────────
	// If the order has already moved forward to CREDIT_PENDING, CREDIT_PROCESSING, or COMPLETED,
	// money has already been accounted for. Mark the webhook as PROCESSED and return success.
	if order.Status == domain.StatusCreditPending ||
		order.Status == domain.StatusCreditProcessing ||
		order.Status == domain.StatusCompleted {
		_, _ = tx.Exec(ctx, `
			UPDATE webhook_events SET status = $1, processed_at = NOW() WHERE id = $2;
		`, domain.WebhookStatusProcessed, webhookRowID)
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &repository.PaymentConfirmationResult{AlreadyProcessed: true, Order: &order}, nil
	}

	// ── STEP 2B: Late-Arriving Webhook on Expired Order (Refund Defense) ───────
	// RACE CONDITION SCENARIO:
	// 1. User created order with 15-minute expiry.
	// 2. 15 minutes passed -> ExpiryWorker marked order EXPIRED and released the reserved ₹10 quota.
	// 3. User immediately created a new order with that released quota.
	// 4. LATER, the bank completes the old payment and sends a delayed "payment.captured" webhook!
	//
	// CRITICAL ACTION:
	// - We CANNOT fulfill/credit this order because the user's daily quota was already released!
	// - We CANNOT ignore it because the bank took the user's real money!
	// - SOLUTION: Mark the order as 'REFUND_REQUIRED' with the payment ID.
	//   This protects our quota invariant while queuing the user's funds for a bank refund.
	if order.Status == domain.StatusExpired {
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

	// If the order is in any state other than PAYMENT_PENDING (e.g. FAILED, REFUND_REQUIRED),
	// it cannot receive payment confirmation.
	if order.Status != domain.StatusPaymentPending {
		return nil, domain.ErrOrderTerminalStatus
	}

	// ── STEP 3: Daily Limit Quota Accounting & Order Status Transition ─────────
	if order.ReservationDate == paidDate {
		// CASE 1: Same-Day Payment (Standard Flow)
		// The order was created and paid on the exact same calendar day (IST).
		// We atomically convert the reserved quota to consumed quota:
		//   reserved_inr = reserved_inr - amount
		//   consumed_inr = consumed_inr + amount
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

		// Move order status from PAYMENT_PENDING -> CREDIT_PENDING
		// This signals the background ReconcilerWorker to credit USDT to the user's wallet.
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
		// CASE 2: Cross-Midnight Payment
		// Example: Order created at 23:55 on Monday (Day 1), but user paid at 00:05 on Tuesday (Day 2).
		// Indian financial rules attribute daily limits to the actual payment capture date.
		//
		// Step 1: Release Day 1's reserved quota (unblocking yesterday's allocation).
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

		// Step 2: Ensure Day 2's daily quota row exists in the database.
		_, err = tx.Exec(ctx, `
			INSERT INTO daily_topup_limits (user_id, usage_date, limit_inr, reserved_inr, consumed_inr)
			VALUES ($1, $2, $3, 0, 0)
			ON CONFLICT (user_id, usage_date) DO NOTHING;
		`, order.UserID, paidDate, dailyLimitINR)
		if err != nil {
			return nil, fmt.Errorf("failed to ensure Day 2 limit record: %w", err)
		}

		// Step 3: Attempt to charge Day 2's consumed quota.
		// Conditional check: (reserved + consumed + amount) <= limit_inr
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
			// Day 2 has sufficient quota available -> Order can be fulfilled!
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
			// Day 2 quota is already exhausted! (e.g., user already topped up ₹10 on Day 2 morning).
			// Fulfilling this order would breach the ₹10 daily regulatory limit.
			// Transition order to REFUND_REQUIRED so money is returned safely.
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

	// ── STEP 4: Mark Webhook Event PROCESSED ───────────────────────────────────
	// Everything succeeded. Mark this webhook event as PROCESSED so future retries are skipped.
	_, err = tx.Exec(ctx, `
		UPDATE webhook_events
		SET status       = $1,
		    processed_at = NOW()
		WHERE id = $2;
	`, domain.WebhookStatusProcessed, webhookRowID)
	if err != nil {
		return nil, fmt.Errorf("failed to update webhook event status: %w", err)
	}

	// ── STEP 5: Commit Everything Atomically ───────────────────────────────────
	// Either all mutations commit together, or none do (zero partial state changes).
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit webhook transaction: %w", err)
	}

	return &repository.PaymentConfirmationResult{
		AlreadyProcessed: false,
		Order:            &order,
	}, nil
}

// ExpireOrderAndReleaseQuotaTx atomically cancels an unpaid expired order and
// returns the held quota back to the user in a single transaction.
//
// WHY THIS EXISTS:
// When a user initiates a top-up, we pre-reserve quota (e.g. ₹2) to prevent daily limit bypass.
// If the user abandons payment or closes their browser, their ₹2 quota is trapped in 'reserved_inr'.
// The ExpiryWorker runs every 30s, finds orders where expires_at < NOW(), and calls this function
// to mark the order EXPIRED and decrement 'reserved_inr' so the user can use their quota again.
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

	// 1. Mark order as EXPIRED (only if it is still in PAYMENT_PENDING or INITIATED state).
	// If a payment webhook just confirmed the order a millisecond ago, status will not match,
	// RowsAffected will be 0, and this will safely abort without rolling back real payment.
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

	// 2. Release reserved quota (+amount back to user's available daily limit)
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

// InitiateTopUpTx pre-reserves daily quota and inserts the top-up order in 'INITIATED' status.
//
// WHY THIS EXISTS:
// When a user clicks "Top Up", we MUST reserve their quota BEFORE calling the external payment gateway.
// If we called Razorpay first and 5 concurrent requests hit simultaneously, a user could bypass
// the ₹10 limit by opening 5 orders at once (TOCTOU race condition).
// By inserting 'INITIATED' and incrementing 'reserved_inr' in an atomic DB transaction,
// the daily limit is strictly enforced before external network I/O begins.
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

	// 1. Check idempotency key under transaction row lock (FOR UPDATE).
	// Prevents duplicate charges if the user double-clicks or their client retries.
	var existing domain.TopUpOrder
	err = tx.QueryRow(ctx, `
		SELECT id, inr_amount, status
		FROM topup_orders
		WHERE user_id = $1 AND idempotency_key = $2
		FOR UPDATE;
	`, order.UserID, order.IdempotencyKey).Scan(&existing.ID, &existing.INRAmount, &existing.Status)

	if err == nil {
		// If the request was retried with the exact same amount, return the existing order.
		if existing.INRAmount == order.INRAmount {
			_ = tx.Rollback(ctx)
			return &existing, nil
		}
		// If the same key was sent with a different amount, reject it as a conflict!
		return nil, domain.ErrIdempotencyConflict
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to check idempotency key: %w", err)
	}

	// 2. Ensure today's daily limit record exists for the user.
	_, err = tx.Exec(ctx, `
		INSERT INTO daily_topup_limits (user_id, usage_date, limit_inr, reserved_inr, consumed_inr)
		VALUES ($1, $2, $3, 0, 0)
		ON CONFLICT (user_id, usage_date) DO NOTHING;
	`, order.UserID, order.ReservationDate, dailyLimitINR)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure daily limit record: %w", err)
	}

	// 3. Atomically reserve quota.
	// The WHERE clause ensures (reserved_inr + consumed_inr + amount) <= limit_inr.
	// If the user has already used their ₹10 limit, 0 rows are affected and we return ErrDailyLimitExceeded.
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

	// 4. Insert the new order in 'INITIATED' status.
	// Database unique constraint uq_topup_user_idempotency guarantees exactly-once creation.
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

	return nil, nil // Proceed to call the payment provider
}

// ActivatePaymentPending transitions an order from 'INITIATED' to 'PAYMENT_PENDING'
// once the payment gateway has successfully created the external checkout order.
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

// CancelInitiatedOrderTx cancels an 'INITIATED' order if calling the payment provider fails.
//
// WHY THIS EXISTS:
// If Razorpay or the mock gateway experiences a network timeout while creating an order,
// we cannot leave the order in 'INITIATED' (which would trap the user's reserved quota).
// This function marks the order 'FAILED' and releases the reserved quota back to the user
// so they can immediately try again.
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

	// 1. Mark order FAILED (only if it is still in INITIATED state).
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

	// 2. Release reserved quota back to user
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
