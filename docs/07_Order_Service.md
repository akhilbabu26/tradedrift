# TradeDrift — Order Service

> **Status:** ✅ Designed (V5)
> Revision notes: V5 audit — removed phantom PENDING/REJECTED states, added CANCELLING to state machine, added cancel-vs-fill race condition rules, market order IOC policy, market order fund reservation strategy, zero-fee V1 policy, schema column types, cleaned event names.

## Purpose

The Order Service owns the complete lifecycle of trading orders. It validates requests, coordinates with the Wallet Service to reserve funds, publishes accepted orders as events for the Matching Engine to consume, tracks order status, supports cancellation, and publishes domain events for downstream services.

## Responsibilities

- Generate a UUIDv7 `order_id` for every new order, before any database write (see [Section 1](#1-order-id-generation--uuidv7-before-persistence)).
- Create Market and Limit orders.
- Validate trading pair, quantity, price, and user permissions.
- Reserve funds through Wallet Service (synchronous call), passing the pre-generated `order_id`.
- Publish validated orders as events for the Matching Engine to consume.
- Track order status: `OPEN`, `PARTIALLY_FILLED`, `FILLED`, `CANCELLING`, `CANCELLED`.
- Cancel eligible orders and release the remaining reserved funds via Wallet Service.
- Publish Kafka events: `OrderCreated`, `OrderCancelRequested`.

## Out of Scope

- Does not own wallet balances.
- Does not perform matching.
- Does not calculate portfolio PnL.
- Does not send notifications directly.
- Does not retry or dead-letter settlement — that is Settlement Service's responsibility (see [Section 8](#8-saga-pattern-and-compensating-actions)).

---

## 1. Order ID Generation — UUIDv7, Before Persistence

Wallet Service's `ReserveFunds` requires `order_id` as a parameter, which means the order's ID must exist before Order Service's own database row does. A `BIGSERIAL` or `IDENTITY` column cannot satisfy this — the ID would not exist until after `INSERT`, but `ReserveFunds` must be called before the order is saved.

> **Fix:** `orders.id` is now a PostgreSQL `UUID` column. Order Service generates the value (UUIDv7) in application code the moment a create-order request is validated, before calling Wallet Service — not via a database default or sequence.

```
Generate UUIDv7 → order_id
  ↓
Validate request
  ↓
ReserveFunds(user_id, order_id, asset, amount)  -- gRPC to Wallet Service
  ↓
Wallet Service confirms reservation
  ↓
DB Transaction { Save Order(id = order_id, status OPEN) + Save Outbox Row (OrderCreated) }
  ↓
Commit → Return Success to client
```

This also means every downstream event (`OrderCreated` onward) carries an `order_id` that was known before the order's own row existed — Wallet, Trade, Portfolio, and Matching Engine never have to wait for a database-assigned ID. This generalizes across every aggregate in the system; see `TradeDrift_ID_Correlation_Standard.md` for the full cross-service rule (UUIDv7, owning-service generation, PostgreSQL `UUID` columns, and `order_id` as the lifecycle correlation key).

## Order Processing Model

Order Service uses a synchronous precondition check followed by a choreography-based saga. It performs one synchronous call (fund reservation) before the order exists, then every step after that is an independent service reacting to Kafka events with no central coordinator.

- **Synchronous part:** generate `order_id`, validate request, then reserve funds via gRPC to Wallet Service. This must complete before an order can be considered accepted, so it stays synchronous.
- **Choreographed part:** once the order is committed, the Matching Engine, Settlement Service, Portfolio Service, and Notification Service each independently consume and react to Kafka events. No service commands another; each just publishes what happened.

## Create Order Flow (Outbox Pattern)

```
API Gateway -> Order Service -> Generate order_id (UUIDv7) -> Validate
  -> Wallet (gRPC ReserveFunds, order_id included)
  -> DB Transaction { Save Order (id=order_id, status OPEN) + Save Outbox Row (OrderCreated) }
  -> Commit -> Return Success to client
  -> Outbox Publisher polls unpublished rows -> Kafka -> Matching Engine consumes
```

## Cancel Order Flow (Outbox Pattern)

```
Client -> Order Service -> Check ownership + status (OPEN or PARTIALLY_FILLED)
  -> DB Transaction { Save Order (status CANCELLING) + Save Outbox Row (OrderCancelRequested) }
  -> Commit -> Return Accepted to client
  -> Outbox Publisher polls unpublished rows -> Kafka -> Matching Engine consumes
  -> Matching Engine confirms removal -> OrderCancelled
  -> Order Service updates status to CANCELLED
  -> Order Service calls Wallet ReleaseFunds(order_id) via gRPC -> remaining funds released
```

### Cancel vs Fill Race Condition

`OrderCancelRequested` uses the **same Kafka partition key** (market symbol) as `OrderCreated`, so the Matching Engine processes them in order within each market. However, between `OrderCreated` being processed and `OrderCancelRequested` arriving, the ME may match the order against a counter-party.

**Rules:**
1. **Fills always take precedence over cancels.** A matched trade cannot be un-matched.
2. If the order is **fully filled** before the cancel arrives, ME ignores the cancel — does NOT publish `OrderCancelled`. Order Service receives `TradeExecuted` instead.
3. If the order is **partially filled**, ME cancels only the unfilled remainder and publishes `OrderCancelled` with the remaining quantity.
4. Order Service must handle receiving `TradeExecuted` for an order in `CANCELLING` state — it accepts the fill, updates filled quantities, and transitions to `PARTIALLY_FILLED` or `FILLED`. A subsequent `OrderCancelled` (if any) covers only the unfilled remainder.
5. **Fund release on cancel:** Order Service calls `Wallet.ReleaseFunds(order_id)` directly via gRPC when it processes `OrderCancelled` — Settlement Service is not involved in cancellations (its role is post-*match* coordination, not cancel coordination).

## Outbox Pattern

- `orders` table stores business state (the order's current status and quantities).
- `outbox` table stores events awaiting publication, written in the same transaction as the business state change that produced them.
- The Matching Engine never queries either table directly — it consumes Kafka only.

### Outbox Publisher Mechanism

V1 uses a polling publisher (short interval, e.g. 100–250ms). A background loop queries `WHERE published_at IS NULL`, publishes each row to Kafka using the row's partition key, then marks it published only after Kafka acknowledges.

## REST APIs

External, browser-facing REST endpoints generated by grpc-gateway from the gRPC contract below — not a second, parallel API surface. All REST traffic still passes through the API Gateway's auth and rate-limit middleware before translation to gRPC.

- `POST /orders`
- `GET /orders/{id}`
- `GET /orders`
- `DELETE /orders/{id}`

## gRPC APIs

- `CreateOrder`
- `CancelOrder`
- `GetOrder`
- `ListOrders`

> **Note:** Order status updates triggered by Kafka consumers (e.g., fill events from Matching Engine) are handled via internal service-layer methods, not exposed as gRPC endpoints — preventing external callers from corrupting order state.

## Kafka Events

- `OrderCreated` — published via outbox by Order Service, consumed by Matching Engine.
- `OrderCancelRequested` — published via outbox by Order Service on the same partition key (market symbol) as `OrderCreated`, consumed by Matching Engine.
- `TradeExecuted` — published by Matching Engine when a match occurs (carries `order_id` for both maker and taker), consumed by Settlement Service.
- `OrderCancelled` — published by Matching Engine once a cancel is confirmed, consumed by Order Service (triggers status update + fund release).

## Database Schema

```sql
orders(
  id UUID PRIMARY KEY,              -- generated by Order Service, UUIDv7
  user_id UUID NOT NULL,
  market_id VARCHAR(20) NOT NULL,   -- e.g. "BTC_USDT"; doubles as Kafka partition key
  side VARCHAR(4) NOT NULL,         -- BUY | SELL
  order_type VARCHAR(10) NOT NULL,  -- LIMIT | MARKET
  price DECIMAL(30,10),             -- NULL for market orders
  quantity DECIMAL(30,10) NOT NULL,
  filled_quantity DECIMAL(30,10) NOT NULL DEFAULT 0,
  remaining_quantity DECIMAL(30,10) NOT NULL,
  status VARCHAR(20) NOT NULL,      -- OPEN | PARTIALLY_FILLED | FILLED | CANCELLING | CANCELLED
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL
)

outbox(
  id UUID PRIMARY KEY,
  aggregate_id UUID,                -- = order_id
  event_type, payload, partition_key,
  published_at, created_at
)
```

## Validation Rules

- Authenticated user.
- Trading pair exists and market is open.
- Positive quantity.
- Limit orders require a price; market orders must NOT include a price.
- Enough available (unreserved) balance.

### Market Order Fund Reservation

For **limit buy orders**, the reservation amount is `price × quantity` (known upfront). For **market buy orders**, no price is known at order time.

> **Decision (V1):** Market buy orders reserve the **user's entire available balance** for the quote asset. After the order fills (fully or partially), the unused remainder is released back to `available_balance` via `ReleaseFunds`. This is the simplest correct approach — it guarantees sufficient funds regardless of fill price, at the cost of temporarily locking excess funds.

For **sell orders** (both limit and market), the reservation is always `quantity` of the base asset — no price dependency.

## Order State Machine

> **Note:** Orders that fail validation or fund reservation are never persisted — they are rejected at the request level and no database row is created. There is no `PENDING` or `REJECTED` state in the database.

```
OPEN
  |
  |-- Full match ----------> FILLED
  |-- Partial match --------> PARTIALLY_FILLED
  |                              |
  |                              |-- Remaining matched ----> FILLED
  |                              `-- Cancel requested -----> CANCELLING
  |                                                              |
  |                                                              |-- ME confirms -------> CANCELLED
  |                                                              `-- Fill before cancel -> PARTIALLY_FILLED / FILLED
  `-- Cancel requested -----> CANCELLING
                                  |
                                  |-- ME confirms -------> CANCELLED
                                  `-- Fill before cancel -> PARTIALLY_FILLED / FILLED
```

**Valid transitions:**
- `OPEN → PARTIALLY_FILLED` — partial match
- `OPEN → FILLED` — full match in one trade
- `OPEN → CANCELLING` — cancel requested, awaiting Matching Engine confirmation
- `PARTIALLY_FILLED → FILLED` — remaining quantity matched
- `PARTIALLY_FILLED → CANCELLING` — cancel requested for unfilled remainder
- `CANCELLING → CANCELLED` — Matching Engine confirmed removal, remaining funds released
- `CANCELLING → PARTIALLY_FILLED` — fill arrived before cancel was processed by ME (see Cancel vs Fill Race Condition)
- `CANCELLING → FILLED` — fully filled before cancel was processed by ME

## Failure Handling

- Idempotency key prevents duplicate orders on client retry.
- If validation or fund reservation fails, the request is rejected immediately — no order row is persisted, no compensation needed.
- On `CANCELLED`: release only the remaining reserved amount, calculated from `remaining_quantity` — not the original reserved amount.
- Retry transient gRPC failures to Wallet Service with exponential backoff; do not retry indefinitely without a circuit breaker.
- Outbox Publisher retries failed Kafka publishes with backoff; a row is only marked published after Kafka acknowledges it.

## 8. Saga Pattern and Compensating Actions

The order-to-settlement lifecycle spans Order Service, Wallet Service, Matching Engine, Settlement Service, Portfolio Service, and Notification Service — with no single database transaction holding it together. This is a **choreography-based saga**: each service reacts to the previous event and emits its own, with no central coordinator.

`order_id` is carried as the correlation ID on every event in the chain (per the ID & Correlation Standard), so the full history of what happened to a given order can be reconstructed by querying all events tagged with that ID across topics.

### Compensating Actions by Failure Point

| Failure point | What happened | Compensating action |
|---|---|---|
| Validation fails | Order never reserved funds | Reject request immediately, no DB row created, no compensation needed |
| Fund reservation fails | Wallet Service declines (insufficient balance) | Reject request, no DB row created, no funds were locked |
| Order rejected after reservation | Funds locked but order not accepted | Release full reserved amount back to available balance |
| Order cancelled (no fills yet) | Funds locked, nothing matched | Release full reserved amount |
| Order cancelled (partial fill) | Some quantity already matched and settled | Release only `remaining_quantity`'s reserved amount; filled portion stays spent |
| Outbox Publisher crashes before publish | Order/cancel committed to DB, event not yet in Kafka | Row stays unpublished; publisher resumes polling on restart and delivers it |
| Settlement Service settlement fails after match | Trade matched in-engine but DB write failed | Settlement Service retries the Postgres transaction from the Kafka offset; offset only commits after success. If retries are exhausted, message goes to a dead-letter topic for manual reconciliation |
| Kafka consumer crashes mid-processing | Event may be redelivered on restart | All event handlers must be idempotent (keyed by `order_id` / `trade_id`) so redelivery does not double-apply a fill or double-release funds |

> **Naming note:** This component was previously called "Executor" in earlier drafts. It is the same service as "Settlement Service" referenced in `07_Wallet_Service.md` — renamed here for consistency across both docs. No behavior changed, only the name.

## Scalability

- Stateless service — horizontal scaling behind the API Gateway.
- Kafka partitioning by market symbol ensures per-market ordering is preserved regardless of how many Order Service instances are running.

## Future-Proofing

- `OrderType` strategy interface for Limit, Market, Stop Loss, Take Profit, Trailing Stop, OCO — implemented as a pluggable interface even though V1 only ships Limit and Market.
- Optional metadata fields on events now (`client_order_id`, `trigger_price`, `tags`) so later Risk Service and behavior-detection features can consume the same event stream without a breaking schema migration.

## V1 Trading Policy

- **Limit orders:** Good Till Cancelled (GTC) — remain `OPEN` until filled or cancelled; reserved funds stay locked while `OPEN`.
- **Market orders:** Immediate or Cancel (IOC) — fill immediately against the existing order book; any unfilled remainder is cancelled and reserved funds for the remainder are released. A market order never rests on the book.
- IOC for limit orders, FOK, and GTD are deferred to a later version.
- **Fees:** V1 operates with zero trading fees. No maker/taker fee model is implemented. Wallet Service's `SettleTrade` signature has no fee parameters. Fee support is planned for a future version.

## Internal Package Structure

```
order-service/
  api/
  service/
  repository/
  kafka/
    publisher/
    consumer/
  wallet/          (gRPC client, sibling to kafka/, not nested under it)
  validator/
  models/
  events/
  db/
```

## Sequence Overview

```
Client → API Gateway → Order Service (generates order_id)
  → Wallet Service (reserve funds) → DB (order + outbox, one transaction)
  → Outbox Publisher → Kafka (OrderCreated) → Matching Engine
  → Kafka (TradeExecuted) → Settlement Service → Wallet Service (SettleTrade)
  → Portfolio Service → Notification Service
```

## Service Interactions

| Service | Interaction |
|---|---|
| Wallet Service | Reserve and release funds (synchronous gRPC). |
| Matching Engine | Consumes `OrderCreated` / `OrderCancelRequested` (via outbox + Kafka); publishes `TradeExecuted` / `OrderCancelled`. No direct calls in either direction. |
| Settlement Service | Consumes `TradeExecuted`; calls Wallet Service's `SettleTrade`; owns retry/dead-letter on settlement failure. |
| Portfolio Service | Consumes execution/settlement events to update holdings. |
| Notification Service | Consumes order and trade events. |