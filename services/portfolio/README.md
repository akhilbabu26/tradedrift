# Portfolio Service (`services/portfolio`)

## 1. Executive Summary & System Role

The **Portfolio Service** is the personal financial accounting, position tracking, and real-time valuation engine for traders in the TradeDrift cryptocurrency exchange.

Within the trading lifecycle:
1. The **Matching Engine** matches crossed orders at deterministic price-time priority and emits `trades.executed`.
2. The **Settlement Service** coordinates multi-phase settlement and commands the **Wallet Service** via gRPC.
3. The **Wallet Service** transfers asset balances atomically and writes user-scoped accounting events to its transactional outbox (`portfolio.user.trades.v1` keyed by `user_id`) as well as trade ledger events (`trades.settled.v1` keyed by `trade_id`).
4. The **Portfolio Service** consumes user-scoped trade events from Kafka (`portfolio.user.trades.v1`), updates cumulative user holdings, calculates weighted-average cost basis and realized PnL, asserts matching-engine sequence uniqueness, increments monotonic position version, and writes outbox events to stream live balance updates to the **Notification & WebSocket Service**.
5. When queried by the **API Gateway** (`GET /api/v1/portfolio/summary` and `GET /api/v1/portfolio/holdings`), the Portfolio Service dynamically blends local crypto holdings with live cash balances from the **Wallet Service** and live mark prices from the **Market Service** to compute total portfolio equity and unrealized PnL on demand.

```
┌─────────────────┐      Kafka       ┌────────────────────┐      gRPC       ┌─────────────────┐
│ Matching Engine ├─────────────────►│ Settlement Service ├────────────────►│ Wallet Service   │
└─────────────────┘ (trades.executed)└────────────────────┘  (SettleTrade)  └────────┬────────┘
                                                                                     │ Outbox Publish
                                                                                     ▼
┌─────────────────┐      gRPC        ┌────────────────────┐   Kafka Event   ┌─────────────────┐
│   API Gateway   │◄─────────────────┤ Portfolio Service  │◄────────────────┤  Kafka Topic    │
│  (:8080 HTTP)   │     (:50058)     │ (Accounting Engine)│ (user-partition)│portfolio.user.  │
└─────────────────┘                  └─────────┬──────────┘                 │    trades.v1    │
                                               │                            └─────────────────┘
                           ┌───────────────────┴───────────────────┐
                           ▼                                       ▼
                 ┌────────────────────┐                 ┌────────────────────┐
                 │     PostgreSQL     │                 │   Kafka (Outbox)   │
                 │(tradedrift_        │                 │  portfolios.       │
                 │  portfolio)        │                 │   updated.v1       │
                 └────────────────────┘                 └─────────┬──────────┘
                                                                  │
                                                                  ▼
                                                        ┌────────────────────┐
                                                        │ Notification & WS  │
                                                        │  (Real-Time Push)  │
                                                        └────────────────────┘
```

---

## 2. Directory Structure & Documentation Map

```
services/portfolio/
├── Dockerfile                    # Multi-stage production container build
├── README.md                     # Master architecture document (This file)
├── cmd/                          # Application entrypoints & composition roots
│   ├── README.md                 # Detailed documentation for cmd/
│   └── server/
│       └── main.go               # 13-stage lifecycle orchestrator & dependency wiring
├── internal/                     # Private application packages (Encapsulated)
│   ├── README.md                 # Architectural guide for internal/ tree
│   ├── config/                   # Configuration parsing & fail-fast validation
│   │   ├── README.md
│   │   └── config.go
│   ├── handler/                  # gRPC transport adapter (portfoliov1.PortfolioServiceServer)
│   │   ├── README.md
│   │   └── grpc.go
│   ├── kafka/                    # Inbound trade consumer, DLQ & Outbox publisher
│   │   ├── README.md
│   │   ├── consumer.go
│   │   ├── consumer_test.go
│   │   ├── partition_ordering_test.go # Mathematical proof of hash partition affinity
│   │   └── publisher.go
│   ├── metrics/                  # Prometheus instrumentation & collectors
│   │   ├── README.md
│   │   └── metrics.go
│   ├── repository/               # Data persistence & atomic accounting engine
│   │   ├── README.md
│   │   ├── accounting_test.go    # Unit tests for weighted-average math & zero clamping
│   │   ├── repository.go         # Domain entity & storage interface
│   │   └── postgres/
│   │       ├── repository.go     # Single-row locking, CTE outbox claiming, version tracking
│   │       └── repository_test.go # Comprehensive PostgreSQL integration test suite
│   └── service/                  # Domain valuation & dynamic pricing service
│       ├── README.md
│       ├── service.go            # Valuation math (Wallet cash + Market tickers)
│       └── service_test.go       # Mock-driven unit tests
└── migration/                    # Goose SQL database migrations
    ├── README.md                 # Database schema & index documentation
    └── 00001_create_portfolio_tables.sql
```

---

## 3. Core Architectural Invariants

| Code | Invariant Name | Architectural Rule |
|---|---|---|
| **PI-1** | **No Cash Persistence** | `USDT` cash balances are **never stored** in the Portfolio database. Cash is dynamically queried from the Wallet Service via gRPC to prevent cash drift across deposits, withdrawals, and fee deductions. |
| **PI-2** | **No Unrealized PnL Persistence** | Unrealized PnL and total portfolio equity are **computed purely on read**. Market prices fluctuate constantly; computing on demand eliminates write amplification. |
| **PI-3** | **Per-User Kafka Partition Affinity** | Accounting events are emitted to `portfolio.user.trades.v1` partitioned by `user_id`. All accounting events for a given user are routed to the same Kafka partition, preserving their Kafka log order for that user and preventing causal order inversions (e.g. Sell arriving before Buy). |
| **PI-4** | **1-Atomic Transaction** | Trade leg deduplication (`processed_user_trades`), sequence collision protection (`processed_market_sequences`), holding adjustments (`holdings`), and outbox records (`portfolio_outbox`) are committed inside **1 single atomic database transaction**. |
| **PI-5** | **Poison Error Quarantining (DLQ)** | Invariant violations (insufficient balance, malformed UUIDs, decimal precision overflow > 10 places, timestamp inversion, sequence collisions) route to `trades.settled.dlq`. The Kafka offset is committed only after DLQ write succeeds. |
| **PI-6** | **Monotonic Portfolio Versioning** | Every holding modification increments `version = version + 1`. This monotonic version is included in outbound `portfolios.updated.v1` events, allowing downstream WebSocket and UI clients to discard stale or out-of-order snapshots. |
| **PI-7** | **Zero Silent Clamping & Full Liquidation Reset** | Negative balance conditions are treated as fatal errors (`ErrInsufficientHoldings`) and never silently clamped to zero. When a position is legitimately fully liquidated ($\text{quantity} = 0$), quantity and total cost are reset to exactly 0 to eliminate floating-point epsilon drift. |

---

## 4. End-to-End System Flows

### 4.1 Write Path: User Trade Ingestion & Accounting
1. Kafka message arrives on `portfolio.user.trades.v1` (partition key: `user_id`).
2. `kafka.Consumer` verifies UUIDs, role (`BUY` or `SELL`), market ID consistency (`market_id == BaseAsset + "-" + QuoteAsset`), strict USDT quote asset, scale limits ($\le 10$ decimal digits), positive prices/quantities, sequence $> 0$, and chronological sanity (`SettledAt >= ExecutedAt`).
3. `postgres.Repository.ProcessUserTrade` begins a database transaction:
   * Asserts sequence collision integrity in `processed_market_sequences` (`market_id`, `sequence`). Rejects reuse across differing trade IDs.
   * Inserts into `processed_user_trades` (`trade_id`, `user_id`) (`ON CONFLICT DO NOTHING`). If already processed, exits harmlessly.
   * Pre-emptively initializes holding row if first-time trader (`ON CONFLICT DO NOTHING`) and acquires exclusive row lock `FOR UPDATE` on `(user_id, asset_code)`.
   * **BUY**: increases quantity, adds execution cost to total cost basis, updates average entry price, increments `version = version + 1`.
   * **SELL**: verifies balance $\ge$ sold quantity (returns `ErrInsufficientHoldings` $\rightarrow$ DLQ if insufficient), calculates cost of sold using average entry, credits realized PnL, subtracts quantity and cost, clamps cost to 0 if fully closed, increments `version = version + 1`.
   * Writes single `PortfolioUpdated` event to `portfolio_outbox` (`status = 'PENDING'`) containing `portfolio_version`.
   * Commits the atomic transaction.
4. Consumer commits Kafka offset.

### 4.2 Read Path: Synchronous Portfolio Valuation
1. API Gateway validates user JWT and forwards `GetPortfolioSummary(user_id)` over gRPC (`:50058`).
2. `handler.Handler` validates UUID format and calls `service.Service`.
3. `service.Service` fetches active crypto holdings ($\text{quantity} > 0$) from PostgreSQL.
4. Queries `WalletService.GetBalances(user_id)` synchronously for available + reserved USDT cash.
5. Queries `MarketService.GetTicker(asset + "-USDT")` for current mark prices.
6. Computes in-memory:
   $$\text{MarketValue}_i = Q_i \times P_{\text{last}, i}$$
   $$\text{UnrealizedPnL}_i = \text{MarketValue}_i - C_i$$
   $$\text{TotalNetEquity} = \text{Cash}_{\text{USDT}} + \sum \text{MarketValue}_i$$
7. Returns protobuf response to API Gateway for JSON serialization to client browser.

### 4.3 Outbound Path: Transactional Outbox Streaming
1. `kafka.OutboxPublisher` polls PostgreSQL every 100ms.
2. Atomically claims up to 50 pending or expired processing rows using a CTE with `FOR UPDATE SKIP LOCKED`.
3. Emits `PortfolioUpdated` events to Kafka topic `portfolios.updated.v1` keyed by `user_id`.
4. Marks claimed rows as `PUBLISHED` with `published_at = NOW()`.
5. Downstream **Notification & WebSocket Service** streams real-time updates to connected client UIs.

---

## 5. Configuration & Operational Reference

| Environment Variable | Default | Purpose |
|---|---|---|
| `PORTFOLIO_POSTGRES_DSN` | *(Required)* | PostgreSQL connection string |
| `PORTFOLIO_MIGRATIONS_DIR` | `services/portfolio/migration` | Goose SQL migrations path |
| `KAFKA_BROKERS` | `localhost:9092` | Kafka bootstrap brokers |
| `KAFKA_GROUP_ID` | `portfolio-service-group` | Consumer group ID |
| `KAFKA_TOPIC_TRADE_SETTLED` | `trades.settled.v1` | Settled trades topic |
| `KAFKA_TOPIC_PORTFOLIO_UPDATED` | `portfolios.updated.v1` | Position updates topic |
| `KAFKA_TOPIC_TRADE_DLQ` | `trades.settled.dlq` | Dead-letter queue topic |
| `WALLET_GRPC_ADDR` | `localhost:50052` | Wallet gRPC endpoint |
| `MARKET_GRPC_ADDR` | `localhost:50054` | Market gRPC endpoint |
| `PORTFOLIO_GRPC_PORT` | `:50058` | gRPC server listen port |
| `PORTFOLIO_METRICS_PORT` | `:9091` | Prometheus `/metrics` and `/healthz`, `/ready` |
| `LOG_LEVEL` | `info` | Zap log level |
