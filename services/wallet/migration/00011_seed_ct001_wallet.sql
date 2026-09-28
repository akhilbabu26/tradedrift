-- +goose Up
-- CT-001 Controlled Taker System Account — Wallet Seed
-- This migration seeds the CT-001 system account wallets with initial trading inventory.
-- CT-001 is the Controlled Taker Service account for simulated taker activity.
-- user_id is a fixed deterministic UUID for the system CT account.
--
-- IMPORTANT: These balances allow CT-001 to pass pre-trade fund reservation in Order Service
-- and double-entry settlement in Settlement Service.

-- +goose StatementBegin
DO $$
DECLARE
    ct_uuid UUID := '00000000-0000-0000-0000-000000000002';
BEGIN

-- BTC wallet: initial inventory to support SELL taker orders
INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, initial_balance, total_balance)
VALUES (
    gen_random_uuid(),
    ct_uuid,
    'BTC',
    100.0000000000,
    0.0000000000,
    100.0000000000,
    100.0000000000
)
ON CONFLICT (user_id, asset) DO NOTHING;

-- ETH wallet: initial inventory to support ETH SELL taker orders
INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, initial_balance, total_balance)
VALUES (
    gen_random_uuid(),
    ct_uuid,
    'ETH',
    500.0000000000,
    0.0000000000,
    500.0000000000,
    500.0000000000
)
ON CONFLICT (user_id, asset) DO NOTHING;

-- SOL wallet: initial inventory to support SOL SELL taker orders
INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, initial_balance, total_balance)
VALUES (
    gen_random_uuid(),
    ct_uuid,
    'SOL',
    5000.0000000000,
    0.0000000000,
    5000.0000000000,
    5000.0000000000
)
ON CONFLICT (user_id, asset) DO NOTHING;

-- USDT wallet: initial inventory to support BUY taker orders across all markets
INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, initial_balance, total_balance)
VALUES (
    gen_random_uuid(),
    ct_uuid,
    'USDT',
    5000000.0000000000,
    0.0000000000,
    5000000.0000000000,
    5000000.0000000000
)
ON CONFLICT (user_id, asset) DO NOTHING;

-- Record initial allocation transactions for audit trail
INSERT INTO wallet_transactions (id, wallet_id, reference_id, reference_type, transaction_type, asset, amount)
SELECT
    gen_random_uuid(),
    w.id,
    ct_uuid,
    'INITIAL_ALLOCATION',
    'CREDIT',
    w.asset,
    w.initial_balance
FROM wallets w
WHERE w.user_id = ct_uuid
  AND w.initial_balance > 0
ON CONFLICT (wallet_id, reference_id, reference_type) DO NOTHING;

END $$;
-- +goose StatementEnd

-- +goose Down
-- Remove CT-001 system account wallets and transaction records
-- +goose StatementBegin
DO $$
DECLARE
    ct_uuid UUID := '00000000-0000-0000-0000-000000000002';
BEGIN
    DELETE FROM wallet_transactions WHERE reference_id = ct_uuid AND reference_type = 'INITIAL_ALLOCATION';
    DELETE FROM wallets WHERE user_id = ct_uuid;
END $$;
-- +goose StatementEnd
