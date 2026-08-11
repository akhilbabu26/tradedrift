# TradeDrift — Microservices Architecture & Service Purpose Catalog

> **Directory:** `services/`  
> **Role:** Master catalog detailing real-world analogies, core purposes, data ownership, and system responsibilities across all 11 TradeDrift microservices.

---

## 1. Executive Summary & Real-World Analogies

In high-performance financial exchange architecture, responsibilities are strictly segregated to achieve sub-millisecond execution speeds, double-entry ledger precision, and fault isolation. Each microservice in TradeDrift owns a single bounded domain context and a single dedicated database (`Database-per-Service` pattern).

### 🏛️ The Exchange Store Analogy Map:

| Microservice | Real-World Physical Analogy | High-Level Purpose |
| :--- | :--- | :--- |
| **`gateway`** | **The Front Door Concierge / Security Desk** | Edge HTTP REST reverse proxy, JWT authentication, rate limiting, and CORS routing. |
| **`auth`** | **The ID Verification & Passport Office** | Identity registration, credentials hashing (Argon2id), MFA/OTP verification, and JWT session minting. |
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

## 2. Microservice Detailed Catalog

---

### 2.1 Auth Service (`services/auth`)
* **Real-World Analogy**: *The ID Verification & Passport Office*
* **Core Purpose**: Manages user identities, secure credential verification, multi-factor authentication (MFA/OTP), and JWT token lifecycle.
* **Key Responsibilities**:
  - User registration & password hashing (Argon2id).
  - Email verification OTP generation & rate limiting.
  - JWT Access/Refresh token pair minting and revocation blacklisting.
* **Target Database**: `tradedrift_auth` (`users`, `refresh_tokens`, `verification_codes`)
* **Primary Output**: JWT Token Pairs, Identity Verification Claims

---

### 2.2 Wallet Service (`services/wallet`)
* **Real-World Analogy**: *The Bank Vault & Double-Entry Ledger*
* **Core Purpose**: Manages multi-asset user balances and guarantees zero-sum financial invariants using an immutable double-entry journal.
* **Key Responsibilities**:
  - Tracks Available vs Reserved balances (`BTC`, `USDT`, `ETH`, `SOL`).
  - Idempotent balance reservations (`ReserveFunds`) during order placement.
  - Settle trade transfers (`SettleTrade`) moving funds between buyer and seller wallets.
* **Target Database**: `tradedrift_wallet` (`wallets`, `reservations`, `ledger_entries`, `transactions`)
* **Primary Output**: Immutable Ledger Entries, Balance Updates

---

### 2.3 Market Service (`services/market`)
* **Real-World Analogy**: *The Exchange Product Catalog & Ticker Display Screen*
* **Core Purpose**: Acts as the metadata authority for trading pair rules and aggregates live 24h market stats and OHLC candlestick charts.
* **Key Responsibilities**:
  - Defines trading pairs (`BTC-USDT`, `ETH-USDT`), precision tick sizes (`0.01`), and lot sizes (`0.0001`).
  - Manages market operational status (`ACTIVE`, `HALTED`, `MAINTENANCE`).
  - Consumes `TradeExecuted` Kafka events to calculate 24h rolling stats (High, Low, Volume, % Change) and OHLC candles (`1m`, `5m`, `15m`, `1h`, `1d`).
* **Target Database**: `tradedrift_market` (`markets`, `market_trades`, `ohlc_candles`)
* **Primary Output**: Ticker Stats, OHLC Bars, Market Pair Metadata RPCs

---

### 2.4 Order Service (`services/order`)
* **Real-World Analogy**: *The Cashier & Order Ticket Desk*
* **Core Purpose**: Owns the complete transactional lifecycle of individual trader orders from placement through cancellation.
* **Key Responsibilities**:
  - Enforces client idempotency to prevent duplicate order placements.
  - Synchronously reserves required funds in Wallet Service (`ReserveFunds`) before persisting orders.
  - Writes order records AND Kafka outbox events (`OrderCreated`, `OrderCancelRequested`) inside **1 atomic transaction**.
  - Tracks order status transitions (`OPEN`, `PARTIALLY_FILLED`, `FILLED`, `CANCELLING`, `CANCELLED`).
* **Target Database**: `tradedrift_order` (`orders`, `outbox`)
* **Primary Output**: Outbox Events (`orders.submitted`, `orders.cancel-requested`), Order Status Updates

---

### 2.5 Matching Engine Service (`services/matching`)
* **Real-World Analogy**: *The In-Memory High-Speed Auctioneer*
* **Core Purpose**: Executes buy and sell orders in memory at sub-millisecond speeds using deterministic Price-Time Priority order books.
* **Key Responsibilities**:
  - Maintains in-memory Bids (`Price DESC`, FIFO) and Asks (`Price ASC`, FIFO) per market (`BTC-USDT`).
  - Runs lock-free single-threaded event loops per market for 100,000+ TPS matching.
  - Publishes executed trade matches to Kafka (`trades.executed`).
* **Target Database**: In-Memory Data Structures + Redis State Backup (No SQL DB on hot path)
* **Primary Output**: Trade Match Events (`trades.executed`)

---

### 2.6 Settlement Service (`services/settlement`)
* **Real-World Analogy**: *The Clearinghouse & Settlement House*
* **Core Purpose**: Asynchronously processes trade matches executed by the Matching Engine and coordinates wallet transfers.
* **Key Responsibilities**:
  - Consumes `TradeExecuted` events from Kafka.
  - Calls `WalletService.SettleTrade` to transfer base asset to buyer and quote asset to seller.
* **Target Database**: `tradedrift_settlement` (`settled_trades`)
* **Primary Output**: Trade Settlement Records

---

### 2.7 Trade Service (`services/trade`)
* **Real-World Analogy**: *The Public Exchange Tape & History Feed*
* **Core Purpose**: Serves historical trade data and public time-and-sales feeds for traders and charts.
* **Key Responsibilities**:
  - Projects trade execution events into searchable read-models.
  - Serves public time-and-sales market history REST/gRPC endpoints.
* **Target Database**: `tradedrift_trade` (`trades`)
* **Primary Output**: Time & Sales Query Feeds

---

### 2.8 Portfolio Service (`services/portfolio`)
* **Real-World Analogy**: *The Personal Financial Portfolio Manager*
* **Core Purpose**: Calculates real-time user portfolio holdings, average cost basis, and unrealized profit & loss (PnL).
* **Key Responsibilities**:
  - Updates asset position quantities per trader post-settlement.
  - Computes cost basis and PnL analytics.
* **Target Database**: `tradedrift_portfolio` (`portfolios`, `positions`)
* **Primary Output**: User Portfolio Analytics, PnL Statistics

---

### 2.9 Notification Service (`services/notification`)
* **Real-World Analogy**: *The Real-Time Pager & Push Notification Center*
* **Core Purpose**: Delivers instant WebSocket events to trader web/mobile UIs when orders fill, cancel, or balances update.
* **Key Responsibilities**:
  - Manages persistent WebSocket client connections.
  - Consumes platform Kafka events and routes notifications to target user channels via Redis Pub/Sub.
* **Target Database**: `tradedrift_notification` (`notifications`, `user_preferences`)
* **Primary Output**: WebSocket Event Frames (`order_filled`, `trade_executed`, `balance_updated`)

---

### 2.10 Admin Service (`services/admin`)
* **Real-World Analogy**: *The Exchange Risk & Control Center*
* **Core Purpose**: Provides administrative control capabilities for exchange operators to maintain market health and security.
* **Key Responsibilities**:
  - Triggering emergency market halts / suspensions.
  - User account freezes and risk controls.
* **Target Database**: `tradedrift_admin` (`admin_users`, `audit_logs`, `market_controls`)
* **Primary Output**: System Audit Logs, Control Events

---

### 2.11 API Gateway (`services/gateway`)
* **Real-World Analogy**: *The Front Door Concierge / Security Desk*
* **Core Purpose**: Serves as the single edge entrypoint for external HTTP REST clients, translating REST requests into gRPC calls.
* **Key Responsibilities**:
  - JWT Bearer token authentication & context injection.
  - Token bucket rate limiting per IP / User.
  - CORS, Request ID tracing, and response standardization.
* **Target Database**: Stateless (Uses Redis for Rate Limiting & Token Blacklist Checks)
* **Primary Output**: Standardized JSON REST Responses (`{ "success": true, "data": ... }`)

---

## 3. Summary Matrix Across All Microservices

| Service | Target Database | Primary Data Owned | Triggered By | Wallet / Ledger Interaction | Primary Output |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **`gateway`** | Stateless (Redis Cache) | HTTP Routes, Rate Limit Tokens | External REST Clients | None | Standardized REST JSON Responses |
| **`auth`** | `tradedrift_auth` | User Credentials, Sessions, OTPs | Gateway Registration/Login REST | None | JWT Access & Refresh Tokens |
| **`wallet`** | `tradedrift_wallet` | Multi-asset Balances, Ledger Logs | Order & Settlement Services | **Direct Ledger Master** | Balance Reservations & Transfers |
| **`market`** | `tradedrift_market` | Trading Pairs, Tickers, OHLC Bars | Admin Seed + Kafka `TradeExecuted` | None | Ticker Stats, OHLC Bars, Pair RPCs |
| **`order`** | `tradedrift_order` | Orders, Idempotency Keys, Outbox | Client Order Requests | Synchronously reserves/releases money | Outbox events (`orders.submitted`, `orders.cancel-requested`) |
| **`matching`** | In-Memory + Redis | Price-Time Limit Order Books | Kafka `orders.submitted` | None | Trade Match Events (`trades.executed`) |
| **`settlement`**| `tradedrift_settlement` | Settled Trade Records | Kafka `trades.executed` | Calls `WalletService.SettleTrade` | Settled Trade Confirmation |
| **`trade`** | `tradedrift_trade` | Public Time & Sales History | Kafka `trades.executed` | None | Public Time & Sales Feeds |
| **`portfolio`** | `tradedrift_portfolio` | User Holdings, Cost Basis, PnL | Kafka `trades.executed` | Reads holding snapshots | Portfolio PnL & Asset Allocation |
| **`notification`**| `tradedrift_notification` | User Alerts, WS Subscriptions | Kafka Platform Events | None | Live WebSocket Frames |
| **`admin`** | `tradedrift_admin` | Audit Logs, Market Control Rules | Admin Operator REST Requests | Can freeze account balances | Market Halt & Control Events |
