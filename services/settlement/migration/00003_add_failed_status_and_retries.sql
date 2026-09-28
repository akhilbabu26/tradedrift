-- +goose Up

-- 1. Drop old status check constraint and add new one with FAILED
ALTER TABLE settled_trades DROP CONSTRAINT IF EXISTS settled_trades_status_check;
ALTER TABLE settled_trades ADD CONSTRAINT settled_trades_status_check CHECK (status IN ('PENDING', 'SETTLED', 'FAILED'));

-- 2. Add retry_count, last_error, next_retry_at columns
ALTER TABLE settled_trades ADD COLUMN IF NOT EXISTS retry_count INT NOT NULL DEFAULT 0;
ALTER TABLE settled_trades ADD COLUMN IF NOT EXISTS last_error TEXT;
ALTER TABLE settled_trades ADD COLUMN IF NOT EXISTS next_retry_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- 3. Replace pending index to index next_retry_at for optimal recovery querying
DROP INDEX IF EXISTS idx_settled_trades_pending;
CREATE INDEX IF NOT EXISTS idx_settled_trades_pending ON settled_trades(next_retry_at)
    WHERE status = 'PENDING';

-- +goose Down

DROP INDEX IF EXISTS idx_settled_trades_pending;
CREATE INDEX IF NOT EXISTS idx_settled_trades_pending ON settled_trades(created_at)
    WHERE status = 'PENDING';

ALTER TABLE settled_trades DROP COLUMN IF EXISTS next_retry_at;
ALTER TABLE settled_trades DROP COLUMN IF EXISTS last_error;
ALTER TABLE settled_trades DROP COLUMN IF EXISTS retry_count;

ALTER TABLE settled_trades DROP CONSTRAINT IF EXISTS settled_trades_status_check;
ALTER TABLE settled_trades ADD CONSTRAINT settled_trades_status_check CHECK (status IN ('PENDING', 'SETTLED'));
