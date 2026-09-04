package postgres

import (
	"context"
	"encoding/json"
	"errors"
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
		SELECT user_id, asset_code, quantity, total_cost, realized_pnl, version, updated_at
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
			&h.Version,
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

// ProcessUserTrade executes the atomic single-user position mutation transaction:
// 1. Assert (market_id, sequence) integrity via processed_market_sequences
// 2. Atomic idempotency check on (trade_id, user_id) via processed_user_trades
// 3. Exclusively lock the single user holding row FOR UPDATE on (user_id, base_asset)
// 4. Apply BUY or SELL accounting formulas
// 5. Assert invariant: quantity >= 0 (fatal if violated)
// 6. Monotonically increment version and upsert holding
// 7. Enqueue outbox message with portfolio_version
func (r *Repository) ProcessUserTrade(ctx context.Context, in repository.UserTradeInput) (*repository.OutboxMessage, error) {
	timer := metrics.DBDurationSeconds.WithLabelValues("process_user_trade")
	start := time.Now()
	defer func() { timer.Observe(time.Since(start).Seconds()) }()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Global Market Sequence Integrity: Ensure (market_id, sequence) is bound to this trade_id.
	// Uses ON CONFLICT DO NOTHING to avoid unnecessary WAL write amplification / dead row creation.
	var registeredTradeID string
	err = tx.QueryRow(ctx, `
		INSERT INTO processed_market_sequences (market_id, sequence, trade_id, recorded_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (market_id, sequence) DO NOTHING
		RETURNING trade_id;
	`, in.MarketID, in.Sequence, in.TradeID).Scan(&registeredTradeID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Row already existed — fetch existing trade_id to verify sequence integrity
			err = tx.QueryRow(ctx, `
				SELECT trade_id
				FROM processed_market_sequences
				WHERE market_id = $1 AND sequence = $2;
			`, in.MarketID, in.Sequence).Scan(&registeredTradeID)
			if err != nil {
				return nil, fmt.Errorf("fetch existing sequence trade_id: %w", err)
			}
		} else {
			return nil, fmt.Errorf("assert market sequence integrity: %w", err)
		}
	}
	if registeredTradeID != in.TradeID {
		metrics.AccountingViolationsTotal.WithLabelValues("sequence_collision").Inc()
		return nil, fmt.Errorf("%w on market %s seq %d: claimed by %s, attempted by %s",
			repository.ErrSequenceCollision, in.MarketID, in.Sequence, registeredTradeID, in.TradeID)
	}

	// 2. User Leg Idempotency Check & Registration
	tag, err := tx.Exec(ctx, `
		INSERT INTO processed_user_trades (trade_id, user_id, market_id, sequence, processed_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (trade_id, user_id) DO NOTHING;
	`, in.TradeID, in.UserID, in.MarketID, in.Sequence)
	if err != nil {
		return nil, fmt.Errorf("insert processed_user_trades: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, repository.ErrTradeAlreadyProcessed
	}

	// 3. Exclusively lock the single user holding row (zero cross-user deadlocks!)
	holding, err := lockHoldingRow(ctx, tx, in.UserID, in.BaseAsset)
	if err != nil {
		return nil, fmt.Errorf("lock holding row for user %s: %w", in.UserID, err)
	}

	// 4. Role-specific Accounting
	switch in.Role {
	case "BUY":
		tradeCost := in.Quantity.Mul(in.Price)
		holding.Quantity = holding.Quantity.Add(in.Quantity)
		holding.TotalCost = holding.TotalCost.Add(tradeCost)

	case "SELL":
		if holding.Quantity.LessThan(in.Quantity) {
			metrics.AccountingViolationsTotal.WithLabelValues("insufficient_holdings").Inc()
			return nil, fmt.Errorf("%w: user %s has %s %s, needed %s",
				repository.ErrInsufficientHoldings, in.UserID, holding.Quantity.String(), in.BaseAsset, in.Quantity.String())
		}

		avgEntryPrice := holding.AverageEntryPrice()
		costOfSold := in.Quantity.Mul(avgEntryPrice)
		tradeRevenue := in.Quantity.Mul(in.Price)
		realizedDelta := tradeRevenue.Sub(costOfSold)

		holding.Quantity = holding.Quantity.Sub(in.Quantity)
		holding.TotalCost = holding.TotalCost.Sub(costOfSold)
		holding.RealizedPnL = holding.RealizedPnL.Add(realizedDelta)

		// Fatal accounting invariant: negative quantity must never be silently masked
		if holding.Quantity.IsNegative() {
			metrics.AccountingViolationsTotal.WithLabelValues("negative_quantity_invariant").Inc()
			return nil, fmt.Errorf("FATAL accounting invariant violation: user %s negative balance %s",
				in.UserID, holding.Quantity.String())
		}

		if holding.Quantity.IsZero() {
			holding.TotalCost = decimal.Zero
		}

	default:
		return nil, fmt.Errorf("unsupported trade role: %q", in.Role)
	}

	// 5. Update Holding in database & increment version
	if err := upsertHolding(ctx, tx, &holding); err != nil {
		return nil, fmt.Errorf("upsert holding: %w", err)
	}

	// 6. Generate Outbox Event with portfolio_version
	now := time.Now().UTC()
	outboxMsg, err := createOutboxMessage(ctx, tx, holding, now)
	if err != nil {
		return nil, fmt.Errorf("create outbox event: %w", err)
	}

	// 7. Commit transaction
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	return &outboxMsg, nil
}

// ProcessTradeSettled executes the 1-atomic transaction for dual-participant trades (legacy/audit):
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

	// 2. Global Market Sequence Integrity
	var registeredTradeID string
	err = tx.QueryRow(ctx, `
		INSERT INTO processed_market_sequences (market_id, sequence, trade_id, recorded_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (market_id, sequence) DO UPDATE SET sequence = EXCLUDED.sequence
		RETURNING trade_id;
	`, in.MarketID, in.Sequence, in.TradeID).Scan(&registeredTradeID)
	if err != nil {
		return nil, fmt.Errorf("assert market sequence integrity: %w", err)
	}
	if registeredTradeID != in.TradeID {
		metrics.AccountingViolationsTotal.WithLabelValues("sequence_collision").Inc()
		return nil, fmt.Errorf("sequence collision detected on market %s seq %d: claimed by %s, attempted by %s",
			in.MarketID, in.Sequence, registeredTradeID, in.TradeID)
	}

	// 3. User Leg Idempotency Check (Buyer)
	tag, err := tx.Exec(ctx, `
		INSERT INTO processed_user_trades (trade_id, user_id, market_id, sequence, processed_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (trade_id, user_id) DO NOTHING;
	`, in.TradeID, in.BuyerID, in.MarketID, in.Sequence)
	if err != nil {
		return nil, fmt.Errorf("insert buyer processed_user_trades: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, repository.ErrTradeAlreadyProcessed
	}

	// Register Seller leg
	_, err = tx.Exec(ctx, `
		INSERT INTO processed_user_trades (trade_id, user_id, market_id, sequence, processed_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (trade_id, user_id) DO NOTHING;
	`, in.TradeID, in.SellerID, in.MarketID, in.Sequence)
	if err != nil {
		return nil, fmt.Errorf("insert seller processed_user_trades: %w", err)
	}

	// 4. Deterministic Row-Locking to eliminate deadlocks (Alice <-> Bob)
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

	// 5. Buyer Accounting: Weighted average cost basis
	buyerTradeCost := in.Quantity.Mul(in.Price)
	buyerHolding.Quantity = buyerHolding.Quantity.Add(in.Quantity)
	buyerHolding.TotalCost = buyerHolding.TotalCost.Add(buyerTradeCost)

	// 6. Seller Accounting: Asset reduction & PnL realization
	if sellerHolding.Quantity.LessThan(in.Quantity) {
		metrics.AccountingViolationsTotal.WithLabelValues("insufficient_holdings").Inc()
		return nil, fmt.Errorf("%w: user %s has %s %s, needed %s",
			repository.ErrInsufficientHoldings, in.SellerID, sellerHolding.Quantity.String(), in.BaseAsset, in.Quantity.String())
	}

	avgEntryPrice := sellerHolding.AverageEntryPrice()
	costOfSold := in.Quantity.Mul(avgEntryPrice)
	tradeRevenue := in.Quantity.Mul(in.Price)
	realizedDelta := tradeRevenue.Sub(costOfSold)

	sellerHolding.Quantity = sellerHolding.Quantity.Sub(in.Quantity)
	sellerHolding.TotalCost = sellerHolding.TotalCost.Sub(costOfSold)
	sellerHolding.RealizedPnL = sellerHolding.RealizedPnL.Add(realizedDelta)

	// Fatal accounting invariant: negative quantity must never be silently masked
	if sellerHolding.Quantity.IsNegative() {
		metrics.AccountingViolationsTotal.WithLabelValues("negative_quantity_invariant").Inc()
		return nil, fmt.Errorf("FATAL accounting invariant violation: seller %s negative balance %s",
			in.SellerID, sellerHolding.Quantity.String())
	}

	if sellerHolding.Quantity.IsZero() {
		sellerHolding.TotalCost = decimal.Zero
	}

	// 7. Update Buyer & Seller Holdings in database & increment versions
	if err := upsertHolding(ctx, tx, &buyerHolding); err != nil {
		return nil, fmt.Errorf("upsert buyer holding: %w", err)
	}
	if err := upsertHolding(ctx, tx, &sellerHolding); err != nil {
		return nil, fmt.Errorf("upsert seller holding: %w", err)
	}

	// 8. Generate Outbox Events with stable event_id and portfolio_version
	now := time.Now().UTC()
	buyerOutbox, err := createOutboxMessage(ctx, tx, buyerHolding, now)
	if err != nil {
		return nil, fmt.Errorf("create buyer outbox event: %w", err)
	}

	sellerOutbox, err := createOutboxMessage(ctx, tx, sellerHolding, now)
	if err != nil {
		return nil, fmt.Errorf("create seller outbox event: %w", err)
	}

	// 9. Commit the 1-atomic transaction
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit accounting transaction: %w", err)
	}

	return []repository.OutboxMessage{buyerOutbox, sellerOutbox}, nil
}

// lockHoldingRow ensures the holding row exists and acquires an exclusive row lock (FOR UPDATE).
func lockHoldingRow(ctx context.Context, tx pgx.Tx, userID, assetCode string) (repository.Holding, error) {
	ensureQuery := `
		INSERT INTO holdings (user_id, asset_code, quantity, total_cost, realized_pnl, version, updated_at)
		VALUES ($1, $2, 0, 0, 0, 0, NOW())
		ON CONFLICT (user_id, asset_code) DO NOTHING;
	`
	if _, err := tx.Exec(ctx, ensureQuery, userID, assetCode); err != nil {
		return repository.Holding{}, fmt.Errorf("ensure holding row: %w", err)
	}

	query := `
		SELECT user_id, asset_code, quantity, total_cost, realized_pnl, version, updated_at
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
		&h.Version,
		&h.UpdatedAt,
	)
	if err != nil {
		return repository.Holding{}, fmt.Errorf("scan locked holding row: %w", err)
	}
	return h, nil
}

func upsertHolding(ctx context.Context, tx pgx.Tx, h *repository.Holding) error {
	query := `
		UPDATE holdings SET
			quantity     = $1,
			total_cost   = $2,
			realized_pnl = $3,
			version      = version + 1,
			updated_at   = NOW()
		WHERE user_id = $4 AND asset_code = $5
		RETURNING version;
	`
	return tx.QueryRow(ctx, query, h.Quantity, h.TotalCost, h.RealizedPnL, h.UserID, h.AssetCode).Scan(&h.Version)
}

func createOutboxMessage(ctx context.Context, tx pgx.Tx, h repository.Holding, now time.Time) (repository.OutboxMessage, error) {
	eventID := uuid.New().String()

	payloadMap := map[string]any{
		"event_id":   eventID,
		"user_id":    h.UserID,
		"asset_code": h.AssetCode,
		// Note: portfolio_version is strictly scoped to (user_id, asset_code), representing
		// the monotonic revision of this specific asset holding for the user (not a global portfolio version).
		"portfolio_version":   h.Version,
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
