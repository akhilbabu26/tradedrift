# Settlement Service — Database Migrations (`migration/`)

> **Directory:** `services/settlement/migration/`  
> **Migration Tool:** `github.com/pressly/goose/v3`  
> **Database:** PostgreSQL (`tradedrift_settlement`)  
> **Applied By:** `main.go` at startup via `platform/postgres.RunMigrations`

---

## 1. Purpose

The `migration/` directory contains all SQL schema definitions for the Settlement Service. Migrations are sequential, version-numbered SQL files applied by **goose** automatically each time the service starts. The service will not process any Kafka events until all pending migrations succeed.

---

## 2. Directory Structure

```
services/settlement/migration/
├── 00001_create_settled_trades.sql   ← Core ledger table, constraints, and indexes
└── README.md                         ← This file
```

---

## 3. How Migrations Are Applied

`main.go` calls:
```go
postgres.RunMigrations(cfg.PostgresDSN, cfg.MigrationsDir)
```

**Why at startup?**  
Applying migrations on startup eliminates a separate deployment step. If the schema is already at the latest version, goose is a no-op. If a migration fails (e.g. naming conflict, constraint violation), the service exits before the Kafka consumer starts — preventing processing against a broken schema.

**Why goose?**  
goose stores its version state in a `goose_db_version` table inside the same database. Each migration file has a `-- +goose Up` section (applied on upgrade) and a `-- +goose Down` section (applied on rollback), making rollbacks safe and reversible.

---

## 4. Migration: `00001_create_settled_trades.sql`

### Table: `settled_trades`

The `settled_trades` table is the **settlement ledger** — a durable, append-style record of every trade that has been processed by this service.

| Column | Type | Constraint | Description |
|---|---|---|---|
| `trade_id` | `UUID` | **PRIMARY KEY** | Idempotency key — comes from the Matching Engine, unique per matched trade |
| `buyer_id` | `UUID` | NOT NULL | Buyer's user ID — used by Wallet to identify whose balance to credit |
| `seller_id` | `UUID` | NOT NULL | Seller's user ID — used by Wallet to identify whose balance to debit |
| `buy_order_id` | `UUID` | NOT NULL | The buy order that was matched |
| `sell_order_id` | `UUID` | NOT NULL | The sell order that was matched — Wallet uses this to locate the seller's reservation |
| `market_id` | `VARCHAR(32)` | NOT NULL | e.g. `"BTC-USDT"` |
| `base_asset` | `VARCHAR(16)` | NOT NULL | e.g. `"BTC"` — the asset being bought/sold |
| `quote_asset` | `VARCHAR(16)` | NOT NULL | e.g. `"USDT"` — the asset used for payment |
| `price` | `DECIMAL(30,10)` | NOT NULL, > 0 | Trade execution price (maker's price) |
| `quantity` | `DECIMAL(30,10)` | NOT NULL, > 0 | Trade quantity in base asset |
| `status` | `VARCHAR(16)` | NOT NULL, DEFAULT `'PENDING'`, CHECK | Current settlement phase — see status flow below |
| `executed_at` | `TIMESTAMPTZ` | NOT NULL | Timestamp from the Matching Engine when the trade was matched |
| `created_at` | `TIMESTAMPTZ` | NOT NULL, DEFAULT `NOW()` | When **this service** registered the trade — used for stale PENDING detection |
| `settled_at` | `TIMESTAMPTZ` | NULL | Set to `NOW()` when `status` transitions to `'SETTLED'` |

---

### Why `trade_id` as `PRIMARY KEY` (not a surrogate `id`)?

`trade_id` is already a globally unique UUID assigned by the Matching Engine. Using it as the primary key means:
- **No surrogate key overhead** — one less index
- **`ON CONFLICT (trade_id) DO NOTHING`** in `INSERT` works directly on the primary key
- **`UPDATE ... WHERE trade_id = $1`** uses the primary key index for O(1) lookup

---

### Why `DECIMAL(30,10)` for `price` and `quantity`?

| Precision | Meaning |
|---|---|
| `30` total digits | Supports prices up to `99,999,999,999,999,999,999.9999999999` — sufficient for any real-world asset |
| `10` decimal places | Covers 8 decimal places (Bitcoin standard) with 2 places of margin |

Using `DECIMAL` (exact numeric) instead of `FLOAT` (IEEE 754) eliminates rounding errors in financial calculations. The Matching Engine serializes prices as exact decimal strings — this type preserves that exactness in the DB.

---

### Why `CHECK (status IN ('PENDING', 'SETTLED'))`?

This is database-level protection. Without the CHECK constraint, any value (e.g. `"FAILED"`, `"COMPLETED"`, `"SETTLED_WRONG"`) could be inserted by a bug. The constraint makes invalid states impossible at the storage layer — independent of the Go code.

---

### Why `created_at` vs `executed_at` for stale detection?

`executed_at` = when the **Matching Engine** matched the trade (before Kafka delivery).

A trade event can sit in a Kafka partition for minutes (e.g. consumer group rebalance, lag catch-up). If the recovery goroutine used `executed_at`, it would immediately flag these as stale even though settlement has not yet had a chance to start.

`created_at = NOW()` = when **this service** first registered the trade. "Older than 60 seconds" means the trade has been stuck *inside the settlement system* for 60 seconds — which is the correct signal for a crash between Phase 2 and Phase 3.

---

### Indexes

```sql
-- Buyer/seller audit: "show me all trades for user X"
CREATE INDEX idx_settled_trades_buyer  ON settled_trades(buyer_id);
CREATE INDEX idx_settled_trades_seller ON settled_trades(seller_id);

-- Recovery goroutine: fast scan for stale PENDING rows
-- Partial index — only indexes rows WHERE status = 'PENDING'.
-- As trades are settled, rows move out of the partial index automatically.
-- This keeps the index tiny even at millions of rows.
CREATE INDEX idx_settled_trades_pending ON settled_trades(created_at)
    WHERE status = 'PENDING';
```

**Why a partial index?**  
Once a trade is `SETTLED`, the recovery goroutine never needs to see it again. A partial index on `WHERE status = 'PENDING'` means only in-flight trades are indexed — the index stays very small regardless of total table size, making recovery scans O(stuck trades), not O(all trades).

---

### Status Transition

```
┌──────────────────────────────────────────────────────────────────────┐
│                    settled_trades state machine                       │
│                                                                        │
│  Phase 1 (INSERT)                Phase 3 (UPDATE)                     │
│  trade_id received          ─────────────────────────────────────▶    │
│  via Kafka                  Wallet.SettleTrade confirmed              │
│        │                                                               │
│        ▼                                                               │
│   status = 'PENDING'    ──────────────────▶   status = 'SETTLED'     │
│   settled_at = NULL                           settled_at = NOW()      │
│                                                                        │
│   (Recovery scans here)                       (Never revisited)       │
└──────────────────────────────────────────────────────────────────────┘
```

---

## 5. Running Migrations Manually

```bash
# Apply all pending migrations
goose -dir migration postgres \
  "postgres://postgres:123@localhost:5432/tradedrift_settlement?sslmode=disable" up

# Check migration version status
goose -dir migration postgres \
  "postgres://postgres:123@localhost:5432/tradedrift_settlement?sslmode=disable" status

# Roll back the last migration
goose -dir migration postgres \
  "postgres://postgres:123@localhost:5432/tradedrift_settlement?sslmode=disable" down
```

> **Note:** Replace the DSN with values from your `.env` file. Never use the default DSN in production.

---

## 6. Adding a New Migration

```bash
# Creates services/settlement/migration/00002_<name>.sql
goose -dir migration create <name> sql
```

Then edit the generated file:
```sql
-- +goose Up
ALTER TABLE settled_trades ADD COLUMN ...;

-- +goose Down
ALTER TABLE settled_trades DROP COLUMN ...;
```

Commit the file — it will be applied automatically on next service startup.
