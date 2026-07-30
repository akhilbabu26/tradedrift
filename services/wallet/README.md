# TradeDrift — Wallet Service

> **Status:** ✅ Complete (All layers implemented — Repository, Service, Handler, Server)
> This service is the financial ledger of the TradeDrift exchange. It owns all user balances, reservation records, and transaction history.

---

## Purpose

The Wallet Service is the single source of truth for money in TradeDrift. Every balance that exists, every fund lock placed, every settlement received — all of it lives here and only here.

No other service can directly modify a user's balance. If Order Service wants to lock funds, it calls this service. If Settlement Service wants to credit a buyer, it calls this service. This strict ownership boundary is what makes the financial invariants provable.

**The Wallet Service:**
- Creates and seeds user wallets during registration (called by Auth Service)
- Locks funds when an order is placed (called by Order Service)
- Returns locked funds when an order is cancelled (called by Order Service)
- Settles trades by crediting buyer and debiting seller (called by Settlement Service)
- Exposes balance views to the API Gateway (for the user dashboard)
- Publishes `UserTradeSettled` events to Kafka via the Transactional Outbox

---

## Why This Service Exists as a Separate Microservice

In a naive design, the Order Service could manage balances directly. But that creates several serious problems:

| Problem | What goes wrong |
|---|---|
| **Double-spend** | Two concurrent orders both read the same balance and both succeed — user spends money they don't have |
| **No audit trail** | Balance changes are scattered across services, reconstructing history is impossible |
| **Tight coupling** | Every service that touches money must understand every other service's logic |
| **No single invariant** | `available + reserved = total` is only enforceable if one service owns all three columns |

By isolating balance ownership into a dedicated service, we get a single place where:
- All balance mutations are serialized and auditable
- The `available_balance >= 0` invariant is enforced at the DB level
- The double-entry ledger (`wallet_transactions`) is always complete and consistent

---

## Architecture

### Chosen Pattern: Clean Architecture + Repository Pattern

```
┌─────────────────────────────────────────────────────┐
│                  gRPC Handler Layer                  │
│  (translates proto requests → service calls)         │
└──────────────────────┬──────────────────────────────┘
                       │
┌──────────────────────▼──────────────────────────────┐
│                  Service Layer                       │
│  (all business rules, idempotency, orchestration)    │
│                                                      │
│  initialize_wallet.go   reserve_funds.go             │
│  release_funds.go       settle_trade.go              │
│  get_balance.go         get_assets.go                │
└──────────────────────┬──────────────────────────────┘
                       │
┌──────────────────────▼──────────────────────────────┐
│              Repository Interface Layer              │
│  (contracts — no SQL, no implementation)             │
│                                                      │
│  WalletRepository    ReservationRepository           │
│  TransactionRepository  AssetRepository              │
│  OutboxRepository                                    │
└──────────────────────┬──────────────────────────────┘
                       │
┌──────────────────────▼──────────────────────────────┐
│           Postgres Implementation Layer              │
│  (concrete SQL implementations of each interface)    │
│                                                      │
│  postgres/wallet_repository.go                       │
│  postgres/reservation_repository.go                  │
│  postgres/transaction_repository.go                  │
│  postgres/asset_repository.go                        │
│  postgres/outbox_repository.go                       │
└──────────────────────┬──────────────────────────────┘
                       │
              ┌────────▼────────┐
              │   PostgreSQL    │
              │  (5 tables)     │
              └─────────────────┘
```

### Why Clean Architecture?

**Separation of concerns.** Each layer has exactly one job:

| Layer | Job | Knows about |
|---|---|---|
| Handler | Translate gRPC ↔ service types | Proto messages, gRPC error codes |
| Service | Business rules and orchestration | Repository interfaces only |
| Repository interface | Define what data operations exist | Domain structs only |
| Postgres implementation | Write SQL | pgx, PostgreSQL |

**What this buys:**
- The service layer can be unit tested with mock repositories — no real database needed
- The database can be swapped (e.g. to CockroachDB) by writing new implementations — zero service changes
- A new developer reading `reserve_funds.go` sees pure business logic — no SQL noise

### Why Repository Pattern?

The repository pattern puts an **interface** between the business logic and the database. Instead of:

```go
// BAD — service directly writes SQL
db.Exec("UPDATE wallets SET available_balance = available_balance - $1 WHERE id = $2", amount, id)
```

The service calls:
```go
// GOOD — service calls an interface method
s.walletRepo.MoveToReserved(ctx, walletID, amount)
```

The SQL lives in `postgres/wallet_repository.go`. The service never sees it.

**Benefits:**
- SQL changes don't require touching business logic files
- The same interface can have a test double (mock) for unit tests
- Reading the service layer reads like plain English business rules

### Why One File Per Use Case?

Each gRPC method has its own Go file:

```
initialize_wallet.go  →  InitializeWallet RPC
reserve_funds.go      →  ReserveFunds RPC
release_funds.go      →  ReleaseFunds RPC
settle_trade.go       →  SettleTrade RPC
get_balance.go        →  GetBalance + GetBalances RPCs
get_assets.go         →  GetSupportedAssets RPC
```

**Benefits:**
- Navigate directly to the file you need
- Git blame and diffs are scoped to one operation
- Code reviews are focused — a PR for `SettleTrade` only touches `settle_trade.go`
- Fewer merge conflicts — two developers can work on different operations simultaneously

This is the same convention used in the Auth Service (`register.go`, `login.go`, `password.go`, etc.)

---

## Key Features

### 1. Idempotency on Every State-Changing Operation

Every mutation is safe to call multiple times. Retried gRPC calls never create duplicate effects:

| Method | Idempotency Key | How it's enforced |
|---|---|---|
| `InitializeWallet` | `(user_id, asset_code)` | Check existing wallet before creating |
| `ReserveFunds` | `order_id` | `UNIQUE(order_id)` on `wallet_reservations` |
| `ReleaseFunds` | reservation `status` | Check `status != RELEASED` before crediting |
| `SettleTrade` | `(trade_id, SETTLEMENT, asset)` | `UNIQUE(reference_id, reference_type, asset)` on `wallet_transactions` |

### 2. Double-Entry Ledger

Every balance change is permanent and recorded. `wallet_transactions` is never updated or deleted — only inserted. The accounting invariant:

```
SUM(CREDIT) − SUM(DEBIT) = available_balance + reserved_balance
```

This means every user's balance is reconstructable from transaction history at any point in time.

### 3. Two-Bucket Balance Model

Each wallet has two balance fields:

```
available_balance  →  spendable right now
reserved_balance   →  locked by an active order
```

When a user places an order:
```
available -= amount   (moved to reserved)
reserved  += amount   (locked for this order)
```

When an order fills:
```
reserved  -= amount   (consumed)
total_balance -= amount  (money has left the account)
```

When an order cancels:
```
reserved  -= remaining   (unlocked)
available += remaining   (returned to spendable)
```

This prevents double-spending without distributed locks: two concurrent orders both see the same `available_balance`, but only one can successfully decrement it (the SQL `WHERE available >= amount` ensures atomicity).

### 4. Transactional Outbox Pattern

Kafka events (`UserTradeSettled`) are written to the `outbox` table **inside the same database transaction** as the balance change. A background publisher reads pending outbox rows and delivers them to Kafka.

This guarantees:
- If the DB commits → the event will eventually be published (even if Kafka is down for hours)
- If the DB rolls back → no event is published
- Events are never lost and never duplicated (published exactly once)

### 5. Decimal Precision

All balances are `DECIMAL(30,10)` in PostgreSQL and `string` in Go. This prevents floating-point rounding errors that would corrupt financial calculations:

```
// float64 — WRONG for finance:
0.1 + 0.2 = 0.30000000000000004

// DECIMAL(30,10) via string — CORRECT:
0.1 + 0.2 = 0.3000000000
```

---

## gRPC API

| Method | Called By | Purpose |
|---|---|---|
| `InitializeWallet` | Auth Service | Create wallets + seed USDT for new user |
| `ReserveFunds` | Order Service | Lock funds before an order goes OPEN |
| `ReleaseFunds` | Order Service | Return locked funds on order cancel |
| `SettleTrade` | Settlement Service | Credit buyer, debit seller after a match |
| `GetBalance` | API Gateway | Single asset balance for dashboard |
| `GetBalances` | API Gateway | All asset balances for dashboard |
| `GetSupportedAssets` | Market Service | Validate asset codes when creating markets |
| `Health` | Infrastructure | Liveness/readiness probe |

---

## Database Tables

| Table | Purpose |
|---|---|
| `supported_assets` | Platform asset registry — defines valid assets and seed amounts |
| `wallets` | Live balance state per (user, asset) |
| `wallet_reservations` | Fund locks per order (ACTIVE → CONSUMED/RELEASED) |
| `wallet_transactions` | Immutable ledger — every balance change, forever |
| `outbox` | Pending Kafka events (Transactional Outbox pattern) |
| `wallet_transfers` | Deposit/withdrawal lifecycle tracking |

See [migration/README.md](./migration/README.md) for full table documentation.

---

## Project Structure

```
services/wallet/
├── cmd/
│   └── server/
│       └── main.go                   ← gRPC server entrypoint
├── internal/
│   ├── repository/                   ← Repository interfaces (contracts)
│   │   ├── wallet.go                 ← WalletRepository interface + Wallet struct
│   │   ├── reservation.go            ← ReservationRepository interface
│   │   ├── transaction.go            ← TransactionRepository interface
│   │   ├── asset.go                  ← AssetRepository interface
│   │   ├── outbox.go                 ← OutboxRepository interface
│   │   ├── errors.go                 ← Domain sentinel errors (ErrNotFound, ErrDuplicate, etc.)
│   │   ├── constants.go              ← Typed string constants (statuses, reference types)
│   │   └── postgres/                 ← Postgres implementations
│   │       ├── wallet_repository.go
│   │       ├── reservation_repository.go
│   │       ├── transaction_repository.go
│   │       ├── asset_repository.go
│   │       └── outbox_repository.go
│   ├── service/                      ← Business logic (one file per operation)
│   │   ├── service.go                ← Service struct + constructor
│   │   ├── initialize_wallet.go      ← InitializeWallet RPC
│   │   ├── reserve_funds.go          ← ReserveFunds RPC
│   │   ├── release_funds.go          ← ReleaseFunds RPC
│   │   ├── settle_trade.go           ← SettleTrade RPC
│   │   ├── get_balance.go            ← GetBalance + GetBalances RPCs
│   │   └── get_assets.go             ← GetSupportedAssets RPC
│   └── handler/
│       └── handler.go                ← gRPC handler (proto ↔ service + error mapping)
└── migration/
    ├── 00001_create_wallet_core_tables.sql
    ├── 00002_create_wallet_transfer_tables.sql
    └── README.md                     ← Migration and table documentation
```

---

## Accounting Invariants

These must hold true at all times. The database enforces them with CHECK constraints:

1. `available_balance >= 0` — a user can never go below zero
2. `reserved_balance >= 0` — reserved funds are never negative
3. `reserved_amount - consumed_amount >= 0` — consumed can never exceed reserved
4. A reservation is released or consumed **exactly once** — enforced by status guard in `ReleaseFunds`
5. A given `trade_id` is settled **exactly once** — enforced by the unique constraint on `wallet_transactions`
6. A given `(user_id, asset)` receives `INITIAL_ALLOCATION` **exactly once** — enforced by the unique constraint

---

## Design Decisions

### Why synchronous wallet creation (not event-driven)?
Auth Service calls `InitializeWallet` gRPC **before** the user's status is set to `VERIFIED` and before tokens are issued. If wallet creation were async (event-driven), a user could log in before their wallet exists — every downstream service would then need to handle "no wallet yet" edge cases. Synchronous creation eliminates this window entirely.

### Why decimal strings instead of float?
`float64` cannot represent all decimal values exactly. In a financial system, rounding errors accumulate and corrupt balances. All amounts are passed as decimal strings (e.g. `"10000.0000000000"`) directly to PostgreSQL's `DECIMAL(30,10)` type, which has exact arithmetic.

### Why UUIDv7 not UUIDv4?
UUIDv7 encodes a timestamp prefix. IDs are chronologically sortable, which improves B-tree index performance for time-ordered queries. UUID v4 is fully random — no ordering benefit.

### Why outbox instead of direct Kafka publish?
Kafka publishing happens outside the database transaction. Without an outbox: if the service crashes after committing the DB but before publishing to Kafka, the event is permanently lost. With the outbox: the event row is committed atomically with the balance change. Even if Kafka is unreachable for hours, the background publisher will retry and deliver it.
