# TradeDrift — Wallet Service

> **Status:** ✅ Designed (V6, Final)
> Revision notes: consolidates prior revisions into one clean document. Fixes a real bug found during an earlier merge attempt — the `wallet_transactions` unique constraint must include `asset`, or a user can only ever receive one `INITIAL_ALLOCATION` total instead of one per seeded asset.

## Purpose

The Wallet Service is the financial ledger of TradeDrift. It owns user balances, reservation records, and transaction history, and executes financial operations requested by Authentication Service (wallet creation), Order Service (reservation), and Settlement Service (settlement). It never performs matching, never decides business workflow, and never initiates a settlement itself.

## Responsibilities

- Create wallet records and seed starting balances for new users (on request from Authentication Service).
- Maintain available and reserved balances per (user, asset).
- Maintain a per-order reservation ledger.
- Reserve funds for new orders (synchronous gRPC from Order Service).
- Release remaining reserved funds after cancellation.
- Execute trade settlement on request from Settlement Service.
- Maintain immutable wallet transaction history — every balance change, including initial seeding, is a ledger entry.
- Publish integration events via the Outbox Pattern.

## Out of Scope

- Order validation and lifecycle — owned by Order Service.
- Order matching — owned by Matching Engine.
- Settlement orchestration, retries, and dead-lettering — owned by Settlement Service.
- Portfolio and PnL calculations, market data, notifications.
- User account creation itself — Authentication Service owns the user record; Wallet Service only owns wallet rows for a `user_id` it's given.

---

## 1. `supported_assets` — Single Authoritative Schema

```sql
CREATE TABLE supported_assets (
    asset_code      VARCHAR(10) PRIMARY KEY,
    asset_name      VARCHAR(50),
    decimals        INT NOT NULL,
    is_enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    seed_amount     DECIMAL(30,10) NOT NULL DEFAULT 0,
    display_order   INT
);
```

| asset_code | seed_amount | display_order |
|---|---|---|
| USDT | 10000 | 1 |
| BTC | 0 | 2 |
| ETH | 0 | 3 |
| SOL | 0 | 4 |

- `decimals` dictates fixed-precision scale, used consistently across `wallets.available_balance`, `wallet_reservations` amounts, and `wallet_transactions.amount` — never a generic float, so a BTC amount at 8 decimals and a USDT amount at 2 decimals never round inconsistently against each other.
- `display_order` lets the dashboard show assets in a fixed, intentional order instead of alphabetical.
- Adding a new tradable asset is a **data change** (insert a row here), not a code or redeploy change.

## 2. InitializeWallet

```protobuf
rpc InitializeWallet(InitializeWalletRequest)
    returns (InitializeWalletResponse);

InitializeWalletRequest {
  user_id: UUID
}
```

**Behavior:**

- For each row in `supported_assets`, check whether a wallet already exists for `(user_id, asset_code)`.
- If missing: create the wallet row with `available_balance = seed_amount`, and insert a matching `INITIAL_ALLOCATION` transaction if `seed_amount > 0`.
- If already present: skip both the wallet row and the transaction for that asset — idempotency is **per (user, asset)**, not per user, so re-running initialization after a new asset is added only backfills the missing one(s).
- Returns success whether this call created new rows or found everything already in place.

> **Why per-asset, not per-user, idempotency:** If idempotency were checked at the user level, adding a new supported asset later would silently leave existing users without a wallet for it, since re-running initialization would no-op immediately. Checking per-asset means new assets backfill correctly for existing users.

## 3. `INITIAL_ALLOCATION` Transaction Type

Starting balances are ledger entries, not silent defaults — `InitializeWallet` inserts a `wallet_transactions` row alongside each new wallet with a non-zero seed.

```sql
wallet_transactions(
  id UUID, wallet_id UUID,
  reference_id UUID,      -- = user_id for this transaction type
  reference_type,         -- 'INITIAL_ALLOCATION'
  transaction_type,       -- 'CREDIT'
  asset, amount, created_at
)
UNIQUE (reference_id, reference_type, asset)
```

> **Bug fixed here:** A parallel draft of this document kept the old `UNIQUE(reference_id, reference_type)` constraint — without `asset` — while adding multi-asset seeding on top of it. Since `reference_id = user_id` and `reference_type = 'INITIAL_ALLOCATION'` are the same for every seeded asset, that constraint would allow only **one** `INITIAL_ALLOCATION` row per user, total — the second seeded asset (e.g. BTC, after USDT) would fail to insert. Including `asset` in the constraint is what makes "one allocation per user per asset" actually true at the database level, and is required for multi-asset seeding to function at all.

A user's transaction history begins with entries like `INITIAL_ALLOCATION +10,000 USDT` rather than the balance simply appearing — consistent with every other balance change already being a ledger entry, not a direct column update.

## 4. Why Synchronous (Option A), Not Event-Driven (Option B)

> **Decision:** Authentication Service calls `InitializeWallet` synchronously during registration, before generating tokens. Async wallet creation (publish `UserRegistered`, let Wallet Service consume it) was rejected for V1 — it introduces a window where a user exists and can log in but has no wallet yet, pushing "what if this user has no wallet" checks into every downstream service. Revisit only if registration throughput or Wallet Service availability makes the synchronous call a bottleneck.

## 5. Identifiers

All ID columns are PostgreSQL `UUID`, generated as **UUIDv7** by the owning service before the row is inserted — see `TradeDrift_ID_Correlation_Standard.md`. Wallet Service generates `wallet_id`, `reservation_id`, and `transaction_id` itself; it receives `order_id` and `user_id` from callers.

## High-Level Architecture

```
Client
  |
API Gateway
  |
Authentication Service --InitializeWallet(gRPC)--> Wallet Service
                                                        |
                                                   Read supported_assets
                                                        |
                                              Create wallets + INITIAL_ALLOCATION txns

Order Service --ReserveFunds(gRPC)--> Wallet Service
                                          |
                                     PostgreSQL
                              (Wallets + Reservations
                               + Transactions + Outbox)

Matching Engine --TradeExecuted(Kafka)--> Settlement Service
                                                |
                                        SettleTrade (gRPC)
                                                |
                                          Wallet Service
```

## Core Database Tables

- `wallets(id UUID, user_id UUID, asset, available_balance, reserved_balance, updated_at)`
- `wallet_reservations(id UUID, order_id UUID, user_id UUID, asset, reserved_amount, consumed_amount, remaining_amount, status, created_at)` — `UNIQUE(order_id)`
- `wallet_transactions(id UUID, wallet_id UUID, reference_id UUID, reference_type, transaction_type, asset, amount, created_at)` — `UNIQUE(reference_id, reference_type, asset)`
- `outbox(id UUID, aggregate_id UUID, event_type, payload, partition_key, status, created_at, published_at)`
- `supported_assets(asset_code PRIMARY KEY, asset_name, decimals, is_enabled, seed_amount, display_order)` — see [Section 1](#1-supported_assets--single-authoritative-schema), single definition, no other version exists.

## Reservation Lifecycle

```
ACTIVE
  |
  |-- Full fill -----> CONSUMED
  |-- Partial fill --> PARTIALLY_CONSUMED
  |                        |
  |                        |-- Further fills --> CONSUMED
  |                        `-- Cancel ---------> RELEASED
  `-- Cancel ---------------------------------> RELEASED
```

`remaining_amount = reserved_amount − consumed_amount` at all times. Release always returns exactly `remaining_amount` to `available_balance`.

## gRPC APIs

- `InitializeWallet(user_id)` — synchronous, called by Authentication Service during registration.
- `ReserveFunds(user_id, order_id, asset, amount)` — synchronous, called by Order Service before `OPEN`.
- `ReleaseFunds(order_id)` — releases `remaining_amount` of the named order's reservation.
- `SettleTrade(trade_id, buyer_id, seller_id, buy_order_id, sell_order_id, base_asset, quote_asset, price, quantity)`.
- `GetBalance(user_id, asset)`
- `GetBalances(user_id)`
- `Health()`

## REST (via grpc-gateway, dashboard-facing only)

- `GET /wallets/me`
- `GET /wallets/balances`
- `GET /wallets/transactions`

*Routed through the API Gateway exactly like Order and Market — same rate-limit / auth / gRPC-forward pipeline, no special-casing.*

## Settlement Flow

- Settlement Service calls `SettleTrade(...)` with the full signature above.
- Lock buyer's and seller's relevant reservation rows (`FOR UPDATE`), using `buy_order_id` / `sell_order_id`.
- **Buyer leg:** `consumed_amount += price × quantity` (quote reservation); credit `quantity` of `base_asset` to buyer's `available_balance`.
- **Seller leg:** `consumed_amount += quantity` (base reservation); credit `price × quantity` of `quote_asset` to seller's `available_balance`.
- Insert `wallet_transactions` rows for both legs (`transaction_type SETTLEMENT`), insert Outbox event (`TradeSettled`), commit atomically.

## Event Ownership: TradeSettled

Wallet Service publishes `TradeSettled` via its own Outbox immediately after `SettleTrade` commits, keeping the write-then-publish guarantee inside the same transactional boundary as the balance change. Portfolio Service and Notification Service consume it from Wallet Service's outbox-backed topic, not from Settlement Service.

## Idempotency & Consistency

- `UNIQUE(reference_id, reference_type, asset)` on `wallet_transactions` — covers settlement/reservation replays and multi-asset initial allocations correctly (see [Section 3](#3-initial_allocation-transaction-type) bug fix).
- `UNIQUE(order_id)` on `wallet_reservations` — one reservation per order.
- `InitializeWallet` idempotent per (user, asset), enforced by the transaction constraint above, not application logic alone.
- Row-level locking via `SELECT ... FOR UPDATE` on every path that mutates a reservation or wallet.

## Failure Handling

- Insufficient balance → `ReserveFunds` fails, no reservation created.
- `SettleTrade` failure → full rollback inside Wallet Service; Settlement Service retries with backoff, then dead-letters.
- Kafka unavailable → Outbox Publisher retries independently of the request/response path.

## Accounting Invariants

- `available_balance ≥ 0` at all times.
- `reserved_amount − consumed_amount ≥ 0` for every reservation row.
- A reservation is released or fully consumed exactly once.
- A given `trade_id` can only be settled once.
- A given (user, asset) can only receive one `INITIAL_ALLOCATION`, ever — enforced at the database level.

## Internal Package Structure

```
wallet-service/
  api/
  service/
  repository/
  grpc/
  kafka/
    outbox_publisher.go
    consumers.go
  validator/
  models/
  events/
  db/
```

## Future Extensions

- Deposits and withdrawals.
- Multi-currency wallets beyond the initial `supported_assets` set.
- Margin and futures wallets.
- Fee and rebate accounting, applied through Settlement Service's `SettleTrade` call.