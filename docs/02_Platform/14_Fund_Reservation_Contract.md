# TradeDrift — Fund Reservation Contract

> **Status:** ✅ Designed (V1.1)
> **Document:** 14_Fund_Reservation_Contract.md
> **Service:** Platform Architecture
> **Version:** V1.1
> **Last Updated:** July 2026
> Revision notes: V1.1 expands contract coverage: (1) adds the SettleTrade gRPC definition; (2) specifies core wallet/reservation balance invariants and mathematical bounds; (3) defines background TTL/reconciliation loop to release stale orphaned reservations; (4) provides a worked step-by-step partial fill example; (5) details settlement trade_id idempotency logic.

---

## 1. Purpose & Core Concepts

In a high-throughput cryptocurrency exchange, order matching must run with sub-millisecond latencies. To achieve this, the Matching Engine maintains its order books entirely in-memory and does not access the database during matches. 

To prevent double-spending (e.g., a user placing multiple orders using the same balance before matching occurs) and default risks, the platform implements a **Fund Reservation Contract**.

```
Client         Order Service             Wallet Service               Postgres DB
  │                  │                         │                            │
  ├─ 1. POST /order ─►                         │                            │
  │                  ├─ 2. ReserveFunds(gRPC) ─►                            │
  │                  │                         ├─ 3. Lock available balance─►
  │                  │                         │  4. Insert Reservation     │
  │                  │                         ◄─ 5. Success response ──────┤
  │                  ├─ 6. Save Order(OPEN) ────────────────────────────────►
```

### Core Rules:
1. **Pre-Flight Lock:** No order can transition to `OPEN` in the Order Service, nor be sent to the Matching Engine queue, until the funds required to cover the order are successfully moved from `available_balance` to `reserved_balance` in the Wallet Service.
2. **Synchronous Validation:** Fund reservation is performed synchronously via gRPC from the Order Service. If the user has insufficient available balance, the reservation fails, and the Order Service immediately rejects the order.
3. **Decoupled Asynchronous Settlement:** Once matched, settlement reads and consumes from the reserved balance asynchronously via the Settlement Service. The Matching Engine is completely decoupled from the balance ledger database.
4. **Eventual Release:** If an order is cancelled or expires, the Order Service calls the Wallet Service to unlock any remaining reserved funds back to the user's available balance.

---

## 2. Reservation Lifecycle & State Transitions

A reservation progresses through distinct states representing its lifecycle:

```
          [ Order Placement ]
                   │
                   ▼
             +-----------+
             |  ACTIVE   | <───────────────────────────┐
             +-----+-----+                             │
                   │                                   │
         (Partial Fill / Match)               (Partial Fill / Match)
                   │                                   │
                   ▼                                   │
        +----------------------+                       │
        |  PARTIALLY_CONSUMED  | ──────────────────────┘
        +----------+-----------+
                   │
         ┌─────────┴─────────┐
    (Full Fill)        (Order Cancelled)
         │                   │
         ▼                   ▼
   +-----------+       +-----------+
   | CONSUMED  |       | RELEASED  |
   +-----------+       +-----------+
```

### State Definitions:
* **`ACTIVE`:** The reservation has been successfully created. The entire `reserved_amount` is locked.
* **`PARTIALLY_CONSUMED`:** A trade has filled a portion of the order. The consumed portion has been deducted, and the remaining portion stays locked.
* **`CONSUMED`:** The order is fully filled. The entire reserved amount has been transferred to counterparties, and the reservation is closed.
* **`RELEASED`:** The unexecuted portion of the order was cancelled, and the remaining locked funds have been returned to the user's `available_balance`.

### Reservation Balance Invariants & Bounds:
At all times, every reservation row must satisfy the following system boundaries:
* **The Reservation Balance Invariant:**
  $$\text{remaining\_amount} = \text{reserved\_amount} - \text{consumed\_amount}$$
* **Mathematical Bounds:**
  $$0 \le \text{consumed\_amount} \le \text{reserved\_amount}$$
  $$\text{remaining\_amount} \ge 0$$
* **Wallet Balance Invariants:**
  $$\text{available\_balance} + \text{reserved\_balance} = \text{total\_balance}$$
  $$\text{available\_balance} \ge 0$$
  $$\text{reserved\_balance} \ge 0$$
  $$\text{total\_balance} \ge 0$$

### 2.2 Worked Partial-Fill Example
To clarify the state transitions, consider a limit BUY order for **0.1 BTC** at **50,000 USDT**:

1. **Step 1: Order Placement & Fund Reservation**
   * **Action:** Order Service calls `ReserveFunds(user_id, order_id, "USDT", "5000")`.
   * **Wallet Balance Change:**
     * `available_balance = available_balance - 5000`
     * `reserved_balance = reserved_balance + 5000`
   * **Reservation State:** A row is created with `reserved_amount = 5000`, `consumed_amount = 0`, `remaining_amount = 5000`, and `status = ACTIVE`.
2. **Step 2: Partial Fill Execution**
   * **Action:** The Matching Engine matches `0.04 BTC` at `50,000 USDT`. Settlement Service calls `SettleTrade(quantity: "0.04", price: "50000")` costing `2000 USDT`.
   * **Wallet Balance Change:**
     * Buyer's Base asset wallet is credited: `base_asset.available_balance += 0.04`
     * Buyer's Quote asset wallet is debited: `quote_asset.reserved_balance -= 2000`
   * **Reservation State:**
     * `consumed_amount = 0 + 2000 = 2000`
     * `remaining_amount = 5000 - 2000 = 3000`
     * Status transitions from `ACTIVE` to `PARTIALLY_CONSUMED`.
3. **Step 3: User Cancels Remaining Quantity**
   * **Action:** Client deletes the order. Order Service calls `ReleaseFunds(order_id)`.
   * **Wallet Balance Change:**
     * Buyer's Quote asset wallet is restored:
       * `quote_asset.available_balance += 3000`
       * `quote_asset.reserved_balance -= 3000`
   * **Reservation State:**
     * Status transitions to `RELEASED`.
     * `remaining_amount` drops to `0`. No further balance changes can occur.
   * **Result:** Total USDT consumed = `2000` (for 0.04 BTC). Total USDT returned = `3000`. Original locked total (`5000`) is perfectly accounted for.

---

## 3. gRPC Interface Specification

The contract is implemented via the following gRPC methods exposed by the Wallet Service:

```protobuf
syntax = "proto3";

package wallet;

service WalletService {
    // Locks a user's available balance and creates a reservation row.
    // Idempotent on order_id.
    rpc ReserveFunds(ReserveFundsRequest) returns (ReserveFundsResponse);

    // Releases the remaining amount of an active reservation back to available balance.
    // Idempotent on order_id and reservation status.
    rpc ReleaseFunds(ReleaseFundsRequest) returns (ReleaseFundsResponse);

    // Synchronously settles quote/base balance transfers between counterparties.
    // Idempotent on trade_id.
    rpc SettleTrade(SettleTradeRequest) returns (SettleTradeResponse);
}

message ReserveFundsRequest {
    string user_id   = 1; // User ID requesting reservation (UUIDv7 format)
    string order_id  = 2; // Order ID requesting reservation (UUIDv7 format)
    string asset     = 3; // e.g. "BTC", "USDT"
    string amount    = 4; // Decimal representation of amount to lock
}

message ReserveFundsResponse {
    string reservation_id   = 1; // Generated UUIDv7 reservation ID
    string order_id         = 2; // Passed order ID
    string asset            = 3; // Reserved asset
    string reserved_amount  = 4; // Total amount locked
    string status           = 5; // e.g. "ACTIVE"
}

message ReleaseFundsRequest {
    string order_id = 1; // Order ID whose remaining funds should be released
}

message ReleaseFundsResponse {
    string order_id         = 1;
    string released_amount  = 2; // The amount returned to available balance
    string status           = 3; // e.g. "RELEASED"
}

message SettleTradeRequest {
    string trade_id      = 1; // Unique Match Trade ID (UUIDv7)
    string buyer_id      = 2; // Buyer User ID
    string seller_id     = 3; // Seller User ID
    string buy_order_id  = 4; // Buyer's original Order ID
    string sell_order_id = 5; // Seller's original Order ID
    string base_asset    = 6; // e.g. "BTC"
    string quote_asset   = 7; // e.g. "USDT"
    string price         = 8; // Match price (Decimal)
    string quantity      = 9; // Match quantity (Decimal)
    string market_id     = 10; // Market pair ID (e.g. "BTC-USDT")
}

message SettleTradeResponse {
    string trade_id = 1;
    bool   success  = 2;
}
```

---

## 4. Database Schema Design

The Wallet Service database maintains the reservation log and locks:

```sql
CREATE TYPE reservation_status AS ENUM ('ACTIVE', 'PARTIALLY_CONSUMED', 'CONSUMED', 'RELEASED');

CREATE TABLE wallet_reservations (
    reservation_id  UUID PRIMARY KEY,                      -- UUIDv7
    order_id        UUID NOT NULL UNIQUE,                  -- Order correlation key
    user_id         UUID NOT NULL,                         -- Owner of the funds
    asset           VARCHAR(16) NOT NULL,                  -- Currency symbol
    reserved_amount DECIMAL(30,10) NOT NULL CHECK (reserved_amount > 0),
    consumed_amount DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (consumed_amount >= 0),
    remaining_amount DECIMAL(30,10) GENERATED ALWAYS AS (reserved_amount - consumed_amount) STORED,
    status          reservation_status NOT NULL DEFAULT 'ACTIVE',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for fast user balance checks
CREATE INDEX idx_reservations_user ON wallet_reservations(user_id, asset);
```

---

## 5. Idempotency & Concurrency Guarantees

Every mutation method in the Fund Reservation contract is designed with strict idempotency to support retries and network redeliveries safely.

### 5.1 `ReserveFunds` Idempotency (Order Placement Replays)
If the Order Service replica experiences a network drop or crash after calling `ReserveFunds`, it will retry the call carrying the same `order_id` (since UUIDv7 order IDs are generated on the client or gateway before the gRPC call).

#### Execution Steps:
1. **Check Existence:** The Wallet Service checks if a reservation already exists for the given `order_id` (using the `UNIQUE(order_id)` constraint or a select query).
2. **If Exists:**
   - The service does **not** treat this as an error.
   - It retrieves the existing reservation row and returns its details (`reserved_amount`, `asset`, `status`) to the caller.
   - If the requested amount or asset in the retry request differ from the database row, the service logs an operational alert and still returns the existing database state as the single source of truth.
3. **If New:**
   - Begins a PostgreSQL transaction.
   - Locks the user's `wallets` balance row for the asset using `SELECT ... FOR UPDATE`.
   - Verifies `available_balance >= requested_amount`. If insufficient, rolls back and returns a gRPC code `FAILED_PRECONDITION`.
   - Deducts `requested_amount` from `available_balance`, adds it to `reserved_balance`.
   - Inserts the `wallet_reservations` row.
   - Inserts a ledger transaction record into `wallet_transactions` (reference_type = `RESERVATION`, reference_id = `order_id`) to maintain balance history.
   - Commits the transaction and returns success.

---

### 5.2 `ReleaseFunds` Idempotency (Cancellation Replays)
A client may trigger a cancellation request multiple times, or the Order Service worker may retry publishing `OrderCancelled` offsets, resulting in multiple calls to `ReleaseFunds(order_id)`.

#### Execution Steps:
1. **Acquire Row Lock:** The Wallet Service starts a database transaction and immediately locks the reservation row:
   ```sql
   SELECT reservation_id, status, remaining_amount, user_id, asset
   FROM wallet_reservations
   WHERE order_id = $1
   FOR UPDATE;
   ```
2. **Evaluate Status:**
   - **If status is `RELEASED` or `CONSUMED`:** The funds have already been processed or returned. The Wallet Service commits the transaction immediately and returns `released_amount = 0` and status `RELEASED`. **It does not credit the balance again.**
   - **If status is `ACTIVE` or `PARTIALLY_CONSUMED`:** 
     - The service sets the status to `RELEASED`.
     - It locks the user's `wallets` balance row: `SELECT ... FOR UPDATE`.
     - It credits `remaining_amount` back to `available_balance` and deducts it from `reserved_balance`.
     - It inserts a ledger transaction record (`reference_type = RELEASE`, `reference_id = order_id`).
     - It commits the transaction and returns the exact `released_amount` that was returned.

> **Critical Safety Invariant:** Without the status check inside the `FOR UPDATE` lock block, duplicate calls to `ReleaseFunds` would credit the user's balance multiple times, manufacturing virtual currency out of thin air (the "double release" bug).

---

### 5.3 Stale Reservation Handling (TTL/Reconciliation)
If the Order Service crashes during order placement *after* reserving funds but *before* committing the order record, or if the Matching Engine restarts and drops its active memory book, the reservation could remain locked in `ACTIVE` or `PARTIALLY_CONSUMED` status indefinitely, lock-starving the user's funds.

* **Reconciliation Runner (Cron Role):** The Wallet Service's Cron Role executes a daily reconciliation job.
* **Audit Flow:**
  1. The job fetches all `wallet_reservations` in `ACTIVE` or `PARTIALLY_CONSUMED` status older than **24 hours**.
  2. For each stale reservation, it queries the Order Service via gRPC: `GetOrder(order_id)`.
  3. **Evaluation Rules:**
     - If the Order Service returns that the order is `FILLED`, `CANCELLED`, or `REJECTED`: The cron job invokes `ReleaseFunds(order_id)` to release any remaining locked balance.
     - If the Order Service returns `NOT_FOUND` (indicating the parent order placement transaction aborted/rolled back and never persisted): The cron job immediately triggers `ReleaseFunds(order_id)` to unlock the user's funds.
     - If the order is confirmed as still actively open in the order book, the reservation is left untouched.

---

### 5.4 `SettleTrade` Idempotency (Trade Matching Replays)
The Settlement Service consumes `TradeExecuted` events from Kafka under at-least-once rules, which can cause duplicate `SettleTrade` gRPC calls to the Wallet Service for the same `trade_id`.

* **Deduplication Check:** The Wallet Service uses the unique transaction ledger constraint to deduplicate settlements. It checks for the existence of a record in `wallet_transactions` matching:
  ```sql
  SELECT id FROM wallet_transactions 
  WHERE reference_id = $1 AND reference_type = 'SETTLEMENT';
  ```
* **Behavior:**
  - If a transaction is found, the trade has already been settled. The Wallet Service short-circuits and returns `success: true` immediately, taking no locks and modifying no balances.
  - If no transaction is found, the Wallet Service acquires locks on the buyer and seller wallets, completes the transfers, logs the transactions, and commits. If a racing concurrent request commits first, the database throws a key conflict on the `UNIQUE(reference_id, reference_type, asset)` index. The Wallet Service catches this exception, rolls back the local balance mutations, and returns `success: true` to the caller. Replays never cause double-settlement.

---

## 6. Saga Failure Recovery & Rollback Scenarios

### 6.1 Insufficient Funds (Order Rejected)
If `ReserveFunds` fails due to insufficient balance, the Order Service does not publish `OrderCreated`. The Order Service immediately updates the local database order status to `REJECTED`, returns the error to the client, and ends the workflow. No funds are locked.

### 6.2 matching Engine Reject
If the Matching Engine rejects an order upon consumption (e.g. price limits exceeded, or engine shutdown) and emits `OrderCancelled` without matches, the Order Service consumes it and calls `ReleaseFunds(order_id)`. Since the reservation status is `ACTIVE`, the Wallet Service safely unlocks 100% of the reserved amount.

---

## 7. Service Invariants

- **FRC-1 (Pre-Flight Lock):** Order Service must not publish `OrderCreated` to Kafka or set order status to `OPEN` unless `ReserveFunds` returns a successful response.
- **FRC-2 (Double-Release Prevention):** `ReleaseFunds` must check that the reservation status is `ACTIVE` or `PARTIALLY_CONSUMED` under a `FOR UPDATE` lock before returning balance to `available_balance`.
- **FRC-3 (Transaction Isolation):** Balance adjustments and reservation row writes must be completed atomically in the same database transaction inside the Wallet Service.
- **FRC-4 (Idempotent Success):** Idempotent replays on `ReserveFunds` and `ReleaseFunds` must return gRPC success codes and matching payloads, not duplicate mutations or error responses.
- **FRC-5 (Mathematical Bounds):** Every reservation row must satisfy the boundaries $0 \le \text{consumed\_amount} \le \text{reserved\_amount}$ and $\text{remaining\_amount} \ge 0$, and available wallet balances must never go negative.
- **FRC-6 (Settlement Idempotency):** The Wallet Service must deduplicate incoming `SettleTrade` calls by `trade_id` using transaction logs, returning a success response without executing redundant balance transfers if the trade is already settled.
