-- +goose Up
-- Matching Engine PostgreSQL Durability Checkpoint Table
CREATE TABLE IF NOT EXISTS kafka_checkpoints (
    topic      VARCHAR(255) NOT NULL,
    partition  INTEGER      NOT NULL,
    "offset"   BIGINT       NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    PRIMARY KEY (topic, partition)
);

-- +goose Down
DROP TABLE IF EXISTS kafka_checkpoints;
