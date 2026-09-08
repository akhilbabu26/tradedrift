# TradeDrift — Portfolio Service

> **Status:** ✅ Designed (V1)
> **Document:** 11_Portfolio_Service.md
> **Service:** Portfolio Service
> **Version:** V1.2  
> **Last Updated:** September 2026  
> Revision notes: V1.2 synchronizes with current implementation: (1) Inbound trade ingestion consumes `portfolio.user.trades.v1` (Wallet Service outbox, partitioned by `user_id`); (2) Market prices queried dynamically via Market Service gRPC (`MarketService.GetTicker`); (3) Sequence collision protection via `processed_market_sequences` and user trade leg deduplication via `processed_user_trades`; (4) Monotonic position versioning stamped into `holdings` and `portfolio_outbox`.

---

## Purpose

The Portfolio Service tracks **holdings, average entry prices, and realized profit and loss (PnL)** for all users. It is a state-tracking microservice: it consumes user-scoped trade events (`portfolio.user.trades.v1`) from Kafka, updates internal holdings state inside a Postgres database, asserts market sequence integrity, increments monotonic version counters, writes event notifications to a transactional outbox table, and publishes `PortfolioUpdated` events to Kafka.

Its responsibilities are:

1. **Maintain user crypto holdings records** (current net balance, total cost basis, average entry price, and cumulative realized PnL per asset).
2. **Expose read-only gRPC APIs** for user portfolio summaries and detailed holdings lists (called by API Gateway).
3. **Calculate total portfolio valuation** dynamically by combining database holdings records with cash balances queried from Wallet Service (gRPC) and latest mark prices queried from Market Service (gRPC).
4. **Publish `PortfolioUpdated` events** via the transactional outbox pattern to `portfolios.updated.v1` to trigger WebSocket client notifications.
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
             Kafka: portfolio.user.trades.v1 (keyed by user_id)
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
                               Outbox Publisher (V1 single active)
                                         │
                                         ▼
                            Kafka: portfolios.updated.v1
                                         │
                                         ▼
                               Notification & WS Service
                               (Real-Time Push)
```

---

## 2. Dynamic Real-time Valuation Strategy

Persisting "unrealized PnL" or "total portfolio valuation" in a database is a major anti-pattern, as changing market prices would render the data stale instantly. Instead, the Portfolio Service uses a **hybrid real-time valuation strategy**:

### 1. Cash Balance (gRPC read on demand)
Portfolio Service does not track cash balance (`USDT`) locally. Doing so introduces high risk of data drift under complex wallet operations (deposits, withdrawals, fees, or initial allocation). 
When a portfolio summary is requested, the Portfolio Service queries Wallet Service synchronously via the `Wallet.GetBalances(user_id)` gRPC interface to retrieve the current available and reserved cash balances.

### 2. Market Prices (Market Service gRPC read on demand)
Portfolio Service queries current mark prices directly from `MarketService.GetTicker(market_id)` (e.g. `BTC-USDT`) over gRPC on demand to compute live asset valuation.

### 3. Cost Basis & Realized PnL (Postgres local)
Holdings (quantity, cost basis, average entry price, realized PnL, monotonic version) are computed from the historical flow of trades and stored locally in Postgres.

```
                  Client GET /api/v1/portfolio/summary
                                │
                                ▼
                       Portfolio Service
                        ├── Query local DB holdings (BTC qty, cost basis)
                        ├── Query Market Service gRPC for mark price (BTC-USDT ticker)
                        └── Query Wallet Service gRPC for USDT cash balance
                                │
                                ▼
                     Calculate on-the-fly:
                     - market_value = qty * last_price
                     - unrealized_pnl = market_value - total_cost
                     - total_equity = cash + sum(market_values)
```

---

## 3. Trade Processing Logic (Accounting Rules)

When a message is consumed from `portfolio.user.trades.v1`, the service executes `ProcessUserTrade(UserTradeInput)` inside a single atomic PostgreSQL transaction. Each message represents one user's trade leg (`role == "BUY"` or `role == "SELL"`).

The calculations are performed using row-level locking on the user's holding row (`SELECT ... FROM holdings WHERE user_id = $1 AND asset_code = $2 FOR UPDATE`). Because events are partitioned by `user_id`, only a single user holding row is locked per transaction, eliminating cross-user deadlocks entirely.

### 3.1 BUY Accounting (Asset Addition)
For a BUY leg, the quantity of the base asset increases, and the cost basis increases by the trade value (`quantity × price`).
- **Formulas:**
  ```text
  quantity += Q
  cost += Q × P
  average_entry = cost / quantity
  version += 1
  ```
- **Realized PnL:** Unchanged during BUY operations.

### 3.2 SELL Accounting (Asset Reduction)
For a SELL leg, the quantity of the base asset decreases. The cost basis is reduced proportionally based on the previous `average_entry_price`. Realized profit or loss is recorded.
- **Invariant Check:** Verifies `holding.quantity >= Q`. If insufficient, returns `ErrInsufficientHoldings` and routes the event to `trades.settled.dlq` (zero silent clamping).
- **Formulas:**
  ```text
  COGS = Q × previous_average_entry
  revenue = Q × P
  realized_PnL += revenue - COGS

  quantity -= Q
  cost -= COGS
  version += 1
  ```
- **Zero-Reset Clamping:** If `quantity == 0` (full position liquidation), `cost` is set to exactly `0` to eliminate floating-point epsilon drift.

### 3.3 Sequence Integrity & Idempotency
1. **Market Sequence Integrity:** Checked against `processed_market_sequences(market_id, sequence)`. If the `(market_id, sequence)` pair was already recorded for a different `trade_id`, transaction aborts with `ErrSequenceCollision`.
2. **User Leg Idempotency:** Checked against `processed_user_trades(trade_id, user_id)`. If the row already exists:
   - If metadata (price, quantity, role, market) matches: safely skipped (`ErrTradeAlreadyProcessed`).
   - If metadata conflicts: transaction aborts with `ErrTradeConflict`.

---

## 4. Database Schema (Goose Migrations 00001 & 00002)

```sql
CREATE TABLE IF NOT EXISTS holdings (
    user_id             UUID NOT NULL,
    asset_code          VARCHAR(10) NOT NULL,
    quantity            DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (quantity >= 0),
    total_cost          DECIMAL(30,10) NOT NULL DEFAULT 0 CHECK (total_cost >= 0),
    realized_pnl        DECIMAL(30,10) NOT NULL DEFAULT 0,
    version             BIGINT NOT NULL DEFAULT 0,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, asset_code)
);

CREATE INDEX IF NOT EXISTS idx_holdings_user ON holdings(user_id);

CREATE TABLE IF NOT EXISTS processed_user_trades (
    trade_id            UUID NOT NULL,
    user_id             UUID NOT NULL,
    market_id           VARCHAR(20) NOT NULL DEFAULT '',
    sequence            BIGINT NOT NULL DEFAULT 0,
    order_id            UUID NOT NULL,
    role                VARCHAR(10) NOT NULL,
    price               DECIMAL(30,10) NOT NULL,
    quantity            DECIMAL(30,10) NOT NULL,
    processed_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (trade_id, user_id)
);

CREATE TABLE IF NOT EXISTS processed_market_sequences (
    market_id           VARCHAR(20) NOT NULL,
    sequence            BIGINT NOT NULL,
    trade_id            UUID NOT NULL,
    recorded_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (market_id, sequence)
);

CREATE TABLE IF NOT EXISTS portfolio_outbox (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_id        UUID NOT NULL,                             -- user_id
    event_type          VARCHAR(50) NOT NULL,                      -- 'PortfolioUpdated'
    payload             JSONB NOT NULL,
    partition_key       VARCHAR(50) NOT NULL,                      -- user_id
    status              VARCHAR(20) NOT NULL DEFAULT 'PENDING',    -- 'PENDING' | 'PROCESSING' | 'PUBLISHED'
    claimed_at          TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at        TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_portfolio_outbox_pending ON portfolio_outbox(created_at)
    WHERE status = 'PENDING';

CREATE INDEX IF NOT EXISTS idx_portfolio_outbox_processing ON portfolio_outbox(claimed_at)
    WHERE status = 'PROCESSING';
```

---

## 5. Transactional Outbox Pattern

To ensure the holding updates and the event publishing are atomically consistent (preventing double-publishing or failing to publish after a DB commit), the Portfolio Service implements a **Transactional Outbox** pattern:

1. **Atomic Write:** The `ProcessUserTrade` transaction mutates the `holdings` row, increments `version = version + 1`, inserts a `PortfolioUpdated` record into the `portfolio_outbox` table (with `portfolio_version`), and commits the transaction.
2. **Background Publisher (Single Active Instance V1):** A background goroutine polls the `portfolio_outbox` table every 100ms using an atomic CTE with `FOR UPDATE SKIP LOCKED`:
   ```sql
   WITH claim AS (
       SELECT id
       FROM portfolio_outbox
       WHERE status = 'PENDING'
          OR (status = 'PROCESSING' AND claimed_at < NOW() - INTERVAL '1 minute')
       ORDER BY created_at ASC
       LIMIT $1
       FOR UPDATE SKIP LOCKED
   )
   UPDATE portfolio_outbox
   SET status = 'PROCESSING', claimed_at = NOW()
   WHERE id IN (SELECT id FROM claim)
   RETURNING id, aggregate_id, event_type, payload, partition_key;
   ```
3. **Kafka Publish:** For each row, the publisher writes the event to Kafka topic `portfolios.updated.v1` (partition key: `user_id`), then marks the row status as `PUBLISHED` (`WHERE id = $1 AND status = 'PROCESSING'`) and sets `published_at = NOW()`.

---

## 6. Kafka Consumer Group Design

- **Consumer Topic:** `portfolio.user.trades.v1`
- **Partition Key:** `user_id`
- **Consumer Group:** `portfolio-service-group`
- **Concurrency & Partition Ordering:** Partitioning by `user_id` ensures that all trade events for any single user arrive at the consumer sequentially in causal log order, eliminating cross-market race conditions for the same user.
- **Physical Row Lock:** `lockHoldingRow` executes `INSERT INTO holdings ... ON CONFLICT (user_id, asset_code) DO NOTHING` followed by `SELECT ... FOR UPDATE`, ensuring physical row existence and mutual exclusion.
- **Deduplication & Conflict Detection:** Idempotency is enforced by `processed_user_trades` (`PRIMARY KEY (trade_id, user_id)`) and `processed_market_sequences` (`PRIMARY KEY (market_id, sequence)`). Redeliveries with identical metadata safely ACK as no-op; metadata discrepancies surface as errors.

---

## 7. Integration Events

### 7.1 Consumed Event: `portfolio.user.trades.v1`
Emitted by Wallet Service outbox (2 events per trade: BUY with `buyer_id`, SELL with `seller_id`).
- **Topic:** `portfolio.user.trades.v1`
- **Partition Key:** `user_id`
- **Payload:**
  ```json
  {
    "trade_id": "uuid",
    "user_id": "uuid",
    "market_id": "BTC-USDT",
    "sequence": 42,
    "order_id": "uuid",
    "role": "BUY",
    "price": "96450.00",
    "quantity": "0.01",
    "executed_at": "2026-09-08T10:00:00Z",
    "settled_at": "2026-09-08T10:00:01Z"
  }
  ```

### 7.2 Published Event: `PortfolioUpdated`
Published by Portfolio Service outbox.
- **Topic:** `portfolios.updated.v1`
- **Partition Key:** `user_id`
- **Payload:**
  ```json
  {
    "user_id": "uuid",
    "asset_code": "BTC",
    "quantity": "0.15",
    "average_entry_price": "55000.00",
    "realized_pnl": "300.00",
    "portfolio_version": 4,
    "timestamp": "2026-09-08T10:00:01Z"
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
| **PI-1** | **No Cash Persistence:** Local database never stores or updates the cash balance. USDT cash balance must be queried dynamically via gRPC from Wallet Service. |
| **PI-2** | **No Unrealized PnL Storage:** Unrealized PnL and total portfolio equity are computed dynamically on read using mark prices fetched from Market Service. |
| **PI-3** | **Per-User Kafka Partition Affinity:** Accounting events are emitted to `portfolio.user.trades.v1` partitioned by `user_id`, preserving log order per user. |
| **PI-4** | **1-Atomic Transaction:** Trade leg deduplication (`processed_user_trades`), sequence collision protection (`processed_market_sequences`), holding adjustments (`holdings`), and outbox records (`portfolio_outbox`) are committed inside 1 single atomic database transaction. |
| **PI-5** | **Poison Error Quarantining (DLQ):** Invariant violations (insufficient balance, malformed UUIDs, decimal precision overflow) route to `trades.settled.dlq`. |
| **PI-6** | **Monotonic Portfolio Versioning:** Every holding modification increments `version = version + 1`, stamped into `holdings` and `portfolio_outbox`. |
| **PI-7** | **Zero Silent Clamping & Full Liquidation Reset:** Negative balance conditions are fatal (`ErrInsufficientHoldings` $\rightarrow$ DLQ). When $\text{quantity} = 0$, quantity and total cost are reset to exactly 0. |
| **PI-8** | **Single Active Outbox Publisher (V1):** Exactly one active Outbox Publisher instance runs in production for V1 to ensure strict per-user FIFO ordering to `portfolios.updated.v1`. |

---

## 11. Internal Package Structure

```
services/portfolio/
├── cmd/
│   └── server/
│       └── main.go       # 13-stage lifecycle orchestrator & dependency wiring
├── internal/
│   ├── config/           # Environment parsing & fail-fast validation
│   ├── handler/          # gRPC transport adapter (portfoliov1.PortfolioServiceServer)
│   ├── kafka/            # Inbound consumer, DLQ & Outbox publisher
│   ├── metrics/          # Prometheus instrumentation & collectors
│   ├── repository/       # Domain entity, interface & PostgreSQL implementation
│   │   └── postgres/     # Single-row locking, CTE outbox claiming, version tracking
│   └── service/          # Domain valuation math (Wallet cash + Market tickers)
└── migration/            # Goose SQL migrations (00001, 00002)
```
