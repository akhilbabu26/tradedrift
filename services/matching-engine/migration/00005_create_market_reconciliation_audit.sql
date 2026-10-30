-- +goose Up
-- Matching Engine PostgreSQL Reconciliation Audit Table
CREATE TABLE IF NOT EXISTS market_reconciliation_audit (
    market_id           VARCHAR(64)  NOT NULL,
    generation          VARCHAR(128) NOT NULL,
    partition           INTEGER      NOT NULL,
    log_end_offset      BIGINT       NOT NULL,
    manifest_checksum   VARCHAR(64)  NOT NULL DEFAULT '',
    status              VARCHAR(32)  NOT NULL DEFAULT 'PENDING',
    created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    approved_at         TIMESTAMPTZ,
    approved_by         VARCHAR(128),
    PRIMARY KEY (market_id, generation)
);

CREATE INDEX IF NOT EXISTS idx_market_reconciliation_market_status ON market_reconciliation_audit (market_id, status);

-- +goose Down
DROP INDEX IF EXISTS idx_market_reconciliation_market_status;
DROP TABLE IF EXISTS market_reconciliation_audit;
