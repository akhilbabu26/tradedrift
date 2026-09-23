# TradeDrift — All Backend APIs Reference

> **Base URL (API Gateway):** `http://localhost:8080`
> **Admin Service URL:** `http://localhost:8090` (direct, not proxied through gateway)
> **Wallet Top-Up URL:** `http://localhost:8091` (direct, not proxied through gateway)
>
> All REST responses are wrapped in `{ "success": true, "data": ... }` format.
> Internal gRPC services are consumed by the gateway or other services — not called directly from the frontend.

---

## Table of Contents

1. [🌐 API Gateway](#1--api-gateway)
2. [🔐 Auth Service (gRPC)](#2--auth-service-grpc)
3. [💰 Wallet Service (gRPC)](#3--wallet-service-grpc)
4. [📋 Order Service (gRPC)](#4--order-service-grpc)
5. [📊 Market Service (gRPC)](#5--market-service-grpc)
6. [🤝 Trade Service (gRPC)](#6--trade-service-grpc)
7. [📈 Portfolio Service (gRPC)](#7--portfolio-service-grpc)
8. [🔔 Notification Service (gRPC)](#8--notification-service-grpc)
9. [⚡ Matching Engine](#9--matching-engine)
10. [🏦 Settlement Service](#10--settlement-service)
11. [💧 Liquidity Engine](#11--liquidity-engine)
12. [💳 Wallet Top-Up Service (HTTP)](#12--wallet-top-up-service-http)
13. [🛡️ Admin Service (HTTP)](#13--admin-service-http)

---

## 1. 🌐 API Gateway

**Port:** `:8080`
**Role:** Single HTTP entrypoint for the frontend. Translates REST → gRPC for all internal services. Handles JWT auth, CORS, rate-limiting, and request tracing.

### 🔓 Authentication — Public

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/auth/register` | Register a new user account |
| `POST` | `/api/v1/auth/verify` | Verify email with OTP code |
| `POST` | `/api/v1/auth/resend` | Resend email verification OTP |
| `POST` | `/api/v1/auth/login` | Login and receive JWT token pair |
| `POST` | `/api/v1/auth/refresh` | Rotate refresh token for a new access token |
| `POST` | `/api/v1/auth/forgot-password` | Request a password reset code via email |
| `POST` | `/api/v1/auth/reset-password` | Set a new password using the reset code |

### 🔒 Authentication — Protected

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/v1/auth/logout` | Bearer JWT | Revoke the current active session |
| `POST` | `/api/v1/auth/logout-all` | Bearer JWT | Revoke all sessions across all devices |
| `POST` | `/api/v1/auth/change-password` | Bearer JWT | Update password while authenticated |

---

### 💰 Wallet — Public

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/wallet/assets` | List all supported exchange currencies/assets |

### 🔒 Wallet — Protected

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/wallet/balances` | Bearer JWT | Get all asset balances for the authenticated user |
| `GET` | `/api/v1/wallet/balances/{asset}` | Bearer JWT | Get balance for a specific asset (e.g. `BTC`, `USDT`) |

---

### 📋 Orders — Protected

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/v1/orders` | Bearer JWT | Place a new limit or market order |
| `GET` | `/api/v1/orders` | Bearer JWT | List order history (filterable by `market_id`, `status`, `limit`) |
| `GET` | `/api/v1/orders/{id}` | Bearer JWT | Get a single order by ID |
| `POST` | `/api/v1/orders/{id}/cancel` | Bearer JWT | Cancel an open order |

**Query Params for `GET /api/v1/orders`:**
- `market_id` — filter by market (e.g. `BTC-USDT`)
- `status` — `OPEN`, `PARTIALLY_FILLED`, `FILLED`, `CANCELLING`, `CANCELLED`
- `limit` — max results (default: 50)

**`POST /api/v1/orders` Request Body:**
```json
{
  "market_id": "BTC-USDT",
  "side": "BUY",
  "order_type": "LIMIT",
  "price": "45000.00",
  "quantity": "0.001"
}
```

---

### 📊 Markets — Public

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/v1/markets` | List all active trading pairs and rules |
| `GET` | `/api/v1/markets/overview` | Get all markets with ticker + candle data (for dashboard) |
| `GET` | `/api/v1/markets/{id}` | Get single market details |
| `GET` | `/api/v1/markets/{id}/ticker` | Get live 24h rolling price & volume stats |
| `GET` | `/api/v1/markets/{id}/candles` | Get historical OHLC candlestick bars |
| `GET` | `/api/v1/markets/{id}/trades` | Get the public market trade tape (no buyer/seller IDs) |

**Query Params for `/api/v1/markets/{id}/candles`:**
- `resolution` — `1m`, `5m`, `15m`, `1h`, `4h`, `1d` (default: `1h`)
- `limit` — number of candles (default: 100)
- `from` — RFC3339 start time (optional)
- `to` — RFC3339 end time (optional)

**Query Params for `/api/v1/markets/{id}/trades`:**
- `cursor` — keyset cursor from previous `next_cursor`
- `limit` — max results (default: 50, max: 200)

---

### 🤝 Trades — Protected

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/trades` | Bearer JWT | Get the authenticated user's fill/trade history |
| `GET` | `/api/v1/trades/{id}` | Bearer JWT | Get a single trade (caller must be buyer or seller) |

**Query Params for `GET /api/v1/trades`:**
- `cursor` — keyset cursor for pagination
- `limit` — max results (default: 20, max: 100)
- `market_id` — filter by market (optional)

---

### 📈 Portfolio — Protected

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/portfolio/summary` | Bearer JWT | Get total equity, PnL, and cash balance |
| `GET` | `/api/v1/portfolio/holdings` | Bearer JWT | Get asset-level holdings breakdown |

---

### 🔔 Notifications — Protected

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/notifications` | Bearer JWT | List notifications (paginated) |
| `GET` | `/api/v1/notifications/unread-count` | Bearer JWT | Get count of unread notifications |
| `POST` | `/api/v1/notifications/{id}/read` | Bearer JWT | Mark a single notification as read |
| `POST` | `/api/v1/notifications/read-all` | Bearer JWT | Mark all notifications as read |

**Query Params for `GET /api/v1/notifications`:**
- `limit` — max results (default: 20, max: 100)
- `cursor_time` — RFC3339 timestamp cursor
- `cursor_id` — notification ID cursor (both required together)
- `type` — filter by notification type

---

### 🔌 WebSocket — Real-Time Streaming

| Endpoint | Auth | Description |
|----------|------|-------------|
| `GET /ws` | Optional JWT (query param `token=<jwt>`) | WebSocket connection for real-time market data |

**WebSocket Subscription Message Format (client → server):**
```json
{ "action": "subscribe", "stream": "<stream_name>" }
{ "action": "unsubscribe", "stream": "<stream_name>" }
```

**Available Stream Types:**

| Stream Format | Description |
|---------------|-------------|
| `orderbook.<market_id>` | Live order book depth updates |
| `ticker.<market_id>` | Live 24h ticker updates |
| `trades.<market_id>` | Public market trade tape (no IDs) |
| `notifications` | User-specific notifications (requires auth) |

---

## 2. 🔐 Auth Service (gRPC)

**Port:** `:50051`
**Protocol:** gRPC
**Proto Package:** `tradedrift.auth.v1`
**Consumed by:** API Gateway, Admin Service

> All methods below are gRPC RPCs. The frontend never calls this service directly — all calls are proxied through the API Gateway.

| RPC Method | Description | Auth Required |
|------------|-------------|---------------|
| `Register` | Register a new user (email, username, password) | ❌ Public |
| `VerifyEmail` | Verify email with OTP, returns JWT token pair | ❌ Public |
| `ResendVerificationCode` | Resend email OTP | ❌ Public |
| `Login` | Authenticate user (email or username + password), returns JWT pair | ❌ Public |
| `RefreshToken` | Exchange refresh token for new access token | ❌ Public |
| `ForgotPassword` | Send password reset code to email | ❌ Public |
| `ResetPassword` | Set new password using reset code | ❌ Public |
| `Logout` | Revoke current session (requires JWT context) | ✅ JWT Context |
| `LogoutAll` | Revoke all sessions for user | ✅ JWT Context |
| `ChangePassword` | Update password | ✅ JWT Context |
| `InvalidateUserSessions` | ⚙️ Internal — revoke all sessions by `user_id` (Admin use) | ⚙️ Internal |
| `SuspendUser` | ⚙️ Internal — suspend a user account | ⚙️ Internal |
| `UnsuspendUser` | ⚙️ Internal — re-enable a suspended account | ⚙️ Internal |
| `ListSuspendedUsers` | ⚙️ Internal — list all suspended users | ⚙️ Internal |

---

## 3. 💰 Wallet Service (gRPC)

**Port:** `:50052`
**Protocol:** gRPC
**Proto Package:** `tradedrift.wallet.v1`
**Consumed by:** API Gateway, Order Service, Settlement Service, Admin Service

| RPC Method | Description | Auth Required |
|------------|-------------|---------------|
| `GetSupportedAssets` | List all tradeable assets with metadata | ❌ Public |
| `GetBalance` | Get balance for a user + asset | ✅ user_id |
| `GetBalances` | Get all balances for a user | ✅ user_id |
| `InitializeWallet` | ⚙️ Internal — create wallet for a new user | ⚙️ Internal |
| `ReserveFunds` | ⚙️ Internal — reserve funds when placing an order | ⚙️ Internal |
| `ReleaseFunds` | ⚙️ Internal — release reserved funds on order cancel | ⚙️ Internal |
| `SettleTrade` | ⚙️ Internal — atomically settle a matched trade | ⚙️ Internal |
| `DepositFunds` | ⚙️ Internal — credit user wallet after top-up payment | ⚙️ Internal |
| `FreezeWallet` | ⚙️ Internal — freeze/unfreeze a wallet asset (Admin) | ⚙️ Internal |
| `Health` | Health check probe | ❌ Public |

---

## 4. 📋 Order Service (gRPC)

**Port:** `:50053`
**Protocol:** gRPC
**Proto Package:** `tradedrift.order.v1`
**Consumed by:** API Gateway, Matching Engine, Liquidity Engine

| RPC Method | Description | Auth Required |
|------------|-------------|---------------|
| `CreateOrder` | Place a new LIMIT or MARKET order | ✅ user_id |
| `CancelOrder` | Cancel an open order by ID | ✅ user_id |
| `GetOrder` | Get a single order by ID | ✅ user_id |
| `ListOrders` | List orders with filters (market, status, side, time range) | ✅ user_id |
| `CancelAllOrders` | ⚙️ Internal — bulk cancel all orders (for market halt) | ⚙️ Internal (Not yet implemented) |

---

## 5. 📊 Market Service (gRPC)

**Port:** `:50054`
**Protocol:** gRPC
**Proto Package:** `tradedrift.market.v1`
**Consumed by:** API Gateway, Matching Engine, Liquidity Engine

| RPC Method | Description | Auth Required |
|------------|-------------|---------------|
| `ListMarkets` | List all active trading pairs | ❌ Public |
| `GetMarket` | Get single market details by `market_id` | ❌ Public |
| `GetTicker` | Get live 24h rolling stats for a market | ❌ Public |
| `GetCandles` | Get OHLC candlestick bars with resolution & time range | ❌ Public |
| `GetMarketsOverview` | Get all markets enriched with ticker + candle data | ❌ Public |

**Supported Candle Resolutions:** `1m`, `5m`, `15m`, `1h`, `4h`, `1d`

---

## 6. 🤝 Trade Service (gRPC)

**Port:** `:50057`
**Protocol:** gRPC
**Proto Package:** `tradedrift.trade.v1`
**Consumed by:** API Gateway, Settlement Service

| RPC Method | Description | Auth Required |
|------------|-------------|---------------|
| `GetTrade` | Get a single trade (enforces caller must be buyer or seller) | ✅ caller_user_id |
| `ListUserTrades` | Get user's fill history, cursor-paginated, newest-first | ✅ user_id |
| `ListMarketTrades` | Get public trade tape for a market (no buyer/seller IDs) | ❌ Public |

---

## 7. 📈 Portfolio Service (gRPC)

**Port:** `:50058`
**Protocol:** gRPC
**Proto Package:** `tradedrift.portfolio.v1`
**Consumed by:** API Gateway

| RPC Method | Description | Auth Required |
|------------|-------------|---------------|
| `GetPortfolioSummary` | Get total equity, realized/unrealized PnL, cash balance | ✅ user_id |
| `GetPortfolioHoldings` | Get per-asset holdings with entry price & current price | ✅ user_id |

---

## 8. 🔔 Notification Service (gRPC)

**Port:** `:50059`
**Protocol:** gRPC
**Proto Package:** `tradedrift.notification.v1`
**Consumed by:** API Gateway, Auth Service, Admin Service, Wallet Service

| RPC Method | Description | Auth Required |
|------------|-------------|---------------|
| `GetNotifications` | Get keyset-paginated list of user notifications | ✅ user_id |
| `GetUnreadCount` | Get count of unread notifications for badge rendering | ✅ user_id |
| `MarkAsRead` | Mark a single notification as read | ✅ user_id |
| `MarkAllAsRead` | Mark all unread notifications as read | ✅ user_id |
| `CreateNotification` | ⚙️ Internal — dispatch a new notification for a user | ⚙️ Internal |

---

## 9. ⚡ Matching Engine

**Protocol:** Kafka (event-driven, no HTTP/gRPC interface)
**Role:** Pure Kafka consumer/producer — listens for order events and produces matched trade events.

> The Matching Engine has **no HTTP or gRPC API**. It is a fully event-driven background service.

**Kafka Topics Consumed:**

| Topic | Description |
|-------|-------------|
| `orders.placed` | New order events from the Order Service |
| `orders.cancelled` | Order cancellation events |

**Kafka Topics Produced:**

| Topic | Description |
|-------|-------------|
| `trades.executed` | Trade match events consumed by Settlement, Trade, and Gateway WS |
| `orderbook.depth` | Order book depth snapshots for Redis/WebSocket streaming |

---

## 10. 🏦 Settlement Service

**Protocol:** Kafka (event-driven, no HTTP/gRPC interface)
**Role:** Consumes trade events from Kafka and calls Wallet Service (gRPC) to atomically settle matched trades.

> The Settlement Service has **no HTTP or gRPC API**. It is a fully event-driven background service.

**Kafka Topics Consumed:**

| Topic | Description |
|-------|-------------|
| `trades.executed` | Trade execution events from the Matching Engine |

**Downstream gRPC Calls:**
- `WalletService.SettleTrade` — atomically moves funds between buyer and seller wallets

---

## 11. 💧 Liquidity Engine

**Protocol:** Internal (event-driven + internal gRPC)
**Role:** Automated market maker that places/cancels liquidity orders to maintain order book depth.

> The Liquidity Engine has **no public HTTP or gRPC API**. It communicates internally with the Order Service and Market Service.

**Internal gRPC Calls:**
- `OrderService.CreateOrder` — places liquidity orders
- `OrderService.CancelOrder` — removes stale liquidity
- `MarketService.GetMarket` — reads market config/tick sizes

---

## 12. 💳 Wallet Top-Up Service (HTTP)

**Port:** `:8091`
**Protocol:** HTTP REST (direct, not proxied through the API Gateway)
**Auth:** Bearer JWT (service validates token internally)

### User Endpoints — Protected

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/v1/topups` | Bearer JWT | Create a new INR top-up payment order |
| `GET` | `/api/v1/topups/daily-usage` | Bearer JWT | Get today's top-up usage vs daily limit |
| `GET` | `/api/v1/topups/{id}` | Bearer JWT | Get top-up order status by ID |

**`POST /api/v1/topups` Request Headers:**
- `Authorization: Bearer <jwt>`
- `X-Idempotency-Key: <unique-key>` *(required)*

**`POST /api/v1/topups` Request Body:**
```json
{ "inr_amount": 100000 }
```

### Webhook Endpoint — Payment Provider Callback

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/v1/webhooks/payment/{provider}` | Provider Signature | Payment gateway webhook callback (Razorpay, etc.) |

### Health Check

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Liveness probe |

---

## 13. 🛡️ Admin Service (HTTP)

**Port:** `:8090`
**Protocol:** HTTP REST (direct, not proxied through the API Gateway)
**Auth:** Admin JWT Bearer token (requires `role: admin` claim)
**Idempotency:** All mutation endpoints require `Idempotency-Key` header

### Probes & Monitoring — Unauthenticated

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Liveness probe |
| `GET` | `/ready` | Readiness probe |
| `GET` | `/metrics` | Prometheus metrics scrape endpoint |

### System Diagnostics — Protected (Admin)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/admin/system/health` | Admin JWT | Full system diagnostic health check across all services |

### User Management — Protected (Admin)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/v1/admin/users/{user_id}/suspend` | Admin JWT + Idempotency-Key | Suspend a user account |
| `POST` | `/api/v1/admin/users/{user_id}/unsuspend` | Admin JWT + Idempotency-Key | Re-enable a suspended user account |

### Wallet Management — Protected (Admin)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/v1/admin/users/{user_id}/wallets/{asset}/freeze` | Admin JWT + Idempotency-Key | Freeze a user's wallet for a specific asset |
| `POST` | `/api/v1/admin/users/{user_id}/wallets/{asset}/unfreeze` | Admin JWT + Idempotency-Key | Unfreeze a user's wallet for a specific asset |

### Market Management — Protected (Admin)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/v1/admin/markets/{market_id}/halt` | Admin JWT + Idempotency-Key | Halt trading on a market (circuit breaker) |
| `POST` | `/api/v1/admin/markets/{market_id}/resume` | Admin JWT + Idempotency-Key | Resume trading on a halted market |

### Incident Management — Protected (Admin)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/admin/incidents` | Admin JWT | List all system incidents |
| `GET` | `/api/v1/admin/incidents/stats` | Admin JWT | Get incident statistics and summary |
| `GET` | `/api/v1/admin/incidents/{incident_id}` | Admin JWT | Get a single incident by ID |
| `GET` | `/api/v1/admin/incidents/{incident_id}/correlated` | Admin JWT | Get incidents correlated with a specific incident |
| `POST` | `/api/v1/admin/incidents/{incident_id}/resolve` | Admin JWT | Mark an incident as resolved |

### Analytics & Risk — Protected (Admin)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/admin/analytics/overview` | Admin JWT | Get operations overview and business analytics |
| `GET` | `/api/v1/admin/analytics/risk-signals` | Admin JWT | Get active risk signals and anomaly detections |

### Topology — Protected (Admin)

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/api/v1/admin/topology` | Admin JWT | Get live service dependency topology map |

---

## Summary Table

| # | Service | Interface | Port | Frontend Facing |
|---|---------|-----------|------|----------------|
| 1 | **API Gateway** | HTTP REST + WebSocket | `:8080` | ✅ Primary entry point |
| 2 | **Auth Service** | gRPC | `:50051` | ❌ Via Gateway |
| 3 | **Wallet Service** | gRPC | `:50052` | ❌ Via Gateway |
| 4 | **Order Service** | gRPC | `:50053` | ❌ Via Gateway |
| 5 | **Market Service** | gRPC | `:50054` | ❌ Via Gateway |
| 6 | **Trade Service** | gRPC | `:50057` | ❌ Via Gateway |
| 7 | **Portfolio Service** | gRPC | `:50058` | ❌ Via Gateway |
| 8 | **Notification Service** | gRPC | `:50059` | ❌ Via Gateway |
| 9 | **Matching Engine** | Kafka (Event-driven) | — | ❌ Internal only |
| 10 | **Settlement Service** | Kafka (Event-driven) | — | ❌ Internal only |
| 11 | **Liquidity Engine** | Internal gRPC | — | ❌ Internal only |
| 12 | **Wallet Top-Up** | HTTP REST | `:8091` | ✅ Direct (top-up flows) |
| 13 | **Admin Service** | HTTP REST | `:8090` | ✅ Admin dashboard only |
