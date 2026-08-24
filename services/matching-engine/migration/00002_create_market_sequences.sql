-- 00002_create_market_sequences.sql
-- Matching Engine PostgreSQL Sequence Tracking Table

CREATE TABLE IF NOT EXISTS market_sequences (
    market_id  VARCHAR(64)  NOT NULL,
    sequence   BIGINT       NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (market_id)
);
