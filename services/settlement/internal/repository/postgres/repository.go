package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tradedrift/services/settlement/internal/repository"
)

// Repository implements repository.Repository using pgxpool.
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a new PostgreSQL-backed Repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

// Insert records a new trade with status=PENDING.
// ON CONFLICT (trade_id) DO NOTHING handles concurrent inserts safely.
func (r *Repository) Insert(ctx context.Context, t *repository.SettledTrade) error {
	const q = `
		INSERT INTO settled_trades (
			trade_id, buyer_id, seller_id, buy_order_id, sell_order_id,
			market_id, base_asset, quote_asset, price, quantity,
			sequence, status, executed_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10,
			$11, $12, $13
		)
		ON CONFLICT (trade_id) DO NOTHING`

	_, err := r.db.Exec(ctx, q,
		t.TradeID, t.BuyerID, t.SellerID, t.BuyOrderID, t.SellOrderID,
		t.MarketID, t.BaseAsset, t.QuoteAsset, t.Price, t.Quantity,
		t.Sequence, repository.StatusPending, t.ExecutedAt,
	)
	if err != nil {
		return fmt.Errorf("insert settled_trade: %w", err)
	}
	return nil
}

// FindByTradeID returns the settlement record for the given trade.
// Returns nil, nil if no row exists.
func (r *Repository) FindByTradeID(ctx context.Context, id uuid.UUID) (*repository.SettledTrade, error) {
	const q = `
		SELECT trade_id, buyer_id, seller_id, buy_order_id, sell_order_id,
		       market_id, base_asset, quote_asset, price, quantity,
		       sequence, status, executed_at, settled_at,
		       retry_count, next_retry_at, last_error
		FROM settled_trades
		WHERE trade_id = $1`

	row := r.db.QueryRow(ctx, q, id)
	t := &repository.SettledTrade{}
	err := row.Scan(
		&t.TradeID, &t.BuyerID, &t.SellerID, &t.BuyOrderID, &t.SellOrderID,
		&t.MarketID, &t.BaseAsset, &t.QuoteAsset, &t.Price, &t.Quantity,
		&t.Sequence, &t.Status, &t.ExecutedAt, &t.SettledAt,
		&t.RetryCount, &t.NextRetryAt, &t.LastError,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("find settled_trade by id: %w", err)
	}
	return t, nil
}

// MarkSettled transitions status from PENDING → SETTLED.
// The WHERE status='PENDING' guard ensures only valid state transitions are made
// and prevents accidentally overwriting an already-settled record.
//
// When 0 rows are affected it distinguishes two cases:
//   - Already SETTLED: idempotent/safe — return nil so the Kafka offset is committed.
//   - Trade not found: programming bug — return an error so the issue is surfaced.
func (r *Repository) MarkSettled(ctx context.Context, id uuid.UUID) error {
	const q = `
		UPDATE settled_trades
		SET    status = $1, settled_at = NOW()
		WHERE  trade_id = $2
		  AND  status = 'PENDING'`

	tag, err := r.db.Exec(ctx, q, repository.StatusSettled, id)
	if err != nil {
		return fmt.Errorf("mark settled: %w", err)
	}

	if tag.RowsAffected() == 0 {
		// 0 rows affected — either already SETTLED or trade doesn't exist.
		// Do a follow-up SELECT to distinguish the two cases.
		existing, err := r.FindByTradeID(ctx, id)
		if err != nil {
			return fmt.Errorf("mark settled: verify state after 0 rows affected: %w", err)
		}
		if existing == nil {
			// Trade not in DB at all — this is a programming bug (Phase 1 must always
			// precede Phase 3). Surface it so it doesn't go silently unnoticed.
			return fmt.Errorf("mark settled: trade_id %s not found in settled_trades (Phase 1 may have been skipped)", id)
		}
		// existing.Status == SETTLED — concurrent retry already completed Phase 3.
		// Wallet idempotency already moved funds. This is safe and expected.
		return nil
	}
	return nil
}

// RecordSettlementFailure atomically updates retry_count, last_error, and next_retry_at.
// If permanent is true, status is transitioned to FAILED.
func (r *Repository) RecordSettlementFailure(ctx context.Context, id uuid.UUID, lastErr string, permanent bool, nextRetry time.Time) error {
	const q = `
		UPDATE settled_trades
		SET    status = CASE WHEN $2 = true THEN 'FAILED' ELSE 'PENDING' END,
		       retry_count = retry_count + 1,
		       last_error = $3,
		       next_retry_at = $4
		WHERE  trade_id = $1
		  AND  status = 'PENDING'`

	_, err := r.db.Exec(ctx, q, id, permanent, lastErr, nextRetry)
	if err != nil {
		return fmt.Errorf("record settlement failure for trade %s: %w", id, err)
	}
	return nil
}

// FindStalePending returns up to limit PENDING rows whose created_at is
// older than olderThan and whose next_retry_at is past.
//
// FOR UPDATE acquires row-level locks to prevent concurrent callers from
// selecting the same rows. SKIP LOCKED means the recovery goroutine skips any
// row currently locked by the Kafka consumer's Phase 3 UPDATE — no blocking.
func (r *Repository) FindStalePending(ctx context.Context, olderThan time.Duration, limit int) ([]*repository.SettledTrade, error) {
	const q = `
		SELECT trade_id, buyer_id, seller_id, buy_order_id, sell_order_id,
		       market_id, base_asset, quote_asset, price, quantity,
		       sequence, status, executed_at, settled_at,
		       retry_count, next_retry_at, last_error
		FROM settled_trades
		WHERE status = $1
		  AND created_at < $2
		  AND (next_retry_at IS NULL OR next_retry_at <= NOW())
		ORDER BY next_retry_at ASC, created_at ASC
		LIMIT $3
		FOR UPDATE SKIP LOCKED`

	cutoff := time.Now().Add(-olderThan)
	rows, err := r.db.Query(ctx, q, repository.StatusPending, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("find stale pending: %w", err)
	}
	defer rows.Close()

	var trades []*repository.SettledTrade
	for rows.Next() {
		t := &repository.SettledTrade{}
		if err := rows.Scan(
			&t.TradeID, &t.BuyerID, &t.SellerID, &t.BuyOrderID, &t.SellOrderID,
			&t.MarketID, &t.BaseAsset, &t.QuoteAsset, &t.Price, &t.Quantity,
			&t.Sequence, &t.Status, &t.ExecutedAt, &t.SettledAt,
			&t.RetryCount, &t.NextRetryAt, &t.LastError,
		); err != nil {
			return nil, fmt.Errorf("scan stale pending row: %w", err)
		}
		trades = append(trades, t)
	}
	return trades, rows.Err()
}

// Compile-time check: ensure *Repository implements repository.Repository.
var _ repository.Repository = (*Repository)(nil)
