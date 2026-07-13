# TradeDrift — Trade Service

> **Status:** ✅ Designed (V1)
> **Document:** 10_Trade_Service.md
> **Service:** Trade Service
> **Version:** V1
> **Last Updated:** July 2026
> Revision notes: V1 initial design. Source of truth: `TradeSettled` (Wallet Service outbox). Introduces `market_id` requirement on `TradeSettled` payload — see §2 for upstream contract change.

---

## Purpose

The Trade Service is the **canonical, queryable, user-facing ledger of settled trades**. It is a read-side projection: its entire write path is the `TradeSettled` Kafka event; it has no write API, no gRPC callers, no outbox, and publishes nothing to Kafka.

Its responsibilities are exactly three:

1. **Persist every settled trade** into a fast, indexed `trades` table.
2. **Serve authenticated user trade history** — `GET /trades` — a user's own fills, newest first.
3. **Serve the public per-market trade feed** — `GET /markets/{market_id}/trades` — anonymous, for price tickers and recent-trade panels.

---

## Out of Scope

| Concern | Owning Service |
|---|---|
| Balance transfer | Wallet Service |
| Settlement retry / dead-letter | Settlement Service |
| Portfolio PnL / holdings | Portfolio Service |
| Real-time WebSocket push | Notification Service |
| OHLC candles / order book | Market Service |
| Any write API | — (Kafka-only write path) |

---

## 1. Event Pipeline Positioning

```
Matching Engine ──TradeExecuted──► Settlement Service ──gRPC SettleTrade──► Wallet Service
                                                                                    │
                                                                     commits balance transfer
                                                                     inserts TradeSettled outbox
                                                                                    │
                                                             Outbox Publisher → Kafka: TradeSettled
                                                                       │              │              │
                                                                Trade Service    Portfolio Svc   Notification Svc
                                                                 (this doc)       (PnL)            (push)
```

**Why `TradeSettled` and not `TradeExecuted`:**

- `TradeExecuted` fires when a match happens. `TradeSettled` fires when balances have actually moved.
- Consuming `TradeSettled` puts Trade Service in the same event layer as Portfolio and Notification — all three react to the same event in the same Kafka partition order. A user can never see a trade in their history before their portfolio has updated.
- In TradeDrift, settlement is guaranteed — Settlement Service retries indefinitely and `SettleTrade` is idempotent. Every `TradeExecuted` will produce a `TradeSettled`. The small latency gap (typically milliseconds) is acceptable; the ordering guarantee is not.

---

## 2. Cross-Service Contract — `TradeSettled`

### 2.1 Required Upstream Change

The current `SettleTrade` gRPC signature (`06_Wallet_Service.md §gRPC APIs`) does not include `market_id`. This field is required by Trade Service for the per-market index.

**Changes required:**
- `09_Settlement_Service.md` — Settlement Service passes `market_id` to `Wallet.SettleTrade()`. It already receives this field from `TradeExecuted`.
- `06_Wallet_Service.md` — `SettleTrade` gRPC signature gains `market_id VARCHAR(20)`; `TradeSettled` outbox payload includes it.

### 2.2 `TradeSettled` Payload (after change)

| Field | Type | Source | Notes |
|---|---|---|---|
| `trade_id` | UUID | Matching Engine (UUIDv7) | Becomes `trades.id` |
| `buyer_id` | UUID | from `TradeExecuted` | |
| `seller_id` | UUID | from `TradeExecuted` | |
| `buy_order_id` | UUID | from `TradeExecuted` | |
| `sell_order_id` | UUID | from `TradeExecuted` | |
| `market_id` | VARCHAR(20) | from `TradeExecuted` | **New field** — e.g. `"BTC_USDT"` |
| `base_asset` | VARCHAR(16) | from `TradeExecuted` | |
| `quote_asset` | VARCHAR(16) | from `TradeExecuted` | |
| `price` | DECIMAL(30,10) | from `TradeExecuted` | |
| `quantity` | DECIMAL(30,10) | from `TradeExecuted` | |
| `executed_at` | TIMESTAMPTZ | Matching Engine clock | When the match happened |
| `settled_at` | TIMESTAMPTZ | Wallet Service clock | When `SettleTrade` committed |

> **Two timestamps:** `executed_at` is the canonical trade time — what clients see, what drives sort order. `settled_at` is the audit timestamp — when balances moved. They are typically milliseconds apart but are semantically distinct and both stored.

---

## 3. Database Schema

```sql
CREATE TABLE trades (
    id            UUID PRIMARY KEY,            -- = trade_id (UUIDv7, owned by Matching Engine)
    buyer_id      UUID NOT NULL,
    seller_id     UUID NOT NULL,
    buy_order_id  UUID NOT NULL,
    sell_order_id UUID NOT NULL,
    market_id     VARCHAR(20) NOT NULL,
    base_asset    VARCHAR(16) NOT NULL,
    quote_asset   VARCHAR(16) NOT NULL,
    price         DECIMAL(30,10) NOT NULL,     -- MONETARY_PRECISION per Glossary.md
    quantity      DECIMAL(30,10) NOT NULL,     -- MONETARY_PRECISION per Glossary.md
    executed_at   TIMESTAMPTZ NOT NULL,        -- ME clock: time of match
    settled_at    TIMESTAMPTZ NOT NULL         -- Wallet clock: time balances moved
);

-- User trade history (buyer side)
CREATE INDEX idx_trades_buyer  ON trades(buyer_id,  executed_at DESC);

-- User trade history (seller side)
CREATE INDEX idx_trades_seller ON trades(seller_id, executed_at DESC);

-- User trade history filtered by market (buyer side)
CREATE INDEX idx_trades_buyer_market  ON trades(buyer_id,  market_id, executed_at DESC);

-- User trade history filtered by market (seller side)
CREATE INDEX idx_trades_seller_market ON trades(seller_id, market_id, executed_at DESC);

-- Public market trade feed
CREATE INDEX idx_trades_market ON trades(market_id, executed_at DESC);
```

**Schema notes:**

- `id = trade_id` — Trade Service does not generate IDs. Per `TradeDrift_ID_Correlation_Standard.md §7`, `trade_id` is owned by the Matching Engine (UUIDv7, generated at match time). Trade Service uses it as its primary key.
- `DECIMAL(30,10)` — matches `MONETARY_PRECISION` in `Glossary.md`, consistent with Order Service and Wallet Service.
- **Append-only** — no `UPDATE` or `DELETE` ever executes on this table. A trade row is written once and never modified.
- No `status` column — only settled trades reach `TradeSettled`. There is no intermediate state in this table.
- No `outbox_events` table — Trade Service publishes no Kafka events.

---

## 4. Idempotency

`TradeSettled` is an at-least-once Kafka event. Idempotency is enforced at the database level:

```sql
INSERT INTO trades (id, buyer_id, seller_id, buy_order_id, sell_order_id,
                    market_id, base_asset, quote_asset, price, quantity,
                    executed_at, settled_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (id) DO NOTHING;
```

A redelivered `TradeSettled` for the same `trade_id` writes nothing and returns cleanly. The Kafka offset is then acknowledged normally. No application-level deduplication table is needed — the `PRIMARY KEY` constraint is sufficient.

This is the same `ON CONFLICT DO NOTHING` pattern established in Settlement Service V1.2 §5.1.

---

## 5. Kafka Consumer

> **Naming:** This event is referred to as `TradeSettled` throughout this document — that is its informal conceptual name. The registered Kafka topic name is `user-trades.settled.v1`. The `event_type` field in the message envelope is `UserTradeSettled`. All three refer to the same event. Source of truth: `15_Kafka_Topic_Design.md §4.5`.

| Property | Value |
|---|---|
| Topic | `user-trades.settled.v1` |
| Partition key | `user_id` |
| Consumer group | `trade-service` |
| Concurrency | One goroutine per Kafka partition |
| Write path | Parse → `INSERT ON CONFLICT DO NOTHING` → ACK |
| Recovery goroutine | Not needed |

**One goroutine per partition** — the same SI-3 ordering invariant used by Settlement Service. Because the topic partitions by `user_id`, all settlement events for a given user land on the same partition in chronological order.

**No recovery goroutine needed.** Unlike Settlement Service, there is no network call between the INSERT and the Kafka ACK. The entire write is a single fast DB INSERT. If it fails, the offset is not committed and Kafka redelivers — which the `ON CONFLICT` handles safely.

### Consumer Step-by-Step

1. Receive `UserTradeSettled` message from `user-trades.settled.v1` Kafka partition.
2. Deserialize and validate payload — all required fields present, UUIDs valid, amounts positive.
3. `INSERT INTO trades (...) ON CONFLICT (id) DO NOTHING`.
4. ACK Kafka offset.

---

## 6. REST API

All endpoints are read-only. The write path is Kafka-only.

### `GET /trades`

| Property | Value |
|---|---|
| Auth | Required (JWT) |
| Description | Authenticated user's trade history — trades where the caller is buyer or seller — newest first |
| Pagination | Cursor-based on `(executed_at DESC, id DESC)` |
| Query params | `limit` (default 20, max 100), `cursor`, optional `market_id` filter |

When `market_id` is supplied, queries are backed by composite indexes `idx_trades_buyer_market` and `idx_trades_seller_market` to prevent full table scans or in-memory sorts.

### `GET /trades/{trade_id}`

| Property | Value |
|---|---|
| Auth | Required (JWT) |
| Description | Single trade by ID |
| Authorization | Returns `403 Forbidden` if the caller is neither `buyer_id` nor `seller_id`, **unless the caller has an admin or auditor role (e.g., `role: admin` or `role: auditor` in JWT claims)**. This allows compliance auditors and operations teams to audit transactions while preventing standard users from leaking counterparty info. |

### `GET /markets/{market_id}/trades`

| Property | Value |
|---|---|
| Auth | None — public endpoint |
| Description | Recent trades for a market — price tickers, recent-trade panels |
| Pagination | Cursor-based, default limit 50, max 200 |
| Response | `trade_id`, `price`, `quantity`, `executed_at` only — **`buyer_id` and `seller_id` are NOT returned** |

---

## 7. gRPC API (Internal)

Internal interface — not exposed via the public REST gateway.

```protobuf
service TradeService {
    rpc GetTrade(GetTradeRequest)
        returns (TradeResponse);

    rpc ListUserTrades(ListUserTradesRequest)
        returns (ListUserTradesResponse);

    rpc ListMarketTrades(ListMarketTradesRequest)
        returns (ListMarketTradesResponse);
}

message GetTradeRequest       { string trade_id  = 1; }
message ListUserTradesRequest { string user_id   = 1; string cursor = 2; int32 limit = 3; }
message ListMarketTradesRequest { string market_id = 1; string cursor = 2; int32 limit = 3; }
```

`ListMarketTrades` returns full trade detail including `buyer_id`/`seller_id` — acceptable on the internal interface, not reachable from outside the cluster.

Primary caller: **Portfolio Service** — may call `ListUserTrades` to reconstruct state on cold start rather than replaying Kafka from offset 0.

---

## 8. Cursor Pagination

All paginated endpoints use a **keyset cursor** on `(executed_at DESC, id DESC)`:

- `executed_at` — primary sort (time ordering).
- `id` — secondary sort for tie-breaking within the same millisecond. UUIDv7 embeds a millisecond timestamp in its high bits, so IDs at the same millisecond are still weakly ordered.

**Cursor encoding:** base64-encoded `{executed_at_unix_nano}:{id_uuid}` — opaque to clients.

```sql
-- User history (buyer side, after cursor, no market filter):
-- Uses: idx_trades_buyer
SELECT * FROM trades
WHERE buyer_id = $user_id
  AND (executed_at, id) < ($cursor_executed_at, $cursor_id)
ORDER BY executed_at DESC, id DESC
LIMIT $limit;

-- User history (buyer side, after cursor, filtered by market):
-- Uses: idx_trades_buyer_market
SELECT * FROM trades
WHERE buyer_id = $user_id
  AND market_id = $market_id
  AND (executed_at, id) < ($cursor_executed_at, $cursor_id)
ORDER BY executed_at DESC, id DESC
LIMIT $limit;
```

For user history (buyer OR seller), a UNION ALL over the two indexes is used and then re-sorted:

```sql
-- Without market filter:
SELECT * FROM (
    SELECT * FROM trades WHERE buyer_id  = $user_id
    UNION ALL
    SELECT * FROM trades WHERE seller_id = $user_id
) t
WHERE (executed_at, id) < ($cursor_executed_at, $cursor_id)
ORDER BY executed_at DESC, id DESC
LIMIT $limit;

-- With market filter:
SELECT * FROM (
    SELECT * FROM trades WHERE buyer_id  = $user_id AND market_id = $market_id
    UNION ALL
    SELECT * FROM trades WHERE seller_id = $user_id AND market_id = $market_id
) t
WHERE (executed_at, id) < ($cursor_executed_at, $cursor_id)
ORDER BY executed_at DESC, id DESC
LIMIT $limit;
```

Stable under concurrent inserts — a new trade arriving while a client is paginating does not cause rows to be skipped or duplicated.

---

## 9. Failure Handling

| Scenario | Impact | Action |
|---|---|---|
| **Duplicate `TradeSettled`** | Same `trade_id` redelivered | `ON CONFLICT (id) DO NOTHING` — silent no-op, ACK offset |
| **Invalid payload** | Missing field, malformed UUID, negative amount | Push to DLQ; ACK original offset; log structured error with `trade_id` and raw payload |
| **Postgres unavailable** | INSERT fails | Do NOT ACK. Retry with exponential backoff. Consumer pauses until DB is reachable |
| **Kafka broker offline** | Cannot consume | Consumer group pauses. No data loss — messages remain in topic. No write-side impact |
| **Consumer crash** | Offset uncommitted | Kafka redelivers from last committed offset on restart. `ON CONFLICT` absorbs re-processed messages |

---

## 10. Internal Package Structure

```
trade-service/
  api/
    grpc/           -- gRPC handlers
    rest/           -- grpc-gateway REST handlers
  service/          -- query building, cursor encode/decode, auth enforcement
  repository/       -- DB queries against trades table
  kafka/
    consumer/       -- TradeSettled consumer (one goroutine per partition)
  models/           -- Trade struct, cursor type, request/response types
  db/               -- connection pool, migrations
```

No `kafka/publisher/`, no `outbox/` — Trade Service never publishes.

---

## 11. Service Invariants

- **TI-1 (Immutable rows):** `trades` is append-only. No `UPDATE` or `DELETE`, ever.
- **TI-2 (Kafka-only writes):** No external write API. Only the `TradeSettled` consumer writes rows.
- **TI-3 (Idempotent consumer):** `ON CONFLICT (id) DO NOTHING` — safe for at-least-once delivery.
- **TI-4 (No Kafka publishing):** Trade Service has no outbox and produces no events.
- **TI-5 (Authorization):** User endpoints enforce party membership. Public market endpoint strips all user identity. Admin/auditor roles bypass individual user auth checks on `GET /trades/{id}` to facilitate compliance auditing and operational resolution.
