-- +goose Up
-- SQL in this section is executed when the migration is applied.

-- Allow 'TOPUP' as a valid reference_type in wallet_transactions
ALTER TABLE wallet_transactions DROP CONSTRAINT IF EXISTS wallet_transactions_reference_type_check;
ALTER TABLE wallet_transactions ADD CONSTRAINT wallet_transactions_reference_type_check
    CHECK (reference_type IN (
        'INITIAL_ALLOCATION', 'RESERVATION', 'RELEASE',
        'SETTLEMENT', 'DEPOSIT', 'TOPUP', 'WITHDRAWAL'
    ));

-- +goose Down
-- SQL in this section is executed when the migration is rolled back.
ALTER TABLE wallet_transactions DROP CONSTRAINT IF EXISTS wallet_transactions_reference_type_check;
ALTER TABLE wallet_transactions ADD CONSTRAINT wallet_transactions_reference_type_check
    CHECK (reference_type IN (
        'INITIAL_ALLOCATION', 'RESERVATION', 'RELEASE',
        'SETTLEMENT', 'DEPOSIT', 'WITHDRAWAL'
    ));
