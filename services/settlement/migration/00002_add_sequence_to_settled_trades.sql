-- +goose Up

-- Add sequence column to persist the ME per-market monotonic counter.
-- This is needed by the recovery goroutine so it can pass the correct
-- sequence to Wallet.SettleTrade instead of the hardcoded value of 1
-- which caused uq_settled_trades_market_seq constraint violations.
ALTER TABLE settled_trades ADD COLUMN IF NOT EXISTS sequence BIGINT NOT NULL DEFAULT 0;

-- +goose Down

ALTER TABLE settled_trades DROP COLUMN IF EXISTS sequence;
