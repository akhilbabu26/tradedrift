-- +goose Up
-- SQL in this section is executed when the migration is applied.

-- 1. Update outbox status check constraint to include 'PROCESSING'
ALTER TABLE outbox DROP CONSTRAINT IF EXISTS outbox_status_check;
ALTER TABLE outbox ADD CONSTRAINT outbox_status_check
    CHECK (status IN ('PENDING', 'PROCESSING', 'PROCESSED', 'FAILED'));

-- 2. Add claimed_at column for lease tracking and timeout recovery
ALTER TABLE outbox ADD COLUMN IF NOT EXISTS claimed_at TIMESTAMPTZ;

-- 3. Add partial index for atomic claiming of PENDING and expired PROCESSING events
CREATE INDEX IF NOT EXISTS idx_outbox_claiming
    ON outbox(created_at)
    WHERE status IN ('PENDING', 'PROCESSING');

-- 4. Fix wallet_transactions uniqueness constraint:
-- Replace (reference_id, reference_type, asset) with (wallet_id, reference_id, reference_type)
-- so both seller and buyer receive their respective settlement ledger records without collision,
-- while guaranteeing per-wallet settlement idempotency against double debits.
ALTER TABLE wallet_transactions DROP CONSTRAINT IF EXISTS wallet_transactions_reference_id_reference_type_asset_key;
ALTER TABLE wallet_transactions ADD CONSTRAINT wallet_transactions_wallet_ref_type_key
    UNIQUE (wallet_id, reference_id, reference_type);

-- +goose Down
-- SQL in this section is executed when the migration is rolled back.
ALTER TABLE wallet_transactions DROP CONSTRAINT IF EXISTS wallet_transactions_wallet_ref_type_key;
ALTER TABLE wallet_transactions ADD CONSTRAINT wallet_transactions_reference_id_reference_type_asset_key
    UNIQUE (reference_id, reference_type, asset);

DROP INDEX IF EXISTS idx_outbox_claiming;
ALTER TABLE outbox DROP COLUMN IF EXISTS claimed_at;

ALTER TABLE outbox DROP CONSTRAINT IF EXISTS outbox_status_check;
ALTER TABLE outbox ADD CONSTRAINT outbox_status_check
    CHECK (status IN ('PENDING', 'PROCESSED', 'FAILED'));
