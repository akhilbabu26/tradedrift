-- +goose Up
-- SQL in this section is executed when the migration is applied.

-- Add a CHECK constraint that enforces total_balance = available_balance + reserved_balance.
-- This makes the accounting identity a database-level invariant rather than an application convention,
-- preventing any future code path from producing a diverged total_balance silently.
ALTER TABLE wallets
    ADD CONSTRAINT chk_wallet_total_balance
    CHECK (total_balance = available_balance + reserved_balance);

-- +goose Down
-- SQL in this section is executed when the migration is rolled back.
ALTER TABLE wallets
    DROP CONSTRAINT IF EXISTS chk_wallet_total_balance;
