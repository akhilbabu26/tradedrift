-- +goose Up
CREATE TABLE IF NOT EXISTS wallet_releases (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id     UUID        NOT NULL UNIQUE,
    status       VARCHAR(20) NOT NULL DEFAULT 'PENDING',
    attempts     INT         NOT NULL DEFAULT 0,
    last_error   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_wallet_releases_pending 
ON wallet_releases(created_at) WHERE status = 'PENDING';

CREATE INDEX IF NOT EXISTS idx_wallet_releases_processed 
ON wallet_releases(processed_at) WHERE status = 'COMPLETED';

-- +goose Down
DROP INDEX IF EXISTS idx_wallet_releases_processed;
DROP INDEX IF EXISTS idx_wallet_releases_pending;
DROP TABLE IF EXISTS wallet_releases;
