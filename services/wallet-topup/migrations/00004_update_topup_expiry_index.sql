-- +goose Up
DROP INDEX IF EXISTS idx_topup_expiry;
CREATE INDEX IF NOT EXISTS idx_topup_expiry
    ON topup_orders(status, expires_at)
    WHERE status IN ('PAYMENT_PENDING', 'INITIATED');

-- +goose Down
DROP INDEX IF EXISTS idx_topup_expiry;
CREATE INDEX IF NOT EXISTS idx_topup_expiry
    ON topup_orders(status, expires_at)
    WHERE status = 'PAYMENT_PENDING';
