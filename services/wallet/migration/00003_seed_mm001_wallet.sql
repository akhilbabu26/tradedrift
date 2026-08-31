-- +goose Up
-- MM-001 Market Maker System Account — Wallet Seed
-- This migration seeds the MM-001 system account wallets with initial inventory.
-- MM-001 is the automated liquidity engine market maker account.
-- user_id is a fixed deterministic UUID for the system MM account.
--
-- IMPORTANT: These balances are the authoritative starting inventory for the LE.
-- The LE reads these via Wallet Service gRPC GetBalances("MM-001-UUID").
-- The LE does NOT call ReserveFunds — it computes its own effective_available
-- by subtracting resting order commitments from wallet.available_balance.

-- Fixed UUID for the MM-001 system account wallet identity
-- Used by: Wallet Service (UUID FK), Settlement Service (buyer/seller UUID)
-- NOTE: Order Service uses the string "MM-001" as user_id (separate identity layer)
DO $$
DECLARE
    mm_uuid UUID := '00000000-0000-0000-0000-000000000001';
BEGIN

-- BTC wallet: initial inventory to seed the ask-side ladder
-- 12 ask levels × ~0.85 BTC each = ~10.2 BTC needed; seed with 100 BTC for headroom
INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, initial_balance, total_balance)
VALUES (
    gen_random_uuid(),
    mm_uuid,
    'BTC',
    100.0000000000,   -- available_balance: LE quotes against this (minus resting commitments)
    0.0000000000,     -- reserved_balance: LE bypasses ReserveFunds — always 0 for MM
    100.0000000000,
    100.0000000000
)
ON CONFLICT (user_id, asset) DO NOTHING;

-- ETH wallet: initial inventory for ETH-USDT ask-side ladder
INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, initial_balance, total_balance)
VALUES (
    gen_random_uuid(),
    mm_uuid,
    'ETH',
    500.0000000000,   -- 12 ask levels × ~1.5 ETH each = ~18 ETH needed; 500 ETH headroom
    0.0000000000,
    500.0000000000,
    500.0000000000
)
ON CONFLICT (user_id, asset) DO NOTHING;

-- SOL wallet: initial inventory for SOL-USDT ask-side ladder
INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, initial_balance, total_balance)
VALUES (
    gen_random_uuid(),
    mm_uuid,
    'SOL',
    5000.0000000000,  -- 12 ask levels × ~20 SOL each = ~240 SOL; 5000 SOL headroom
    0.0000000000,
    5000.0000000000,
    5000.0000000000
)
ON CONFLICT (user_id, asset) DO NOTHING;

-- USDT wallet: initial inventory for the bid-side ladder across all 3 markets
-- BTC bids:  12 levels × ~0.85 BTC × ~$96,450 ≈ $983,790
-- ETH bids:  12 levels × ~1.50 ETH × ~$2,781  ≈ $50,058
-- SOL bids:  12 levels × ~20 SOL  × ~$188     ≈ $45,120
-- Total needed: ~$1,078,968 — seed with $5,000,000 USDT for headroom
INSERT INTO wallets (id, user_id, asset, available_balance, reserved_balance, initial_balance, total_balance)
VALUES (
    gen_random_uuid(),
    mm_uuid,
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
    mm_uuid,
    'INITIAL_ALLOCATION',
    'CREDIT',
    w.asset,
    w.initial_balance
FROM wallets w
WHERE w.user_id = mm_uuid
  AND w.initial_balance > 0
ON CONFLICT (reference_id, reference_type, asset) DO NOTHING;

END $$;

-- +goose Down
-- Remove MM-001 system account wallets and transaction records
DO $$
DECLARE
    mm_uuid UUID := '00000000-0000-0000-0000-000000000001';
BEGIN
    DELETE FROM wallet_transactions WHERE reference_id = mm_uuid AND reference_type = 'INITIAL_ALLOCATION';
    DELETE FROM wallets WHERE user_id = mm_uuid;
END $$;
