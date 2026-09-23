-- +goose Up
-- Extend candle resolution constraint to include 4h
ALTER TABLE ohlc_candles DROP CONSTRAINT IF EXISTS candles_resolution_check;
ALTER TABLE ohlc_candles ADD CONSTRAINT candles_resolution_check
    CHECK (resolution IN ('1m', '5m', '15m', '1h', '4h', '1d'));

-- +goose Down
ALTER TABLE ohlc_candles DROP CONSTRAINT IF EXISTS candles_resolution_check;
ALTER TABLE ohlc_candles ADD CONSTRAINT candles_resolution_check
    CHECK (resolution IN ('1m', '5m', '15m', '1h', '1d'));
