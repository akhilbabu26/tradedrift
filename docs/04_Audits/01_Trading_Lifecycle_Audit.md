# TradeDrift Audit — 01. Trading Lifecycle

> **Status:** ✅ Validated (V1.0)
> **Document:** 01_Trading_Lifecycle_Audit.md
> **Domain:** Business Workflows & Matching Logic

---

## 1. Scope

This audit validates the lifecycle of trading orders, matching engine execution, partial/full fill transitions, client-initiated cancellations, and the race conditions that occur when cancellations and matches occur concurrently.

---

## 2. Scenario Validations

### 2.1 Limit Order Placement & Validation
* **Workflow:** Users submit order creations via `POST /orders`. The request is validated by the Order Service:
  - Validates trading pair exists and market is open (cached in Order Service with a 10s TTL, fails-closed if cache is cold and service is down).
  - Validates positive quantity and limit price presence.
  - Generates a UUIDv7 `order_id` in application code *before* persistence.
* **Reservation Contract:** Order Service calls the Wallet Service synchronously via `ReserveFunds` gRPC using the pre-generated `order_id`. Wallet Service locks available funds (`available_balance -= amount`, `reserved_balance += amount`).
* **Database Persist:** On reservation success, Order Service opens a transaction, inserts the order with status `OPEN` and writes `OrderCreated` into its transactional outbox.
* **Idempotency:** Client-supplied `Idempotency-Key` headers prevent duplicate placements on API retries. Duplicate `ReserveFunds` calls are absorbed by the Wallet Service's `UNIQUE(order_id)` constraint, returning success.

### 2.2 Market Order Placement (IOC Semantics)
* **Workflow:** Market orders do not specify a price. 
* **Reservation Strategy:** To guarantee default safety, buy market orders reserve the **entire available balance** of the quote asset. Sell market orders reserve the original base asset quantity.
* **Matching Loop:** Market orders are processed as Immediate-or-Cancel (IOC) by the Matching Engine. They fill against the resting book.
* **Resolution:** Any unfilled remainder is immediately cancelled. Matching Engine publishes a trade fill (`TradeExecuted`) and a cancel for the remainder (`OrderCancelled`). The Order Service consumes the cancel and calls `Wallet.ReleaseFunds(order_id)` to return the remainder back to available balance.

### 2.3 Matching Loop & Fill Transitions
* **Execution:** Matching is done in-memory. Fills always execute at the **maker's price** (the resting order's price).
* **State Progression:** 
  - **Partial Fill:** Resting remaining quantity decrements (`remaining_quantity = remaining_quantity - fill_qty`). Order Service transitions order status to `PARTIALLY_FILLED`.
  - **Full Fill:** Order status transitions to `FILLED` in both Matching Engine memory and the Order database. Wallet reservation transitions to `CONSUMED` (or `PARTIALLY_CONSUMED` on partial trade matching).
* **Ledger Invariant:** At all points during partial matching:
  $$\text{consumed\_amount} + \text{remaining\_amount} = \text{reserved\_amount}$$
  This is audited and verified in the database constraints.

### 2.4 Cancellation Race Conditions
If a user submits a cancel request at the same moment the Matching Engine matches the order:
* **Sequential Ordering:** The API Gateway forwards the cancel request to the Order Service, which publishes `OrderCancelRequested` to Kafka. Because both `OrderCreated` and `OrderCancelRequested` utilize the symbol (e.g., `BTC_USDT`) as the Kafka partition key, the Matching Engine processes them sequentially.
* **Precedence:** Fills always take precedence.
  - **If the Match processes first:** The order is filled. The subsequent cancel request finds no resting order and is ignored (silent idempotent success). Order Service receives `TradeExecuted`.
  - **If the Cancel processes first:** The order is removed from the book. Slices of opposite matching orders bypass it. The engine publishes `OrderCancelled`, and Order Service releases the reserved balance.

---

## 3. Discovered Inconsistencies & Resolutions

* **Order Status Misalignments:** Pre-audit documents mixed `PENDING` and `OPEN` order statuses. This was resolved by removing `ORDER_STATUS_PENDING` from `common.proto` enums, ensuring orders are directly saved and returned as `OPEN` upon reservation approval.
* **ReleaseFunds Double-Credit:** A lack of status check in the `ReleaseFunds` endpoint allowed duplicate cancels to double-credit user balances. This was resolved by locking the reservation row `FOR UPDATE` and checking that status is not already `RELEASED` before crediting.
