-- TradeDrift Market Database Schema

CREATE TABLE markets (
    id           VARCHAR(20) PRIMARY KEY,
    base_asset   VARCHAR(10) NOT NULL,
    quote_asset  VARCHAR(10) NOT NULL,
    tick_size    DECIMAL(30,10) NOT NULL CHECK (tick_size > 0),
    lot_size     DECIMAL(30,10) NOT NULL CHECK (lot_size > 0),
    is_enabled   BOOLEAN NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE market_stats_daily (
    market_id   VARCHAR(20) NOT NULL REFERENCES markets(id) ON DELETE CASCADE,
    window_date DATE NOT NULL,
    open_price  DECIMAL(30,10) NOT NULL CHECK (open_price > 0),
    high_price  DECIMAL(30,10) NOT NULL CHECK (high_price > 0),
    low_price   DECIMAL(30,10) NOT NULL CHECK (low_price > 0),
    close_price DECIMAL(30,10) NOT NULL CHECK (close_price > 0),
    volume      DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (volume >= 0),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (market_id, window_date)
);

CREATE INDEX idx_markets_enabled ON markets (is_enabled) WHERE is_enabled = TRUE;
