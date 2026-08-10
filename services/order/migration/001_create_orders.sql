-- +goose Up
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS orders (
    id                  UUID            PRIMARY KEY,
    user_id             UUID            NOT NULL,
    market_id           VARCHAR(20)     NOT NULL,
    side                VARCHAR(4)      NOT NULL,
    order_type          VARCHAR(10)     NOT NULL,
    price               DECIMAL(30,10),
    quantity            DECIMAL(30,10)  NOT NULL,
    filled_quantity     DECIMAL(30,10)  NOT NULL DEFAULT 0,
    remaining_quantity  DECIMAL(30,10)  NOT NULL,
    status              VARCHAR(20)     NOT NULL,
    idempotency_key     UUID            UNIQUE,
    created_at          TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ     NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_orders_user_id   ON orders(user_id);
CREATE INDEX IF NOT EXISTS idx_orders_market_id ON orders(market_id);
CREATE INDEX IF NOT EXISTS idx_orders_status    ON orders(status);
CREATE INDEX IF NOT EXISTS idx_orders_user_market_created
    ON orders(user_id, market_id, created_at DESC);

CREATE TABLE IF NOT EXISTS outbox (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_id    UUID        NOT NULL,
    event_type      VARCHAR(50) NOT NULL,
    payload         JSONB       NOT NULL,
    partition_key   VARCHAR(20) NOT NULL,
    processing_at   TIMESTAMPTZ,
    published_at    TIMESTAMPTZ,
    attempts        INT         NOT NULL DEFAULT 0,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_outbox_unpublished
    ON outbox(created_at) WHERE published_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS outbox;
DROP TABLE IF EXISTS orders;
