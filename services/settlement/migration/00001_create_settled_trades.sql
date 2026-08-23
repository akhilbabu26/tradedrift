-- +goose Up

CREATE TABLE IF NOT EXISTS settled_trades (
    trade_id      UUID PRIMARY KEY,
    buyer_id      UUID NOT NULL,
    seller_id     UUID NOT NULL,
    buy_order_id  UUID NOT NULL,
    sell_order_id UUID NOT NULL,
    market_id     VARCHAR(32) NOT NULL,
    base_asset    VARCHAR(16) NOT NULL,
    quote_asset   VARCHAR(16) NOT NULL,
    price         DECIMAL(30,10) NOT NULL,
    quantity      DECIMAL(30,10) NOT NULL,
    status        VARCHAR(16) NOT NULL DEFAULT 'PENDING'
                    CHECK (status IN ('PENDING', 'SETTLED')),
    executed_at   TIMESTAMPTZ NOT NULL,

    -- created_at records when THIS SERVICE received and registered the trade.
    -- Used by the recovery goroutine to detect genuinely stuck settlements:
    -- "PENDING for > 60 seconds in our system" — not "executed > 60 seconds ago".
    -- This avoids false positives when Kafka delivers delayed (but not stuck) events.
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at    TIMESTAMPTZ
);

-- Support audit queries: all trades by buyer or seller
CREATE INDEX idx_settled_trades_buyer   ON settled_trades(buyer_id);
CREATE INDEX idx_settled_trades_seller  ON settled_trades(seller_id);

-- Recovery goroutine: fast scan for stale PENDING rows.
-- Partial index only covers PENDING rows — stays tiny even at high volume.
-- Uses created_at (not executed_at) to avoid false positives from delayed Kafka delivery.
CREATE INDEX idx_settled_trades_pending ON settled_trades(created_at)
    WHERE status = 'PENDING';

-- +goose Down

DROP TABLE IF EXISTS settled_trades;
