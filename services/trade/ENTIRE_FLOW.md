# Trade Service — Complete End-to-End System Flows

This document details every operational and data flow handled by the **Trade Service** (`services/trade`).

For the comprehensive folder, file, and function architecture breakdown, see [README.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/trade/README.md).

---

## Table of Contents

1. [Flow 1: Bootstrap & Graceful Teardown Flow](#flow-1-bootstrap--graceful-teardown-flow)
2. [Flow 2: Happy-Path Kafka Ingestion & Persistence Flow](#flow-2-happy-path-kafka-ingestion--persistence-flow)
3. [Flow 3: Redelivery & Idempotent No-Op Flow](#flow-3-redelivery--idempotent-no-op-flow)
4. [Flow 4: Producer Sequence Collision Violation Flow](#flow-4-producer-sequence-collision-violation-flow)
5. [Flow 5: Poison Message DLQ Isolation Flow](#flow-5-poison-message-dlq-isolation-flow)
6. [Flow 6: Private User Fills Keyset-Paginated Query Flow](#flow-6-private-user-fills-keyset-paginated-query-flow)
7. [Flow 7: Public Market Tape Query Flow (TI-7 Counterparty Redaction)](#flow-7-public-market-tape-query-flow-ti-7-counterparty-redaction)
8. [Flow 8: Authorized Single Trade Retrieval Flow (TI-8 Party Check)](#flow-8-authorized-single-trade-retrieval-flow-ti-8-party-check)
9. [Flow 9: Observability, Liveness & Readiness Probing Flow](#flow-9-observability-liveness--readiness-probing-flow)
10. [Summary Matrix of System Invariants & Protections](#10-summary-matrix-of-system-invariants--protections)

---

### Flow 1: Bootstrap & Graceful Teardown Flow

```
[OS Boot / Container Start]
      │
      ▼
1. Load Environment Configuration (config.Load)
      │  ├─ Validates TRADE_POSTGRES_DSN and KAFKA_BROKERS
      │  └─ Rejects missing/malformed settings immediately (Fail-Fast)
      ▼
2. Instantiates Structured Logger (logger.New)
      ▼
3. Run Goose Database Migrations (platformpg.RunMigrations)
      │  └─ Executes 00001_create_trades.sql (creates table and 6 indexes)
      ▼
4. Connect PostgreSQL Pool (platformpg.NewPool)
      │  └─ Configures pool with MaxConns: 15; tests connection with dbPool.Ping()
      ▼
5. Dependency Injection Assembly
      │  ├─ Repo: tradepg.NewRepository(dbPool)
      │  ├─ Svc:  tradesvc.NewService(repo, logger)
      │  └─ Hdlr: tradehandler.NewGRPCHandler(svc, logger)
      ▼
6. Launch gRPC Server on :50057
      │  └─ Registers tradev1.TradeServiceServer; listens for inbound RPCs
      ▼
7. Launch HTTP Metrics & Health Server on :9090
      │  ├─ /metrics: Prometheus scraping
      │  ├─ /healthz: Liveness probe (200 OK)
      │  └─ /ready:   Readiness probe (tests dbPool.Ping())
      ▼
8. Launch Kafka Consumer on trades.settled.v1
      │  └─ Spawns consumer.Start(ctx) in background goroutine
      ▼
[SERVICE OPERATIONAL - SERVING TRAFFIC]
      │
[OS Signal: SIGINT / SIGTERM Intercepted]
      │
      ▼
9. Graceful Shutdown Phase (Coordinated via signal.NotifyContext)
      │  ├─ 1. grpcServer.GracefulStop() (finishes in-flight RPCs)
      │  ├─ 2. metricsServer.Shutdown() (drains HTTP probes)
      │  ├─ 3. Context cancellation terminates Kafka consumer loop
      │  ├─ 4. consumer.Close() shuts down Kafka reader and DLQ writer
      │  └─ 5. dbPool.Close() drains database connection pool
      ▼
[Process Exits Cleanly with Code 0]
```

---

### Flow 2: Happy-Path Kafka Ingestion & Persistence Flow

```mermaid
sequenceDiagram
    autonumber
    participant Wallet as Wallet Service Outbox
    participant Kafka as Kafka (trades.settled.v1)
    participant Consumer as Trade Consumer (internal/kafka)
    participant Repo as Postgres Repository (internal/repository)
    participant DB as PostgreSQL (trades table)
    participant Metrics as Prometheus (:9090)

    Wallet->>Kafka: Publish TradeSettled event
    Kafka->>Consumer: FetchMessage(ctx)
    Consumer->>Metrics: Record Event Freshness (ConsumerEventAgeSeconds)
    Consumer->>Consumer: json.Unmarshal() -> TradeSettledEvent
    Consumer->>Consumer: Validate UUIDs, Price > 0, Qty > 0, Sequence > 0
    Consumer->>Repo: Create(ctx, trade)
    Repo->>DB: INSERT INTO trades (...) VALUES (...) ON CONFLICT (id) DO NOTHING
    DB-->>Repo: 1 Row Inserted
    Repo-->>Consumer: nil (Success)
    Consumer->>Metrics: EventsConsumedTotal.WithLabelValues("success").Inc()
    Consumer->>Kafka: CommitMessages(ctx, msg)
```

---

### Flow 3: Redelivery & Idempotent No-Op Flow

```mermaid
sequenceDiagram
    autonumber
    participant Kafka as Kafka (trades.settled.v1)
    participant Consumer as Trade Consumer (internal/kafka)
    participant Repo as Postgres Repository
    participant DB as PostgreSQL (trades table)

    Kafka->>Consumer: FetchMessage (Redelivered offset due to previous consumer crash)
    Consumer->>Consumer: Validate TradeSettledEvent
    Consumer->>Repo: Create(ctx, trade)
    Repo->>DB: INSERT INTO trades (...) VALUES (...) ON CONFLICT (id) DO NOTHING
    Note over DB: Primary key id already exists!<br/>ON CONFLICT triggers no-op.
    DB-->>Repo: 0 Rows Inserted, Error: nil
    Repo-->>Consumer: nil (Success)
    Consumer->>Kafka: CommitMessages(ctx, msg)
```

---

### Flow 4: Producer Sequence Collision Violation Flow

```mermaid
sequenceDiagram
    autonumber
    participant Kafka as Kafka (trades.settled.v1)
    participant Consumer as Trade Consumer (internal/kafka)
    participant Repo as Postgres Repository
    participant DB as PostgreSQL
    participant DLQ as Kafka (trades.settled.dlq)

    Kafka->>Consumer: FetchMessage (Trade B: market="BTC-USDT", seq=1042)
    Consumer->>Consumer: Validate payload -> OK
    Consumer->>Repo: Create(ctx, tradeB)
    Repo->>DB: INSERT INTO trades (id="UUID-B", market_id="BTC-USDT", me_sequence=1042)
    Note over DB: Unique index idx_trades_market_sequence violated!<br/>Sequence 1042 already held by Trade A ("UUID-A").
    DB-->>Repo: SQLSTATE 23505 (Unique Violation)
    Repo-->>Consumer: ErrSequenceConflict
    Consumer->>Consumer: Wrap as *PoisonError (Producer integrity bug)
    Consumer->>DLQ: sendToDLQ(msg, "sequence conflict...")
    DLQ-->>Consumer: DLQ Publish ACK
    Consumer->>Kafka: CommitMessages(ctx, msg)
```

---

### Flow 5: Poison Message DLQ Isolation Flow

```mermaid
sequenceDiagram
    autonumber
    participant Kafka as Kafka (trades.settled.v1)
    participant Consumer as Trade Consumer (internal/kafka)
    participant DLQ as Kafka (trades.settled.dlq)
    participant Metrics as Prometheus (:9090)

    Kafka->>Consumer: FetchMessage (Corrupted payload: sequence=0 or invalid UUID)
    alt Malformed JSON
        Consumer->>DLQ: sendToDLQ(msg, "malformed JSON")
        Consumer->>Metrics: DLQEventsTotal("malformed_json").Inc()
        Consumer->>Kafka: CommitMessages(ctx, msg)
    else Validation Failed (e.g. self-trade: buyer_id == seller_id)
        Consumer->>Consumer: process() returns *PoisonError
        Consumer->>DLQ: sendToDLQ(msg, "self-trade...")
        Consumer->>Metrics: DLQEventsTotal("self_trade").Inc()
        Consumer->>Kafka: CommitMessages(ctx, msg)
    end
    Note over Consumer,Kafka: Corrupted message skipped; partition continues processing without stalling.
```

---

### Flow 6: Private User Fills Keyset-Paginated Query Flow

```mermaid
sequenceDiagram
    autonumber
    participant Client as Web / Mobile Frontend
    participant Gateway as API Gateway (:8080)
    participant Handler as gRPC Handler (:50057)
    participant Svc as Trade Service (internal/service)
    participant Repo as Postgres Repo (internal/repository)
    participant DB as PostgreSQL

    Client->>Gateway: GET /api/v1/users/me/trades?limit=20&cursor=MTc...
    Gateway->>Handler: ListUserTrades(user_id="U1", cursor="MTc...", limit=20)
    Handler->>Svc: ListUserTrades("U1", market="", cursor="MTc...", limit=20)
    Svc->>Svc: clamp(limit) -> 20
    Svc->>Svc: decodeCursor("MTc...") -> Cursor(ExecutedAt, ID)
    Svc->>Repo: ListByUser(ctx, "U1", market="", after, limit=20)
    Repo->>DB: SELECT ... UNION ALL over idx_trades_buyer and idx_trades_seller
    DB-->>Repo: 20 Trade Records
    Repo-->>Svc: []Trade
    Svc->>Svc: encodeCursor(lastRecord) -> "MTg0..."
    Svc-->>Handler: []Trade, nextCursor="MTg0..."
    loop For each trade
        Handler->>Handler: toProtoTrade(t) (Includes buyer_id and seller_id)
    end
    Handler-->>Gateway: ListUserTradesResponse { trades, next_cursor }
    Gateway-->>Client: HTTP 200 OK JSON
```

---

### Flow 7: Public Market Tape Query Flow (TI-7 Counterparty Redaction)

```mermaid
sequenceDiagram
    autonumber
    participant PublicClient as Public Market Viewer / Chart
    participant Gateway as API Gateway (:8080)
    participant Handler as gRPC Handler (:50057)
    participant Svc as Trade Service (internal/service)
    participant Repo as Postgres Repo
    participant DB as PostgreSQL (idx_trades_market)

    PublicClient->>Gateway: GET /api/v1/markets/BTC-USDT/trades?limit=50
    Gateway->>Handler: ListMarketTrades(market_id="BTC-USDT", limit=50, cursor="")
    Handler->>Svc: ListMarketTrades("BTC-USDT", cursor="", limit=50)
    Svc->>Svc: clamp(50) -> 50
    Svc->>Repo: ListByMarket(ctx, "BTC-USDT", after=nil, limit=50)
    Repo->>DB: SELECT ... WHERE market_id = 'BTC-USDT' ORDER BY executed_at DESC, id DESC LIMIT 50
    DB-->>Repo: 50 Trade Records
    Repo-->>Svc: []Trade
    Svc-->>Handler: []Trade, nextCursor
    loop For each trade
        Handler->>Handler: toProtoMarketTrade(t) (REDACTS buyer_id, seller_id, order IDs)
    end
    Handler-->>Gateway: ListMarketTradesResponse { trades: MarketTrade[], next_cursor }
    Gateway-->>PublicClient: HTTP 200 OK (Public tape without trader identities)
```

---

### Flow 8: Authorized Single Trade Retrieval Flow (TI-8 Party Check)

```mermaid
sequenceDiagram
    autonumber
    participant Caller as Trader / Admin
    participant Gateway as API Gateway
    participant Handler as gRPC Handler
    participant Svc as Trade Service
    participant Repo as Postgres Repo

    Caller->>Gateway: GET /api/v1/trades/UUID-1
    Gateway->>Handler: GetTrade(trade_id="UUID-1", caller_user_id="U1", is_admin=false)
    Handler->>Svc: GetTrade(tradeID="UUID-1", callerID="U1", isAdmin=false)
    Svc->>Repo: GetByID(ctx, "UUID-1")
    Repo-->>Svc: Trade(Buyer="U1", Seller="U2")
    alt Caller is Buyer, Seller, or Admin
        Svc-->>Handler: Trade Record
        Handler-->>Gateway: GetTradeResponse { trade }
    else Caller is Unauthorized Third Party ("U3")
        Svc-->>Handler: ErrNotParty
        Handler-->>Gateway: gRPC Error: codes.PermissionDenied ("caller is not a party")
        Gateway-->>Caller: HTTP 403 Forbidden
    end
```

---

### Flow 9: Observability, Liveness & Readiness Probing Flow

```
                         HTTP Management Port (:9090)
                                       │
        ┌──────────────────────────────┼──────────────────────────────┐
        ▼                              ▼                              ▼
GET /healthz                   GET /ready                     GET /metrics
(Liveness Probe)               (Readiness Probe)              (Prometheus Scrape)
        │                              │                              │
Check process status           Ping PostgreSQL Connection     Scrape Telemetry Counters:
- Return HTTP 200 "OK"         - dbPool.Ping(ctx)             - events_consumed_total
                               - Return 200 "READY" if ok     - dlq_events_total
                               - Return 503 if unreachable    - consumer_event_age_seconds
                                                              - db_duration_seconds
                                                              - grpc_requests_total
                                                              - grpc_duration_seconds
```

---

## 10. Summary Matrix of System Invariants & Protections

| Invariant / Requirement | Enforced In | Mechanism | Failure Mode Prevented |
|---|---|---|---|
| **Public Tape Privacy (TI-7)** | `handler.toProtoMarketTrade` | Strips `buyer_id`, `seller_id`, `buy_order_id`, and `sell_order_id` | Counterparty deanonymization on public tape |
| **Private Trade Access Control (TI-8)** | `service.GetTrade` | Verifies `caller == BuyerID \|\| caller == SellerID \|\| isAdmin` | Unauthorized trade inspection by third parties |
| **Ingestion Idempotency** | `postgres.Create` | `INSERT INTO trades ... ON CONFLICT (id) DO NOTHING` | Duplicate trade records on Kafka offset redelivery |
| **Monotonic Sequence Integrity** | PostgreSQL + `postgres.Create` | `idx_trades_market_sequence` unique index + `ErrSequenceConflict` | Out-of-order execution or duplicate sequence producer bugs |
| **Poison Message Deadlock Isolation** | `kafka.Consumer` | Identifies `*PoisonError`, routes to DLQ with headers, ACKs offset | Partition stall / head-of-line blocking on corrupted events |
| **Zero Data Loss on DLQ Failure** | `kafka.Consumer.Start` | Skips offset commit if `sendToDLQ` fails | Dropping poison events without persisting to DLQ |
| **O(log N) Query Performance** | `repository.postgres` | Keyset pagination `(executed_at, id) < ($1, $2)` | O(N) full-table scans from deep `OFFSET` pagination |
| **Index-Optimized User Fills** | `postgres.ListByUser` | `UNION ALL` of separate `buyer_id` and `seller_id` subqueries | Degraded execution plans caused by PostgreSQL `OR` bitmap scans |
| **High-Precision Financials** | Database schema | `DECIMAL(30,10)` stored in PostgreSQL, mapped via `decimal.Decimal` | Floating-point rounding errors and balance discrepancies |
| **Clean Shutdown Lifecycle** | `cmd/server/main.go` | 4-stage teardown with `grpcServer.GracefulStop()` and context wait | Dropping active database transactions or in-flight gRPC calls |
