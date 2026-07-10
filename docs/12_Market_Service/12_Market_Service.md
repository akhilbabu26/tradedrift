# TradeDrift — Market Service

> **Status:** ✅ Designed (V1)
> **Document:** 12_Market_Service.md
> **Service:** Market Service
> **Version:** V1.5
> **Last Updated:** July 2026
> Revision notes: V1.5 finalizes scalability and reliability: (1) details three deployment roles (API, Worker, Cron) on a single codebase; (2) implements Go singleflight request coalescing to prevent cache stampedes on the Postgres fallback path; (3) adds a batched `GET /markets/tickers` endpoint using Redis MGET; (4) documents K8s recovery policies for the single-replica Cron role; (5) explains the orderbook 503 fallback rationale. Source of truth for historical candles and tickers: `TradeExecuted` (Matching Engine Kafka event). Order books fetched directly from Redis (pushed by ME). Asset validation checks against Wallet Service `GetSupportedAssets` gRPC endpoint.

---

## Purpose

The Market Service manages the **exchange's trading pair configuration** and serves **read-side public market data** (order books, 24h tickers, and historical OHLC candles).

Its responsibilities are:

1. **Own the `markets` configuration ledger** (valid trading pairs with tick size, lot size, and active status).
2. **Expose REST APIs** for listing markets, fetching the active order book depth, fetching 24h ticker statistics, and fetching historical OHLC candles.
3. **Expose gRPC APIs** for internal services (e.g. Matching Engine to load market parameters at startup, Order Service to validate orders).
4. **Aggregate and write OHLC candles** to Postgres by consuming `TradeExecuted` Kafka events.
5. **Update and cache rolling 24h ticker statistics** in Redis.

---

## Out of Scope

| Concern | Owning Service |
|---|---|
| In-memory order matching & L2 book updates | Matching Engine |
| Settlement saga processing | Settlement Service |
| User trade history / fills ledger | Trade Service |
| User portfolio holdings & valuation | Portfolio Service |
| Real-time WebSocket event streaming | Notification Service |

---

## 1. System Architecture & Context

```
                         Matching Engine
                           │          │
        pushes L2 depth    │          │ publishes
        directly to Redis  │          │ TradeExecuted
                           ▼          ▼
             Redis: orderbook       Kafka: TradeExecuted
                     │                │
                     │                ▼
                     │       Market Service Consumer
                     │        ├── Update Postgres candles (1m, 5m, 15m, 1h, 1d)
                     │        └── Update Postgres trades log / Redis ticker
                     ▼                │
            Market Service REST API ◄─┘
            (GET /orderbook, /candles, /ticker)
```

- **Order Book Path (Redis):** The Matching Engine pushes a JSON L2 depth snapshot (`orderbook:{market_id}`) directly to Redis after matching. The Market Service bypasses the database completely and reads from Redis to serve `GET /markets/{id}/orderbook`.
- **Candles Path (Kafka):** Market Service consumes `TradeExecuted` from Kafka. Since it needs instant feedback for charts and tickers, it reacts to executions, not settlement.

---

## 2. Bootstrapping & Seeding

Markets are created and populated via database migrations.

### 2.1 Initial Market List (V1)

| Market ID | Base Asset | Quote Asset | Tick Size (Min Price Step) | Lot Size (Min Qty Step) |
|---|---|---|---|---|
| `BTC_USDT` | `BTC` | `USDT` | `0.01` | `0.0001` |
| `ETH_USDT` | `ETH` | `USDT` | `0.01` | `0.001` |
| `SOL_USDT` | `SOL` | `USDT` | `0.001` | `0.01` |

### 2.2 Bootstrapping script
Seeding is handled via standard SQL migrations checked into the repository (e.g., `market-service/db/migrations/0002_seed_initial_markets.up.sql`) to guarantee repeatable environments.

---

## 3. Database Schema

```sql
-- Configures trading pair rules
CREATE TABLE markets (
    id            VARCHAR(20) PRIMARY KEY,             -- e.g. "BTC_USDT"
    base_asset    VARCHAR(10) NOT NULL,                -- e.g. "BTC"
    quote_asset   VARCHAR(10) NOT NULL,                -- e.g. "USDT"
    tick_size     DECIMAL(30,10) NOT NULL,             -- min price step increment (e.g. 0.01)
    lot_size      DECIMAL(30,10) NOT NULL,             -- min quantity step increment (e.g. 0.0001)
    is_enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Stores trade records for 24h ticker calculations
CREATE TABLE market_trades (
    id            UUID PRIMARY KEY,                    -- = trade_id
    market_id     VARCHAR(20) NOT NULL,
    price         DECIMAL(30,10) NOT NULL,
    quantity      DECIMAL(30,10) NOT NULL,
    executed_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_market_trades_rolling ON market_trades(market_id, executed_at DESC);

-- Stores candles across resolutions
CREATE TABLE ohlc_candles (
    market_id     VARCHAR(20) NOT NULL,
    resolution    VARCHAR(5) NOT NULL,                 -- '1m' | '5m' | '15m' | '1h' | '1d'
    start_time    TIMESTAMPTZ NOT NULL,                -- start of resolution window
    open_price    DECIMAL(30,10) NOT NULL,
    high_price    DECIMAL(30,10) NOT NULL,
    low_price     DECIMAL(30,10) NOT NULL,
    close_price   DECIMAL(30,10) NOT NULL,
    volume        DECIMAL(30,10) NOT NULL DEFAULT 0,   -- base asset traded volume
    quote_volume  DECIMAL(30,10) NOT NULL DEFAULT 0,   -- quote asset traded volume (volume * price)
    PRIMARY KEY (market_id, resolution, start_time)
);

CREATE INDEX idx_candles_time ON ohlc_candles(market_id, resolution, start_time DESC);
```

**Schema notes:**
- `DECIMAL(30,10)` — matches the `MONETARY_PRECISION` standard in `Glossary.md`.
- No outbox table is required for Market Service, as it does not publish integration events in V1.

---

## 4. Candle Aggregation (On-Write Strategy)

Market Service consumes `TradeExecuted` events from Kafka and aggregates them dynamically into the `ohlc_candles` table.

```
                   Kafka consumer receives TradeExecuted
                                    │
                                    ▼
                     For resolutions (1m, 5m, 15m, 1h, 1d):
                     Calculate start_time window boundary
                                    │
                                    ▼
                UPSERT ohlc_candles row for resolution + start_time
                ON CONFLICT (market_id, resolution, start_time)
                DO UPDATE SET
                   high_price   = MAX(high_price, EXCLUDED.high_price),
                   low_price    = MIN(low_price, EXCLUDED.low_price),
                   close_price  = EXCLUDED.close_price,
                   volume       = volume + EXCLUDED.volume,
                   quote_volume = quote_volume + EXCLUDED.quote_volume
```

* **Formula for start_time:**
  * For resolution $R$ (e.g. 15 minutes): `start_time = truncate(executed_at) to closest previous R-minute boundary`.
* **Idempotency & Atomicity:**
  `TradeExecuted` has at-least-once delivery. To prevent double-aggregating volume, the consumer deduplicates incoming trades using a local `processed_trades` log or a unique constraint catch on `market_trades.id = trade_id`. 
  **Atomic Transaction Rule:** The trade deduplication log write (`INSERT INTO market_trades`) and the upserts of all five resolution candles (1m, 5m, 15m, 1h, 1d) must execute inside a **single atomic database transaction**. This prevents partial-failure states where some resolutions are updated while others are skipped, ensuring strict data consistency across all chart resolutions.

---

## 5. Ticker Statistics Engine (24h Rolling Window)

The 24h ticker statistics (high, low, volume, last price, price change percent) are stored in Redis under the hash key `ticker:{market_id}`.

### 5.1 Update Loop (Option A)
To prevent complex in-memory state tracking, Market Service runs a background ticker calculations job every **10 seconds**:

1. For each active market, execute:
   ```sql
   SELECT 
       MIN(price) as low_24h,
       MAX(price) as high_24h,
       SUM(quantity) as volume_24h,
       (SELECT price FROM market_trades WHERE market_id = $1 AND executed_at > NOW() - INTERVAL '24 hours' ORDER BY executed_at ASC LIMIT 1) as open_24h,
       (SELECT price FROM market_trades WHERE market_id = $1 ORDER BY executed_at DESC LIMIT 1) as last_price
   FROM market_trades
   WHERE market_id = $1 
     AND executed_at > NOW() - INTERVAL '24 hours';
   ```
2. Calculate:
   `price_change_percent_24h = ((last_price - open_24h) / open_24h) * 100`
3. Write/refresh the stats in Redis key `ticker:{market_id}`.

---

## 6. Admin API & Validation

Market Service exposes admin endpoints for trading pair management. They check for a `role` claim in the request context (forwarded by the API Gateway from the JWT). Only `"role": "admin"` is permitted.

### `POST /admin/markets`
- **Payload:** `id` (e.g. `BTC_USDT`), `base_asset`, `quote_asset`, `tick_size`, `lot_size`
- **Validation:**
  - `tick_size > 0` and `lot_size > 0`.
  - Makes a gRPC call to `Wallet.GetSupportedAssets()` and validates both `base_asset` and `quote_asset` exist in the returned list. If not, rejects with `400 Bad Request`.
- **Immutability:** `tick_size` and `lot_size` are immutable once the market is created.

### `PATCH /admin/markets/{id}`
- **Payload:** `is_enabled` (boolean)
- **Behavior:** Toggles trading pair status. Modifying status does not delete open orders or shut down the matching engine. It only triggers the Order Service cache lookup.
- **Fail-closed Policy (Downstream):** If the Market Service is completely unreachable and the Order Service's local cache is cold/expired, the Order Service must **fail-closed** and reject all incoming order placement requests for that market (returning a `market_configuration_unavailable` or `service_unavailable` error) to prevent orders with invalid parameters from corrupting the matching logic.

---

## 7. Match Engine Provisioning & Disabling Runbooks

Because V1 of the Matching Engine runs matching engine loops for all active markets within a single, shared node process (no hot-loading or distributed cluster), adding or removing a market configuration requires a controlled restart of the shared Matching Engine node, briefly taking all active markets offline. This is an accepted design constraint for V1.

### 7.1 Runbook: Adding a New Market (V1)
1. **Register Configuration:** Admin registers the new pair configuration via `POST /admin/markets`.
2. **Update Deployment Configuration:** Admin adds the new market to the list of active markets in the Matching Engine's configuration settings or environment variables.
3. **Restart ME Node:** Admin restarts the single Matching Engine process/container.
4. **Recovery / Startup:** Upon startup, the ME node queries `GetMarket` via gRPC for all listed markets. 
   - For the new market, it detects no checkpoint and starts consuming Kafka from offset `0` (normal cold start).
   - For existing markets, it recovers their resting books in-memory by loading their last saved database checkpoints and replaying Kafka offsets.

### 7.2 Runbook: Disabling & Decommissioning a Market (V1)
1. **Disable:** Admin toggles market status to `is_enabled = FALSE` via `PATCH /admin/markets/{id}`. The Order Service immediately blocks new order placements (subject to the 10-second cache TTL).
2. **Cool-down (15 seconds):** Wait 15 seconds. This ensures the Order Service cache has fully expired and all in-flight orders submitted during the transition window have been processed by the Matching Engine. The Matching Engine node **remains running** during this window.
3. **Purge Open Orders:** Admin calls the Order Service's endpoint `POST /admin/orders/cancel-all?market_id={id}` to cancel all open resting orders on the book.
4. **Remove & Restart ME Node:** Admin removes the market from the active list in the Matching Engine configuration, then restarts the Matching Engine process. The restarted node will no longer spin up a matching loop or partition consumer for the decommissioned market.

---

## 8. REST API

Public read-only endpoints.

### `GET /markets`
- Returns a list of all active trading pairs.

### `GET /markets/{market_id}/orderbook`
- Serves Level 2 orderbook depth directly from Redis `orderbook:{market_id}`.
- **Failover:** If Redis is down, returns `503 Service Unavailable` with error code `market_data_temporarily_unavailable`. Order books are a real-time, in-memory data structure owned by the Matching Engine; the Market Service does not attempt to reconstruct them from historical trades because execution logs do not contain resting limit liquidity.
- **Response:**
  ```json
  {
    "bids": [["58200.00", "0.25"], ["58190.00", "1.10"]],
    "asks": [["58210.00", "0.05"], ["58220.00", "0.95"]],
    "timestamp": 1799298302000
  }
  ```

### `GET /markets/{market_id}/ticker`
- Returns rolling 24h price change, high, low, volume, and last price from Redis `ticker:{market_id}`.
- **Failover:** If Redis is down, the Query node executes the Postgres 24h ticker aggregation query (§5.1) on the fly, using Go's `singleflight` library to coalesce concurrent requests and caching the result in-memory with a configurable TTL (Default: `1 second`).

### `GET /markets/tickers`
- **Description:** Batched endpoint to fetch tickers for multiple markets in a single network round-trip.
- **Query params:** `ids` (comma-separated list, e.g. `BTC_USDT,ETH_USDT`. If omitted, returns all active markets).
- **Implementation:** Executes a single Redis **`MGET`** call for all keys to avoid N+1 queries.
- **Response:** Array of ticker statistics objects.

### `GET /markets/{market_id}/candles`
- Query params: `resolution` ('1m'|'5m'|'15m'|'1h'|'1d'), `from` (timestamp), `to` (timestamp).
- Returns the historical candles array sorted by `start_time` ASC.

---

## 9. gRPC API (Internal)

```protobuf
service MarketService {
    rpc GetMarket(GetMarketRequest)
        returns (MarketResponse);

    rpc ListMarkets(ListMarketsRequest)
        returns (ListMarketsResponse);
}

message GetMarketRequest   { string market_id = 1; }
message ListMarketsRequest {}

message MarketResponse {
    string id          = 1;
    string base_asset  = 2;
    string quote_asset = 3;
    string tick_size   = 4;
    string lot_size    = 5;
    bool   is_enabled  = 6;
}
message ListMarketsResponse {
    repeated MarketResponse markets = 1;
}
```

- **Order Service:** Calls `GetMarket` (cached with 10s TTL) to check if the market exists and is active before accepting orders.
- **Matching Engine:** Calls `GetMarket` at startup to fetch tick and lot sizes.

---

## 10. Service Invariants

- **MI-1 (Immutability of Grid):** `tick_size` and `lot_size` columns on `markets` can never be updated after creation.
- **MI-2 (Immutability of History):** `market_trades` and `ohlc_candles` are append-only (candles are updated via upserts, never deleted).
- **MI-3 (Wallet Asset Integrity):** New markets can only be created using base and quote assets that exist in Wallet Service's `GetSupportedAssets` query.
- **MI-4 (Redis Book Source):** Order books are always served from Redis, which is maintained as a write-through replica by the Matching Engine.
- **MI-5 (Admin Authorization):** Admin writes require the caller's JWT role claim to equal `"admin"`.

---

## 11. Scalability & Reliability Strategy

To maintain simple codebase management while optimizing runtime characteristics, the Market Service operates as a **single microservice codebase** built into a single container image, but deployed under **three distinct runtime roles** in Kubernetes:

### 11.1 API Role (`market-service-api`)
* **Replicas:** N (Scales horizontally for client read volume).
* **Endpoints:** Serves REST and gRPC API queries.
* **Request Coalescing:** To prevent a "cache stampede" where multiple queries hit PostgreSQL concurrently during a Redis outage, the API nodes use Go's `singleflight` library (`golang.org/x/sync/singleflight`) to coalesce identical queries. If 100 concurrent requests for `BTC_USDT` ticker arrive, only 1 database query is executed, and all 100 receive the same result.
* **Configurable Fallback Cache:** The result of the singleflight DB scan is cached in-memory with a configurable TTL (Default: `1 second`) to shield the database from rapid client polling.
* **HTTP Caching:** Exposes orderbook and ticker REST endpoints with `Cache-Control: public, max-age=1, s-maxage=1` headers to leverage CDN edge caching.

### 11.2 Worker Role (`market-service-worker`)
* **Replicas:** N (Scales horizontally for write and ingestion throughput).
* **Consumer Group:** `market-service` subscribing to `TradeExecuted`.
* **Automatic Rebalancing:** Kafka distributes partitions (by `market_id` partition key) across active worker replicas. If a worker replica is terminated, the coordinator automatically reassigns its partitions to surviving workers, which resume from the last committed database offsets.
* **Independent In-Memory Buffers:** The 100ms or 100-event batch buffer runs independently per partition thread, flushing directly to Postgres using the atomic CTE upsert query.

### 11.3 Cron Role (`market-service-cron`)
* **Replicas:** 1 (Strictly single instance to prevent duplicate query load and write-races in Redis).
* **Schedules:** Runs the 10-second ticker query and the periodic candle rollup engine.
* **K8s SPOF Recovery:** To ensure high availability, the cron deployment enforces `RestartPolicy = Always` and Liveness/Readiness probes checking scheduler health. If the scheduler blocks or crashes, Kubernetes automatically restarts the pod and spawns a replacement container within seconds.

---

## 12. Cold-Start Market Liquidity (Market Making)

When a new market (e.g. `BTC_USDT`) is created and only a single user is registered on the exchange, organic trading is impossible because **matching requires two distinct counterparty users** (self-trading is strictly rejected/no-oped to prevent wash trading). 

To bootstrap the market and create liquidity:

```
           [Exchange Admin]
                  │
     1. Registers Liquidity Bots (e.g., User-100, User-101)
     2. Wallets seeded with USDT/BTC via INITIAL_ALLOCATION
                  │
                  ▼
          [Liquidity Bots] ─── 3. Place Limit Buy & Sell orders ───► [Matching Engine]
                                                                            │
                                                                   Updates Redis L2 book
                                                                            │
                                                                            ▼
          [Human User] ◄────── 4. Sees active order book ────────── [REST Gateway]
               │
      5. Places Limit/Market order
               │
               ▼
        [Match Executed] (User crosses with Bot order)
```

### 12.1 Liquidity Bots (Market Makers)
* **Creation:** The exchange operator registers one or more administrative bot accounts (e.g., `role: bot` or standard users).
* **Initial Funding:** These bot accounts are provisioned with wallets and funded with assets (e.g. 5 BTC and 300,000 USDT) via the standard seeding allocations in the Wallet Service.
* **Order Books:** The bots run an algorithmic script (e.g. quoting at fixed spreads around external index prices) and submit limit buy (bid) and limit sell (ask) orders to the Order Service.
* **First Trade:** When the first human user places an order (e.g. buys 0.1 BTC), their order crosses with the bot's resting limit sell order in the Matching Engine, triggering the market's very first trade match and candle entry!

### 12.2 Single-User Behavior
If a single user is active without bots:
* If they place a **Limit Order**: It is accepted and sits in the `orderbook` in Redis as the sole resting bid or ask. No match occurs.
* If they place a **Market Order**: Since the book has zero opposite-side liquidity, the market order fails the execution validation immediately and is cancelled (due to IOC policy).
* If they attempt to match against their own resting order (**Self-Trade**): Although V1's Matching Engine does not implement Self-Trade Prevention (STP) and will execute a self-match normally, relying on this for liquidity is undesirable (it produces no real price discovery and generates meaningless transaction logs). Liquidity bots are provided as the operational solution to establish real bid-ask spreads. (Self-trades are no-oped inside the Portfolio Service to prevent cost-basis corruption).
