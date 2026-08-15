-- +goose Up
-- 1. Trading pair rules table
CREATE TABLE IF NOT EXISTS markets (
    id            VARCHAR(20) PRIMARY KEY, -- e.g. "BTC-USDT"
    base_asset    VARCHAR(10) NOT NULL, -- e.g. "BTC"
    quote_asset   VARCHAR(10) NOT NULL, -- e.g. "USDT"
    tick_size     DECIMAL(30,10) NOT NULL CHECK (tick_size > 0), -- price step increment (e.g. 0.01)
    lot_size      DECIMAL(30,10) NOT NULL CHECK (lot_size > 0), -- base asset quantity increment (e.g. 0.0001)
    status        VARCHAR(20) NOT NULL DEFAULT 'ACTIVE', -- 'ACTIVE', 'HALTED', 'MAINTENANCE'
    min_quantity  DECIMAL(30,10) NOT NULL DEFAULT 0.0001 CHECK (min_quantity > 0), -- minimum base asset quantity per order
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Ensure columns exist if markets table was previously created with an older schema
ALTER TABLE markets ADD COLUMN IF NOT EXISTS status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE';
ALTER TABLE markets ADD COLUMN IF NOT EXISTS min_quantity DECIMAL(30,10) NOT NULL DEFAULT 0.0001;
ALTER TABLE markets ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE markets ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- 2. Stores trade records for rolling 24h ticker calculations
CREATE TABLE IF NOT EXISTS market_trades (
    id            UUID PRIMARY KEY, -- trade_id
    market_id     VARCHAR(20) NOT NULL,
    price         DECIMAL(30,10) NOT NULL CHECK (price > 0),
    quantity      DECIMAL(30,10) NOT NULL CHECK (quantity > 0),
    executed_at   TIMESTAMPTZ NOT NULL,

    CONSTRAINT fk_market_trades_market
        FOREIGN KEY (market_id)
        REFERENCES markets(id)
);

CREATE INDEX IF NOT EXISTS idx_market_trades_rolling 
    ON market_trades(market_id, executed_at DESC);

-- 3. Stores candles across resolutions (1m, 5m, 15m, 1h, 1d)
CREATE TABLE IF NOT EXISTS ohlc_candles (
    market_id      VARCHAR(20) NOT NULL,
    resolution     VARCHAR(5) NOT NULL, -- '1m', '5m', '15m', '1h', '1d'
    start_time     TIMESTAMPTZ NOT NULL, -- start of resolution window
    open_price     DECIMAL(30,10) NOT NULL CHECK (open_price > 0),
    high_price     DECIMAL(30,10) NOT NULL CHECK (high_price > 0),
    low_price      DECIMAL(30,10) NOT NULL CHECK (low_price > 0),
    close_price    DECIMAL(30,10) NOT NULL CHECK (close_price > 0),
    volume         DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (volume >= 0),
    quote_volume   DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (quote_volume >= 0),
    open_trade_at  TIMESTAMPTZ NOT NULL,
    close_trade_at TIMESTAMPTZ NOT NULL,

    PRIMARY KEY (market_id, resolution, start_time),

    CONSTRAINT fk_candles_market
        FOREIGN KEY (market_id)
        REFERENCES markets(id),

    CONSTRAINT candles_resolution_check
        CHECK (resolution IN ('1m', '5m', '15m', '1h', '1d'))
);

ALTER TABLE ohlc_candles ADD COLUMN IF NOT EXISTS open_trade_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE ohlc_candles ADD COLUMN IF NOT EXISTS close_trade_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

CREATE INDEX IF NOT EXISTS idx_candles_time 
    ON ohlc_candles(market_id, resolution, start_time DESC);

-- 4. Seed initial default markets
INSERT INTO markets (id, base_asset, quote_asset, tick_size, lot_size, status, min_quantity) VALUES
    ('BTC-USDT', 'BTC', 'USDT', 0.0100000000, 0.0001000000, 'ACTIVE', 0.0001000000),
    ('ETH-USDT', 'ETH', 'USDT', 0.0100000000, 0.0010000000, 'ACTIVE', 0.0010000000),
    ('SOL-USDT', 'SOL', 'USDT', 0.0010000000, 0.0100000000, 'ACTIVE', 0.0100000000)
ON CONFLICT (id) DO UPDATE SET
    base_asset = EXCLUDED.base_asset,
    quote_asset = EXCLUDED.quote_asset,
    tick_size = EXCLUDED.tick_size,
    lot_size = EXCLUDED.lot_size,
    status = EXCLUDED.status,
    min_quantity = EXCLUDED.min_quantity;

-- +goose Down
DROP TABLE IF EXISTS ohlc_candles;
DROP TABLE IF EXISTS market_trades;
DROP TABLE IF EXISTS markets;
