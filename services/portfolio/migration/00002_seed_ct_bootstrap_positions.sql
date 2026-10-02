-- +goose Up

-- portfolio_bootstrap_positions tracks authoritative opening positions for system accounts
-- (e.g. CT-001, MM-001) that were seeded directly into the Wallet service at bootstrap time,
-- bypassing the normal trade pipeline that Portfolio accounting relies on.
--
-- applied_at = NULL  → bootstrap position defined but not yet applied to holdings
-- applied_at = <ts>  → bootstrap position applied exactly once; subsequent startups are no-ops
--
-- PRIMARY KEY (user_id, asset_code, source) guarantees:
--   CT-001 + BTC + SYSTEM_BOOTSTRAP can only have one bootstrap record.
CREATE TABLE IF NOT EXISTS portfolio_bootstrap_positions (
    user_id    UUID           NOT NULL,
    asset_code VARCHAR(10)    NOT NULL,
    quantity   DECIMAL(30,10) NOT NULL CHECK (quantity >= 0),
    cost_basis DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (cost_basis >= 0),
    source     VARCHAR(50)    NOT NULL DEFAULT 'SYSTEM_BOOTSTRAP',
    created_at TIMESTAMPTZ    NOT NULL DEFAULT NOW(),
    applied_at TIMESTAMPTZ    NULL,
    PRIMARY KEY (user_id, asset_code, source)
);

-- CT-001 opening positions.
-- Quantities MUST mirror wallet/migration/00003_seed_ct_account.sql exactly.
-- USDT is deliberately excluded: Portfolio never stores USDT holdings (PI-1 invariant).
-- The corresponding wallet seed is: BTC=100, ETH=500, SOL=5000.
INSERT INTO portfolio_bootstrap_positions (user_id, asset_code, quantity, cost_basis, source)
VALUES
    ('00000000-0000-0000-0000-000000000002', 'BTC',   100.0000000000, 0, 'SYSTEM_BOOTSTRAP'),
    ('00000000-0000-0000-0000-000000000002', 'ETH',   500.0000000000, 0, 'SYSTEM_BOOTSTRAP'),
    ('00000000-0000-0000-0000-000000000002', 'SOL',  5000.0000000000, 0, 'SYSTEM_BOOTSTRAP')
ON CONFLICT (user_id, asset_code, source) DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS portfolio_bootstrap_positions;
