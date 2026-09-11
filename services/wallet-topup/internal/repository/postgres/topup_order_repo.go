package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/wallet-topup/internal/domain"
	"tradedrift/services/wallet-topup/internal/repository"
)

type TopUpOrderRepo struct {
	db *pgxpool.Pool
}

var _ repository.TopUpOrderRepository = (*TopUpOrderRepo)(nil)

func NewTopUpOrderRepo(db *pgxpool.Pool) *TopUpOrderRepo {

	return &TopUpOrderRepo{db: db}
}

// Create inserts a new top-up order into the database.
func (r *TopUpOrderRepo) Create(ctx context.Context, order *domain.TopUpOrder) error {
	query := `
		INSERT INTO topup_orders (
			id, user_id, idempotency_key, inr_amount, usdt_amount, reservation_date,
			provider, provider_order_id, payment_id, status, attempt_count,
			expires_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13, $14
		);
	`
	_, err := r.db.Exec(ctx, query,
		order.ID, order.UserID, order.IdempotencyKey, order.INRAmount, order.USDTAmount, order.ReservationDate,
		order.Provider, order.ProviderOrderID, order.PaymentID, order.Status, order.AttemptCount,
		order.ExpiresAt, order.CreatedAt, order.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrIdempotencyConflict
		}
		return fmt.Errorf("failed to create topup order: %w", err)
	}
	return nil
}

// GetByID retrieves a top-up order by ID.
func (r *TopUpOrderRepo) GetByID(ctx context.Context, id string) (*domain.TopUpOrder, error) {
	query := `
		SELECT id, user_id, idempotency_key, inr_amount, usdt_amount, reservation_date,
		       provider, provider_order_id, payment_id, status, claim_token,
		       claim_until, claimed_at, attempt_count, last_error, expires_at,
		       completed_at, created_at, updated_at
		FROM topup_orders
		WHERE id = $1;
	`
	row := r.db.QueryRow(ctx, query, id)
	return scanOrder(row)
}

// GetByProviderOrderID retrieves a top-up order by provider and provider-assigned order ID.
func (r *TopUpOrderRepo) GetByProviderOrderID(ctx context.Context, provider, providerOrderID string) (*domain.TopUpOrder, error) {
	query := `
		SELECT id, user_id, idempotency_key, inr_amount, usdt_amount, reservation_date,
		       provider, provider_order_id, payment_id, status, claim_token,
		       claim_until, claimed_at, attempt_count, last_error, expires_at,
		       completed_at, created_at, updated_at
		FROM topup_orders
		WHERE provider = $1 AND provider_order_id = $2;
	`
	row := r.db.QueryRow(ctx, query, provider, providerOrderID)
	return scanOrder(row)
}

// GetByIdempotencyKey retrieves an existing order for the given user and idempotency key.
func (r *TopUpOrderRepo) GetByIdempotencyKey(ctx context.Context, userID, key string) (*domain.TopUpOrder, error) {
	query := `
		SELECT id, user_id, idempotency_key, inr_amount, usdt_amount, reservation_date,
		       provider, provider_order_id, payment_id, status, claim_token,
		       claim_until, claimed_at, attempt_count, last_error, expires_at,
		       completed_at, created_at, updated_at
		FROM topup_orders
		WHERE user_id = $1 AND idempotency_key = $2;
	`
	row := r.db.QueryRow(ctx, query, userID, key)
	order, err := scanOrder(row)
	if err != nil {
		if errors.Is(err, domain.ErrOrderNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return order, nil
}

// GetPendingExpired returns orders that are in PAYMENT_PENDING or INITIATED and past their expires_at timestamp.
func (r *TopUpOrderRepo) GetPendingExpired(ctx context.Context, now time.Time, limit int) ([]*domain.TopUpOrder, error) {
	query := `
		SELECT id, user_id, idempotency_key, inr_amount, usdt_amount, reservation_date,
		       provider, provider_order_id, payment_id, status, claim_token,
		       claim_until, claimed_at, attempt_count, last_error, expires_at,
		       completed_at, created_at, updated_at
		FROM topup_orders
		WHERE status IN ('PAYMENT_PENDING', 'INITIATED') AND expires_at < $1
		ORDER BY expires_at ASC
		LIMIT $2;
	`
	rows, err := r.db.Query(ctx, query, now, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending expired orders: %w", err)
	}
	defer rows.Close()

	var orders []*domain.TopUpOrder
	for rows.Next() {
		order, err := scanOrderFromRows(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, nil
}

// ExpireOrder marks a PAYMENT_PENDING order as EXPIRED.
func (r *TopUpOrderRepo) ExpireOrder(ctx context.Context, orderID string) error {
	query := `
		UPDATE topup_orders
		SET status = 'EXPIRED',
		    updated_at = NOW()
		WHERE id = $1 AND status = 'PAYMENT_PENDING';
	`
	tag, err := r.db.Exec(ctx, query, orderID)
	if err != nil {
		return fmt.Errorf("failed to mark order as expired: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrOrderTerminalStatus
	}
	return nil
}

// ClaimBatchForCredit acquires a lease on eligible CREDIT_PENDING or expired CREDIT_PROCESSING orders.
// Executed in a short transaction with FOR UPDATE SKIP LOCKED.
func (r *TopUpOrderRepo) ClaimBatchForCredit(ctx context.Context, workerToken string, batchSize int, leaseDuration time.Duration) ([]*domain.TopUpOrder, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin claim transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	selectQuery := `
		SELECT id
		FROM topup_orders
		WHERE status = 'CREDIT_PENDING'
		   OR (status = 'CREDIT_PROCESSING' AND claim_until < NOW())
		ORDER BY created_at ASC
		LIMIT $1
		FOR UPDATE SKIP LOCKED;
	`
	rows, err := tx.Query(ctx, selectQuery, batchSize)
	if err != nil {
		return nil, fmt.Errorf("failed to select orders for claiming: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()

	if len(ids) == 0 {
		return nil, nil
	}

	updateQuery := `
		UPDATE topup_orders
		SET status        = 'CREDIT_PROCESSING',
		    claim_token   = $1,
		    claimed_at    = NOW(),
		    claim_until   = NOW() + ($2 * INTERVAL '1 millisecond'),
		    attempt_count = attempt_count + 1,
		    updated_at    = NOW()
		WHERE id = ANY($3);
	`
	_, err = tx.Exec(ctx, updateQuery, workerToken, leaseDuration.Milliseconds(), ids)
	if err != nil {
		return nil, fmt.Errorf("failed to update claim fields: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit claim transaction: %w", err)
	}

	// Fetch full order details
	fetchQuery := `
		SELECT id, user_id, idempotency_key, inr_amount, usdt_amount, reservation_date,
		       provider, provider_order_id, payment_id, status, claim_token,
		       claim_until, claimed_at, attempt_count, last_error, expires_at,
		       completed_at, created_at, updated_at
		FROM topup_orders
		WHERE id = ANY($1);
	`
	fetchRows, err := r.db.Query(ctx, fetchQuery, ids)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch claimed orders: %w", err)
	}
	defer fetchRows.Close()

	var claimedOrders []*domain.TopUpOrder
	for fetchRows.Next() {
		order, err := scanOrderFromRows(fetchRows)
		if err != nil {
			return nil, err
		}
		claimedOrders = append(claimedOrders, order)
	}

	return claimedOrders, nil
}

// CompleteOrder completes the order with lease protection.
func (r *TopUpOrderRepo) CompleteOrder(ctx context.Context, orderID, workerToken string) (bool, error) {
	query := `
		UPDATE topup_orders
		SET status       = 'COMPLETED',
		    completed_at = NOW(),
		    updated_at   = NOW()
		WHERE id          = $1
		  AND status      = 'CREDIT_PROCESSING'
		  AND claim_token = $2;
	`
	tag, err := r.db.Exec(ctx, query, orderID, workerToken)
	if err != nil {
		return false, fmt.Errorf("failed to mark order completed: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// RecordClaimFailure updates the failure info and resets claim_until with lease token fencing.
func (r *TopUpOrderRepo) RecordClaimFailure(ctx context.Context, orderID, workerToken, errMsg string) (bool, error) {
	query := `
		UPDATE topup_orders
		SET last_error   = $1,
		    claim_until  = NOW(),
		    updated_at   = NOW()
		WHERE id          = $2
		  AND status      = 'CREDIT_PROCESSING'
		  AND claim_token = $3;
	`
	tag, err := r.db.Exec(ctx, query, errMsg, orderID, workerToken)
	if err != nil {
		return false, fmt.Errorf("failed to record claim failure: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// TransitionToCreditPending moves order from PAYMENT_PENDING to CREDIT_PENDING upon successful payment.
func (r *TopUpOrderRepo) TransitionToCreditPending(ctx context.Context, orderID, provider, paymentID string) error {
	query := `
		UPDATE topup_orders
		SET status     = 'CREDIT_PENDING',
		    payment_id = $1,
		    updated_at = NOW()
		WHERE id       = $2
		  AND status   = 'PAYMENT_PENDING'
		  AND provider = $3;
	`
	tag, err := r.db.Exec(ctx, query, paymentID, orderID, provider)
	if err != nil {
		return fmt.Errorf("failed to transition order to CREDIT_PENDING: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrOrderTerminalStatus
	}
	return nil
}

// TransitionToRefundRequired moves order to REFUND_REQUIRED (e.g. cross-midnight Day 2 quota exhaustion).
func (r *TopUpOrderRepo) TransitionToRefundRequired(ctx context.Context, orderID, provider, paymentID, reason string) error {
	query := `
		UPDATE topup_orders
		SET status     = 'REFUND_REQUIRED',
		    payment_id = $1,
		    last_error = $2,
		    updated_at = NOW()
		WHERE id       = $3
		  AND status   = 'PAYMENT_PENDING'
		  AND provider = $4;
	`
	tag, err := r.db.Exec(ctx, query, paymentID, reason, orderID, provider)
	if err != nil {
		return fmt.Errorf("failed to transition order to REFUND_REQUIRED: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrOrderTerminalStatus
	}
	return nil
}

func scanOrder(row pgx.Row) (*domain.TopUpOrder, error) {
	var o domain.TopUpOrder
	var rDate time.Time
	err := row.Scan(
		&o.ID, &o.UserID, &o.IdempotencyKey, &o.INRAmount, &o.USDTAmount, &rDate,
		&o.Provider, &o.ProviderOrderID, &o.PaymentID, &o.Status, &o.ClaimToken,
		&o.ClaimUntil, &o.ClaimedAt, &o.AttemptCount, &o.LastError, &o.ExpiresAt,
		&o.CompletedAt, &o.CreatedAt, &o.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrOrderNotFound
		}
		return nil, err
	}
	o.ReservationDate = rDate.Format("2006-01-02")
	return &o, nil
}

func scanOrderFromRows(rows pgx.Rows) (*domain.TopUpOrder, error) {
	var o domain.TopUpOrder
	var rDate time.Time
	err := rows.Scan(
		&o.ID, &o.UserID, &o.IdempotencyKey, &o.INRAmount, &o.USDTAmount, &rDate,
		&o.Provider, &o.ProviderOrderID, &o.PaymentID, &o.Status, &o.ClaimToken,
		&o.ClaimUntil, &o.ClaimedAt, &o.AttemptCount, &o.LastError, &o.ExpiresAt,
		&o.CompletedAt, &o.CreatedAt, &o.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	o.ReservationDate = rDate.Format("2006-01-02")
	return &o, nil
}
