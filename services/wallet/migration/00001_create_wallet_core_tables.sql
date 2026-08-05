-- +goose Up
-- SQL in this section is executed when the migration is applied.
-- 1. Supported Assets (no FK dependencies — must come first)
CREATE TABLE supported_assets (
    asset_code VARCHAR(10) PRIMARY KEY,
    asset_name VARCHAR(50),
    decimals INT NOT NULL,
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    seed_amount DECIMAL(30,10) NOT NULL DEFAULT 0,
    display_order INT
);

-- Seed the 4 supported assets
INSERT INTO supported_assets (asset_code, asset_name, decimals, is_enabled, seed_amount, display_order) VALUES
    ('USDT', 'Tether',   2, true, 10000.0000000000, 1),
    ('BTC',  'Bitcoin',  8, true, 0.0000000000,     2),
    ('ETH',  'Ethereum', 8, true, 0.0000000000,     3),
    ('SOL',  'Solana',   9, true, 0.0000000000,     4);

-- 2. Wallets (one row per user per asset)
CREATE TABLE wallets(
    id  UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    asset VARCHAR(10) NOT NULL REFERENCES supported_assets(asset_code),
    available_balance DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (available_balance >= 0),
    reserved_balance DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (reserved_balance >= 0),
    is_frozen BOOLEAN NOT NULL DEFAULT FALSE,
    frozen_at TIMESTAMPTZ,
    frozen_by VARCHAR(64),
    freeze_reason TEXT,
    initial_balance DECIMAL(30,10) NOT NULL DEFAULT 0,
    total_balance DECIMAL(30,10) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, asset)
);

CREATE INDEX idx_wallets_user ON wallets(user_id);


-- 3. Wallet Reservations (one row per order)
CREATE TABLE wallet_reservations (
    id               UUID           PRIMARY KEY,
    order_id         UUID           NOT NULL UNIQUE,
    user_id          UUID           NOT NULL,
    asset            VARCHAR(10)    NOT NULL,
    reserved_amount  DECIMAL(30,10) NOT NULL CHECK (reserved_amount > 0),
    consumed_amount  DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (consumed_amount >= 0),
    remaining_amount DECIMAL(30,10) NOT NULL CHECK (remaining_amount >= 0),
    status           VARCHAR(25)    NOT NULL DEFAULT 'ACTIVE'
                     CHECK (status IN ('ACTIVE', 'PARTIALLY_CONSUMED', 'CONSUMED', 'RELEASED')),
    created_at       TIMESTAMPTZ    NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_reservations_user ON wallet_reservations(user_id);
CREATE INDEX idx_reservations_order ON wallet_reservations(order_id);

-- 4. Wallet Transactions (immutable ledger — idempotency anchor)
CREATE TABLE wallet_transactions (
    id               UUID           PRIMARY KEY,
    wallet_id        UUID           NOT NULL REFERENCES wallets(id),
    reference_id     UUID           NOT NULL,
    reference_type   VARCHAR(30)    NOT NULL
                     CHECK (reference_type IN (
                         'INITIAL_ALLOCATION', 'RESERVATION',
                         'RELEASE', 'SETTLEMENT', 'DEPOSIT', 'WITHDRAWAL'
                     )),
    transaction_type VARCHAR(10)    NOT NULL CHECK (transaction_type IN ('CREDIT', 'DEBIT')),
    asset            VARCHAR(10)    NOT NULL,
    amount           DECIMAL(30,10) NOT NULL CHECK (amount > 0),
    created_at       TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    UNIQUE (reference_id, reference_type, asset)
);
CREATE INDEX idx_transactions_wallet ON wallet_transactions(wallet_id);
CREATE INDEX idx_transactions_reference ON wallet_transactions(reference_id);

-- 5. Outbox (Transactional Outbox Pattern for Kafka publishing)
CREATE TABLE outbox (
    id            UUID         PRIMARY KEY,
    aggregate_id  UUID         NOT NULL,
    event_type    VARCHAR(255) NOT NULL,
    payload       JSONB        NOT NULL,
    partition_key VARCHAR(255) NOT NULL,
    status        VARCHAR(50)  NOT NULL DEFAULT 'PENDING'
                  CHECK (status IN ('PENDING', 'PROCESSED', 'FAILED')),
    failed_reason TEXT,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    published_at  TIMESTAMPTZ
);
CREATE INDEX idx_outbox_pending ON outbox(created_at) WHERE status = 'PENDING';

-- +goose Down
-- SQL in this section is executed when the migration is rolled back.
DROP INDEX IF EXISTS idx_outbox_pending;
DROP TABLE IF EXISTS outbox;
DROP INDEX IF EXISTS idx_transactions_reference;
DROP INDEX IF EXISTS idx_transactions_wallet;
DROP TABLE IF EXISTS wallet_transactions;
DROP INDEX IF EXISTS idx_reservations_order;
DROP INDEX IF EXISTS idx_reservations_user;
DROP TABLE IF EXISTS wallet_reservations;
DROP INDEX IF EXISTS idx_wallets_user;
DROP TABLE IF EXISTS wallets;
DROP TABLE IF EXISTS supported_assets;