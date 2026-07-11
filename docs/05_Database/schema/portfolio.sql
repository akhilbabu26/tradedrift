-- TradeDrift Portfolio Database Schema

CREATE TABLE holdings (
    id                   UUID PRIMARY KEY,
    user_id              UUID NOT NULL,
    asset                VARCHAR(10) NOT NULL,
    total_quantity       DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (total_quantity >= 0),
    average_entry_price  DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (average_entry_price >= 0),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_holdings_user_asset UNIQUE (user_id, asset)
);

CREATE TABLE processed_trades (
    trade_id     UUID PRIMARY KEY,
    user_id      UUID NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_holdings_user_id ON holdings (user_id);
