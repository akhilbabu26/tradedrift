-- +goose Up

CREATE TABLE IF NOT EXISTS trades (
    id            UUID           PRIMARY KEY,      -- trade_id (UUIDv7, ME-generated)
    buyer_id      UUID           NOT NULL,
    seller_id     UUID           NOT NULL,
    buy_order_id  UUID           NOT NULL,
    sell_order_id UUID           NOT NULL,
    market_id     VARCHAR(20)    NOT NULL,
    base_asset    VARCHAR(16)    NOT NULL,
    quote_asset   VARCHAR(16)    NOT NULL,
    price         DECIMAL(30,10) NOT NULL,
    quantity      DECIMAL(30,10) NOT NULL,
    -- me_sequence: Matching Engine's per-market monotonic counter (> 0).
    -- NOT NULL, no DEFAULT. A missing or zero sequence is rejected at the
    -- application layer (process() validation) before reaching this INSERT.
    -- No DEFAULT ensures any producer bug that bypasses the app layer is
    -- still caught here rather than silently storing 0.
    me_sequence   BIGINT         NOT NULL,
    executed_at   TIMESTAMPTZ    NOT NULL,         -- ME clock: authoritative trade time
    settled_at    TIMESTAMPTZ    NOT NULL          -- Wallet clock: when balances moved
);

-- User trade history — buyer side, all markets
CREATE INDEX IF NOT EXISTS idx_trades_buyer
    ON trades(buyer_id, executed_at DESC, id DESC);

-- User trade history — seller side, all markets
CREATE INDEX IF NOT EXISTS idx_trades_seller
    ON trades(seller_id, executed_at DESC, id DESC);

-- User trade history — buyer side, single market
CREATE INDEX IF NOT EXISTS idx_trades_buyer_market
    ON trades(buyer_id, market_id, executed_at DESC, id DESC);

-- User trade history — seller side, single market
CREATE INDEX IF NOT EXISTS idx_trades_seller_market
    ON trades(seller_id, market_id, executed_at DESC, id DESC);

-- Public market trade tape
CREATE INDEX IF NOT EXISTS idx_trades_market
    ON trades(market_id, executed_at DESC, id DESC);

-- Per-market sequence uniqueness.
-- ME guarantees sequence > 0 and monotonically increasing per market.
-- A different trade_id sharing the same (market_id, me_sequence) is a
-- producer integrity bug — this constraint catches it at the DB layer.
CREATE UNIQUE INDEX IF NOT EXISTS idx_trades_market_sequence
    ON trades(market_id, me_sequence);

-- +goose Down

DROP TABLE IF EXISTS trades;
