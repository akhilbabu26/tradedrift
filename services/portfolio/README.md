# Portfolio Service (`services/portfolio`)

## 1. Executive Summary & System Role

The **Portfolio Service** is the personal financial accounting, position tracking, and real-time valuation engine for traders in the TradeDrift cryptocurrency exchange.

Within the trading lifecycle:
1. The **Matching Engine** matches crossed orders at deterministic price-time priority and emits `trades.executed`.
2. The **Settlement Service** coordinates multi-phase settlement and commands the **Wallet Service** via gRPC.
3. The **Wallet Service** transfers asset balances atomically and writes `TradeSettled` events to its transactional outbox.
4. The **Portfolio Service** consumes `TradeSettled` events from Kafka (`trades.settled.v1`), updates cumulative user holdings, calculates weighted-average cost basis and realized PnL, records matching-engine sequence audit logs, and writes outbox events to stream live balance updates to the **Notification & WebSocket Service**.
5. When queried by the **API Gateway** (`GET /api/v1/portfolio/summary` and `GET /api/v1/portfolio/holdings`), the Portfolio Service dynamically blends local crypto holdings with live cash balances from the **Wallet Service** and live mark prices from the **Market Service** to compute total portfolio equity and unrealized PnL on demand.

```
┌─────────────────┐      Kafka       ┌────────────────────┐      gRPC       ┌─────────────────┐
│ Matching Engine ├─────────────────►│ Settlement Service ├────────────────►│ Wallet Service   │
└─────────────────┘ (trades.executed)└────────────────────┘  (SettleTrade)  └────────┬────────┘
                                                                                     │ Outbox Publish
                                                                                     ▼
┌─────────────────┐      gRPC        ┌────────────────────┐   Kafka Event   ┌─────────────────┐
│   API Gateway   │◄─────────────────┤ Portfolio Service  │◄────────────────┤  Kafka Topic    │
│  (:8080 HTTP)   │     (:50058)     │ (Accounting Engine)│ (trades.settled)│trades.settled.v1│
└─────────────────┘                  └─────────┬──────────┘                 └─────────────────┘
                                               │
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
│   │   └── publisher.go
│   ├── metrics/                  # Prometheus instrumentation & collectors
│   │   ├── README.md
│   │   └── metrics.go
│   ├── repository/               # Data persistence & atomic accounting engine
│   │   ├── README.md
│   │   ├── accounting_test.go    # Unit tests for weighted-average math & zero clamping
│   │   ├── repository.go         # Domain entity & storage interface
│   │   └── postgres/
│   │       └── repository.go     # Deterministic row locking, CTE outbox claiming & deduplication
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
| **PI-3** | **Deterministic Row-Locking** | When locking buyer and seller holding rows, user IDs are strictly sorted alphabetically (`min(buyer, seller) -> max(buyer, seller)`), eliminating PostgreSQL `40P01` deadlocks. |
| **PI-4** | **1-Atomic Transaction** | Trade deduplication (`processed_trades`), holding updates (`holdings`), and outbox events (`portfolio_outbox`) are committed inside **1 single atomic database transaction**. |
| **PI-5** | **Poison Error Quarantining (DLQ)** | Invariant violations (insufficient balance, self-trades, malformed UUIDs, zero sequences) route to `trades.settled.dlq`. The offset is committed only after the DLQ write succeeds. |
| **PI-6** | **Strict Per-User Outbox Partitioning** | Outbound `portfolios.updated.v1` events use `PartitionKey = user_id`, guaranteeing that all position updates for a single trader land in the exact same Kafka partition for in-order WebSocket delivery. |
| **PI-7** | **Full Liquidation Zero-Reset** | When a position is fully sold ($\text{quantity} = 0$), both quantity and total cost basis are clamped to exactly `0` to prevent floating-point epsilon drift. |

---

## 4. End-to-End System Flows

### 4.1 Write Path: Settled Trade Ingestion & Accounting
1. Kafka message arrives on `trades.settled.v1`.
2. `kafka.Consumer` verifies UUIDs, positive prices/quantities, `Sequence > 0`, and strict RFC3339 timestamps.
3. `postgres.Repository` begins a database transaction:
   * Inserts into `processed_trades` (`ON CONFLICT DO NOTHING`). If already processed, exits harmlessly.
   * Locks `min(buyer, seller)` then `max(buyer, seller)` holding rows.
   * Buyer: increases quantity, adds execution cost to total cost basis, updates average entry price.
   * Seller: verifies balance $\ge$ sold quantity, calculates cost of sold using average entry, credits realized PnL, subtracts quantity and cost, clamps to 0 if fully closed.
   * Writes two `PortfolioUpdated` events to `portfolio_outbox` (`status = 'PENDING'`).
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
