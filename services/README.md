# TradeDrift — Microservices Architecture & Service Purpose Catalog

> **Directory:** `services/`  
> **Role:** Master catalog detailing real-world analogies, core purposes, data ownership, communication topologies, and implementation status across all TradeDrift microservices.

---

## 1. Executive Summary & Real-World Analogies

In high-performance financial exchange architecture, responsibilities are strictly segregated to achieve sub-millisecond execution speeds, double-entry ledger precision, and fault isolation. Each microservice in TradeDrift owns a single bounded domain context and a single dedicated database (`Database-per-Service` pattern).

### 🏛️ The Exchange Store Analogy Map:

| Microservice | Real-World Physical Analogy | High-Level Purpose |
| :--- | :--- | :--- |
| **`gateway`** | **The Front Door Concierge / Security Desk** | Edge HTTP REST reverse proxy, JWT authentication, token-bucket rate limiting, and CORS routing. |
| **`auth`** | **The ID Verification & Passport Office** | Identity registration, credential hashing (Argon2id), email OTP verification, and JWT session minting. |
| **`wallet`** | **The Bank Vault & Double-Entry Ledger** | Multi-asset user balances, immutable ledger journal, balance reservations, and trade settlements. |
| **`market`** | **The Exchange Product Catalog & Ticker Display Screen** | Trading pair rules (`BTC-USDT`), tick/lot sizes, 24h rolling stats, and historical OHLC candlestick bars. |
| **`order`** | **The Cashier & Order Ticket Desk** | Idempotent order placement, synchronous wallet fund reservations, transactional outbox logging, and order lifecycle tracking. |
| **`matching`** | **The In-Memory High-Speed Auctioneer** | Sub-millisecond FIFO limit order books in memory matching buyers and sellers deterministically. |
| **`settlement`** | **The Clearinghouse & Settlement House** | Consumes trade matches from Kafka and coordinates multi-asset fund transfers in Wallet Service. |
| **`trade`** | **The Public Exchange Tape & History Feed** | Read-side projector serving historical trade records, public time & sales, and audit trails. |
| **`portfolio`** | **The Personal Financial Portfolio Manager** | Calculates real-time user holdings, unrealized PnL, cost basis, and asset allocations. |
| **`notification`** | **The Real-Time Pager & Push Notification Center** | Pushes live WebSocket updates for order fills, cancellations, and balance changes to traders. |
| **`admin`** | **The Exchange Risk & Control Center** | Administrative control plane for emergency market halts, account freezes, and system maintenance. |

---

## 2. Platform Architecture & Communication Topologies

TradeDrift combines **synchronous gRPC** for low-latency command/query operations with **asynchronous Kafka event streaming** for high-throughput execution pipelines.

```
                                  ┌───────────────────┐
                                  │  Web / Mobile UI  │
                                  └─────────┬─────────┘
                                            │ HTTP / JSON
                                            ▼
                                  ┌───────────────────┐
                                  │    API Gateway    │ (Port :8080)
                                  └─────────┬─────────┘
                                            │
               ┌────────────────────────────┼────────────────────────────┐
          gRPC │                       gRPC │                       gRPC │                       gRPC │
               ▼                            ▼                            ▼                            ▼
      ┌─────────────────┐          ┌─────────────────┐          ┌─────────────────┐          ┌─────────────────┐
      │   Auth Service  │          │  Wallet Service │          │  Order Service  │          │  Market Service │
      │   (Port :50051) │          │   (Port :50052) │          │   (Port :50053) │          │   (Port :50054) │
      └─────────────────┘          └────────▲────────┘          └────────┬────────┘          └────────▲────────┘
                                            │                            │                            │
                                            │ Synchronous Fund Reserve   │ Outbox: OrderCreated       │ Consume
                                            └────────────────────────────┘                            │ TradeExecuted
                                                                         │                            │
                                                                         ▼                            │
                                                                 ┌───────────────┐                    │
                                                                 │  Apache Kafka │────────────────────┘
                                                                 └───────┬───────┘
                                                                         │
                                                 ┌───────────────────────┼───────────────────────┐
                                                 │ TradeExecuted         │ TradeExecuted         │ OrderEvents
                                                 ▼                       ▼                       ▼
                                        ┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
                                        │ Matching Engine │     │Settlement Engine│     │  Notification   │
                                        │    (In-Memory)  │     │   (Clearing)    │     │   (WebSocket)   │
                                        └─────────────────┘     └─────────────────┘     └─────────────────┘
```

---

## 3. Microservice Detailed Catalog

---

### 3.1 API Gateway (`services/gateway`)
* **Real-World Analogy**: *The Front Door Concierge / Security Desk*
* **Core Purpose**: Serves as the single edge entrypoint for external HTTP REST clients, translating REST requests into gRPC calls across domain microservices.
* **Internal Structure**:
  * [`handler/common/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/common/README.md) — Cross-service context tracing (`x-request-id`), timeouts, DTO formatters, and gRPC status mapping.
  * [`handler/auth/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/auth/README.md) — User registration, email verification, session login, JWT refresh.
  * [`handler/wallet/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/wallet/README.md) — Supported currency assets and protected user balances.
  * [`handler/order/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/order/README.md) — Order submission, fill status queries, cancellation.
  * [`handler/market/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/market/README.md) — Trading pair rules, live 24h tickers, historical OHLC candlesticks.
* **Network & Port**: HTTP REST `:8080` (Docker Container: `tradedrift-gateway`)

---

### 3.2 Auth Service (`services/auth`)
* **Real-World Analogy**: *The ID Verification & Passport Office*
* **Core Purpose**: Manages user identities, secure credential verification, email OTP verification, and JWT session minting.
* **Key Responsibilities**:
  - User registration & password hashing (Argon2id).
  - Email verification OTP generation & rate limiting.
  - JWT Access/Refresh token pair minting and Redis revocation blacklisting.
* **Target Database**: `tradedrift_auth` (`users`, `refresh_tokens`, `verification_codes`)
* **Network & Port**: gRPC `:50051` (Docker Container: `tradedrift-auth`)

---

### 3.3 Wallet Service (`services/wallet`)
* **Real-World Analogy**: *The Bank Vault & Double-Entry Ledger*
* **Core Purpose**: Manages multi-asset user balances and guarantees zero-sum financial invariants using an immutable double-entry journal.
* **Key Responsibilities**:
  - Tracks Available vs Reserved balances (`BTC`, `USDT`, `ETH`, `SOL`).
  - Idempotent balance reservations (`ReserveFunds`) during order placement.
  - Settle trade transfers (`SettleTrade`) moving funds between buyer and seller wallets.
* **Target Database**: `tradedrift_wallet` (`wallets`, `reservations`, `ledger_entries`, `transactions`)
* **Network & Port**: gRPC `:50052` (Docker Container: `tradedrift-wallet`)

---

### 3.4 Order Service (`services/order`)
* **Real-World Analogy**: *The Cashier & Order Ticket Desk*
* **Core Purpose**: Owns the complete transactional lifecycle of individual trader orders from placement through cancellation.
* **Key Responsibilities**:
  - Enforces client idempotency to prevent duplicate order placements.
  - Synchronously reserves required funds in Wallet Service (`ReserveFunds`) before persisting orders.
  - Writes order records AND Kafka outbox events (`OrderCreated`, `OrderCancelRequested`) inside **1 atomic transaction**.
  - Tracks order status transitions (`OPEN`, `PARTIALLY_FILLED`, `FILLED`, `CANCELLING`, `CANCELLED`).
* **Target Database**: `tradedrift_order` (`orders`, `outbox`)
* **Network & Port**: gRPC `:50053` (Docker Container: `tradedrift-order`)

---

### 3.5 Market Service (`services/market`)
* **Real-World Analogy**: *The Exchange Product Catalog & Ticker Display Screen*
* **Core Purpose**: Acts as the metadata authority for trading pair rules and aggregates live 24h market stats and OHLC candlestick charts.
* **Key Responsibilities**:
  - Defines trading pairs (`BTC-USDT`, `ETH-USDT`), precision tick sizes (`0.01`), and lot sizes (`0.0001`).
  - Manages market operational status (`ACTIVE`, `HALTED`, `MAINTENANCE`).
  - Consumes `TradeExecuted` Kafka events to calculate 24h rolling stats (High, Low, Volume, % Change) and OHLC candles (`1m`, `5m`, `15m`, `1h`, `1d`).
  - Idempotent event ingestion with `ON CONFLICT (id) DO NOTHING` and timestamp-based out-of-order resolution.
* **Target Database**: `tradedrift_market` (`markets`, `market_trades`, `ohlc_candles`)
* **Network & Port**: gRPC `:50054` (Docker Container: `tradedrift-market`)

---

### 3.6 Matching Engine Service (`services/matching`)
* **Real-World Analogy**: *The In-Memory High-Speed Auctioneer*
* **Core Purpose**: Executes buy and sell orders in memory at sub-millisecond speeds using deterministic Price-Time Priority order books.
* **Key Responsibilities**:
  - Maintains in-memory Bids (`Price DESC`, FIFO) and Asks (`Price ASC`, FIFO) per market (`BTC-USDT`).
  - Runs lock-free single-threaded event loops per market for 100,000+ TPS matching.
  - Publishes executed trade matches to Kafka (`trade.executed.v1`).
* **Target Database**: In-Memory Data Structures + Redis State Backup (No SQL DB on hot path)
* **Primary Output**: Trade Match Events (`trade.executed.v1`)

---

### 3.7 Settlement Service (`services/settlement`)
* **Real-World Analogy**: *The Clearinghouse & Settlement House*
* **Core Purpose**: Asynchronously processes trade matches executed by the Matching Engine and coordinates wallet transfers.
* **Key Responsibilities**:
  - Consumes `TradeExecuted` events from Kafka.
  - Calls `WalletService.SettleTrade` to transfer base asset to buyer and quote asset to seller.
* **Target Database**: `tradedrift_settlement` (`settled_trades`)
* **Primary Output**: Trade Settlement Records

---

### 3.8 Trade Service (`services/trade`)
* **Real-World Analogy**: *The Public Exchange Tape & History Feed*
* **Core Purpose**: Serves historical trade data and public time-and-sales feeds for traders and charts.
* **Target Database**: `tradedrift_trade` (`trades`)

---

### 3.9 Portfolio Service (`services/portfolio`)
* **Real-World Analogy**: *The Personal Financial Portfolio Manager*
* **Core Purpose**: Calculates real-time user portfolio holdings, average cost basis, and unrealized profit & loss (PnL).
* **Target Database**: `tradedrift_portfolio` (`portfolios`, `positions`)

---

### 3.10 Notification Service (`services/notification`)
* **Real-World Analogy**: *The Real-Time Pager & Push Notification Center*
* **Core Purpose**: Delivers instant WebSocket events to trader web/mobile UIs when orders fill, cancel, or balances update.
* **Target Database**: `tradedrift_notification` (`notifications`, `user_preferences`)

---

### 3.11 Admin Service (`services/admin`)
* **Real-World Analogy**: *The Exchange Risk & Control Center*
* **Core Purpose**: Provides administrative control capabilities for exchange operators to maintain market health and security.
* **Target Database**: `tradedrift_admin` (`admin_users`, `audit_logs`, `market_controls`)

---

## 4. Operational & Deployment Matrix

> **Last Updated:** August 21, 2026

| Service | Port | Protocol | Target Database | Docker Container Name | Status |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **`gateway`** | `:8080` | HTTP REST | Stateless | `tradedrift-gateway` | ✅ Live & Tested |
| **`auth`** | `:50051` | gRPC | `tradedrift_auth` | `tradedrift-auth` | ✅ Live & Tested |
| **`wallet`** | `:50052` | gRPC | `tradedrift_wallet` | `tradedrift-wallet` | ✅ Live & Tested |
| **`order`** | `:50053` | gRPC + Kafka | `tradedrift_order` | `tradedrift-order` | ✅ Live & Tested |
| **`market`** | `:50054` | gRPC + Kafka | `tradedrift_market` | `tradedrift-market` | ✅ Live & Tested |
| **`matching-engine`** | — | Kafka In/Out | In-Memory + Redis + PG checkpoint | `tradedrift-matching-engine` | ✅ Live & Tested |
| **`settlement`**| `:50056` | Kafka consumer + gRPC | `tradedrift_settlement` | `tradedrift-settlement`| 🔨 **Implementation Next** |
| **`trade`** | `:50057` | gRPC / Kafka | `tradedrift_trade` | `tradedrift-trade` | ⏳ After Settlement |
| **`portfolio`** | `:50058` | gRPC / Kafka | `tradedrift_portfolio` | `tradedrift-portfolio` | ⏳ After Settlement |
| **`notification`**| `:8081` | WebSockets | `tradedrift_notification`| `tradedrift-notification`| ⏳ After Trade/Portfolio |
| **`admin`** | `:50059` | gRPC | `tradedrift_admin` | `tradedrift-admin` | ⏳ After core stable |

