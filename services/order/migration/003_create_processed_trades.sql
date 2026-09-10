-- +goose Up
CREATE TABLE IF NOT EXISTS processed_trades (
    trade_id    UUID        PRIMARY KEY,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS processed_trades;
