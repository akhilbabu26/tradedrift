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
	"github.com/shopspring/decimal"
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
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && (pgErr.ConstraintName == "orders_idempotency_key_key" || strings.Contains(pgErr.ConstraintName, "idempotency_key") || strings.Contains(pgErr.Detail, "idempotency_key")) {
			return repository.ErrDuplicateIdempotencyKey
		}
		return fmt.Errorf("failed to insert order: %w", err)
	}

	if len(outboxPayload) > 0 {
		insertOutboxQuery := `
			INSERT INTO outbox (aggregate_id, event_type, payload, partition_key)
			VALUES ($1, 'OrderCreated', $2, $3)`

		_, err = tx.Exec(ctx, insertOutboxQuery, o.ID, outboxPayload, o.MarketID)
		if err != nil {
			return fmt.Errorf("failed to insert outbox event: %w", err)
		}
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

func (r *orderRepository) ApplyTradeFill(ctx context.Context, tradeID, buyOrderID, sellOrderID, fillQty string) ([]string, error) {
	fillQtyDec, err := decimal.NewFromString(fillQty)
	if err != nil || !fillQtyDec.GreaterThan(decimal.Zero) {
		return nil, fmt.Errorf("fill quantity must be positive decimal: %s", fillQty)
	}

	if buyOrderID != "" && sellOrderID != "" && buyOrderID == sellOrderID {
		return nil, fmt.Errorf("self-match rejected: buy_order_id equals sell_order_id (%s)", buyOrderID)
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx for trade fill: %w", err)
	}
	defer tx.Rollback(ctx)

	// Idempotency: check if already processed
	var alreadyProcessed bool
	err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM processed_trades WHERE trade_id = $1)", tradeID).Scan(&alreadyProcessed)
	if err != nil {
		return nil, fmt.Errorf("check processed trade %s: %w", tradeID, err)
	}
	if alreadyProcessed {
		return nil, nil // Already processed this trade — idempotent no-op
	}

	// Deterministic row locking order (lexicographically by UUID) to prevent deadlock
	var orderIDsToLock []string
	if buyOrderID != "" && sellOrderID != "" {
		if buyOrderID < sellOrderID {
			orderIDsToLock = []string{buyOrderID, sellOrderID}
		} else {
			orderIDsToLock = []string{sellOrderID, buyOrderID}
		}
	} else if buyOrderID != "" {
		orderIDsToLock = []string{buyOrderID}
	} else if sellOrderID != "" {
		orderIDsToLock = []string{sellOrderID}
	}

	type lockedOrder struct {
		id                string
		marketID          string
		side              string
		remainingQuantity decimal.Decimal
		status            string
	}
	ordersByID := make(map[string]*lockedOrder, len(orderIDsToLock))

	for _, id := range orderIDsToLock {
		var o lockedOrder
		var remStr string
		err := tx.QueryRow(ctx, `
			SELECT id, market_id, side, remaining_quantity, status
			FROM orders
			WHERE id = $1
			FOR UPDATE`, id).Scan(&o.id, &o.marketID, &o.side, &remStr, &o.status)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("order %s not found: %w", id, repository.ErrOrderNotFound)
			}
			return nil, fmt.Errorf("lock order %s: %w", id, err)
		}

		remDec, err := decimal.NewFromString(remStr)
		if err != nil {
			return nil, fmt.Errorf("invalid remaining quantity for order %s: %w", id, err)
		}
		o.remainingQuantity = remDec
		ordersByID[id] = &o
	}

	// Authoritative validation of locked orders
	if buyOrderID != "" {
		buyOrder, ok := ordersByID[buyOrderID]
		if !ok {
			return nil, fmt.Errorf("buy order %s not found", buyOrderID)
		}
		if buyOrder.side != string(repository.SideBuy) {
			return nil, fmt.Errorf("buy order %s has invalid side %s (expected BUY)", buyOrderID, buyOrder.side)
		}
		if buyOrder.status != string(repository.StatusOpen) &&
			buyOrder.status != string(repository.StatusPartiallyFilled) &&
			buyOrder.status != string(repository.StatusCancelling) &&
			buyOrder.status != string(repository.StatusCancelled) {
			return nil, fmt.Errorf("buy order %s has invalid status %s for fill", buyOrderID, buyOrder.status)
		}
		if buyOrder.remainingQuantity.LessThan(fillQtyDec) {
			return nil, fmt.Errorf("buy order %s remaining quantity %s is less than fill quantity %s",
				buyOrderID, buyOrder.remainingQuantity.String(), fillQtyDec.String())
		}
	}

	if sellOrderID != "" {
		sellOrder, ok := ordersByID[sellOrderID]
		if !ok {
			return nil, fmt.Errorf("sell order %s not found", sellOrderID)
		}
		if sellOrder.side != string(repository.SideSell) {
			return nil, fmt.Errorf("sell order %s has invalid side %s (expected SELL)", sellOrderID, sellOrder.side)
		}
		if sellOrder.status != string(repository.StatusOpen) &&
			sellOrder.status != string(repository.StatusPartiallyFilled) &&
			sellOrder.status != string(repository.StatusCancelling) &&
			sellOrder.status != string(repository.StatusCancelled) {
			return nil, fmt.Errorf("sell order %s has invalid status %s for fill", sellOrderID, sellOrder.status)
		}
		if sellOrder.remainingQuantity.LessThan(fillQtyDec) {
			return nil, fmt.Errorf("sell order %s remaining quantity %s is less than fill quantity %s",
				sellOrderID, sellOrder.remainingQuantity.String(), fillQtyDec.String())
		}
	}

	if buyOrderID != "" && sellOrderID != "" {
		if ordersByID[buyOrderID].marketID != ordersByID[sellOrderID].marketID {
			return nil, fmt.Errorf("market mismatch: buy order %s in %s, sell order %s in %s",
				buyOrderID, ordersByID[buyOrderID].marketID, sellOrderID, ordersByID[sellOrderID].marketID)
		}
	}

	// Record processed trade ONLY after validation passes
	var insertedTradeID string
	err = tx.QueryRow(ctx,
		`INSERT INTO processed_trades (trade_id) VALUES ($1)
		 ON CONFLICT (trade_id) DO NOTHING
		 RETURNING trade_id`,
		tradeID,
	).Scan(&insertedTradeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Concurrently processed trade — idempotent no-op
			return nil, nil
		}
		return nil, fmt.Errorf("record processed trade %s: %w", tradeID, err)
	}

	updateBuyQuery := `
		UPDATE orders
		SET filled_quantity = LEAST(quantity, filled_quantity + $1::numeric),
		    remaining_quantity = GREATEST(0, remaining_quantity - $1::numeric),
		    status = CASE 
		        WHEN remaining_quantity - $1::numeric <= 0 THEN 'FILLED' 
		        WHEN status = 'CANCELLING' THEN 'CANCELLING'
		        WHEN status = 'CANCELLED' THEN 'CANCELLED'
		        ELSE 'PARTIALLY_FILLED' 
		    END,
		    updated_at = NOW()
		WHERE id = $2 AND side = 'BUY'
		RETURNING id, status`

	updateSellQuery := `
		UPDATE orders
		SET filled_quantity = LEAST(quantity, filled_quantity + $1::numeric),
		    remaining_quantity = GREATEST(0, remaining_quantity - $1::numeric),
		    status = CASE 
		        WHEN remaining_quantity - $1::numeric <= 0 THEN 'FILLED' 
		        WHEN status = 'CANCELLING' THEN 'CANCELLING'
		        WHEN status = 'CANCELLED' THEN 'CANCELLED'
		        ELSE 'PARTIALLY_FILLED' 
		    END,
		    updated_at = NOW()
		WHERE id = $2 AND side = 'SELL'
		RETURNING id, status`

	var filledOrderIDs []string

	if buyOrderID != "" {
		var id, status string
		err := tx.QueryRow(ctx, updateBuyQuery, fillQty, buyOrderID).Scan(&id, &status)
		if err != nil {
			return nil, fmt.Errorf("update buy order %s: %w", buyOrderID, err)
		}
		if status == string(repository.StatusFilled) {
			filledOrderIDs = append(filledOrderIDs, id)
		}
	}

	if sellOrderID != "" {
		var id, status string
		err := tx.QueryRow(ctx, updateSellQuery, fillQty, sellOrderID).Scan(&id, &status)
		if err != nil {
			return nil, fmt.Errorf("update sell order %s: %w", sellOrderID, err)
		}
		if status == string(repository.StatusFilled) {
			filledOrderIDs = append(filledOrderIDs, id)
		}
	}

	// Durably enqueue wallet release for each order that reached FILLED status in this transaction
	for _, filledID := range filledOrderIDs {
		_, err := tx.Exec(ctx, `
			INSERT INTO wallet_releases (order_id, status)
			VALUES ($1, 'PENDING')
			ON CONFLICT (order_id) DO NOTHING`, filledID)
		if err != nil {
			return nil, fmt.Errorf("enqueue wallet release for filled order %s: %w", filledID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit trade fill tx: %w", err)
	}

	r.logger.Info("Applied trade fill to orders",
		zap.String("trade_id", tradeID),
		zap.String("buy_order_id", buyOrderID),
		zap.String("sell_order_id", sellOrderID),
		zap.String("fill_qty", fillQty),
		zap.Strings("filled_orders", filledOrderIDs),
	)
	return filledOrderIDs, nil
}

func (r *orderRepository) MarkOrderCancelled(ctx context.Context, orderID string) (*repository.Order, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx for mark order cancelled: %w", err)
	}
	defer tx.Rollback(ctx)

	query := `
		UPDATE orders
		SET status = 'CANCELLED', updated_at = NOW()
		WHERE id = $1 AND status IN ('OPEN', 'PARTIALLY_FILLED', 'CANCELLING')
		RETURNING id, user_id, market_id, side, order_type, price, quantity,
		          filled_quantity, remaining_quantity, status, idempotency_key,
		          created_at, updated_at`

	row := tx.QueryRow(ctx, query, orderID)
	order, err := scanOrder(row)
	if err != nil {
		if errors.Is(err, repository.ErrOrderNotFound) {
			return nil, nil // idempotent: order not in cancellable state or already cancelled
		}
		return nil, fmt.Errorf("mark order cancelled: %w", err)
	}

	// Durably enqueue wallet release for the cancelled order in the same transaction
	_, err = tx.Exec(ctx, `
		INSERT INTO wallet_releases (order_id, status)
		VALUES ($1, 'PENDING')
		ON CONFLICT (order_id) DO NOTHING`, orderID)
	if err != nil {
		return nil, fmt.Errorf("enqueue wallet release for cancelled order %s: %w", orderID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit mark order cancelled tx: %w", err)
	}

	return order, nil
}

func (r *orderRepository) ReconcileAbsentOrder(ctx context.Context, orderID string) (*repository.Order, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx for reconcile absent order: %w", err)
	}
	defer tx.Rollback(ctx)

	query := `
		UPDATE orders
		SET status = CASE 
				WHEN filled_quantity >= quantity THEN 'FILLED'
				ELSE 'CANCELLED'
			END,
			remaining_quantity = CASE
				WHEN filled_quantity >= quantity THEN 0
				ELSE remaining_quantity
			END,
			updated_at = NOW()
		WHERE id = $1 AND status IN ('OPEN', 'PARTIALLY_FILLED', 'CANCELLING')
		RETURNING id, user_id, market_id, side, order_type, price, quantity,
		          filled_quantity, remaining_quantity, status, idempotency_key,
		          created_at, updated_at`

	row := tx.QueryRow(ctx, query, orderID)
	order, err := scanOrder(row)
	if err != nil {
		if errors.Is(err, repository.ErrOrderNotFound) {
			return nil, nil // idempotent: already settled (FILLED or CANCELLED)
		}
		return nil, fmt.Errorf("reconcile absent order: %w", err)
	}

	// In both terminal outcomes (FILLED or CANCELLED), durably enqueue wallet release in the same transaction
	_, err = tx.Exec(ctx, `
		INSERT INTO wallet_releases (order_id, status)
		VALUES ($1, 'PENDING')
		ON CONFLICT (order_id) DO NOTHING`, orderID)
	if err != nil {
		return nil, fmt.Errorf("enqueue wallet release for reconciled order %s: %w", orderID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit reconcile absent order tx: %w", err)
	}

	return order, nil
}



