# TradeDrift — Wallet Service

> **Status:** ✅ Designed (V10, Final)
> Revision notes: V10 specifies the first-class deposit and withdrawal lifecycle, including the `wallet_transfers` database schema, execution state machines, outbox event definitions, and gRPC endpoints to prevent integration and funding discrepancies.

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
- **Treat every state-changing gRPC method as idempotent on its natural key, and say so explicitly for each one** — a redelivered request must return the same success response as the original, never a false failure and never a duplicate effect.

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

> **Bug fixed in V6:** A parallel draft of this document kept the old `UNIQUE(reference_id, reference_type)` constraint — without `asset` — while adding multi-asset seeding on top of it. Since `reference_id = user_id` and `reference_type = 'INITIAL_ALLOCATION'` are the same for every seeded asset, that constraint would allow only **one** `INITIAL_ALLOCATION` row per user, total — the second seeded asset (e.g. BTC, after USDT) would fail to insert. Including `asset` in the constraint is what makes "one allocation per user per asset" actually true at the database level, and is required for multi-asset seeding to function at all.

A user's transaction history begins with entries like `INITIAL_ALLOCATION +10,000 USDT` rather than the balance simply appearing — consistent with every other balance change already being a ledger entry, not a direct column update.

## 4. Why Synchronous (Option A), Not Event-Driven (Option B)

> **Decision:** Authentication Service calls `InitializeWallet` synchronously during email verification, before the user status is set to `VERIFIED` and tokens are issued. Async wallet creation (publish `UserRegistered`, let Wallet Service consume it) was rejected for V1 — it introduces a window where a user exists and can log in but has no wallet yet, pushing "what if this user has no wallet" checks into every downstream service. Revisit only if registration throughput or Wallet Service availability makes the synchronous call a bottleneck.

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

- `wallets(id UUID, user_id UUID, asset, available_balance, reserved_balance, is_frozen BOOLEAN, frozen_at TIMESTAMPTZ, frozen_by VARCHAR(64), freeze_reason TEXT, initial_balance DECIMAL, total_balance DECIMAL, updated_at)`
- `wallet_reservations(id UUID, order_id UUID, user_id UUID, asset, reserved_amount, consumed_amount, remaining_amount, status, created_at)` — `UNIQUE(order_id)`
- `wallet_transactions(id UUID, wallet_id UUID, reference_id UUID, reference_type, transaction_type, asset, amount, created_at)` — `UNIQUE(reference_id, reference_type, asset)`
- `outbox(id UUID, aggregate_id UUID, event_type, payload, partition_key, status, failed_reason TEXT, created_at, published_at)`
- `supported_assets(asset_code PRIMARY KEY, asset_name, decimals, is_enabled, seed_amount, display_order)` — see [Section 1](#1-supported_assets--single-authoritative-schema), single definition, no other version exists.

### 6.1 `reference_type` / `transaction_type` map (new in V7)

Every state-changing gRPC method writes `wallet_transactions` rows through the same table and the same `UNIQUE(reference_id, reference_type, asset)` constraint. This table makes explicit what each call writes, so it's easy to confirm the idempotency constraint actually covers every mutation path the same way:

| gRPC Method | `reference_id` | `reference_type` | `transaction_type` | Notes |
|---|---|---|---|---|
| `InitializeWallet` | `user_id` | `INITIAL_ALLOCATION` | `CREDIT` | One row per seeded asset |
| `ReserveFunds` | `order_id` | `RESERVATION` | `DEBIT` (available → reserved) | One row per reservation |
| `ReleaseFunds` | `order_id` | `RELEASE` | `CREDIT` (reserved → available) | One row per release; see §8.3 for idempotency |
| `SettleTrade` | `trade_id` | `SETTLEMENT` | `CREDIT` | Two rows per trade — one per leg, differing by `asset` (see §7, §8.1) |

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

`remaining_amount = reserved_amount − consumed_amount` at all times. Release always returns exactly `remaining_amount` to `available_balance`, and only ever once (see §8.3).

## gRPC APIs

- `InitializeWallet(user_id)` — synchronous, called by Authentication Service during email verification.
- `ReserveFunds(user_id, order_id, asset, amount)` — synchronous, called by Order Service before `OPEN`.
- `ReleaseFunds(order_id)` — releases `remaining_amount` of the named order's reservation.
- `SettleTrade(trade_id, buyer_id, seller_id, buy_order_id, sell_order_id, base_asset, quote_asset, price, quantity, market_id)`.
- `GetBalance(user_id, asset)`
- `GetBalances(user_id)`
- `GetSupportedAssets()` — read-only gRPC endpoint returning list of supported assets (code, name, decimals, seeding details). Used by Market Service to validate base/quote assets.
- `Health()`

All four state-changing methods above are idempotent on their natural key. See §8 for the explicit per-method behavior.

## REST (via grpc-gateway, dashboard-facing only)

- `GET /wallets/me`
- `GET /wallets/balances`
- `GET /wallets/transactions`

*Routed through the API Gateway exactly like Order and Market — same rate-limit / auth / gRPC-forward pipeline, no special-casing.*

## 7. Settlement Flow

- Settlement Service calls `SettleTrade(...)` with the full signature above.
- **Idempotency check (new in V7 — see §8.1):** before doing anything else, check whether a `wallet_transactions` row already exists for `(trade_id, 'SETTLEMENT', <either asset>)`. If so, this is a redelivery of an already-settled trade — return `SettleTradeResponse{success: true}` immediately, no locks taken, no balances touched.
- Otherwise, lock buyer's and seller's relevant reservation rows (`FOR UPDATE`), using `buy_order_id` / `sell_order_id`.
- **Buyer leg:** `consumed_amount += price × quantity` (quote reservation); credit `quantity` of `base_asset` to buyer's `available_balance`.
- **Seller leg:** `consumed_amount += quantity` (base reservation); credit `price × quantity` of `quote_asset` to seller's `available_balance`.
- Insert `wallet_transactions` rows for both legs (`transaction_type CREDIT`, `reference_type SETTLEMENT`, `reference_id = trade_id`), insert Outbox event (`TradeSettled`), commit atomically.
- If the insert of either leg's `wallet_transactions` row hits the `UNIQUE(reference_id, reference_type, asset)` constraint (a concurrent duplicate call that raced past the upfront check), catch the unique-violation, roll back the balance mutation for that call, and return `SettleTradeResponse{success: true}` — same as the upfront-check path. **A unique-violation on this constraint is a success signal, not an error, for this endpoint.**

## Event Ownership: UserTradeSettled

Wallet Service publishes two separate `UserTradeSettled` events via its own Outbox immediately after `SettleTrade` commits (one for the buyer, one for the seller), keeping the write-then-publish guarantee inside the same transactional boundary as the balance change. The outbox entries are written with `partition_key = user_id` (so buyer's event is partitioned by `buyer_id` and seller's event is partitioned by `seller_id`). Portfolio Service, Notification Service, and Trade Service consume them from Wallet Service's outbox-backed topic `user-trades.settled.v1`, **not from Settlement Service**.

**`UserTradeSettled` payload fields:**

| Field | Type | Source |
|---|---|---|
| `trade_id` | UUID | Matching Engine (UUIDv7) |
| `user_id` | UUID | Recipient User ID (buyer_id or seller_id) |
| `side` | VARCHAR(10) | "BUYER" | "SELLER" |
| `order_id` | UUID | Recipient Order ID (buy_order_id or sell_order_id) |
| `market_id` | VARCHAR(20) | from `TradeExecuted` via Settlement Service |
| `base_asset` | VARCHAR(16) | from `TradeExecuted` |
| `quote_asset` | VARCHAR(16) | from `TradeExecuted` |
| `price` | DECIMAL(30,10) | from `TradeExecuted` |
| `quantity` | DECIMAL(30,10) | from `TradeExecuted` |
| `settled_at` | TIMESTAMPTZ | Wallet Service clock — time `SettleTrade` committed |

> **`market_id` added in V8:** Required by Trade Service ([Trade_Service.md](../10_Trade_Service/Trade_Service.md)) for its `(market_id, executed_at DESC)` index, which powers `GET /markets/{id}/trades`. Settlement Service passes `market_id` from the `TradeExecuted` event payload into the `SettleTrade` gRPC call.

> **Settlement Service publishes no Kafka events.** After `SettleTrade` returns successfully, Settlement Service only updates its local `settled_trades.status` to `SETTLED` and acknowledges the Kafka consumer offset. It has no outbox table and writes to no Kafka topics. `UserTradeSettled` (from this service) is the single authoritative source for all downstream consumers. See [Settlement_Service.md § SI-4](../09_Settlement_Service/Settlement_Service.md).

> **Note on idempotent replays and `UserTradeSettled`:** when `SettleTrade` short-circuits on an already-settled `trade_id` (either check in §7), it does **not** re-publish `UserTradeSettled` or insert a second Outbox row — the event was already published the one time this trade actually settled. Idempotent-success means "no new effects," not "replay the effects."

## 8. Idempotency & Consistency

Every state-changing gRPC method is idempotent on its natural key. The database constraint alone only *detects* a duplicate — each handler below states explicitly what it *does* with that detection, which is what makes the guarantee real rather than assumed.

### 8.1 `SettleTrade` — idempotent on `trade_id`

Detection: `UNIQUE(reference_id, reference_type, asset)` on `wallet_transactions`, keyed on `(trade_id, 'SETTLEMENT', asset)`.
Behavior: **return `success: true`**, not an error — whether detected via the upfront check or via a caught unique-violation during insert (§7). This is the fix this revision makes explicit: Settlement Service's own retry/backoff/dead-letter logic depends on genuinely-idempotent calls returning success, not a generic failure that would cause a correctly-settled trade to be needlessly retried and eventually dead-lettered.

### 8.2 `ReserveFunds` — idempotent on `order_id`

Detection: `UNIQUE(order_id)` on `wallet_reservations`.
Behavior: if a reservation already exists for `order_id`, **do not treat this as an error.** Return the existing reservation's details in the response (same `asset`, `reserved_amount` as originally created) rather than surfacing the constraint violation to Order Service. If the incoming request's `asset`/`amount` differ from the existing row (which should never legitimately happen for the same `order_id`), log a warning and still return the existing reservation — the first successful reservation is authoritative.

### 8.3 `ReleaseFunds` — idempotent on reservation `status`

Detection: no separate constraint needed — the reservation's own `status` column is the guard. Lock the row with `FOR UPDATE` first.
Behavior: if `status` is already `RELEASED` or `CONSUMED`, **return success immediately and do not credit `available_balance` again.** Only transition `ACTIVE`/`PARTIALLY_CONSUMED` → `RELEASED` and credit `remaining_amount` when the row is in one of those two states. This is the fix this revision makes explicit: without this check, a redelivered cancel (Order Service retry, at-least-once messaging) would credit the same `remaining_amount` back twice, silently manufacturing funds — directly violating the Accounting Invariant that a reservation is released exactly once.

### 8.4 `InitializeWallet` — idempotent on `(user_id, asset)`

Unchanged from V6 — see §2. Included here for completeness, since it's the one endpoint that already documented this behavior explicitly and was the template for §8.1–8.3.

### Supporting constraints

- `UNIQUE(reference_id, reference_type, asset)` on `wallet_transactions` — covers settlement/reservation/release replays and multi-asset initial allocations correctly (see §3 bug fix).
- `UNIQUE(order_id)` on `wallet_reservations` — one reservation per order.
- Row-level locking via `SELECT ... FOR UPDATE` on every path that mutates a reservation or wallet.
- **Lock ordering note:** `SettleTrade` locks both the buyer's and seller's reservation rows in the same transaction. Under the current one-goroutine-per-market-partition concurrency model, no two concurrent `SettleTrade` calls can ever contend for the same pair of reservations, so lock ordering is not currently a deadlock risk. If settlement concurrency is increased in a future revision (e.g. parallel settlement across trades within a market), lock acquisition must be ordered consistently (e.g. always by ascending `reservation_id`) to prevent circular-wait deadlocks between two settlements that happen to share a counterparty.

## Failure Handling

- Insufficient balance → `ReserveFunds` fails, no reservation created. (This is a genuine error response, distinct from the idempotent-replay case in §8.2.)
- `SettleTrade` failure → full rollback inside Wallet Service; Settlement Service retries with backoff, then dead-letters. **This applies only to genuine failures** (insufficient reserved balance, DB unavailable, lock timeout) — an already-settled `trade_id` is handled per §8.1 and is not a failure path.
- Kafka unavailable → Outbox Publisher retries independently of the request/response path.

## Accounting Invariants

- `available_balance ≥ 0` at all times.
- `reserved_amount − consumed_amount ≥ 0` for every reservation row.
- A reservation is released or fully consumed exactly once — enforced by the `status` check in §8.3, not left to caller discipline.
- A given `trade_id` can only be settled once — enforced by the constraint in §8.1, with explicit idempotent-success handling on retry.
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

---

## 9. GetSupportedAssets gRPC Specification

To support referential integrity checks during market creation in the Market Service, Wallet Service exposes the following gRPC Protobuf schema:

```protobuf
rpc GetSupportedAssets(GetSupportedAssetsRequest)
    returns (GetSupportedAssetsResponse);

message GetSupportedAssetsRequest {}

message GetSupportedAssetsResponse {
    repeated AssetInfo assets = 1;
}

message AssetInfo {
    string asset_code    = 1;  // e.g. "BTC"
    string asset_name    = 2;  // e.g. "Bitcoin"
    int32  decimals      = 3;  // e.g. 8
    bool   is_enabled    = 4;  // e.g. true
    string seed_amount   = 5;  // e.g. "0.0000000000"
    int32  display_order = 6;  // e.g. 2
}
```

---

## 10. Deposit & Withdrawal Lifecycle

To support external account funding and secure asset retrieval, the Wallet Service implements a dedicated deposit and withdrawal state machine.

### 10.1 Database Schema
```sql
CREATE TYPE transfer_type AS ENUM ('DEPOSIT', 'WITHDRAWAL');
CREATE TYPE transfer_status AS ENUM ('PENDING', 'COMPLETED', 'FAILED');

CREATE TABLE wallet_transfers (
    id            UUID PRIMARY KEY,                      -- UUIDv7 identifier
    wallet_id     UUID NOT NULL REFERENCES wallets(id),
    type          transfer_type NOT NULL,
    amount        DECIMAL(30,10) NOT NULL,
    status        transfer_status NOT NULL DEFAULT 'PENDING',
    reference_id  VARCHAR(64) NOT NULL,                  -- Idempotency key from external payment/bridge gateway
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT    uq_transfer_ref UNIQUE(reference_id)
);

CREATE INDEX idx_transfers_wallet ON wallet_transfers(wallet_id);
```

---

### 10.2 Deposit Execution Flow (Idempotent Webhook Ingestion)
```
[ Payment Webhook / Bridge ]
             │
      (Inbound credit notify)
             ▼
     [ API Gateway ]
             │
      (gRPC DepositFunds)
             ▼
     [ Wallet Service ] ──(Check reference_id)──► [ db: wallet_transfers ]
             │                                              │
             ├── (Exists: returns success, no-op) ◄─────────┘
             │
             └── (New: Starts DB Transaction)
                      ├── 1. Insert PENDING wallet_transfers
                      ├── 2. available_balance += deposit_amount
                      ├── 3. status = 'COMPLETED', updated_at = NOW()
                      ├── 4. Insert Outbox (DepositCompleted)
                      └── COMMIT ──► Publish Event
```

#### Steps:
1. External bank wire or cryptocurrency bridge publishes a credit notification webhook to the API Gateway, which forwards to `DepositFunds(user_id, asset, amount, reference_id)`.
2. **Idempotency Guard:** If a transfer row already exists matching `reference_id`, the call short-circuits and returns success immediately to prevent duplicate credits.
3. **Transaction Execution:**
   - Lock user wallet row `FOR UPDATE`.
   - Insert a row into `wallet_transfers` with status `COMPLETED` (or `PENDING` if awaiting async blockchain confirmation triggers).
   - Credit `available_balance = available_balance + amount`.
   - Write a `wallet_transactions` history record (`reference_type = DEPOSIT`).
   - Write `DepositCompleted` event into the Transactional Outbox.
   - Commit transaction atomically.

---

### 10.3 Withdrawal Execution Flow (Secure Reserve-then-Debit)
```
[ User UI ] ──(Request withdraw)──► [ Wallet Service ] ──► [ available_balance >= amount? ]
                                                                      │
                                        ┌─────────────────────────────┴─────────────────────────────┐
                                        ▼ (Yes)                                                     ▼ (No)
                           [ Start DB Transaction ]                                          [ Reject Request ]
                             ├── 1. available_balance -= amount
                             ├── 2. reserved_balance += amount
                             ├── 3. Insert PENDING wallet_transfers
                             ├── 4. Insert Outbox (WithdrawalInitiated)
                             └── COMMIT ──────────────────────────────────────────┐
                                                                                  ▼
                                                                      [ Call External Bridge API ]
                                                                                  │
                                        ┌─────────────────────────────────────────┴─────────────────────────┐
                                        ▼ (Success Callback)                                                ▼ (Failure Callback)
                           [ Start DB Transaction ]                                          [ Start DB Transaction ]
                             ├── 1. reserved_balance -= amount                                 ├── 1. reserved_balance -= amount
                             ├── 2. status = 'COMPLETED'                                       ├── 2. available_balance += amount
                             ├── 3. Insert Outbox (WithdrawalCompleted)                        ├── 3. status = 'FAILED'
                             └── COMMIT                                                        └── COMMIT
```

#### Steps:
1. **Initiation Phase:** User requests a withdrawal via the `WithdrawFunds` gRPC service.
   - Check if `available_balance >= withdrawal_amount`. If not, reject immediately.
   - Lock available funds: debit `available_balance` and credit `reserved_balance` by the withdrawal amount.
   - Insert a row into `wallet_transfers` with status `PENDING` and a generated `reference_id` (used as transactional idempotency key for external gateways).
   - Insert `WithdrawalInitiated` event into outbox.
   - Commit and initiate the external transfer (blockchain transaction or fiat payout).
2. **Settlement Phase (Callback):**
   - **On Success:** Debit the funds from `reserved_balance`, update `wallet_transfers.status` to `COMPLETED`, insert a `wallet_transactions` record (`reference_type = WITHDRAWAL`), write `WithdrawalCompleted` event to outbox, and commit.
   - **On Failure:** Move the funds back from `reserved_balance` to `available_balance`, update `wallet_transfers.status` to `FAILED`, and commit (restoring the user's funds).

---

### 10.4 Outbox Integration Events

#### `DepositCompleted` Event Payload:
```json
{
  "event_id": "018f60f3-c540-7798-8422-efa6b29f9999",
  "event_type": "wallet.deposit_completed.v1",
  "event_version": 1,
  "data": {
    "user_id": "018f60f3-a120-7798-8422-cfb6a29e11aa",
    "wallet_id": "018f60f3-b240-7798-8422-dfb6a29e22bb",
    "asset": "USDT",
    "amount": "5000.0000000000",
    "reference_id": "tx_dep_987654321",
    "completed_at": "2026-07-10T14:48:12Z"
  }
}
```

#### `WithdrawalCompleted` Event Payload:
```json
{
  "event_id": "018f60f3-d120-7798-8422-dfb8a29f8888",
  "event_type": "wallet.withdrawal_completed.v1",
  "event_version": 1,
  "data": {
    "user_id": "018f60f3-a120-7798-8422-cfb6a29e11aa",
    "wallet_id": "018f60f3-b240-7798-8422-dfb6a29e22bb",
    "asset": "BTC",
    "amount": "0.0500000000",
    "reference_id": "tx_wdr_123456789",
    "completed_at": "2026-07-10T14:50:42Z"
  }
}
```