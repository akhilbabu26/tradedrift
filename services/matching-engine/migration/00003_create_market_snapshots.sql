-- +goose Up
-- Matching Engine PostgreSQL Snapshot Table
CREATE TABLE IF NOT EXISTS market_snapshots (
    market_id      VARCHAR(64)  NOT NULL,
    sequence       BIGINT       NOT NULL,
    partition      INTEGER      NOT NULL,
    "offset"       BIGINT       NOT NULL,
    schema_version INTEGER      NOT NULL,
    snapshot       JSONB        NOT NULL,
    checksum       BYTEA        NOT NULL,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (market_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_market_snapshots_market_sequence ON market_snapshots (market_id, sequence DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_market_snapshots_market_sequence;
DROP TABLE IF EXISTS market_snapshots;
