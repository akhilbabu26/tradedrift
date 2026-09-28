-- +goose Up

-- 1. Holdings — per-user, per-asset position tracking
-- total_cost is the cumulative cost basis used for average price and P&L calculation
-- realized_pnl accumulates gains/losses from closed positions
CREATE TABLE IF NOT EXISTS holdings (
    user_id      UUID NOT NULL,
    asset_code   VARCHAR(10) NOT NULL,
    quantity     DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (quantity >= 0),
    total_cost   DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (total_cost >= 0),
    realized_pnl DECIMAL(30,10) NOT NULL DEFAULT 0,
    version      BIGINT NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, asset_code)
);

CREATE INDEX IF NOT EXISTS idx_holdings_user ON holdings(user_id);

-- 2. Processed User Trades — deduplication log for incoming trade events
-- (trade_id, user_id) PK means buyer and seller each get their own row
-- order_id, role, price, quantity are captured for portfolio event replay
CREATE TABLE IF NOT EXISTS processed_user_trades (
    trade_id     UUID NOT NULL,
    user_id      UUID NOT NULL,
    market_id    VARCHAR(20) NOT NULL DEFAULT '',
    sequence     BIGINT NOT NULL DEFAULT 0,
    order_id     UUID NOT NULL,
    role         VARCHAR(10) NOT NULL,
    price        DECIMAL(30,10) NOT NULL,
    quantity     DECIMAL(30,10) NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (trade_id, user_id)
);

-- 3. Processed Market Sequences — per-market monotonic sequence tracking
-- Ensures portfolio processing advances strictly in order per market
-- (market_id, sequence) PK enforces exactly-once processing per sequence slot
CREATE TABLE IF NOT EXISTS processed_market_sequences (
    market_id   VARCHAR(20) NOT NULL,
    sequence    BIGINT NOT NULL,
    trade_id    UUID NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (market_id, sequence)
);

-- 4. Portfolio Outbox — reliable Kafka event publishing for portfolio updates
-- Follows the Transactional Outbox Pattern; PENDING rows published by outbox relay worker
CREATE TABLE IF NOT EXISTS portfolio_outbox (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_id  UUID NOT NULL,
    event_type    VARCHAR(50) NOT NULL,
    payload       JSONB NOT NULL,
    partition_key VARCHAR(50) NOT NULL,
    status        VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    claimed_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at  TIMESTAMPTZ
);

-- Partial index for fast PENDING outbox polling
CREATE INDEX IF NOT EXISTS idx_portfolio_outbox_pending ON portfolio_outbox(created_at)
    WHERE status = 'PENDING';

-- Partial index for recovering expired PROCESSING leases (claimed but not yet published)
CREATE INDEX IF NOT EXISTS idx_portfolio_outbox_processing ON portfolio_outbox(claimed_at)
    WHERE status = 'PROCESSING';

-- +goose Down
DROP INDEX IF EXISTS idx_portfolio_outbox_processing;
DROP INDEX IF EXISTS idx_portfolio_outbox_pending;
DROP TABLE IF EXISTS portfolio_outbox;
DROP TABLE IF EXISTS processed_market_sequences;
DROP TABLE IF EXISTS processed_user_trades;
DROP INDEX IF EXISTS idx_holdings_user;
DROP TABLE IF EXISTS holdings;
