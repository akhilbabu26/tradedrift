package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"tradedrift/services/trade/internal/repository"
)

const pgUniqueViolation = "23505"
const seqUniqueIndex = "idx_trades_market_sequence"

// Repository implements repository.Repository using pgxpool.
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository creates a new PostgreSQL-backed Repository.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Create inserts a trade row. Idempotent on Trade.ID via ON CONFLICT (id) DO NOTHING.
//
// Error classification:
//   - nil                         → success (inserted or duplicate trade_id, both are fine)
//   - repository.ErrSequenceConflict → unique violation on (market_id, me_sequence)
//     for a DIFFERENT trade_id — producer integrity bug, not retryable
//   - any other error             → transient DB/network failure, retryable
func (r *Repository) Create(ctx context.Context, t *repository.Trade) error {
	const q = `
		INSERT INTO trades (
			id, buyer_id, seller_id, buy_order_id, sell_order_id,
			market_id, base_asset, quote_asset, price, quantity,
			me_sequence, executed_at, settled_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (id) DO NOTHING`

	_, err := r.pool.Exec(ctx, q,
		t.ID, t.BuyerID, t.SellerID, t.BuyOrderID, t.SellOrderID,
		t.MarketID, t.BaseAsset, t.QuoteAsset,
		t.Price.String(), t.Quantity.String(),
		t.Sequence, t.ExecutedAt, t.SettledAt,
	)
	if err != nil {
		if isSequenceConflict(err) {
			return repository.ErrSequenceConflict
		}
		return fmt.Errorf("create trade %s: %w", t.ID, err)
	}
	return nil
}

// GetByID returns the trade with the given id, or nil if not found.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (*repository.Trade, error) {
	const q = `
		SELECT id, buyer_id, seller_id, buy_order_id, sell_order_id,
		       market_id, base_asset, quote_asset, price, quantity,
		       me_sequence, executed_at, settled_at
		FROM trades
		WHERE id = $1`

	row := r.pool.QueryRow(ctx, q, id)
	t, err := scanOne(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get trade %s: %w", id, err)
	}
	return t, nil
}

// ListByUser returns trades where userID is buyer OR seller.
// Uses UNION ALL over two index scans — avoids OR which prevents index use.
// Cursor-paginated on (executed_at DESC, id DESC).
func (r *Repository) ListByUser(
	ctx context.Context,
	userID uuid.UUID,
	marketID string,
	after *repository.Cursor,
	limit int,
) ([]repository.Trade, error) {
	var (
		q    string
		args []any
	)

	cursorAt, cursorID := cursorArgs(after)

	if marketID == "" {
		q = `
			SELECT id, buyer_id, seller_id, buy_order_id, sell_order_id,
			       market_id, base_asset, quote_asset, price, quantity,
			       me_sequence, executed_at, settled_at
			FROM (
				SELECT * FROM trades WHERE buyer_id  = $1
				UNION ALL
				SELECT * FROM trades WHERE seller_id = $1
			) t
			WHERE ($2::timestamptz IS NULL OR (executed_at, id) < ($2, $3::uuid))
			ORDER BY executed_at DESC, id DESC
			LIMIT $4`
		args = []any{userID, cursorAt, cursorID, limit}
	} else {
		q = `
			SELECT id, buyer_id, seller_id, buy_order_id, sell_order_id,
			       market_id, base_asset, quote_asset, price, quantity,
			       me_sequence, executed_at, settled_at
			FROM (
				SELECT * FROM trades WHERE buyer_id  = $1 AND market_id = $2
				UNION ALL
				SELECT * FROM trades WHERE seller_id = $1 AND market_id = $2
			) t
			WHERE ($3::timestamptz IS NULL OR (executed_at, id) < ($3, $4::uuid))
			ORDER BY executed_at DESC, id DESC
			LIMIT $5`
		args = []any{userID, marketID, cursorAt, cursorID, limit}
	}

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list user trades: %w", err)
	}
	defer rows.Close()
	return scanMany(rows)
}

// ListByMarket returns public trades for a market.
// Strips no fields here — the handler/service layer omits buyer_id/seller_id
// from the public response.
func (r *Repository) ListByMarket(
	ctx context.Context,
	marketID string,
	after *repository.Cursor,
	limit int,
) ([]repository.Trade, error) {
	const q = `
		SELECT id, buyer_id, seller_id, buy_order_id, sell_order_id,
		       market_id, base_asset, quote_asset, price, quantity,
		       me_sequence, executed_at, settled_at
		FROM trades
		WHERE market_id = $1
		  AND ($2::timestamptz IS NULL OR (executed_at, id) < ($2, $3::uuid))
		ORDER BY executed_at DESC, id DESC
		LIMIT $4`

	cursorAt, cursorID := cursorArgs(after)
	rows, err := r.pool.Query(ctx, q, marketID, cursorAt, cursorID, limit)
	if err != nil {
		return nil, fmt.Errorf("list market trades %s: %w", marketID, err)
	}
	defer rows.Close()
	return scanMany(rows)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func isSequenceConflict(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == pgUniqueViolation &&
			pgErr.ConstraintName == seqUniqueIndex
	}
	return false
}

func cursorArgs(c *repository.Cursor) (any, any) {
	if c == nil {
		return nil, nil
	}
	return c.ExecutedAt, c.ID
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOne(row rowScanner) (*repository.Trade, error) {
	t := &repository.Trade{}
	var priceStr, qtyStr string
	err := row.Scan(
		&t.ID, &t.BuyerID, &t.SellerID, &t.BuyOrderID, &t.SellOrderID,
		&t.MarketID, &t.BaseAsset, &t.QuoteAsset,
		&priceStr, &qtyStr,
		&t.Sequence, &t.ExecutedAt, &t.SettledAt,
	)
	if err != nil {
		return nil, err
	}
	t.Price, _ = decimal.NewFromString(priceStr)
	t.Quantity, _ = decimal.NewFromString(qtyStr)
	return t, nil
}

func scanMany(rows pgx.Rows) ([]repository.Trade, error) {
	var trades []repository.Trade
	for rows.Next() {
		t, err := scanOne(rows)
		if err != nil {
			return nil, fmt.Errorf("scan trade row: %w", err)
		}
		trades = append(trades, *t)
	}
	return trades, rows.Err()
}

// Compile-time check: ensure *Repository implements repository.Repository.
var _ repository.Repository = (*Repository)(nil)
