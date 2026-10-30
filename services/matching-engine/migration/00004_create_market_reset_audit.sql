-- +goose Up
-- Matching Engine PostgreSQL Reset Audit Table
CREATE TABLE IF NOT EXISTS market_reset_audit (
    market_id       VARCHAR(64)  NOT NULL,
    partition       INTEGER      NOT NULL,
    generation      VARCHAR(128) NOT NULL,
    log_end_offset  BIGINT       NOT NULL,
    sequence        BIGINT       NOT NULL,
    token_hash      VARCHAR(64)  NOT NULL DEFAULT '',
    authorized_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (market_id, generation)
);

CREATE INDEX IF NOT EXISTS idx_market_reset_audit_partition ON market_reset_audit (market_id, partition);

-- +goose Down
DROP INDEX IF EXISTS idx_market_reset_audit_partition;
DROP TABLE IF EXISTS market_reset_audit;
