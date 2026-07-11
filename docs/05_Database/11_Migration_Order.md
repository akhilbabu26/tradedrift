# TradeDrift — Database Migration Order

> **Status:** ✅ Frozen (V1.0)
> **Document:** 11_Migration_Order.md
> **Directory:** docs/05_Database/
> **Last Updated:** July 2026

---

## 1. Migration Dependency Sequence

To prevent referential lookup errors, schema bootstrap mismatches, or dependency cycles, database migrations must be executed in the following chronological order:

```
[ Extensions ]
      │
      ▼
   [ Auth ]
      │
      ▼
  [ Wallet ]
      │
      ▼
  [ Market ]
      │
      ▼
   [ Order ] ──► [ Settlement ]
                    │
         ┌──────────┴──────────┐
         ▼                     ▼
   [ Portfolio ]            [ Trade ] ──► [ Notification ]
```

---

## 2. Sequence Rationale

### Step 1: Database Extensions
- **Dependencies:** None.
- **Goal:** Enable required procedural extensions (e.g. `uuid-ossp` or crypto libraries) within PostgreSQL before any tables are initialized.

### Step 2: Auth Database
- **Dependencies:** Extensions.
- **Goal:** Provision user tables. The `user_id` identifier generated here acts as the primary identity key across all other down-stream service databases.

### Step 3: Wallet Database
- **Dependencies:** Auth.
- **Goal:** Wallets can only be initialized once a valid `user_id` is created by the Auth service. Credits (seed allocations) must exist before trading begins.

### Step 4: Market Database
- **Dependencies:** Wallet.
- **Goal:** Defines the enabled tickers and trading pair limitations (e.g. `BTC-USDT` tick constraints) that govern order validation rules.

### Step 5: Order Database
- **Dependencies:** Wallet & Market.
- **Goal:** Orders require validation against active market pairs (from Market DB) and balance checks/reservations (from Wallet DB) before they can be persisted.

### Step 6: Settlement Database
- **Dependencies:** Order.
- **Goal:** Settle matches executed by the Matching Engine.

### Step 7: Read Projections (Portfolio, Trade, Notification)
- **Dependencies:** Settlement.
- **Goal:** Portfolio balances, trade histories, and user inbox alerts are generated directly in response to settled trade updates and order fills.
