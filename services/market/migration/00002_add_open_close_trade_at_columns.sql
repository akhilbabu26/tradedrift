-- +goose Up
ALTER TABLE ohlc_candles ADD COLUMN IF NOT EXISTS open_trade_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE ohlc_candles ADD COLUMN IF NOT EXISTS close_trade_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- +goose Down
ALTER TABLE ohlc_candles DROP COLUMN IF EXISTS close_trade_at;
ALTER TABLE ohlc_candles DROP COLUMN IF EXISTS open_trade_at;
