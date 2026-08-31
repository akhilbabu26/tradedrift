package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"tradedrift/services/order/internal/repository"
)

type orderRepository struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewOrderRepository(db *pgxpool.Pool, logger *zap.Logger) repository.OrderRepository {
	return &orderRepository{
		db:     db,
		logger: logger,
	}
}

func (r *orderRepository) FindByIdempotencyKey(ctx context.Context, key string) (*repository.Order, error) {
	query := `
		SELECT id, user_id, market_id, side, order_type, price, quantity,
		       filled_quantity, remaining_quantity, status, idempotency_key,
		       created_at, updated_at
		FROM orders WHERE idempotency_key = $1`

	row := r.db.QueryRow(ctx, query, key)
	order, err := scanOrder(row)
	if err != nil {
		if errors.Is(err, repository.ErrOrderNotFound) {
			return nil, nil // Return (nil, nil) when key does not exist!
		}
		return nil, err
	}
	return order, nil
}

func (r *orderRepository) CreateOrder(ctx context.Context, o *repository.Order, outboxPayload []byte) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	insertOrderQuery := `
		INSERT INTO orders (id, user_id, market_id, side, order_type, price,
		    quantity, filled_quantity, remaining_quantity, status,
		    idempotency_key, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`

	_, err = tx.Exec(ctx, insertOrderQuery,
		o.ID, o.UserID, o.MarketID, string(o.Side), string(o.OrderType),
		o.Price, o.Quantity, o.FilledQuantity, o.RemainingQuantity,
		string(o.Status), o.IdempotencyKey, o.CreatedAt, o.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "orders_idempotency_key_key" {
			return repository.ErrDuplicateIdempotencyKey
		}
		return fmt.Errorf("failed to insert order: %w", err)
	}

	insertOutboxQuery := `
		INSERT INTO outbox (aggregate_id, event_type, payload, partition_key)
		VALUES ($1, 'OrderCreated', $2, $3)`

	_, err = tx.Exec(ctx, insertOutboxQuery, o.ID, outboxPayload, o.MarketID)
	if err != nil {
		return fmt.Errorf("failed to insert outbox event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit create order tx: %w", err)
	}
	return nil
}

func (r *orderRepository) GetByID(ctx context.Context, orderID, userID string) (*repository.Order, error) {
	query := `
		SELECT id, user_id, market_id, side, order_type, price, quantity,
		       filled_quantity, remaining_quantity, status, idempotency_key,
		       created_at, updated_at
		FROM orders WHERE id = $1 AND user_id = $2`

	row := r.db.QueryRow(ctx, query, orderID, userID)
	return scanOrder(row)
}

func (r *orderRepository) UpdateStatusToCancelling(ctx context.Context, o *repository.Order, outboxPayload []byte) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	updateQuery := `
		UPDATE orders SET status = 'CANCELLING', updated_at = NOW()
		WHERE id = $1 AND user_id = $2 AND status IN ('OPEN', 'PARTIALLY_FILLED')`

	res, err := tx.Exec(ctx, updateQuery, o.ID, o.UserID)
	if err != nil {
		return fmt.Errorf("failed to update order status: %w", err)
	}

	// Distinguish whether order does not exist vs already in a non-cancellable state (CANCELLING, CANCELLED, FILLED)
	if res.RowsAffected() == 0 {
		var exists bool
		checkQuery := `SELECT EXISTS(SELECT 1 FROM orders WHERE id = $1 AND user_id = $2)`
		if err := tx.QueryRow(ctx, checkQuery, o.ID, o.UserID).Scan(&exists); err == nil && exists {
			return repository.ErrOrderNotCancellable
		}
		return repository.ErrOrderNotFound
	}

	insertOutboxQuery := `
		INSERT INTO outbox (aggregate_id, event_type, payload, partition_key)
		VALUES ($1, 'OrderCancelRequested', $2, $3)`

	_, err = tx.Exec(ctx, insertOutboxQuery, o.ID, outboxPayload, o.MarketID)
	if err != nil {
		return fmt.Errorf("failed to insert cancel outbox event: %w", err)
	}

	return tx.Commit(ctx)
}

func (r *orderRepository) ListOrders(ctx context.Context, userID, marketID, cursorStr string, side repository.OrderSide, status repository.OrderStatus, fromTime, toTime *time.Time, limit int32) ([]*repository.Order, error) {
	query := `
		SELECT id, user_id, market_id, side, order_type, price, quantity,
		       filled_quantity, remaining_quantity, status, idempotency_key,
		       created_at, updated_at
		FROM orders
		WHERE user_id = $1`

	args := []any{userID}
	paramIdx := 2

	if marketID != "" {
		query += ` AND market_id = $` + strconv.Itoa(paramIdx)
		args = append(args, marketID)
		paramIdx++
	}
	if side != "" {
		query += ` AND side = $` + strconv.Itoa(paramIdx)
		args = append(args, string(side))
		paramIdx++
	}
	if status != "" {
		query += ` AND status = $` + strconv.Itoa(paramIdx)
		args = append(args, string(status))
		paramIdx++
	}
	if fromTime != nil {
		query += ` AND created_at >= $` + strconv.Itoa(paramIdx)
		args = append(args, *fromTime)
		paramIdx++
	}
	if toTime != nil {
		query += ` AND created_at <= $` + strconv.Itoa(paramIdx)
		args = append(args, *toTime)
		paramIdx++
	}

	// Keyset Pagination: decode (created_at, id) cursor. Return error if invalid!
	if cursorStr != "" {
		cursorTime, cursorID, err := decodeCursor(cursorStr)
		if err != nil {
			return nil, fmt.Errorf("invalid pagination cursor: %w", err)
		}
		query += ` AND (created_at, id) < ($` + strconv.Itoa(paramIdx) + `, $` + strconv.Itoa(paramIdx+1) + `)`
		args = append(args, cursorTime, cursorID)
		paramIdx += 2
	}

	if limit <= 0 {
		limit = 20
	} else if limit > 500 {
		limit = 500
	}

	query += ` ORDER BY created_at DESC, id DESC LIMIT $` + strconv.Itoa(paramIdx)
	args = append(args, limit)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query list orders: %w", err)
	}
	defer rows.Close()

	var orders []*repository.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}
	return orders, nil
}

func decodeCursor(cursorStr string) (time.Time, string, error) {
	if cursorStr == "" {
		return time.Time{}, "", nil
	}
	data, err := base64.URLEncoding.DecodeString(cursorStr)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: %v", repository.ErrInvalidPaginationCursor, err)
	}
	parts := strings.Split(string(data), "|")
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("%w: invalid format", repository.ErrInvalidPaginationCursor)
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: invalid timestamp", repository.ErrInvalidPaginationCursor)
	}
	return t, parts[1], nil
}

func scanOrder(row pgx.Row) (*repository.Order, error) {
	var o repository.Order
	var sideStr, typeStr, statusStr string

	err := row.Scan(
		&o.ID, &o.UserID, &o.MarketID, &sideStr, &typeStr,
		&o.Price, &o.Quantity, &o.FilledQuantity, &o.RemainingQuantity,
		&statusStr, &o.IdempotencyKey, &o.CreatedAt, &o.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, repository.ErrOrderNotFound
		}
		return nil, err
	}

	o.Side = repository.OrderSide(sideStr)
	o.OrderType = repository.OrderType(typeStr)
	o.Status = repository.OrderStatus(statusStr)
	return &o, nil
}
