# TradeDrift — Portfolio Service

> **Status:** ✅ Designed (V1)
> **Document:** 11_Portfolio_Service.md
> **Service:** Portfolio Service
> **Version:** V1.1
> **Last Updated:** July 2026
> Revision notes: V1.1 fixes two design gaps: (1) clarifies that cross-market trades for a single user run on separate Kafka partition goroutines concurrently, making DB row-locking the sole protection against lost updates; (2) adds a dedicated self-trade accounting handler to prevent deadlocks and cost-basis corruption. Source of truth for trades: `TradeSettled` (Wallet Service outbox). Source of truth for cash balance: Wallet Service gRPC. Source of truth for market prices: Redis.

---

## Purpose

The Portfolio Service tracks **holdings, average entry prices, and realized profit and loss (PnL)** for all users. It is a state-tracking microservice: it consumes `TradeSettled` events from Kafka, updates internal holdings state inside a Postgres database, writes event notifications to a transactional outbox table, and publishes `PortfolioUpdated` events to Kafka.

Its responsibilities are:

1. **Maintain user crypto holdings records** (current net balance, total cost basis, and cumulative realized PnL per asset).
2. **Expose read-only APIs** for user portfolio summaries and detailed holdings lists.
3. **Calculate total portfolio valuation** dynamically by combining database holdings records with cash balances queried from Wallet Service (gRPC) and latest market prices read from Redis.
4. **Publish `PortfolioUpdated` events** via the transactional outbox pattern to trigger WebSocket client notifications.
5. **Support self-healing/bootstrap** by rebuilding a user's holdings state from Trade Service's indexed gRPC trade records on startup or on-demand.

---

## Out of Scope

| Concern | Owning Service |
|---|---|
| Balance and reservation ledger | Wallet Service |
| Match orchestration | Matching Engine |
| Settlement orchestration | Settlement Service |
| Static trade history | Trade Service |
| Real-time WebSocket push | Notification Service |
| OHLC candles and order books | Market Service |

---

## 1. System Context & Event Flow

```
                  Kafka: TradeSettled
                           │
                           ▼
               Portfolio Service Consumer
                           │
             ┌─────────────┴─────────────┐
             ▼                           ▼
      Update postgres             Insert outbox
      holdings table              (PortfolioUpdated)
             │                           │
             ▼                           ▼
          Commit DB transaction atomically
                                         │
                                         ▼
                               Outbox Goroutine
                                         │
                                         ▼
                            Kafka: PortfolioUpdated
                                         │
                                         ▼
                               Notification Service
                               (WebSocket Push)
```

---

## 2. Dynamic Real-time Valuation Strategy

Persisting "unrealized PnL" or "total portfolio valuation" in a database is a major anti-pattern, as changing market prices would render the data stale instantly. Instead, the Portfolio Service uses a **hybrid real-time valuation strategy**:

### 1. Cash Balance (gRPC read on demand)
Portfolio Service does not track cash balance (`USDT`) locally. Doing so introduces high risk of data drift under complex wallet operations (deposits, withdrawals, fees, or initial allocation). 
When a portfolio summary is requested, the Portfolio Service queries Wallet Service synchronously via the `Wallet.GetBalances(user_id)` gRPC interface to retrieve the current available cash balance.

### 2. Market Prices (Redis read on demand)
Market Service writes the rolling 24h ticker statistics to Redis under the hash key `ticker:{market_id}` (e.g. `ticker:BTC_USDT`). Portfolio Service queries the `last_price` field from this hash on demand to get the latest asset valuation price.

### 3. Cost Basis & Realized PnL (Postgres local)
Holdings (quantity, cost basis, average price, realized PnL) are computed from the historical flow of trades and stored locally in Postgres.

```
                  Client GET /portfolio/summary
                                │
                                ▼
                       Portfolio Service
                        ├── Query local DB holdings (BTC qty, cost basis)
                        ├── Query Redis for market last_price (from ticker:BTC_USDT)
                        └── Query Wallet Service gRPC for USDT cash balance
                                │
                                ▼
                     Calculate on-the-fly:
                     - market_value = qty * market_price
                     - unrealized_pnl = market_value - total_cost
                     - total_value = cash + market_value
```

---

## 3. Trade Processing Logic (Accounting Rules)

When a `TradeSettled` message is consumed, the service updates the holdings row for the **base asset** of the trade (e.g. `BTC` for a `BTC_USDT` trade) for both the buyer and the seller. The quote asset (`USDT`) balance change is ignored by Portfolio Service since USDT balance is fetched dynamically from Wallet Service.

The calculations are performed inside a Postgres transaction using row-level locking (`SELECT ... FOR UPDATE` on user holding rows).

### 3.1 Buyer Leg (Asset Addition)
For the buyer, the quantity of the base asset increases, and the cost basis increases by the trade value (`quantity × price`).
- **Existing state:** `qty_prev`, `cost_prev`, `realized_pnl_prev`
- **Updated state:**
  ```sql
  qty_new = qty_prev + trade.quantity
  cost_new = cost_prev + (trade.quantity * trade.price)
  realized_pnl_new = realized_pnl_prev -- unchanged
  ```
- **Derived values:** `average_entry_price = cost_new / qty_new`

### 3.2 Seller Leg (Asset Reduction)
For the seller, the quantity of the base asset decreases. The cost basis is reduced proportionally based on the current `average_entry_price`. The difference between the sale price and the cost basis of the sold quantity is realized as profit or loss.
- **Existing state:** `qty_prev`, `cost_prev`, `realized_pnl_prev`
- **Derivation:** `avg_entry_price = cost_prev / qty_prev`
- **Updated state:**
  ```sql
  qty_new = qty_prev - trade.quantity
  cost_of_sold_qty = trade.quantity * avg_entry_price
  
  qty_new = MAX(0, qty_new) -- safety clamp
  cost_new = cost_prev - cost_of_sold_qty
  
  -- realize profit or loss
  trade_revenue = trade.quantity * trade.price
  realized_pnl_new = realized_pnl_prev + (trade_revenue - cost_of_sold_qty)
  ```
- **Clamping:** If `qty_new == 0`, then `cost_new` is set to exactly `0` to prevent fractional floating-point remainder drift or division-by-zero errors on subsequent trades.

### 3.3 Self-Trades (Wash Trades)

A self-trade is a trade where `buyer_id == seller_id`. In a self-trade, the user is buying from and selling to themselves.
- The user's net holding quantity does not change.
- The net cost basis does not change.
- No realized PnL is generated (self-sales cannot realize profits or losses).

**Handler Rule:**
If the consumer receives a `TradeSettled` event where `buyer_id == seller_id`:
1. The consumer skips mutating the `holdings` table entirely.
2. The consumer inserts the `trade_id` into the `processed_trades` table (to prevent double-processing on replay).
3. The consumer inserts a `PortfolioUpdated` event into `portfolio_outbox` carrying the current holdings unchanged (triggering UI refresh).
4. Commits the transaction.

This bypass prevents database deadlocks (trying to acquire two row-locks on the same `(user_id, asset_code)` row in the same transaction) and avoids cost-basis corruption.

---

## 4. Database Schema

```sql
CREATE TABLE holdings (
    user_id             UUID NOT NULL,
    asset_code          VARCHAR(10) NOT NULL,
    quantity            DECIMAL(30,10) NOT NULL DEFAULT 0,  -- current asset count
    total_cost          DECIMAL(30,10) NOT NULL DEFAULT 0,  -- net cost basis in quote currency
    realized_pnl        DECIMAL(30,10) NOT NULL DEFAULT 0,  -- cumulative realized PnL
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, asset_code)
);

-- Fast user lookup index
CREATE INDEX idx_holdings_user ON holdings(user_id);

-- Outbox table for atomic event publishing
CREATE TABLE portfolio_outbox (
    id            UUID PRIMARY KEY,
    aggregate_id  UUID NOT NULL,                             -- user_id
    event_type    VARCHAR(50) NOT NULL,                      -- 'PortfolioUpdated'
    payload       JSONB NOT NULL,
    partition_key VARCHAR(50) NOT NULL,                      -- user_id (keeps user events ordered)
    status        VARCHAR(20) NOT NULL DEFAULT 'PENDING',    -- 'PENDING' | 'PUBLISHED'
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at  TIMESTAMPTZ
);

-- Index to support the background publisher scanning for unpublished rows
CREATE INDEX idx_portfolio_outbox_pending ON portfolio_outbox(created_at) 
    WHERE status = 'PENDING';
```

---

## 5. Transactional Outbox Pattern

To ensure the holding updates and the event publishing are atomically consistent (preventing double-publishing or failing to publish after a DB commit), the Portfolio Service implements a **Transactional Outbox** pattern:

1. **Atomic Write:** The `TradeSettled` Kafka consumer opens a Postgres transaction, mutates the `holdings` row, inserts a `PortfolioUpdated` record into the `portfolio_outbox` table, and commits the transaction.
2. **Background Publisher:** A dedicated background goroutine polls the `portfolio_outbox` table using:
   ```sql
   SELECT id, aggregate_id, event_type, payload, partition_key
   FROM portfolio_outbox
   WHERE status = 'PENDING'
   ORDER BY created_at ASC
   LIMIT 100
   FOR UPDATE SKIP LOCKED;
   ```
3. **Kafka Publish:** For each row, the publisher writes the event to Kafka, then marks the row status as `PUBLISHED` and sets `published_at = NOW()` inside a short database transaction.

---

## 6. Kafka Consumer Group Design

- **Consumer Topic:** `TradeSettled`
- **Partition Key:** `market_id`
- **Consumer Group:** `portfolio-service`
- **Concurrency:** One goroutine per Kafka partition.
- **Race Condition Warning (Critical):** Partitioning by `market_id` ensures that events for a single market are sequential. However, a single user can trade on multiple markets concurrently (e.g. BTC-USDT and BTC-EUR). Because these events have different `market_id` partition keys, they are processed by **different consumer goroutines concurrently**, and will attempt to write to the same `(user_id, 'BTC')` holding row at the same time.
- **Sole Concurrency Protection:** Therefore, the row-level lock (`SELECT ... FOR UPDATE` in §3) is the **sole safety mechanism** that prevents concurrent updates from overriding each other (lost updates). Do not remove these row locks under the false assumption that Kafka partition ordering protects against multi-market user updates.
- **Deadlock Avoidance:** To prevent deadlocks when locking both the buyer and seller holding rows in a single transaction, rows must always be locked in a consistent deterministic order (e.g., always lock the lower `user_id` first: `IF buyer_id < seller_id`).
- **Idempotency Guard:** `TradeSettled` is at-least-once. However, since the database schema uses a `PRIMARY KEY (user_id, asset_code)` constraint and updates rows cumulatively, processing a duplicate event would cause double-counting.
- **Deduplication Check:** To guarantee idempotency, Portfolio Service maintains a small duplicate checking table or checks trade records:
  ```sql
  CREATE TABLE processed_trades (
      trade_id    UUID PRIMARY KEY,
      processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
  );
  ```
  Before processing a `TradeSettled` event, the consumer checks if `trade_id` exists in `processed_trades`. If it exists, the event is immediately ignored and the Kafka offset is acknowledged as a safe no-op. If `buyer_id == seller_id`, the self-trade bypass logic (§3.3) is applied.

---

## 7. Integration Events

### 7.1 Consumed Event: `TradeSettled`
Consumes from Wallet Service. See [07_Wallet_Service.md](../03_Wallet_Service/07_Wallet_Service.md) for the exact payload definition.

### 7.2 Published Event: `PortfolioUpdated`
Published by Portfolio Service outbox.

- **Topic:** `portfolio-updates`
- **Partition Key:** `user_id` (so all portfolio updates for a single user are processed sequentially by the Notification Service)
- **Payload:**
  ```json
  {
    "user_id": "uuid",
    "asset_code": "BTC",
    "quantity": "0.15",
    "average_entry_price": "55000.00",
    "realized_pnl": "300.00",
    "timestamp": "executed_at"
  }
  ```

---

## 8. REST API

All endpoints are read-only and require authentication.

### `GET /portfolio/summary`
- **Auth:** Required (JWT)
- **Description:** Returns the user's total portfolio valuation, realized PnL, unrealized PnL, and cash balance.
- **Response:**
  ```json
  {
    "user_id": "c1a967f6-6c8f-4d92-b430-c6d9bf764fbb",
    "total_value": "12500.5000000000",
    "realized_pnl": "450.0000000000",
    "unrealized_pnl": "850.5000000000",
    "cash_balance": "1000.0000000000",
    "updated_at": "2026-07-10T15:00:00Z"
  }
  ```

### `GET /portfolio/holdings`
- **Auth:** Required (JWT)
- **Description:** Returns the detailed active holdings list.
- **Response:**
  ```json
  [
    {
      "asset_code": "BTC",
      "quantity": "0.1500000000",
      "average_entry_price": "55000.0000000000",
      "total_cost": "8250.0000000000",
      "current_price": "58200.0000000000",
      "market_value": "8730.0000000000",
      "unrealized_pnl": "480.0000000000",
      "realized_pnl": "300.0000000000"
    }
  ]
  ```

---

## 9. Cold Start Bootstrap Strategy (Option B)

If the Portfolio Service's Postgres database is lost or corrupted, or when a user's holdings state is missing, the service employs a **self-healing bootstrap strategy** by calling the Trade Service:

1. **Detect Gap:** If a user queries `/portfolio` but no records exist in the `holdings` table (excluding new users with zero trades), or during a system-wide bootstrap command.
2. **gRPC Pull:** Portfolio Service calls Trade Service's gRPC endpoint `ListUserTrades(user_id, cursor=nil, limit=1000)`.
3. **Reconstruct:**
   - Sort all returned trades in chronological order (`executed_at ASC`).
   - Iterate through the trades sequentially in-memory and apply the trade accounting rules (§3) to calculate `quantity`, `total_cost`, and `realized_pnl`.
   - Update/INSERT the final holdings rows into Postgres.
4. **Resilience:** This prevents the need to replay months of Kafka messages from offset 0, providing predictable and rapid recovery times.

---

## 10. Service Invariants

| ID | Invariant |
|---|---|
| **PI-1** | **No USDT holdings row:** Local database never stores or updates the cash balance. USDT cash balance must be queried dynamically via gRPC to ensure consistency. |
| **PI-2** | **No Unrealized PnL storage:** Unrealized PnL is computed dynamically on the fly using market prices fetched from Redis. |
| **PI-3** | **At-least-once Deduplication:** Every processed `trade_id` is registered in `processed_trades` within the same transaction to prevent duplicate processing. |
| **PI-4** | **Row Locking Order:** Transactions mutate `holdings` and `processed_trades` under row locks, preventing concurrent update races for the same user. |
| **PI-5** | **Strict Decimal Precision:** All quantities, prices, costs, and PnL values use `DECIMAL(30,10)` columns per the glossary standards. |

---

## 11. Internal Package Structure

```
portfolio-service/
  api/
    grpc/           -- gRPC server (bootstrap endpoint if needed)
    rest/           -- grpc-gateway REST handlers
  service/          -- valuation calculator, trade processor, bootstrapping logic
  repository/       -- DB transactions (holdings, processed_trades, outbox)
  kafka/
    consumer/       -- TradeSettled consumer (group portfolio-service)
    publisher/      -- Outbox polling publisher (portfolio-updates topic)
  client/
    wallet/         -- Wallet Service gRPC client (GetBalances wrapper)
    trade/          -- Trade Service gRPC client (ListUserTrades wrapper)
  db/               -- connection pool, migrations
```
