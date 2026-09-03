# Trade Service (`services/trade`)

## 1. Executive Summary & System Role

The **Trade Service** is the authoritative ledger and query engine for all executed and settled trades in the TradeDrift cryptocurrency exchange.

Within the trading lifecycle:
1. The **Matching Engine** matches crossed orders at deterministic price-time priority and emits `trades.executed`.
2. The **Settlement Service** coordinates multi-phase settlement and commands the **Wallet Service** via gRPC.
3. The **Wallet Service** transfers asset balances atomically and writes `TradeSettled` events to its transactional outbox.
4. The **Trade Service** ingests `TradeSettled` events from Kafka (`trades.settled.v1`), stores them immutably in PostgreSQL, and serves high-throughput historical queries via gRPC to the API Gateway.

```
┌─────────────────┐      Kafka       ┌────────────────────┐      gRPC       ┌─────────────────┐
│ Matching Engine ├─────────────────►│ Settlement Service ├────────────────►│ Wallet Service   │
└─────────────────┘ (trades.executed)└────────────────────┘  (SettleTrade)  └────────┬────────┘
                                                                                     │ Outbox Publish
                                                                                     ▼
┌─────────────────┐      gRPC        ┌────────────────────┐   Kafka Event   ┌─────────────────┐
│   API Gateway   │◄─────────────────┤   Trade Service    │◄────────────────┤  Kafka Topic    │
│  (:8080 HTTP)   │     (:50057)     │ (Immutable Ledger) │ (trades.settled)│trades.settled.v1│
└─────────────────┘                  └─────────┬──────────┘                 └─────────────────┘
                                               │
                                               ▼
                                     ┌────────────────────┐
                                     │     PostgreSQL     │
                                     │ (tradedrift_trade) │
                                     └────────────────────┘
```

---

## 2. Directory Structure & Folder Map

```
services/trade/
├── cmd/                          # Application entrypoints & composition roots
│   ├── README.md                 # Detailed documentation for cmd/
│   └── server/
│       └── main.go               # Top-level lifecycle orchestrator & dependency wiring
├── internal/                     # Private application packages (Encapsulated)
│   ├── README.md                 # Architectural guide for the internal/ tree
│   ├── config/                   # Configuration parsing & fail-fast validation
│   │   ├── README.md
│   │   └── config.go
│   ├── handler/                  # gRPC transport adapter (tradev1.TradeServiceServer)
│   │   ├── README.md
│   │   └── grpc.go
│   ├── kafka/                    # Kafka consumer loop & DLQ isolation
│   │   ├── README.md
│   │   ├── consumer.go
│   │   └── consumer_test.go
│   ├── metrics/                  # Prometheus instrumentation & collectors
│   │   ├── README.md
│   │   └── metrics.go
│   ├── repository/               # Data access abstraction & PostgreSQL engine
│   │   ├── README.md
│   │   ├── repository.go         # Domain entity & interface
│   │   └── postgres/
│   │       └── repository.go     # pgxpool implementation with keyset pagination
│   └── service/                  # Business query logic, TI-8 rules & pagination tokens
│       ├── README.md
│       ├── service.go
│       └── service_test.go
├── migration/                    # Versioned database DDL migrations
│   ├── README.md                 # Schema and index justification documentation
│   └── 00001_create_trades.sql   # Goose migration for trades table & indexes
├── Dockerfile                    # Multi-stage production container build
├── go.mod                        # Go module dependencies
└── go.sum                        # Checksums
```

---

## 3. Purpose & Problem Solving by Folder

### 1. [`cmd/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/trade/cmd) — Composition Root & Lifecycle Management
* **Purpose**: Orchestrates initialization, dependency injection, and shutdown sequence in `cmd/server/main.go`.
* **Problem Solved**: Prevents race conditions during startup and data corruption during teardown.
* **How It Solves It**:
  - Deterministic 10-stage boot sequence (Config → Logger → Goose Migrations → DB Pool → Dependency Injection → gRPC Server → HTTP Metrics Server → Kafka Consumer).
  - Traps OS signals (`SIGINT`, `SIGTERM`) and coordinates a 4-stage graceful teardown: stops incoming RPCs, finishes active database inserts, commits final Kafka offsets, and closes connection pools.

---

### 2. [`internal/config/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/trade/internal/config) — Configuration Layer
* **Purpose**: Centralized typed configuration loader.
* **Problem Solved**: Configuration sprawl, silent failures, and whitespace syntax bugs in broker lists.
* **How It Solves It**:
  - `Load()` enforces fail-fast validation on mandatory environment variables (`TRADE_POSTGRES_DSN`, `KAFKA_BROKERS`) before any network sockets open.
  - `parseBrokers()` automatically trims irregular whitespace and filters trailing commas.

---

### 3. [`internal/handler/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/trade/internal/handler) — gRPC Transport Adapter
* **Purpose**: Implements protobuf contract `tradev1.TradeServiceServer`.
* **Problem Solved**: Counterparty de-anonymization, unauthorized record inspection, and transport error ambiguity.
* **How It Solves It**:
  - **TI-7 Public Tape Redaction**: `ListMarketTrades` strips `buyer_id`, `seller_id`, and order IDs from public feeds.
  - **TI-8 Authorization**: `GetTrade` enforces that only the buyer, seller, or an administrator can view a private trade record.
  - Maps domain errors into standard gRPC status codes (`codes.NotFound`, `codes.PermissionDenied`, `codes.InvalidArgument`).
  - Records Prometheus latency and request counters on every RPC call.

---

### 4. [`internal/kafka/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/trade/internal/kafka) — Event Ingestion & Dead Letter Queue
* **Purpose**: Asynchronous consumer ingesting settled trade events from `trades.settled.v1`.
* **Problem Solved**: Poison pill partition stalls, infinite retry loops, and silent data loss.
* **How It Solves It**:
  - Distinguishes between retryable transient DB errors and unrecoverable `*PoisonError`s (bad UUIDs, zero sequence, self-trades).
  - Routes poison messages to `trades.settled.dlq` with rich diagnostic headers (`dlq-reason`, `dlq-offset`, `dlq-partition`), then commits the source offset to keep the partition moving.
  - Never commits the original offset if writing to the DLQ fails, guaranteeing zero data loss.
  - Catches Go zero-value sequence bugs (`Sequence == 0`) before hitting PostgreSQL.

---

### 5. [`internal/metrics/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/trade/internal/metrics) — Prometheus Instrumentation
* **Purpose**: Real-time telemetry exposed at `:9090/metrics`.
* **Problem Solved**: Black-box operational blindness and high-cardinality metric explosion.
* **How It Solves It**:
  - Exposes bounded-label counters (`EventsConsumedTotal`, `DLQEventsTotal`, `GRPCRequestsTotal`).
  - Tracks event pipeline freshness per partition (`ConsumerEventAgeSeconds`).
  - Provides sub-millisecond histogram buckets (0.5ms to 1s) for database (`DBDurationSeconds`) and gRPC latency (`GRPCDurationSeconds`).

---

### 6. [`internal/repository/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/trade/internal/repository) — PostgreSQL Persistence Layer
* **Purpose**: Low-latency data access engine backed by `jackc/pgx/v5`.
* **Problem Solved**: Slow offset pagination, full-table scans on `OR` queries, duplicate deliveries, and sequence conflicts.
* **How It Solves It**:
  - **Keyset Cursor Pagination**: Uses `(executed_at DESC, id DESC)` range predicates for $O(\log N)$ seeks, eliminating slow `OFFSET` scans.
  - **`UNION ALL` User Query**: Avoids slow `OR` bitmap scans by executing two independent index seeks over `buyer_id` and `seller_id` indexes.
  - **Idempotency**: Uses `ON CONFLICT (id) DO NOTHING` to make redeliveries safe.
  - **Monotonic Sequence Enforcement**: Traps unique violations on `(market_id, me_sequence)` (SQLSTATE `23505`) and returns `ErrSequenceConflict`.

---

### 7. [`internal/service/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/trade/internal/service) — Domain Query Logic & Security
* **Purpose**: Query-side domain logic and security enforcement.
* **Problem Solved**: Trade snooping, out-of-memory DoS via huge page requests, and leaking database internals.
* **How It Solves It**:
  - Enforces TI-8 counterparty checks before returning trade records.
  - Clamps client limits (`[1, 100]` for user fills, `[1, 200]` for public tape).
  - Encodes internal keyset coordinates into opaque, URL-safe Base64 tokens (`encodeCursor`).
  - Suppresses `next_cursor` when the last page is reached to eliminate phantom requests.

---

### 8. [`migration/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/trade/migration) — Schema Migrations
* **Purpose**: Goose-managed DDL creating the `trades` table and specialized indexes.
* **Problem Solved**: Schema drift, query latency degradation, and floating-point financial loss.
* **How It Solves It**:
  - Programmatically executes `00001_create_trades.sql` on service startup.
  - Establishes 6 specialized B-Tree compound indexes matching all query patterns.
  - Enforces `DECIMAL(30,10)` for lossless financial precision.

---

## 4. Key Functions Reference Across the Service

| Function | Package / File | Purpose & Responsibilities |
|---|---|---|
| `main()` | `cmd/server/main.go` | Boots migrations, connection pools, gRPC server, metrics HTTP server, and Kafka consumer. |
| `Load()` | `internal/config/config.go` | Parses and validates environment variables; injects defaults. |
| `GetTrade()` | `internal/handler/grpc.go` | gRPC handler for single trade retrieval; enforces TI-8 authorization. |
| `ListUserTrades()` | `internal/handler/grpc.go` | gRPC handler returning private fill history with keyset pagination. |
| `ListMarketTrades()` | `internal/handler/grpc.go` | gRPC handler returning public market tape with counterparty redaction (TI-7). |
| `Start()` | `internal/kafka/consumer.go` | Non-blocking Kafka consume loop with manual commit control. |
| `process()` | `internal/kafka/consumer.go` | Validates UUIDs, sequence > 0, non-zero prices/quantities, and executes DB insert. |
| `sendToDLQ()` | `internal/kafka/consumer.go` | Preserves original payload and attaches diagnostic headers before sending to DLQ. |
| `Create()` | `internal/repository/postgres/repository.go` | Idempotent PostgreSQL insert with sequence conflict detection. |
| `ListByUser()` | `internal/repository/postgres/repository.go` | Executes index-optimized `UNION ALL` query for buyer/seller trade history. |
| `ListByMarket()` | `internal/repository/postgres/repository.go` | Executes keyset-paginated range query for public market tape. |
| `encodeCursor()` | `internal/service/service.go` | Serializes `(ExecutedAt, ID)` into URL-safe base64 opaque token. |
| `decodeCursor()` | `internal/service/service.go` | Deserializes and validates base64 token back into typed keyset cursor. |
| `clamp()` | `internal/service/service.go` | Enforces mathematical upper and lower boundaries on query limits. |

---

## 5. End-to-End System Flows

### Flow A: Ingestion, Settlement & Dead Letter Queue (Write Path)

```mermaid
sequenceDiagram
    autonumber
    participant Wallet as Wallet Service Outbox
    participant Kafka as Kafka (trades.settled.v1)
    participant Consumer as Trade Consumer (internal/kafka)
    participant Repo as Postgres Repository (internal/repository)
    participant DB as PostgreSQL (trades table)
    participant DLQ as Kafka (trades.settled.dlq)
    participant Metrics as Prometheus (:9090)

    Wallet->>Kafka: Publish TradeSettled event
    Kafka->>Consumer: FetchMessage()
    Consumer->>Metrics: Record Event Freshness (ConsumerEventAgeSeconds)
    Consumer->>Consumer: Unmarshal JSON & validate (UUIDs, Sequence > 0, Decimals)
    alt Invalid Payload (e.g. sequence == 0 or bad UUID)
        Consumer->>DLQ: Write to DLQ with headers [dlq-reason, dlq-offset, ...]
        Consumer->>Metrics: DLQEventsTotal.Inc() & EventsConsumedTotal("poison").Inc()
        Consumer->>Kafka: CommitMessages() (Acknowledge & skip)
    else Valid Payload
        Consumer->>Repo: Create(trade)
        Repo->>DB: INSERT INTO trades ... ON CONFLICT (id) DO NOTHING
        alt Sequence Collision (me_sequence conflict on different trade_id)
            DB-->>Repo: SQLSTATE 23505 (idx_trades_market_sequence)
            Repo-->>Consumer: ErrSequenceConflict
            Consumer->>DLQ: Write to DLQ (Producer integrity bug)
            Consumer->>Kafka: CommitMessages()
        else Successful Insert or Duplicate No-Op
            DB-->>Repo: Acknowledged (1 or 0 rows affected)
            Repo-->>Consumer: nil (Success)
            Consumer->>Metrics: EventsConsumedTotal("success").Inc()
            Consumer->>Kafka: CommitMessages()
        end
    end
```

---

### Flow B: API Gateway Keyset-Paginated Trade Query (Read Path)

```mermaid
sequenceDiagram
    autonumber
    participant Client as Postman / Web Trader
    participant Gateway as API Gateway (:8080)
    participant Handler as gRPC Handler (internal/handler)
    participant Svc as Trade Service (internal/service)
    participant Repo as Postgres Repo (internal/repository)
    participant DB as PostgreSQL (idx_trades_market)

    Client->>Gateway: GET /api/v1/markets/BTC-USDT/trades?limit=50
    Gateway->>Handler: ListMarketTrades(market_id="BTC-USDT", limit=50, cursor="")
    Handler->>Svc: ListMarketTrades("BTC-USDT", cursorStr="", limit=50)
    Svc->>Svc: clamp(50) -> 50
    Svc->>Svc: decodeCursor("") -> nil
    Svc->>Repo: ListByMarket(ctx, "BTC-USDT", after=nil, limit=50)
    Repo->>DB: SELECT ... WHERE market_id = 'BTC-USDT' ORDER BY executed_at DESC, id DESC LIMIT 50
    DB-->>Repo: 50 Trade Records (Last record: T_50)
    Repo-->>Svc: []Trade
    Svc->>Svc: encodeCursor(T_50.ExecutedAt, T_50.ID) -> "MTc4ODQ...=="
    Svc-->>Handler: []Trade, nextCursor="MTc4ODQ...=="
    loop For each trade
        Handler->>Handler: toProtoMarketTrade(t) (TI-7: REDACT buyer_id and seller_id)
    end
    Handler-->>Gateway: gRPC Response { trades: [...], next_cursor: "MTc4ODQ...==" }
    Gateway-->>Client: HTTP 200 OK JSON { "trades": [...], "next_cursor": "MTc4ODQ...==" }
```

---

## 6. Configuration & Operational Reference

### Environment Variables

| Variable | Default | Description |
|---|---|---|
| `TRADE_POSTGRES_DSN` | *Required* | PostgreSQL connection string (`postgres://user:pass@host:5432/tradedrift_trade?sslmode=disable`). |
| `KAFKA_BROKERS` | `localhost:9092` | Comma-separated Kafka broker addresses. |
| `TRADE_MIGRATIONS_DIR` | `migration` | Path to Goose SQL migration directory. |
| `KAFKA_GROUP_ID` | `trade-service` | Consumer group ID on `trades.settled.v1`. |
| `KAFKA_TOPIC_TRADE_SETTLED`| `trades.settled.v1` | Source topic for settled trade events. |
| `KAFKA_TOPIC_TRADE_DLQ` | `trades.settled.dlq` | Dead Letter Queue topic for poison events. |
| `TRADE_GRPC_PORT` | `:50057` | Internal gRPC server bind port. |
| `TRADE_METRICS_PORT` | `:9090` | Prometheus `/metrics`, `/healthz`, `/ready` bind port. |
| `LOG_LEVEL` | `info` | Minimum Zap logger severity (`debug`, `info`, `warn`, `error`). |

### Network Port Exposure

* **`:50057` (gRPC)**: Serves `tradev1.TradeService` methods:
  - `GetTrade`
  - `ListUserTrades`
  - `ListMarketTrades`
* **`:9090` (HTTP)**: Serves internal management endpoints:
  - `GET /metrics`: Prometheus scraping endpoint.
  - `GET /healthz`: Kubernetes liveness probe (HTTP 200 OK).
  - `GET /ready`: Kubernetes readiness probe (tests PostgreSQL connection pool ping).
