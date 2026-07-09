# TradeDrift Settlement Service Specification

**Document:** 09_Settlement_Service.md  
**Service:** Settlement Service  
**Version:** V1.0  
**Status:** Design Complete  
**Last Updated:** July 2026  

---

## 1. Purpose

The Settlement Service is the transactional ledger gateway of the TradeDrift platform. It consumes execution output events from the Matching Engine, performs strict idempotency filtering, calls the Wallet Service to securely shift reserved quote/base balances between buyers and sellers, and utilizes the transactional outbox pattern to notify downstream services (such as Portfolio and Market Services).

Its core objectives are:
1. **Zero Double-Settlement**: Guarantee that no trade is ever settled more than once, even under Kafka partition rebalances or network redeliveries.
2. **Guaranteed Delivery**: Ensure every trade execution successfully transfers balances and registers in transaction logs, preventing asset discrepancies.
3. **Low-Latency Processing**: Handle settlements asynchronously and concurrently per account leg to ensure clearing lag does not block matching.

---

## 2. System Architecture & Context

The Settlement Service acts as a bridge between the asynchronous matching queue and the transactional wallet database.

```
┌─────────────────────────────────┐
│     Matching Engine Instance    │
└────────────────┬────────────────┘
                 │
                 │ publishes to
                 ▼
┌─────────────────────────────────┐
│   Kafka: trades (Topic)         │
└────────────────┬────────────────┘
                 │
                 │ consumes (partitioned by market_id)
                 ▼
┌─────────────────────────────────┐
│    Settlement Service Node      │
│  ┌───────────────────────────┐  │
│  │   PostgreSQL DB           │  │
│  │   - settled_trades table  │  │
│  │   - outbox_events table   │  │
│  └─────────────┬─────────────┘  │
└────────────────┼────────────────┘
                 │
                 ├───────────────────────────────┐
                 │ gRPC call                     │ outbox polling / CDC
                 ▼                               ▼
┌─────────────────────────────────┐     ┌─────────────────────────────────┐
│      Wallet Service             │     │      Kafka: trade-cleared       │
│  (Locks/unlocks user balances)  │     │  (Consumed by Portfolio, etc.)  │
└─────────────────────────────────┘     └─────────────────────────────────┘
```

---

## 3. Cross-Service Contracts (Single Source of Truth)

The Settlement Service integrates directly against the output payloads of the Matching Engine. To prevent contract drift, the event schemas defined in the Matching Engine documentation serve as the single source of truth:

* **Trigger Event**: Consumes the `TradeExecuted` event payload defined in **[06_Event_Contracts.md (Section 4.1)](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/08_Matching_Engine/06_Event_Contracts.md#L133-L157)**.
* **Idempotency Key**: Uses `trade_id` (generated as a UUIDv7 in-memory by the Matching Engine at match time) as the unique primary key for the ledger.
* **Pass-Through Fields**: Invokes the Wallet Service's gRPC method using field-for-field mappings from the event payload.

---

## 4. Idempotency & Database Schema

To prevent double-settlement, the Settlement Service implements an **Idempotent Consumer** pattern backed by a PostgreSQL database instance.

### 4.1 SQL Schema

```sql
-- Represents the registry of all successfully settled trades
CREATE TABLE settled_trades (
    trade_id UUID PRIMARY KEY,
    buyer_id UUID NOT NULL,
    seller_id UUID NOT NULL,
    buy_order_id UUID NOT NULL,
    sell_order_id UUID NOT NULL,
    base_asset VARCHAR(16) NOT NULL,
    quote_asset VARCHAR(16) NOT NULL,
    price NUMERIC(28, 8) NOT NULL,
    quantity NUMERIC(28, 8) NOT NULL,
    settled_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL
);

-- Index to support auditing and account balance ledger history queries
CREATE INDEX idx_settled_trades_buyer ON settled_trades(buyer_id);
CREATE INDEX idx_settled_trades_seller ON settled_trades(seller_id);

-- Outbox table for reliable downstream event publishing
CREATE TABLE outbox_events (
    event_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_type VARCHAR(64) NOT NULL,
    aggregate_id VARCHAR(64) NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP NOT NULL,
    processed BOOLEAN DEFAULT FALSE NOT NULL
);

CREATE INDEX idx_outbox_events_processed ON outbox_events(processed) WHERE processed = FALSE;
```

---

## 5. Settlement Execution Pipeline

For every `TradeExecuted` message consumed from Kafka:

```
                  Receive TradeExecuted event
                              │
                              ▼
                Start PostgreSQL Transaction
                              │
                              ▼
           Attempt INSERT into settled_trades table
                              │
            ┌─────────────────┴─────────────────┐
            │ Unique Violation?                 │ Success
            ▼                                   ▼
    [Duplicate Delivery]               1. Call Wallet.SettleTrade()
    - Abort Transaction                   (gRPC double-leg transfer)
    - Acknowledge Kafka (no-op)                 │
                                                ▼
                                       2. Insert into outbox_events
                                          (Portfolio & transaction history payload)
                                                │
                                                ▼
                                       3. Commit PostgreSQL Transaction
                                                │
                                                ▼
                                       4. Acknowledge Kafka message
```

### 5.1 Step-by-Step Logic

1. **Transaction Begin**: Open a local database transaction in PostgreSQL.
2. **Idempotency Register**: Perform an INSERT of the `trade_id` and trade details into the `settled_trades` table.
   * If this insert fails with a **unique primary key violation** (SQLSTATE `23505`), it indicates that the trade was already successfully settled during a prior run. The transaction is aborted immediately, and the Kafka offset is acknowledged as a safe no-op.
3. **Wallet Balance Update (gRPC Leg)**: Execute a synchronous gRPC call to `Wallet.SettleTrade(...)` on the Wallet Service. This call executes a double-leg balance shift within the Wallet DB:
   * Moves quote asset (`quantity * price`) from the Buyer's reserved balance to the Seller's available balance.
   * Moves base asset (`quantity`) from the Seller's reserved balance to the Buyer's available balance.
   * Deducts calculated taker/maker fees from the respective legs.
4. **Outbox Entry**: Insert an event record into `outbox_events` containing the details for the `TradeCleared` event.
5. **Transaction Commit**: Commit the PostgreSQL transaction.
6. **Kafka Acknowledge**: Commit the Kafka consumer offset.

---

## 6. Transactional Outbox Pattern

To ensure downstream systems (like Portfolio and Market Services) are notified of settled trades without introducing distributed two-phase commit (2PC) bottlenecks:

1. **Atomic Write**: The outbox record is inserted inside the same database transaction that registers the trade as settled.
2. **Outbox Publisher Goroutine**: An independent goroutine polls the `outbox_events` table for unprocessed events (`processed = false`) with small, indexed batches:
   ```sql
   SELECT event_id, aggregate_type, aggregate_id, event_type, payload
   FROM outbox_events
   WHERE processed = FALSE
   ORDER BY created_at ASC
   LIMIT 100
   FOR UPDATE SKIP LOCKED;
   ```
3. **Publish to Kafka**: The publisher goroutine writes the events to downstream Kafka topics (e.g., `trade-cleared` or `portfolio-updates`).
4. **Mark Processed**: Once Kafka acknowledges the write, the publisher marks the outbox records as processed in the database:
   ```sql
   UPDATE outbox_events SET processed = TRUE WHERE event_id = ANY(?);
   ```

---

## 7. Error Handling, Retries, & Failure Modes

Because the Settlement Service sits on the critical path of money movements, error scenarios are tightly handled:

| Failure Scenario | Impact | Action / Remediation |
|---|---|---|
| **Wallet Service gRPC Offline / Timeout** | Cannot clearing balances | The local PostgreSQL transaction is rolled back. The Kafka offset is **not** acknowledged. The consumer retries the message with exponential backoff. |
| **Postgres Database Offline** | Cannot verify idempotency | Kafka consumer halts processing, generating an alert. Once Postgres resumes, consumption resumes from the last uncommitted offset. |
| **Invalid Trade Payload (Logical Error)** | Out-of-bounds quantity or price | Transaction is rolled back. The message is pushed to the **DLQ (Dead Letter Queue)** for manual inspection, and the main queue continues to prevent blocking other traders. |
| **Kafka Broker Offline during Outbox Publish** | Delayed portfolio updates | The outbox publisher goroutine retries the publish loop indefinitely. Outbox records remain `processed = false`. No events are lost. |

---

## 8. Summary of Service Invariants

* **SI-1 (Atomicity)**: Wallet balance clearing and Outbox logging must occur inside a single, non-distributed local transaction boundaries.
* **SI-2 (Lead Idempotency)**: The primary key of the settlement ledger is `trade_id` propagated from the Matching Engine. A unique constraint violation must drop subsequent processing without raising alerts.
* **SI-3 (Order Preservation)**: Since Kafka consumer partitions are mapped directly by `market_id`, trades are settled sequentially in the exact chronological order they were matched.
