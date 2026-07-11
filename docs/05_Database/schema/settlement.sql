-- TradeDrift Settlement Database Schema

CREATE TABLE settled_trades (
    trade_id     UUID PRIMARY KEY,
    buyer_id     UUID NOT NULL,
    seller_id    UUID NOT NULL,
    market_id    VARCHAR(20) NOT NULL,
    price        DECIMAL(30,10) NOT NULL CHECK (price > 0),
    quantity     DECIMAL(30,10) NOT NULL CHECK (quantity > 0),
    status       VARCHAR(20) NOT NULL DEFAULT 'SETTLED' CHECK (status IN ('SETTLED', 'FAILED')),
    settled_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
