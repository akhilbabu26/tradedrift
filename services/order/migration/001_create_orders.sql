-- +goose Up
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- 1. Orders — core order lifecycle table
-- idempotency_key (VARCHAR 64) prevents duplicate order submissions from clients
-- remaining_quantity starts equal to quantity and decreases with each fill
CREATE TABLE IF NOT EXISTS orders (
    id                 UUID           PRIMARY KEY,
    user_id            UUID           NOT NULL,
    market_id          VARCHAR(20)    NOT NULL,
    side               VARCHAR(4)     NOT NULL,
    order_type         VARCHAR(10)    NOT NULL,
    price              DECIMAL(30,10),
    quantity           DECIMAL(30,10) NOT NULL,
    filled_quantity    DECIMAL(30,10) NOT NULL DEFAULT 0,
    remaining_quantity DECIMAL(30,10) NOT NULL,
    status             VARCHAR(20)    NOT NULL,
    idempotency_key    VARCHAR(64)    UNIQUE,
    created_at         TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_orders_user_id   ON orders(user_id);
CREATE INDEX IF NOT EXISTS idx_orders_market_id ON orders(market_id);
CREATE INDEX IF NOT EXISTS idx_orders_status    ON orders(status);

-- Composite index for fast user order history per market, newest first
CREATE INDEX IF NOT EXISTS idx_orders_user_market_created ON orders(user_id, market_id, created_at DESC);

-- Keyset pagination index for user orders: user_id + created_at DESC + id DESC
CREATE INDEX IF NOT EXISTS idx_orders_user_created ON orders(user_id, created_at DESC, id DESC);

-- Fast filtered history by status: user_id + status + created_at DESC + id DESC
CREATE INDEX IF NOT EXISTS idx_orders_user_status_created ON orders(user_id, status, created_at DESC, id DESC);

-- Fast lookup for open/active orders (e.g. portfolio & active orders widget)
CREATE INDEX IF NOT EXISTS idx_orders_user_open ON orders(user_id, created_at DESC) WHERE status IN ('OPEN', 'PARTIALLY_FILLED', 'CANCELLING');

-- 2. Outbox — Transactional Outbox for reliable Kafka command publishing
-- published_at NULL = pending; partial index keeps recovery scans fast
CREATE TABLE IF NOT EXISTS outbox (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_id  UUID        NOT NULL,
    event_type    VARCHAR(50) NOT NULL,
    payload       JSONB       NOT NULL,
    partition_key VARCHAR(20) NOT NULL,
    processing_at TIMESTAMPTZ,
    published_at  TIMESTAMPTZ,
    attempts      INT         NOT NULL DEFAULT 0,
    last_error    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Partial index: only covers unpublished rows — stays tiny even under high order volume
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON outbox(created_at) WHERE published_at IS NULL;

-- Retention index: fast batch deletion of published events by the reaper without full-table scans
CREATE INDEX IF NOT EXISTS idx_outbox_published_at ON outbox(published_at) WHERE published_at IS NOT NULL;

-- 3. Processed Trades — deduplication log for incoming ME trade events
-- Prevents double-processing of the same trade_id on Kafka redelivery
CREATE TABLE IF NOT EXISTS processed_trades (
    trade_id   UUID        PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Fast retention scans/audits for processed trades
CREATE INDEX IF NOT EXISTS idx_processed_trades_created_at ON processed_trades(created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_processed_trades_created_at;
DROP TABLE IF EXISTS processed_trades;
DROP INDEX IF EXISTS idx_outbox_published_at;
DROP INDEX IF EXISTS idx_outbox_unpublished;
DROP TABLE IF EXISTS outbox;
DROP INDEX IF EXISTS idx_orders_user_open;
DROP INDEX IF EXISTS idx_orders_user_status_created;
DROP INDEX IF EXISTS idx_orders_user_created;
DROP INDEX IF EXISTS idx_orders_user_market_created;
DROP INDEX IF EXISTS idx_orders_status;
DROP INDEX IF EXISTS idx_orders_market_id;
DROP INDEX IF EXISTS idx_orders_user_id;
DROP TABLE IF EXISTS orders;
