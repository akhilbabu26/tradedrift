-- +goose Up

-- 1. Supported Assets — registry of tradeable assets
-- seed_amount: default allocation given to new users on wallet creation
CREATE TABLE supported_assets (
    asset_code    VARCHAR(10) PRIMARY KEY,
    asset_name    VARCHAR(50),
    decimals      INT NOT NULL,
    is_enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    seed_amount   DECIMAL(30,10) NOT NULL DEFAULT 0,
    display_order INT
);

-- Seed the 4 supported assets
INSERT INTO supported_assets (asset_code, asset_name, decimals, is_enabled, seed_amount, display_order) VALUES
    ('USDT', 'Tether',   2, true, 10000.0000000000, 1),
    ('BTC',  'Bitcoin',  8, true, 0.0000000000,     2),
    ('ETH',  'Ethereum', 8, true, 0.0000000000,     3),
    ('SOL',  'Solana',   9, true, 0.0000000000,     4);

-- 2. Wallets — one row per user per asset
-- total_balance = available_balance + reserved_balance (enforced at DB layer)
-- initial_balance tracks what was seeded for audit; the LE reads available_balance via gRPC
CREATE TABLE wallets (
    id                UUID           PRIMARY KEY,
    user_id           UUID           NOT NULL,
    asset             VARCHAR(10)    NOT NULL REFERENCES supported_assets(asset_code),
    available_balance DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (available_balance >= 0),
    reserved_balance  DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (reserved_balance >= 0),
    is_frozen         BOOLEAN        NOT NULL DEFAULT FALSE,
    frozen_at         TIMESTAMPTZ,
    frozen_by         VARCHAR(64),
    freeze_reason     TEXT,
    initial_balance   DECIMAL(30,10) NOT NULL DEFAULT 0,
    total_balance     DECIMAL(30,10) NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, asset),
    -- Accounting identity: total must always equal available + reserved
    CONSTRAINT chk_wallet_total_balance CHECK (total_balance = available_balance + reserved_balance)
);

CREATE INDEX idx_wallets_user ON wallets(user_id);

-- 3. Wallet Reservations — one row per open order, tracking pre-trade fund locking
-- ACTIVE → PARTIALLY_CONSUMED → CONSUMED (full fill) | RELEASED (cancel)
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

CREATE INDEX idx_reservations_user  ON wallet_reservations(user_id);
CREATE INDEX idx_reservations_order ON wallet_reservations(order_id);

-- 4. Wallet Transactions — immutable double-entry ledger
-- UNIQUE (wallet_id, reference_id, reference_type) ensures idempotency per wallet per event:
--   both buyer and seller get their own row without collision
-- reference_type includes TOPUP for payment top-up credits
CREATE TABLE wallet_transactions (
    id               UUID           PRIMARY KEY,
    wallet_id        UUID           NOT NULL REFERENCES wallets(id),
    reference_id     UUID           NOT NULL,
    reference_type   VARCHAR(30)    NOT NULL
                     CHECK (reference_type IN (
                         'INITIAL_ALLOCATION', 'RESERVATION', 'RELEASE',
                         'SETTLEMENT', 'DEPOSIT', 'TOPUP', 'WITHDRAWAL'
                     )),
    transaction_type VARCHAR(10)    NOT NULL CHECK (transaction_type IN ('CREDIT', 'DEBIT')),
    asset            VARCHAR(10)    NOT NULL,
    amount           DECIMAL(30,10) NOT NULL CHECK (amount > 0),
    created_at       TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    CONSTRAINT wallet_transactions_wallet_ref_type_key UNIQUE (wallet_id, reference_id, reference_type)
);

CREATE INDEX idx_transactions_wallet    ON wallet_transactions(wallet_id);
CREATE INDEX idx_transactions_reference ON wallet_transactions(reference_id);

-- 5. Wallet Transfers — deposit/withdrawal lifecycle tracking
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
    CONSTRAINT uq_transfer_ref UNIQUE (reference_id)
);

CREATE INDEX idx_transfers_wallet ON wallet_transfers(wallet_id);

-- 6. Outbox — Transactional Outbox for reliable Kafka event publishing
-- status includes PROCESSING to support atomic lease claiming
-- claim_token: per-claim UUID for lease fencing (prevents double-publish on worker crash)
-- claimed_at: timestamp when a worker acquired the lease (used for timeout recovery)
CREATE TABLE outbox (
    id            UUID         PRIMARY KEY,
    aggregate_id  UUID         NOT NULL,
    event_type    VARCHAR(255) NOT NULL,
    payload       JSONB        NOT NULL,
    partition_key VARCHAR(255) NOT NULL,
    status        VARCHAR(50)  NOT NULL DEFAULT 'PENDING'
                  CHECK (status IN ('PENDING', 'PROCESSING', 'PROCESSED', 'FAILED')),
    failed_reason TEXT,
    claimed_at    TIMESTAMPTZ,
    claim_token   UUID,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    published_at  TIMESTAMPTZ
);

-- FIFO claiming index: covers PENDING and PROCESSING for atomic lease and timeout recovery
-- Ordered by (created_at ASC, id ASC) for deterministic FIFO queue behavior
CREATE INDEX idx_outbox_claiming ON outbox(created_at ASC, id ASC)
    WHERE status IN ('PENDING', 'PROCESSING');

-- 7. Settled Trades — idempotency anchor for Wallet settlement
-- PRIMARY KEY (trade_id): 1 trade = exactly 1 settlement
-- UNIQUE (market_id, sequence): 1 market sequence slot = exactly 1 settlement
CREATE TABLE settled_trades (
    trade_id   UUID        PRIMARY KEY,
    market_id  VARCHAR(20) NOT NULL,
    sequence   BIGINT      NOT NULL,
    settled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_settled_trades_market_seq UNIQUE (market_id, sequence)
);

-- +goose Down
DROP TABLE IF EXISTS settled_trades;
DROP INDEX IF EXISTS idx_outbox_claiming;
DROP TABLE IF EXISTS outbox;
DROP INDEX IF EXISTS idx_transfers_wallet;
DROP TABLE IF EXISTS wallet_transfers;
DROP TYPE IF EXISTS transfer_status;
DROP TYPE IF EXISTS transfer_type;
DROP INDEX IF EXISTS idx_transactions_reference;
DROP INDEX IF EXISTS idx_transactions_wallet;
DROP TABLE IF EXISTS wallet_transactions;
DROP INDEX IF EXISTS idx_reservations_order;
DROP INDEX IF EXISTS idx_reservations_user;
DROP TABLE IF EXISTS wallet_reservations;
DROP INDEX IF EXISTS idx_wallets_user;
DROP TABLE IF EXISTS wallets;
DROP TABLE IF EXISTS supported_assets;
