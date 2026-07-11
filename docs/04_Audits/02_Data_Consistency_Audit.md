# TradeDrift Audit — 02. Data Consistency

> **Status:** ✅ Validated (V1.0)
> **Document:** 02_Data_Consistency_Audit.md
> **Domain:** Databases, Event Contracts, and Ledgers

---

## 1. Scope

This audit validates database schemas, numeric scale representation, transactional outbox sequence logic, event broker contracts, and data ownership boundaries to prevent ledger corruption and event drift.

---

## 2. Scenario Validations

### 2.1 Fixed-Decimal Precision Validation
* **Problem:** Floating point arithmetic is inherently prone to rounding errors, which is unacceptable for a financial ledger.
* **Audit Resolution:**
  - All balance, price, and volume calculations are represented as PostgreSQL `DECIMAL(30,10)` fields.
  - All internal gRPC and Kafka schemas serialize numeric values as string primitives (`string` in Protobuf and JSON representation), forcing calling applications to parse them using exact-precision numeric libraries (e.g. `shopspring/decimal` in Go or `Decimal` in Python).
  - Individual assets define decimals (e.g., BTC = 8, USDT = 2) in the `supported_assets` catalog.

### 2.2 Transactional Outbox Isolation & Concurrency
* **Workflow:** To ensure write-then-publish consistency without distributed transactions, services insert outbound events into a local `outbox` table in the same database transaction as the business entity changes.
* **Concurrency Guard:**
  - Background outbox publisher daemons lease unpublished batches using:
    ```sql
    SELECT * FROM outbox 
    WHERE status = 'PENDING' 
    ORDER BY created_at ASC 
    LIMIT 100 
    FOR UPDATE SKIP LOCKED;
    ```
  - `SKIP LOCKED` ensures horizontal replicas process independent event batches concurrently with zero lock contention.
  - Transactions only commit and mark events `PUBLISHED` after the Kafka broker returns a partition delivery acknowledgement, guaranteeing at-least-once delivery.

### 2.3 Event Ownership Boundaries
To prevent split-brain event processing, domain events have single authoritative publishers:
* **`OrderCreated` & `OrderCancelRequested`:** Sole owner is the **Order Service**.
* **`TradeExecuted` & `OrderCancelled`:** Sole owner is the **Matching Engine**.
* **`TradeSettled`:** Sole owner is the **Wallet Service**.
  - *Previous Drift:* Settlement Service and Wallet Service documents both claimed publishing responsibilities for `TradeSettled`. The audit corrected this: `WalletService` writes the outbox event in the same Postgres transaction that applies the ledger mutations during `SettleTrade`. Settlement Service publishes no events.

---

## 3. Discovered Inconsistencies & Resolutions

* **`INITIAL_ALLOCATION` Seeding Constraint:** The Wallet Service database had a `UNIQUE(reference_id, reference_type)` constraint for transaction records. Because `reference_id = user_id` for wallet initial allocations, this allowed seeding only **one** asset per user. The constraint was resolved by changing it to `UNIQUE(reference_id, reference_type, asset)` to permit multi-asset seeding.
* **`SettleTrade` contract drift:** The Matching Engine's event contracts document defined `SettleTrade` with only 9 parameters, missing `market_id`. The contract was corrected to require 10 parameters, ensuring downstream trade indexing performs correctly on the market symbol.
