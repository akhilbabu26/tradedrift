-- TradeDrift Order Database Schema

CREATE TABLE orders (
    id                  UUID PRIMARY KEY,
    user_id             UUID NOT NULL, -- Application-level user reference
    market_id           VARCHAR(20) NOT NULL,
    side                VARCHAR(10) NOT NULL CHECK (side IN ('BUY', 'SELL')),
    order_type          VARCHAR(10) NOT NULL CHECK (order_type IN ('LIMIT', 'MARKET')),
    price               DECIMAL(30,10) NOT NULL CHECK (price > 0),
    quantity            DECIMAL(30,10) NOT NULL CHECK (quantity > 0),
    filled_quantity     DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (filled_quantity >= 0),
    remaining_quantity  DECIMAL(30,10) GENERATED ALWAYS AS (quantity - filled_quantity) STORED,
    status              VARCHAR(20) NOT NULL DEFAULT 'OPEN' CHECK (status IN ('OPEN', 'PARTIALLY_FILLED', 'FILLED', 'CANCELLING', 'CANCELLED')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_order_filled_qty CHECK (filled_quantity <= quantity)
);

CREATE TABLE outbox (
    id             UUID PRIMARY KEY,
    aggregate_id   UUID NOT NULL,
    event_type     VARCHAR(50) NOT NULL,
    payload        JSONB NOT NULL,
    partition_key  VARCHAR(100) NOT NULL,
    status         VARCHAR(20) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'PUBLISHED', 'FAILED')),
    failed_reason  TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at   TIMESTAMPTZ
);

-- Partial index for fast outbox leasing
CREATE INDEX idx_outbox_leasing ON outbox (created_at) WHERE status = 'PENDING';

-- Keyset cursor pagination index for user orders
CREATE INDEX idx_orders_user_pagination ON orders (user_id, created_at DESC, id DESC);
