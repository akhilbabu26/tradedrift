-- +goose Up

CREATE TYPE transfer_type   AS ENUM ('DEPOSIT', 'WITHDRAWAL');
CREATE TYPE transfer_status AS ENUM ('PENDING', 'COMPLETED', 'FAILED');

CREATE TABLE wallet_transfers (
    id           UUID            PRIMARY KEY,
    wallet_id    UUID            NOT NULL REFERENCES wallets(id),
    type         transfer_type   NOT NULL,
    amount       DECIMAL(30,10)  NOT NULL CHECK (amount > 0),
    status       transfer_status NOT NULL DEFAULT 'PENDING',
    reference_id VARCHAR(64)     NOT NULL,
    created_at   TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ     NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_transfer_ref UNIQUE(reference_id)
);

CREATE INDEX idx_transfers_wallet ON wallet_transfers(wallet_id);

-- +goose Down
DROP INDEX IF EXISTS idx_transfers_wallet;
DROP TABLE IF EXISTS wallet_transfers;
DROP TYPE IF EXISTS transfer_status;
DROP TYPE IF EXISTS transfer_type;
