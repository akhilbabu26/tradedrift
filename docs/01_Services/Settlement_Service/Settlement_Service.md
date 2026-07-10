# TradeDrift Settlement Service Specification

**Document:** 09_Settlement_Service.md  
**Service:** Settlement Service  
**Version:** V1.3  
**Status:** Design Complete  
**Last Updated:** July 2026  
**Revision notes:** V1.2 — aligned `settled_trades.price/quantity` to `DECIMAL(30,10)`; documented `ON CONFLICT DO NOTHING`; tightened `SKIP LOCKED` explanation. V1.3 — adds `market_id` to `Wallet.SettleTrade()` gRPC call (required by Trade Service for per-market index); adds Trade Service as a `TradeSettled` consumer.

---

## 1. Purpose

The Settlement Service is the transactional ledger gateway of the TradeDrift platform. It consumes execution output events from the Matching Engine, performs strict idempotency filtering, and calls the Wallet Service to securely shift reserved quote/base balances between buyers and sellers. Downstream notification of Portfolio and other consumers is handled exclusively by the **Wallet Service** via its own outbox after `SettleTrade` commits — Settlement Service does not publish to Kafka directly.

Its core objectives are:
1. **Zero Double-Settlement**: Guarantee that no trade is ever settled more than once, even under Kafka partition rebalances or network redeliveries.
2. **Guaranteed Delivery**: Ensure every trade execution successfully transfers balances and registers in the settlement ledger, preventing asset discrepancies.
3. **Short-Lived Transactions**: Database connections are never held open while a network call is in flight. Each transaction commits before the gRPC call begins, preventing connection pool exhaustion under Wallet Service degradation.

---

## 2. System Architecture & Context

The Settlement Service acts as a bridge between the asynchronous matching queue and the Wallet Service's transactional database.

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
│  └─────────────┬─────────────┘  │
└────────────────┼────────────────┘
                 │
                 │ gRPC call (outside any DB transaction)
                 ▼
┌─────────────────────────────────────────────────────┐
│      Wallet Service                                 │
│  (Locks/unlocks user balances)                      │
│  Publishes TradeSettled via its own outbox →        │
│  → Kafka: trade-settled  (consumed by Portfolio,    │
│    Notification, etc.)                              │
└─────────────────────────────────────────────────────┘
```

> **Downstream notification ownership:** Portfolio Service and Notification Service consume the `TradeSettled` event published by the **Wallet Service's** own outbox-backed Kafka topic — not from Settlement Service. Settlement Service has no outbox table and publishes no Kafka events of its own.

---

## 3. Cross-Service Contracts (Single Source of Truth)

The Settlement Service integrates directly against the output payloads of the Matching Engine. To prevent contract drift, the event schemas defined in the Matching Engine documentation serve as the single source of truth:

* **Trigger Event**: Consumes the `TradeExecuted` event payload defined in **[06_Event_Contracts.md (Section 4.1)](file:///C:/Users/AKHIL BABU/OneDrive/Desktop/tradedrift/docs/01_Services/Settlement_Service/08_Matching_Engine/06_Event_Contracts.md#L133-L157)**.
* **Idempotency Key**: Uses `trade_id` (generated as a UUIDv7 in-memory by the Matching Engine at match time) as the unique primary key for the ledger.
* **Pass-Through Fields**: Invokes the Wallet Service's gRPC method using field-for-field mappings from the event payload.

---

## 4. Idempotency & Database Schema

To prevent double-settlement, the Settlement Service implements an **Idempotent Consumer** pattern backed by a PostgreSQL database instance.

A `status` column (`PENDING` | `SETTLED`) is the key to making crash recovery safe without holding long database transactions:

* `PENDING` — the trade has been registered in the ledger, but the gRPC call to Wallet Service has not yet confirmed completion.
* `SETTLED` — both the ledger registration and the wallet balance transfer are confirmed complete.

### 4.1 SQL Schema

```sql
-- Represents the registry of all settled and in-progress trades
CREATE TABLE settled_trades (
    trade_id      UUID PRIMARY KEY,
    buyer_id      UUID NOT NULL,
    seller_id     UUID NOT NULL,
    buy_order_id  UUID NOT NULL,
    sell_order_id UUID NOT NULL,
    base_asset    VARCHAR(16) NOT NULL,
    quote_asset   VARCHAR(16) NOT NULL,
    price         DECIMAL(30,10) NOT NULL,
    quantity      DECIMAL(30,10) NOT NULL,
    status        VARCHAR(16) NOT NULL DEFAULT 'PENDING',   -- 'PENDING' | 'SETTLED'
    executed_at   TIMESTAMP WITH TIME ZONE NOT NULL,        -- copied from TradeExecuted.executed_at
    settled_at    TIMESTAMP WITH TIME ZONE                  -- populated when status transitions to 'SETTLED'
);

-- Index to support auditing and account ledger history queries
CREATE INDEX idx_settled_trades_buyer   ON settled_trades(buyer_id);
CREATE INDEX idx_settled_trades_seller  ON settled_trades(seller_id);

-- Partial index to support the recovery goroutine scanning for stale PENDING rows
CREATE INDEX idx_settled_trades_pending ON settled_trades(executed_at)
    WHERE status = 'PENDING';
```

> **Why no `outbox_events` table:** Settlement Service does not publish Kafka events. The `TradeSettled` event (consumed by Portfolio and Notification Services) is published by the Wallet Service's own outbox immediately after `SettleTrade` commits atomically inside the Wallet DB. See [06_Wallet_Service.md § Event Ownership: TradeSettled](../../07_Wallet_Service/07_Wallet_Service.md).

---

## 5. Settlement Execution Pipeline

For every `TradeExecuted` message consumed from Kafka, the service executes a **two-phase settlement** that keeps each database transaction short and never holds an open connection while waiting on a network call.

```
            Receive TradeExecuted event
                        │
                        ▼
         Query trade_id in settled_trades
                        │
        ┌───────────────┴──────────────────────┐
        │ Row found                            │ No row found
        ▼                                      │
  Read status                                  │
        │                                      │
  ┌─────┴──────────┐                           │
  │ SETTLED        │ PENDING                   │
  ▼                │                           │
[ACK Kafka]        │              ── PHASE 1 (Short TX) ──
 (no-op, done)     │              INSERT settled_trades
                   │              ON CONFLICT (trade_id) DO NOTHING
                   │              (status='PENDING',
                   │               executed_at=event.executed_at)
                   │              COMMIT  ← DB connection released
                   │                           │
                   └───────────────────────────┘
                                 │
                                 ▼
                       ── PHASE 2 (No DB TX) ──
                       Call Wallet.SettleTrade(...)
                       (gRPC, outside any DB transaction)
                                 │
                     ┌───────────┴────────────┐
                     │ Failure / Timeout      │ Success
                     ▼                        ▼
              Do NOT ACK Kafka      ── PHASE 3 (Short TX) ──
              Retry w/ backoff      UPDATE settled_trades
              (recovery goroutine   SET status = 'SETTLED',
               handles crash cases)     settled_at = NOW()
                                    WHERE trade_id = ?
                                    COMMIT
                                          │
                                          ▼
                                     ACK Kafka
```

### 5.1 Step-by-Step Logic

1. **Idempotency Check**: Query the `settled_trades` table for the incoming `trade_id`.
   - If a row with `status = 'SETTLED'` exists: the trade is fully complete. Acknowledge the Kafka offset as a safe no-op and stop.
   - If a row with `status = 'PENDING'` exists: a prior attempt registered the trade but the gRPC call did not complete (e.g., a crash between phases). Skip Phase 1 and proceed directly to Phase 2.
   - If no row exists: proceed to Phase 1.
2. **Phase 1 — Registration (Short Transaction)**: Open a short database transaction. INSERT the trade details into `settled_trades` with `status = 'PENDING'` and `executed_at` copied verbatim from the event payload, using `ON CONFLICT (trade_id) DO NOTHING`. Commit immediately and release the database connection. The `ON CONFLICT` clause handles the race where two consumer goroutines (e.g. during a brief partition rebalance overlap) attempt to INSERT the same `trade_id` simultaneously — the second INSERT becomes a silent no-op rather than a unique-constraint error, and both goroutines safely proceed to Phase 2 where Wallet-side idempotency absorbs any duplicate. The service holds no open DB connection beyond this point.
3. **Phase 2 — Wallet Balance Update (gRPC, No DB Connection Held)**: Execute a synchronous gRPC call to `Wallet.SettleTrade(trade_id, buyer_id, seller_id, buy_order_id, sell_order_id, base_asset, quote_asset, price, quantity, market_id)` on the Wallet Service. `market_id` is passed through from the `TradeExecuted` event payload — it is required by Trade Service for its per-market `(market_id, executed_at DESC)` index. This call executes a double-leg balance shift within the Wallet DB:
   * Moves quote asset (`quantity × price`) from the Buyer's reserved balance to the Seller's available balance.
   * Moves base asset (`quantity`) from the Seller's reserved balance to the Buyer's available balance.
   * Upon commit, the Wallet Service atomically inserts a `TradeSettled` outbox event for downstream consumers (Trade Service, Portfolio, Notification). Settlement Service is not involved in this step.

   > **Fee deduction:** Explicit fee parameters (`taker_fee`, `maker_fee`) are not present in the current `TradeExecuted` event payload or `SettleTrade` gRPC signature. Fee accounting is deferred to a future enhancement. No fee logic executes in V1. See [06_Wallet_Service.md § Future Extensions](file:///C:/Users/AKHIL BABU/OneDrive/Desktop/tradedrift/docs/01_Services/Settlement_Service/06_Wallet_Service.md).

4. **Phase 3 — Completion (Short Transaction)**: Open a second short database transaction. UPDATE `settled_trades` SET `status = 'SETTLED'`, `settled_at = NOW()` WHERE `trade_id = ?`. Commit and release the database connection.
5. **Kafka Acknowledge**: Commit the Kafka consumer offset only after Phase 3 commits successfully.

---

## 6. Crash Recovery — PENDING Row Handling

Because the Kafka offset is only acknowledged after Phase 3 commits, a service crash between Phases 1 and 3 leaves a `PENDING` row in `settled_trades` with an uncommitted Kafka offset. Two independent mechanisms ensure these are always resolved without data loss:

### 6.1 Kafka Redelivery (Primary Path)

On consumer restart, the Kafka client replays from the last uncommitted offset. The idempotency check in Step 1 finds the `PENDING` row and skips Phase 1, retrying the gRPC call directly in Phase 2. This resolves the common crash case with no additional tooling.

### 6.2 Recovery Goroutine (Safety Net)

An independent background goroutine runs on a configurable polling interval and scans for `PENDING` rows older than a stale threshold (e.g. 60 seconds):

```sql
SELECT trade_id, buyer_id, seller_id, buy_order_id, sell_order_id,
       base_asset, quote_asset, price, quantity
FROM settled_trades
WHERE status = 'PENDING'
  AND executed_at < NOW() - INTERVAL '60 seconds'
ORDER BY executed_at ASC
LIMIT 50
FOR UPDATE SKIP LOCKED;
```

For each row returned, the goroutine retries `Wallet.SettleTrade(...)` directly and, on success, performs Phase 3 (UPDATE to `SETTLED`). `FOR UPDATE SKIP LOCKED` is critical here: it means the goroutine only acquires rows that are not already locked by another session. If the main Kafka consumer is concurrently retrying Phase 2 for a `PENDING` row (holding a row-level lock via its own Phase 3 UPDATE), the recovery goroutine skips that row entirely rather than blocking on it — preventing two callers from simultaneously invoking `Wallet.SettleTrade` for the same trade. Wallet-side idempotency via `trade_id` means a concurrent duplicate call is safe, but `SKIP LOCKED` avoids the redundant round-trip entirely.

> **Wallet-side idempotency**: `Wallet.SettleTrade` uses `trade_id` as its own idempotency key via the `UNIQUE(reference_id, reference_type, asset)` constraint on `wallet_transactions`. Duplicate gRPC calls for the same `trade_id` — from either the main consumer or the recovery goroutine — are silently absorbed by the Wallet Service and return success.

---

## 7. Error Handling, Retries, & Failure Modes

Because the Settlement Service sits on the critical path of money movements, error scenarios are tightly handled:

| Failure Scenario | Impact | Action / Remediation |
|---|---|---|
| **Wallet Service gRPC Offline / Timeout** | Phase 2 cannot complete | Phase 3 is skipped; Kafka offset is **not** acknowledged. The consumer retries the message with exponential backoff. The recovery goroutine independently retries stale `PENDING` rows. No DB connection is held during retries. |
| **Postgres Database Offline** | Phase 1 or Phase 3 cannot execute | Kafka consumer halts processing, generating an alert. On resume, consumption restarts from the last uncommitted offset. Partially registered `PENDING` rows from the prior run are resolved by Kafka redelivery or the recovery goroutine on restart. |
| **Invalid Trade Payload (Logical Error)** | Out-of-bounds quantity or price | Phase 1 transaction is rolled back. The message is pushed to the **DLQ (Dead Letter Queue)** for manual inspection. The main consumer continues to prevent blocking other traders. |
| **Service Crash Between Phase 1 and Phase 3** | `PENDING` row in `settled_trades`; Kafka offset uncommitted | Kafka redelivery retries Phase 2 on restart (primary path). Recovery goroutine additionally scans for and retries stale `PENDING` rows. Wallet Service absorbs any duplicate gRPC calls idempotently via `trade_id`. |
| **Kafka Broker Offline** | Cannot consume new trades | Consumer group pauses. No data loss — messages remain in the topic. Settlement Service has no Kafka publishing responsibility, so broker outages do not affect the settlement write path. |

---

## 8. Summary of Service Invariants

* **SI-1 (Short Transactions)**: No database transaction may remain open while a network call (gRPC) is in flight. Phase 1 and Phase 3 are each committed and their database connections released before the next step begins.
* **SI-2 (Lead Idempotency)**: The primary key of the settlement ledger is `trade_id` propagated from the Matching Engine. A row with `status = 'SETTLED'` must cause all subsequent deliveries of the same event to be acknowledged as no-ops without raising alerts.
* **SI-3 (Order Preservation)**: Since Kafka consumer partitions are mapped by `market_id`, and the service runs **exactly one processing goroutine per Kafka partition** (no intra-partition concurrency), trades for the same market are settled sequentially in the exact chronological order they were matched. The recovery goroutine operates independently on stale `PENDING` rows and does not consume partition messages, so it does not affect per-market ordering.
* **SI-4 (No Kafka Publishing)**: Settlement Service publishes no events to Kafka directly. All downstream event notification (`TradeSettled`) is the exclusive responsibility of the Wallet Service's own outbox, ensuring a single authoritative source for downstream consumers.
