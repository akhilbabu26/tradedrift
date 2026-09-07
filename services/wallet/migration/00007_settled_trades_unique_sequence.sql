-- +goose Up
-- SQL in this section is executed when the migration is applied.

-- Convert the non-unique index on (market_id, sequence) to a UNIQUE CONSTRAINT.
-- This enforces: for any given market, each sequence number maps to exactly one trade.
-- Combined with the existing PRIMARY KEY (trade_id), the table now enforces:
--   1 TradeID  → exactly 1 row (primary key)
--   1 (market, seq) → exactly 1 row (unique constraint)
DROP INDEX IF EXISTS idx_settled_trades_market_seq;
ALTER TABLE settled_trades
    ADD CONSTRAINT uq_settled_trades_market_seq UNIQUE (market_id, sequence);

-- +goose Down
-- SQL in this section is executed when the migration is rolled back.
ALTER TABLE settled_trades
    DROP CONSTRAINT IF EXISTS uq_settled_trades_market_seq;
CREATE INDEX idx_settled_trades_market_seq
    ON settled_trades(market_id, sequence);
