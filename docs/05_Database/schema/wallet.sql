-- TradeDrift Wallet Database Schema

CREATE TABLE supported_assets (
    asset_code     VARCHAR(10) PRIMARY KEY,
    asset_name     VARCHAR(50) NOT NULL,
    decimals       INT NOT NULL,
    is_enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    seed_amount    DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (seed_amount >= 0),
    display_order  INT NOT NULL
);

CREATE TABLE wallets (
    id                 UUID PRIMARY KEY,
    user_id            UUID NOT NULL, -- Application-level user reference (no cross-service FK)
    asset              VARCHAR(10) NOT NULL REFERENCES supported_assets(asset_code),
    available_balance  DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (available_balance >= 0),
    reserved_balance   DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (reserved_balance >= 0),
    is_frozen          BOOLEAN NOT NULL DEFAULT FALSE,
    frozen_at          TIMESTAMPTZ,
    frozen_by          VARCHAR(64),
    freeze_reason      TEXT,
    initial_balance    DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (initial_balance >= 0),
    total_balance      DECIMAL(30,10) GENERATED ALWAYS AS (available_balance + reserved_balance) STORED,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_wallets_user_asset UNIQUE (user_id, asset)
);

CREATE TABLE wallet_reservations (
    id                UUID PRIMARY KEY,
    order_id          UUID NOT NULL UNIQUE,
    user_id           UUID NOT NULL,
    asset             VARCHAR(10) NOT NULL,
    reserved_amount   DECIMAL(30,10) NOT NULL CHECK (reserved_amount >= 0),
    consumed_amount   DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (consumed_amount >= 0),
    remaining_amount  DECIMAL(30,10) GENERATED ALWAYS AS (reserved_amount - consumed_amount) STORED,
    status            VARCHAR(20) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'CONSUMED', 'RELEASED')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_reservation_amounts CHECK (consumed_amount <= reserved_amount)
);

CREATE TABLE wallet_transactions (
    id                UUID PRIMARY KEY,
    wallet_id         UUID NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    reference_id      UUID NOT NULL,
    reference_type    VARCHAR(30) NOT NULL CHECK (reference_type IN ('INITIAL_ALLOCATION', 'RESERVATION', 'SETTLEMENT', 'RELEASE', 'DEPOSIT', 'WITHDRAWAL')),
    transaction_type  VARCHAR(10) NOT NULL CHECK (transaction_type IN ('CREDIT', 'DEBIT')),
    asset             VARCHAR(10) NOT NULL,
    amount            DECIMAL(30,10) NOT NULL CHECK (amount >= 0),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_wallet_txn_ref UNIQUE (reference_id, reference_type, asset)
);

-- Idempotent type creations for transfers
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'transfer_type') THEN
        CREATE TYPE transfer_type AS ENUM ('DEPOSIT', 'WITHDRAWAL');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'transfer_status') THEN
        CREATE TYPE transfer_status AS ENUM ('PENDING', 'COMPLETED', 'FAILED');
    END IF;
END $$;

-- Transfers table schema (Deposits & Withdrawals)
CREATE TABLE wallet_transfers (
    id            UUID PRIMARY KEY,
    wallet_id     UUID NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
    type          transfer_type NOT NULL,
    amount        DECIMAL(30,10) NOT NULL CHECK (amount > 0),
    status        transfer_status NOT NULL DEFAULT 'PENDING',
    reference_id  VARCHAR(64) NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT    uq_transfer_ref UNIQUE(reference_id)
);

CREATE INDEX idx_transfers_wallet ON wallet_transfers(wallet_id);

-- Initial Seeding of Supported Assets
-- standard seeding is done via SQL at database provision time.
-- Runtime updates/additions are handled via standard SQL transactional data changes.
INSERT INTO supported_assets (asset_code, asset_name, decimals, is_enabled, seed_amount, display_order) VALUES
('USDT', 'Tether USD', 10, true, 10000.0000000000, 1),
('BTC', 'Bitcoin', 10, true, 0.0000000000, 2),
('ETH', 'Ethereum', 10, true, 0.0000000000, 3),
('SOL', 'Solana', 10, true, 0.0000000000, 4)
ON CONFLICT (asset_code) DO NOTHING;

