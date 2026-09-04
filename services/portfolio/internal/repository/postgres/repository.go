package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"tradedrift/services/portfolio/internal/metrics"
	"tradedrift/services/portfolio/internal/repository"
)

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// GetHoldingsByUser returns all active crypto asset holdings for the given user.
func (r *Repository) GetHoldingsByUser(ctx context.Context, userID string) ([]repository.Holding, error) {
	timer := metrics.DBDurationSeconds.WithLabelValues("get_holdings_by_user")
	start := time.Now()
	defer func() { timer.Observe(time.Since(start).Seconds()) }()

	query := `
		SELECT user_id, asset_code, quantity, total_cost, realized_pnl, updated_at
		FROM holdings
		WHERE user_id = $1 AND quantity > 0
		ORDER BY asset_code ASC;
	`

	rows, err := r.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("query holdings by user: %w", err)
	}
	defer rows.Close()

	var holdings []repository.Holding
	for rows.Next() {
		var h repository.Holding
		if err := rows.Scan(
			&h.UserID,
			&h.AssetCode,
			&h.Quantity,
			&h.TotalCost,
			&h.RealizedPnL,
			&h.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan holding row: %w", err)
		}
		holdings = append(holdings, h)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate holdings rows: %w", err)
	}

	return holdings, nil
}

// ProcessTradeSettled executes the 1-atomic transaction:
// Check dedup -> Lock rows in deterministic order -> Buyer leg -> Seller leg -> Outbox -> ProcessedTrade.
func (r *Repository) ProcessTradeSettled(ctx context.Context, in repository.TradeSettledInput) ([]repository.OutboxMessage, error) {
	timer := metrics.DBDurationSeconds.WithLabelValues("process_trade_settled")
	start := time.Now()
	defer func() { timer.Observe(time.Since(start).Seconds()) }()

	// 1. Invariant Check: Self-trades are strictly prohibited
	if in.BuyerID == in.SellerID {
		metrics.AccountingViolationsTotal.WithLabelValues("self_trade").Inc()
		return nil, repository.ErrSelfTrade
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// 2. Atomic Idempotency Check & Registration
	// INSERT ... ON CONFLICT (trade_id) DO NOTHING guarantees atomic check-and-reserve.
	tag, err := tx.Exec(ctx, `
		INSERT INTO processed_trades (trade_id, user_id, market_id, sequence, processed_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (trade_id) DO NOTHING;
	`, in.TradeID, in.BuyerID, in.MarketID, in.Sequence)
	if err != nil {
		return nil, fmt.Errorf("insert processed_trades: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, repository.ErrTradeAlreadyProcessed
	}

	// 3. Deterministic Row-Locking to eliminate deadlocks (Alice <-> Bob)
	firstUser, secondUser := in.BuyerID, in.SellerID
	if firstUser > secondUser {
		firstUser, secondUser = secondUser, firstUser
	}

	firstHolding, err := lockHoldingRow(ctx, tx, firstUser, in.BaseAsset)
	if err != nil {
		return nil, fmt.Errorf("lock first holding (%s): %w", firstUser, err)
	}

	secondHolding, err := lockHoldingRow(ctx, tx, secondUser, in.BaseAsset)
	if err != nil {
		return nil, fmt.Errorf("lock second holding (%s): %w", secondUser, err)
	}

	var buyerHolding, sellerHolding repository.Holding
	if firstUser == in.BuyerID {
		buyerHolding = firstHolding
		sellerHolding = secondHolding
	} else {
		sellerHolding = firstHolding
		buyerHolding = secondHolding
	}

	// 4. Buyer Accounting: Weighted average cost basis
	// qty_new = qty_prev + trade.qty
	// cost_new = cost_prev + (trade.qty * trade.price)
	buyerTradeCost := in.Quantity.Mul(in.Price)
	buyerHolding.Quantity = buyerHolding.Quantity.Add(in.Quantity)
	buyerHolding.TotalCost = buyerHolding.TotalCost.Add(buyerTradeCost)
	// realized_pnl unchanged for buyer

	// 5. Seller Accounting: Asset reduction & PnL realization
	// Invariant: seller must have sufficient holdings
	if sellerHolding.Quantity.LessThan(in.Quantity) {
		metrics.AccountingViolationsTotal.WithLabelValues("insufficient_holdings").Inc()
		return nil, fmt.Errorf("%w: user %s has %s %s, needed %s",
			repository.ErrInsufficientHoldings, in.SellerID, sellerHolding.Quantity.String(), in.BaseAsset, in.Quantity.String())
	}

	// Calculate cost of sold quantity using average entry price
	avgEntryPrice := sellerHolding.AverageEntryPrice()
	costOfSold := in.Quantity.Mul(avgEntryPrice)
	tradeRevenue := in.Quantity.Mul(in.Price)
	realizedDelta := tradeRevenue.Sub(costOfSold)

	sellerHolding.Quantity = sellerHolding.Quantity.Sub(in.Quantity)
	sellerHolding.TotalCost = sellerHolding.TotalCost.Sub(costOfSold)
	sellerHolding.RealizedPnL = sellerHolding.RealizedPnL.Add(realizedDelta)

	// Safety clamping: If position is fully liquidated, reset cost to exactly zero
	if sellerHolding.Quantity.IsZero() || sellerHolding.Quantity.IsNegative() {
		sellerHolding.Quantity = decimal.Zero
		sellerHolding.TotalCost = decimal.Zero
	}

	// 6. Update Buyer & Seller Holdings in database
	if err := upsertHolding(ctx, tx, buyerHolding); err != nil {
		return nil, fmt.Errorf("upsert buyer holding: %w", err)
	}
	if err := upsertHolding(ctx, tx, sellerHolding); err != nil {
		return nil, fmt.Errorf("upsert seller holding: %w", err)
	}

	// 7. Generate Outbox Events with stable event_id
	now := time.Now().UTC()
	buyerOutbox, err := createOutboxMessage(ctx, tx, buyerHolding, now)
	if err != nil {
		return nil, fmt.Errorf("create buyer outbox event: %w", err)
	}

	sellerOutbox, err := createOutboxMessage(ctx, tx, sellerHolding, now)
	if err != nil {
		return nil, fmt.Errorf("create seller outbox event: %w", err)
	}

	// 8. Commit the 1-atomic transaction
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit accounting transaction: %w", err)
	}

	return []repository.OutboxMessage{buyerOutbox, sellerOutbox}, nil
}

// lockHoldingRow ensures the holding row exists and acquires an exclusive row lock (FOR UPDATE).
// If the row does not exist, an initial zero-state row is inserted first (ON CONFLICT DO NOTHING),
// ensuring SELECT ... FOR UPDATE always acquires a real lock even for first-time buyers/sellers.
func lockHoldingRow(ctx context.Context, tx pgx.Tx, userID, assetCode string) (repository.Holding, error) {
	// 1. Guarantee row exists in holdings table
	ensureQuery := `
		INSERT INTO holdings (user_id, asset_code, quantity, total_cost, realized_pnl, updated_at)
		VALUES ($1, $2, 0, 0, 0, NOW())
		ON CONFLICT (user_id, asset_code) DO NOTHING;
	`
	if _, err := tx.Exec(ctx, ensureQuery, userID, assetCode); err != nil {
		return repository.Holding{}, fmt.Errorf("ensure holding row: %w", err)
	}

	// 2. Lock physical row exclusively
	query := `
		SELECT user_id, asset_code, quantity, total_cost, realized_pnl, updated_at
		FROM holdings
		WHERE user_id = $1 AND asset_code = $2
		FOR UPDATE;
	`
	var h repository.Holding
	err := tx.QueryRow(ctx, query, userID, assetCode).Scan(
		&h.UserID,
		&h.AssetCode,
		&h.Quantity,
		&h.TotalCost,
		&h.RealizedPnL,
		&h.UpdatedAt,
	)
	if err != nil {
		return repository.Holding{}, fmt.Errorf("scan locked holding row: %w", err)
	}
	return h, nil
}

func upsertHolding(ctx context.Context, tx pgx.Tx, h repository.Holding) error {
	query := `
		UPDATE holdings SET
			quantity     = $1,
			total_cost   = $2,
			realized_pnl = $3,
			updated_at   = NOW()
		WHERE user_id = $4 AND asset_code = $5;
	`
	_, err := tx.Exec(ctx, query, h.Quantity, h.TotalCost, h.RealizedPnL, h.UserID, h.AssetCode)
	return err
}

func createOutboxMessage(ctx context.Context, tx pgx.Tx, h repository.Holding, now time.Time) (repository.OutboxMessage, error) {
	eventID := uuid.New().String()

	payloadMap := map[string]any{
		"event_id":            eventID,
		"user_id":             h.UserID,
		"asset_code":          h.AssetCode,
		"quantity":            h.Quantity.String(),
		"total_cost":          h.TotalCost.String(),
		"average_entry_price": h.AverageEntryPrice().String(),
		"realized_pnl":        h.RealizedPnL.String(),
		"timestamp":           now.Format(time.RFC3339Nano),
	}

	payloadBytes, err := json.Marshal(payloadMap)
	if err != nil {
		return repository.OutboxMessage{}, fmt.Errorf("marshal outbox payload: %w", err)
	}

	msg := repository.OutboxMessage{
		ID:           eventID,
		AggregateID:  h.UserID,
		EventType:    "PortfolioUpdated",
		Payload:      payloadBytes,
		PartitionKey: h.UserID,
		Status:       "PENDING",
		CreatedAt:    now,
	}

	query := `
		INSERT INTO portfolio_outbox (id, aggregate_id, event_type, payload, partition_key, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7);
	`
	_, err = tx.Exec(ctx, query, msg.ID, msg.AggregateID, msg.EventType, msg.Payload, msg.PartitionKey, msg.Status, msg.CreatedAt)
	if err != nil {
		return repository.OutboxMessage{}, fmt.Errorf("insert portfolio_outbox: %w", err)
	}

	return msg, nil
}

// FetchPendingOutbox atomically claims up to limit unhandled outbox events using
// a transition to 'PROCESSING' with lease timeout recovery (1 minute) and FOR UPDATE SKIP LOCKED.
func (r *Repository) FetchPendingOutbox(ctx context.Context, limit int) ([]repository.OutboxMessage, error) {
	timer := metrics.DBDurationSeconds.WithLabelValues("fetch_pending_outbox")
	start := time.Now()
	defer func() { timer.Observe(time.Since(start).Seconds()) }()

	query := `
		WITH claimable AS (
			SELECT id
			FROM portfolio_outbox
			WHERE (status = 'PENDING')
			   OR (status = 'PROCESSING' AND claimed_at < NOW() - INTERVAL '1 minute')
			ORDER BY created_at ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE portfolio_outbox
		SET status = 'PROCESSING', claimed_at = NOW()
		WHERE id IN (SELECT id FROM claimable)
		RETURNING id, aggregate_id, event_type, payload, partition_key, status, claimed_at, created_at;
	`

	rows, err := r.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("claim pending outbox: %w", err)
	}
	defer rows.Close()

	var messages []repository.OutboxMessage
	for rows.Next() {
		var m repository.OutboxMessage
		if err := rows.Scan(
			&m.ID,
			&m.AggregateID,
			&m.EventType,
			&m.Payload,
			&m.PartitionKey,
			&m.Status,
			&m.ClaimedAt,
			&m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan outbox row: %w", err)
		}
		messages = append(messages, m)
	}

	return messages, rows.Err()
}

// MarkOutboxPublished updates outbox records to 'PUBLISHED' with published_at = NOW().
func (r *Repository) MarkOutboxPublished(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	timer := metrics.DBDurationSeconds.WithLabelValues("mark_outbox_published")
	start := time.Now()
	defer func() { timer.Observe(time.Since(start).Seconds()) }()

	query := `
		UPDATE portfolio_outbox
		SET status = 'PUBLISHED', published_at = NOW()
		WHERE id = ANY($1);
	`

	_, err := r.pool.Exec(ctx, query, ids)
	return err
}
