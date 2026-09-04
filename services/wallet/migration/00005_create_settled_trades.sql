-- +goose Up
-- SQL in this section is executed when the migration is applied.

-- 1. Create settled_trades table as the primary database-level idempotency anchor for trades.
-- Enforces: 1 TradeID = exactly 1 settlement across the exchange.
CREATE TABLE IF NOT EXISTS settled_trades (
    trade_id    UUID PRIMARY KEY,
    market_id   VARCHAR(20) NOT NULL,
    sequence    BIGINT NOT NULL,
    settled_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_settled_trades_market_seq
    ON settled_trades(market_id, sequence);

-- +goose Down
-- SQL in this section is executed when the migration is rolled back.
DROP TABLE IF EXISTS settled_trades;
