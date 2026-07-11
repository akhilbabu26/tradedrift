# TradeDrift — Wallet Database Design

> **Status:** ✅ Frozen (V1.0)
> **Document:** 03_Wallet_Database.md
> **Directory:** docs/07_Database/
> **Last Updated:** July 2026

---

## 1. Purpose

The Wallet Database serves as the core financial ledger of the platform, tracking balances, credit/debit transaction records, currency settings, and active order reservations.

---

## 2. Table Schemas

### 2.1 Table: `supported_assets`
```sql
CREATE TABLE supported_assets (
    asset_code     VARCHAR(10) PRIMARY KEY,
    asset_name     VARCHAR(50) NOT NULL,
    decimals       INT NOT NULL,
    is_enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    seed_amount    DECIMAL(30,10) NOT NULL DEFAULT 0,
    display_order  INT NOT NULL
);
```

### 2.2 Table: `wallets`
```sql
CREATE TABLE wallets (
    id                 UUID PRIMARY KEY,                      -- UUIDv7
    user_id            UUID NOT NULL,                         -- Logical user reference
    asset              VARCHAR(10) NOT NULL REFERENCES supported_assets(asset_code),
    available_balance  DECIMAL(30,10) NOT NULL DEFAULT 0,
    reserved_balance   DECIMAL(30,10) NOT NULL DEFAULT 0,
    is_frozen          BOOLEAN NOT NULL DEFAULT FALSE,
    frozen_at          TIMESTAMPTZ,
    frozen_by          VARCHAR(64),
    freeze_reason      TEXT,
    initial_balance    DECIMAL(30,10) NOT NULL DEFAULT 0,
    total_balance      DECIMAL(30,10) GENERATED ALWAYS AS (available_balance + reserved_balance) STORED,
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_wallets_user_asset UNIQUE (user_id, asset)
);
```

### 2.3 Table: `wallet_reservations`
```sql
CREATE TABLE wallet_reservations (
    id                UUID PRIMARY KEY,                      -- UUIDv7
    order_id          UUID NOT NULL UNIQUE,                  -- Logically unique per order
    user_id           UUID NOT NULL,
    asset             VARCHAR(10) NOT NULL,
    reserved_amount   DECIMAL(30,10) NOT NULL,
    consumed_amount   DECIMAL(30,10) NOT NULL DEFAULT 0,
    remaining_amount  DECIMAL(30,10) GENERATED ALWAYS AS (reserved_amount - consumed_amount) STORED,
    status            VARCHAR(20) NOT NULL DEFAULT 'ACTIVE', -- 'ACTIVE', 'CONSUMED', 'RELEASED'
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### 2.4 Table: `wallet_transactions`
```sql
CREATE TABLE wallet_transactions (
    id                UUID PRIMARY KEY,                      -- UUIDv7
    wallet_id         UUID NOT NULL REFERENCES wallets(id),
    reference_id      UUID NOT NULL,                         -- order_id, trade_id, or transfer_id
    reference_type    VARCHAR(30) NOT NULL,                  -- 'INITIAL_ALLOCATION', 'RESERVATION', 'SETTLEMENT', 'TRANSFER'
    transaction_type  VARCHAR(10) NOT NULL,                  -- 'CREDIT', 'DEBIT'
    asset             VARCHAR(10) NOT NULL,
    amount            DECIMAL(30,10) NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_wallet_txn_ref UNIQUE (reference_id, reference_type, asset)
);
```

---

## 3. Query Design & Expected Patterns

### 3.1 Get User Balance (Query)
```sql
SELECT available_balance, reserved_balance, is_frozen 
FROM wallets 
WHERE user_id = $1 AND asset = $2;
```
*Index support:* Implicit index from the `uq_wallets_user_asset` constraint.

### 3.2 Reserve Funds for Limit Order (Transaction)
```sql
BEGIN;

-- 1. Lock wallet row and check freeze status
SELECT id, available_balance, is_frozen 
FROM wallets 
WHERE user_id = $1 AND asset = $2 
FOR UPDATE;

-- 2. Deduct available, increase reserved
UPDATE wallets 
SET available_balance = available_balance - $3,
    reserved_balance = reserved_balance + $3,
    updated_at = NOW()
WHERE id = $4;

-- 3. Write reservation row
INSERT INTO wallet_reservations (id, order_id, user_id, asset, reserved_amount) 
VALUES ($5, $6, $1, $2, $3);

-- 4. Write transaction log
INSERT INTO wallet_transactions (id, wallet_id, reference_id, reference_type, transaction_type, asset, amount)
VALUES ($7, $4, $6, 'RESERVATION', 'DEBIT', $2, $3);

COMMIT;
```

### 3.3 Settle Trade (Transaction with sorted locks)
To settle a match between a buyer and seller, we sort `buyReservationID` and `sellReservationID` in memory and lock:
```sql
BEGIN;

-- 1. Sort locks lexicographically and locking reservation rows
SELECT id, status, remaining_amount FROM wallet_reservations WHERE id = $1 FOR UPDATE;
SELECT id, status, remaining_amount FROM wallet_reservations WHERE id = $2 FOR UPDATE;

-- 2. Lock both user wallet rows in sorted order to prevent deadlocks
SELECT id, available_balance, reserved_balance FROM wallets WHERE id = $3 FOR UPDATE;
SELECT id, available_balance, reserved_balance FROM wallets WHERE id = $4 FOR UPDATE;

-- 3. Perform adjustments: Deduct reserved from buyer, credit available to seller
-- ... perform balance calculations ...
-- 4. Write two wallet_transactions rows (idempotent reference: trade_id)
-- 5. Write two outbox rows for UserTradeSettled (one buyer, one seller)

COMMIT;
```
*Index support:* Lexicographical sorting on UUID strings handles deadlock prevention natively in Go application memory.
