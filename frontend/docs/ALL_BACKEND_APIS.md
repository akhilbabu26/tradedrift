# TradeDrift — All Backend APIs & Real-Time Protocol Reference

> **Base URL (API Gateway):** `http://localhost:8080`  
> **WebSocket URL (API Gateway):** `ws://localhost:8080/ws`  
> **Wallet Top-Up Service URL:** `http://localhost:8091` *(Direct HTTP, not proxied through Gateway)*  
> **Admin Service URL:** `http://localhost:8090` *(Direct HTTP, not proxied through Gateway)*  
>
> ⚠️ **Important Architecture & Contract Notes:**
> - **No Global Response Envelope:** REST endpoints do **NOT** wrap responses in `{"success": true, "data": ...}`. Responses return domain DTOs directly or in dedicated collection keys (e.g. `{"markets": [...]}`, `{"orders": [...]}`).
> - **Error Response Format:** On error, the API Gateway returns:
>   ```json
>   {
>     "errorCode": "INVALID_ARGUMENT",
>     "message": "Human-readable error explanation",
>     "timestamp": "2026-09-26T14:00:00Z"
>   }
>   ```
> - **Casing Conventions:**
>   - **`snake_case`**: Orders (`market_id`, `order_type`, `filled_quantity`), Markets (`base_asset`, `tick_size`), Trades (`buy_order_id`, `executed_at`), Admin operations.
>   - **`camelCase`**: Auth tokens & profile (`accessToken`, `refreshToken`, `userId`), Portfolio summary & holdings (`totalValue`, `realizedPnl`, `unrealizedPnl`, `cashBalance`), Notifications (`referenceId`, `isRead`, `unreadCount`), Wallet Top-Up (`topupId`, `inrAmount`).
> - **Internal Microservices (gRPC/Kafka):** Services 2–11 run internally on gRPC or Kafka and are proxied by the API Gateway. Frontend applications interact exclusively with the API Gateway (or direct services 12–13 where specified).

---

## Table of Contents

1. [🌐 1. API Gateway (HTTP REST)](#1--api-gateway-http-rest)
   - [🔐 Authentication (Public & Protected)](#-authentication)
   - [💰 Wallet](#-wallet)
   - [📋 Orders](#-orders)
   - [📊 Markets](#-markets)
   - [🤝 Trades](#-trades)
   - [📈 Portfolio](#-portfolio)
   - [🔔 Notifications](#-notifications)
2. [🔌 2. API Gateway (WebSocket Protocol)](#2--api-gateway-websocket-protocol)
3. [💳 3. Wallet Top-Up Service (Direct HTTP)](#3--wallet-top-up-service-direct-http)
4. [🛡️ 4. Admin Service (Direct HTTP)](#4--admin-service-direct-http)
5. [⚙️ 5. Internal Microservices (gRPC & Event Engines)](#5--internal-microservices-grpc--event-engines)
   - [🔐 Auth Service (gRPC)](#51-auth-service-grpc)
   - [💰 Wallet Service (gRPC)](#52-wallet-service-grpc)
   - [📋 Order Service (gRPC)](#53-order-service-grpc)
   - [📊 Market Service (gRPC)](#54-market-service-grpc)
   - [🤝 Trade Service (gRPC)](#55-trade-service-grpc)
   - [📈 Portfolio Service (gRPC)](#56-portfolio-service-grpc)
   - [🔔 Notification Service (gRPC)](#57-notification-service-grpc)
   - [⚡ Matching Engine (Kafka / Redis)](#58-matching-engine-kafka--redis)
   - [🏦 Settlement Service (Kafka / gRPC)](#59-settlement-service-kafka--grpc)
   - [💧 Liquidity Engine (Internal MM)](#510-liquidity-engine-internal-mm)
6. [📋 6. Summary Matrix](#6--summary-matrix)

---

## 1. 🌐 API Gateway (HTTP REST)

**Base URL:** `http://localhost:8080`  
**Authentication Header:** `Authorization: Bearer <accessToken>` (required for all protected routes)

---

### 🔐 Authentication

#### `POST /api/v1/auth/register`
* **Access:** Public
* **Explanation:** Creates a new user account with `PENDING_VERIFICATION` status and triggers an email verification code (OTP). Does not issue JWTs immediately.
* **Request Body:**
  ```json
  {
    "email": "trader@example.com",
    "username": "trader01",
    "password": "SecurePassword123"
  }
  ```
* **Response:** `201 Created`
  ```json
  {
    "user_id": "01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
    "verification_required": true
  }
  ```

#### `POST /api/v1/auth/verify`
* **Access:** Public
* **Explanation:** Validates the 6-digit OTP code sent to the user's email. On verification, activates the account, provisions default wallets, and returns the initial JWT token pair.
* **Request Body:**
  ```json
  {
    "email": "trader@example.com",
    "code": "123456"
  }
  ```
* **Response:** `200 OK`
  ```json
  {
    "user": {
      "userId": "01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
      "email": "trader@example.com",
      "username": "trader01"
    },
    "accessToken": "eyJhbGciOiJIUzI1Ni...",
    "refreshToken": "eyJhbGciOiJIUzI1Ni...",
    "accessTokenExpiresAt": "2026-09-26T14:15:00Z",
    "refreshTokenExpiresAt": "2026-10-03T14:00:00Z"
  }
  ```

#### `POST /api/v1/auth/resend`
* **Access:** Public
* **Explanation:** Generates and resends a fresh email verification code if the previous OTP expired or was not received.
* **Request Body:**
  ```json
  {
    "email": "trader@example.com"
  }
  ```
* **Response:** `200 OK`
  ```json
  {
    "message": "verification code resent"
  }
  ```

#### `POST /api/v1/auth/login`
* **Access:** Public
* **Explanation:** Authenticates an existing verified user using their email or username and password. Issues fresh access and refresh token pair.
* **Request Body:**
  ```json
  {
    "identifier": "trader@example.com",
    "password": "SecurePassword123"
  }
  ```
* **Response:** `200 OK`
  ```json
  {
    "user": {
      "userId": "01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
      "email": "trader@example.com",
      "username": "trader01"
    },
    "accessToken": "eyJhbGciOiJIUzI1Ni...",
    "refreshToken": "eyJhbGciOiJIUzI1Ni...",
    "accessTokenExpiresAt": "2026-09-26T14:15:00Z",
    "refreshTokenExpiresAt": "2026-10-03T14:00:00Z"
  }
  ```

#### `POST /api/v1/auth/refresh`
* **Access:** Public
* **Explanation:** Rotates an existing refresh token to generate a new access token and new refresh token pair. If a revoked token is reused, all user sessions are automatically invalidated.
* **Request Body:**
  ```json
  {
    "refreshToken": "eyJhbGciOiJIUzI1Ni..."
  }
  ```
* **Response:** `200 OK`
  ```json
  {
    "accessToken": "eyJhbGciOiJIUzI1Ni...",
    "refreshToken": "eyJhbGciOiJIUzI1Ni...",
    "accessTokenExpiresAt": "2026-09-26T14:15:00Z",
    "refreshTokenExpiresAt": "2026-10-03T14:00:00Z"
  }
  ```

#### `POST /api/v1/auth/forgot-password`
* **Access:** Public
* **Explanation:** Initiates a password reset flow by sending a 6-digit reset code via email. Always returns 200 OK to prevent user enumeration attacks.
* **Request Body:**
  ```json
  {
    "email": "trader@example.com"
  }
  ```
* **Response:** `200 OK`
  ```json
  {
    "message": "password reset email sent"
  }
  ```

#### `POST /api/v1/auth/reset-password`
* **Access:** Public
* **Explanation:** Consumes the password reset code and sets a new password. Revokes all active refresh tokens and blacklists issued access tokens.
* **Request Body:**
  ```json
  {
    "email": "trader@example.com",
    "code": "654321",
    "newPassword": "NewSecurePassword456"
  }
  ```
* **Response:** `200 OK`
  ```json
  {
    "message": "password reset successfully"
  }
  ```

#### `POST /api/v1/auth/logout`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Revokes the specific active refresh token and blacklists the current JWT session in Redis.
* **Request Body:**
  ```json
  {
    "refreshToken": "eyJhbGciOiJIUzI1Ni..."
  }
  ```
* **Response:** `200 OK`
  ```json
  {
    "message": "logged out successfully"
  }
  ```

#### `POST /api/v1/auth/logout-all`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Revokes all active sessions across all devices for the authenticated user by invalidating all user tokens in Redis.
* **Request Body:** `{}`
* **Response:** `200 OK`
  ```json
  {
    "message": "logged out from all devices"
  }
  ```

#### `POST /api/v1/auth/change-password`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Allows an authenticated user to change their password by validating their current password and assigning a new one.
* **Request Body:**
  ```json
  {
    "oldPassword": "CurrentPassword123",
    "newPassword": "NewSecurePassword456"
  }
  ```
* **Response:** `200 OK`
  ```json
  {
    "message": "password changed successfully"
  }
  ```

---

### 💰 Wallet

#### `GET /api/v1/wallet/assets`
* **Access:** Public
* **Explanation:** Returns metadata for all supported trading assets on the exchange (decimals, display order, seed status, enabled state).
* **Response:** `200 OK`
  ```json
  {
    "assets": [
      {
        "symbol": "BTC",
        "name": "Bitcoin",
        "decimals": 8,
        "isEnabled": true,
        "seedAmount": "1.00000000",
        "displayOrder": 1
      },
      {
        "symbol": "USDT",
        "name": "Tether USD",
        "decimals": 2,
        "isEnabled": true,
        "seedAmount": "10000.00",
        "displayOrder": 2
      }
    ]
  }
  ```

#### `GET /api/v1/wallet/balances`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Retrieves the user's available and reserved balances across all supported assets.
* **Response:** `200 OK`
  ```json
  {
    "balances": [
      {
        "asset": "BTC",
        "available_balance": "0.85000000",
        "reserved_balance": "0.15000000"
      },
      {
        "asset": "USDT",
        "available_balance": "4500.50",
        "reserved_balance": "1000.00"
      }
    ]
  }
  ```

#### `GET /api/v1/wallet/balances/{asset}`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Retrieves available and reserved balance for a single asset specified in the path (e.g., `BTC` or `USDT`).
* **Path Parameters:**
  - `asset` (string, required): e.g. `BTC`
* **Response:** `200 OK`
  ```json
  {
    "asset": "BTC",
    "available_balance": "0.85000000",
    "reserved_balance": "0.15000000"
  }
  ```

---

### 📋 Orders

#### `POST /api/v1/orders`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Validates balance, reserves required funds in the wallet, and submits a new `LIMIT` or `MARKET` order to the order service and Kafka pipeline.
* **Headers:**
  - `Idempotency-Key` (optional string): Client UUID to prevent duplicate submissions.
* **Request Body:**
  ```json
  {
    "market_id": "BTC-USDT",
    "side": "BUY",
    "order_type": "LIMIT",
    "price": "65000.00",
    "quantity": "0.01000000"
  }
  ```
  *(Note: `side` must be `"BUY"` or `"SELL"`. `order_type` must be `"LIMIT"` or `"MARKET"`. `price` is required for `LIMIT` orders).*
* **Response:** `201 Created`
  ```json
  {
    "id": "01923e20-7b3c-70e1-b441-df0a8f89e1a2",
    "user_id": "01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
    "market_id": "BTC-USDT",
    "side": "ORDER_SIDE_BUY",
    "order_type": "ORDER_TYPE_LIMIT",
    "status": "ORDER_STATUS_OPEN",
    "price": "65000.00",
    "quantity": "0.01000000",
    "filled_quantity": "0.00000000",
    "created_at": "2026-09-26T14:10:00Z",
    "updated_at": "2026-09-26T14:10:00Z"
  }
  ```

#### `GET /api/v1/orders`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Retrieves a list of orders placed by the authenticated user, optionally filtered by market, status, and paginated by limit.
* **Query Parameters:**
  - `market_id` (string, optional): e.g. `BTC-USDT`
  - `status` (string, optional): Uppercase string (`OPEN`, `PARTIALLY_FILLED`, `FILLED`, `CANCELLING`, `CANCELLED`)
  - `limit` (int, optional): Max orders to return (default: `50`)
* **Response:** `200 OK`
  ```json
  {
    "orders": [
      {
        "id": "01923e20-7b3c-70e1-b441-df0a8f89e1a2",
        "user_id": "01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
        "market_id": "BTC-USDT",
        "side": "ORDER_SIDE_BUY",
        "order_type": "ORDER_TYPE_LIMIT",
        "status": "ORDER_STATUS_OPEN",
        "price": "65000.00",
        "quantity": "0.01000000",
        "filled_quantity": "0.00000000",
        "created_at": "2026-09-26T14:10:00Z",
        "updated_at": "2026-09-26T14:10:00Z"
      }
    ]
  }
  ```

#### `GET /api/v1/orders/{id}`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Retrieves detailed information for a single order by its ID. Enforces ownership check (returns 404 if not found or unauthorized).
* **Path Parameters:**
  - `id` (string, required): Order UUID
* **Response:** `200 OK`
  ```json
  {
    "id": "01923e20-7b3c-70e1-b441-df0a8f89e1a2",
    "user_id": "01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
    "market_id": "BTC-USDT",
    "side": "ORDER_SIDE_BUY",
    "order_type": "ORDER_TYPE_LIMIT",
    "status": "ORDER_STATUS_OPEN",
    "price": "65000.00",
    "quantity": "0.01000000",
    "filled_quantity": "0.00000000",
    "created_at": "2026-09-26T14:10:00Z",
    "updated_at": "2026-09-26T14:10:00Z"
  }
  ```

#### `POST /api/v1/orders/{id}/cancel`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Requests cancellation of an open or partially filled order. Emits a cancellation event to the Matching Engine and marks status as `ORDER_STATUS_CANCELLING` or `ORDER_STATUS_CANCELLED`.
* **Path Parameters:**
  - `id` (string, required): Order UUID
* **Response:** `200 OK`
  ```json
  {
    "id": "01923e20-7b3c-70e1-b441-df0a8f89e1a2",
    "user_id": "01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
    "market_id": "BTC-USDT",
    "side": "ORDER_SIDE_BUY",
    "order_type": "ORDER_TYPE_LIMIT",
    "status": "ORDER_STATUS_CANCELLING",
    "price": "65000.00",
    "quantity": "0.01000000",
    "filled_quantity": "0.00000000",
    "created_at": "2026-09-26T14:10:00Z",
    "updated_at": "2026-09-26T14:12:00Z"
  }
  ```

---

### 📊 Markets

#### `GET /api/v1/markets`
* **Access:** Public
* **Explanation:** Returns all active trading pairs and their technical constraints (`tick_size`, `lot_size`, `min_quantity`, `status`).
* **Response:** `200 OK`
  ```json
  {
    "markets": [
      {
        "id": "BTC-USDT",
        "base_asset": "BTC",
        "quote_asset": "USDT",
        "tick_size": "0.01",
        "lot_size": "0.0001",
        "status": "MARKET_STATUS_ACTIVE",
        "min_quantity": "0.0001",
        "created_at": "2026-09-01T00:00:00Z",
        "updated_at": "2026-09-26T14:00:00Z"
      }
    ]
  }
  ```

#### `GET /api/v1/markets/overview`
* **Access:** Public
* **Explanation:** Dashboard-ready aggregated view of all markets, enriched with 24h rolling price stats, 24h volume, and an array of recent close prices (`trend`) for sparklines.
* **Query Parameters:**
  - `resolution` (string, optional): Resolution of trend candles (`1m`, `5m`, `15m`, `1h`, `4h`, `1d`; default `1h`)
  - `limit` (int, optional): Number of trend points per market (default: `168`, max: `500`)
* **Response:** `200 OK`
  ```json
  {
    "markets": [
      {
        "market_id": "BTC-USDT",
        "symbol": "BTC/USDT",
        "base_asset": "BTC",
        "quote_asset": "USDT",
        "last_price": "65240.00",
        "high_24h": "66100.00",
        "low_24h": "64500.00",
        "volume_24h": "142.35",
        "quote_volume_24h": "9287310.00",
        "price_change_24h_percent": "1.15",
        "trend": ["64800.00", "65000.00", "65120.00", "65240.00"]
      }
    ]
  }
  ```

#### `GET /api/v1/markets/{id}`
* **Access:** Public
* **Explanation:** Fetches configuration and trading rules for a single market specified by ID.
* **Path Parameters:**
  - `id` (string, required): e.g. `BTC-USDT`
* **Response:** `200 OK`
  ```json
  {
    "id": "BTC-USDT",
    "base_asset": "BTC",
    "quote_asset": "USDT",
    "tick_size": "0.01",
    "lot_size": "0.0001",
    "status": "MARKET_STATUS_ACTIVE",
    "min_quantity": "0.0001",
    "created_at": "2026-09-01T00:00:00Z",
    "updated_at": "2026-09-26T14:00:00Z"
  }
  ```

#### `GET /api/v1/markets/{id}/ticker`
* **Access:** Public
* **Explanation:** Returns real-time 24h rolling price statistics, volume, and percentage change for a market.
* **Path Parameters:**
  - `id` (string, required): e.g. `BTC-USDT`
* **Response:** `200 OK`
  ```json
  {
    "market_id": "BTC-USDT",
    "last_price": "65240.00",
    "high_24h": "66100.00",
    "low_24h": "64500.00",
    "volume_24h": "142.35",
    "quote_volume_24h": "9287310.00",
    "price_change_24h_percent": "1.15"
  }
  ```

#### `GET /api/v1/markets/{id}/candles`
* **Access:** Public
* **Explanation:** Retrieves historical OHLCV candlestick bars for charting widgets (e.g. Lightweight Charts, TradingView).
* **Path Parameters:**
  - `id` (string, required): e.g. `BTC-USDT`
* **Query Parameters:**
  - `resolution` (string, optional): `1m`, `5m`, `15m`, `1h`, `4h`, `1d` (default: `1h`)
  - `limit` (int, optional): Number of candles (default: `100`, max: `500`)
  - `from` (string, optional): RFC3339 start timestamp
  - `to` (string, optional): RFC3339 end timestamp
* **Response:** `200 OK`
  ```json
  {
    "candles": [
      {
        "start_time": "2026-09-26T13:00:00Z",
        "open": "64950.00",
        "high": "65300.00",
        "low": "64900.00",
        "close": "65240.00",
        "volume": "18.52",
        "quote_volume": "1205620.00"
      }
    ]
  }
  ```

#### `GET /api/v1/markets/{id}/trades`
* **Access:** Public
* **Explanation:** Public market trade tape (time & sales). Anonymized—buyer and seller user IDs are stripped out.
* **Path Parameters:**
  - `id` (string, required): e.g. `BTC-USDT`
* **Query Parameters:**
  - `cursor` (string, optional): Keyset cursor from previous page
  - `limit` (int, optional): Max trades to return (default: `50`, max: `200`)
* **Response:** `200 OK`
  ```json
  {
    "trades": [
      {
        "id": "01923e25-8c01-7000-8812-4521bcde3344",
        "market_id": "BTC-USDT",
        "base_asset": "BTC",
        "quote_asset": "USDT",
        "price": "65240.00",
        "quantity": "0.02500000",
        "executed_at": "2026-09-26T14:14:22.124562Z"
      }
    ],
    "next_cursor": "eyJleGVjdXRlZF9hdCI6IjIwMjYtMDktMjZUMTQ6MTQ6MjIuMTI0NTYyWiIsImlkIjoiMDE5MjNlMjUtOGMwMS03MDAwLTg4MTItNDUyMWJjZGUzMzQ0In0="
  }
  ```

---

### 🤝 Trades

#### `GET /api/v1/trades`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Retrieves the authenticated user's private trade/fill execution history, sorted newest-first.
* **Query Parameters:**
  - `market_id` (string, optional): Restrict to a specific pair
  - `cursor` (string, optional): Keyset pagination cursor
  - `limit` (int, optional): Default `20`, max `100`
* **Response:** `200 OK`
  ```json
  {
    "trades": [
      {
        "id": "01923e25-8c01-7000-8812-4521bcde3344",
        "buyer_id": "01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
        "seller_id": "01923e10-12a4-7f31-9002-1133557799aa",
        "buy_order_id": "01923e20-7b3c-70e1-b441-df0a8f89e1a2",
        "sell_order_id": "01923e19-90ef-7123-aa32-889911223344",
        "market_id": "BTC-USDT",
        "base_asset": "BTC",
        "quote_asset": "USDT",
        "price": "65240.00",
        "quantity": "0.02500000",
        "executed_at": "2026-09-26T14:14:22.124562Z",
        "settled_at": "2026-09-26T14:14:22.180231Z"
      }
    ],
    "next_cursor": "..."
  }
  ```

#### `GET /api/v1/trades/{id}`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Retrieves a single trade record. Server enforces privacy: caller must be either the `buyer_id` or `seller_id` (returns `403 Forbidden` otherwise).
* **Path Parameters:**
  - `id` (string, required): Trade UUID
* **Response:** `200 OK`
  ```json
  {
    "id": "01923e25-8c01-7000-8812-4521bcde3344",
    "buyer_id": "01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
    "seller_id": "01923e10-12a4-7f31-9002-1133557799aa",
    "buy_order_id": "01923e20-7b3c-70e1-b441-df0a8f89e1a2",
    "sell_order_id": "01923e19-90ef-7123-aa32-889911223344",
    "market_id": "BTC-USDT",
    "base_asset": "BTC",
    "quote_asset": "USDT",
    "price": "65240.00",
    "quantity": "0.02500000",
    "executed_at": "2026-09-26T14:14:22.124562Z",
    "settled_at": "2026-09-26T14:14:22.180231Z"
  }
  ```

---

### 📈 Portfolio

#### `GET /api/v1/portfolio/summary`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Dynamically calculates total portfolio valuation, realized PnL, unrealized PnL based on live prices, and available cash balance.
* **Response:** `200 OK`
  ```json
  {
    "userId": "01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
    "totalValue": "15420.50",
    "realizedPnl": "240.00",
    "unrealizedPnl": "680.50",
    "cashBalance": "4500.00",
    "updatedAt": "2026-09-26T14:15:00Z"
  }
  ```

#### `GET /api/v1/portfolio/holdings`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Provides a detailed breakdown of current crypto asset holdings, including volume-weighted average entry price, current market price, and unrealized PnL per asset.
* **Response:** `200 OK`
  ```json
  {
    "userId": "01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
    "holdings": [
      {
        "asset": "BTC",
        "totalQuantity": "0.15000000",
        "averageEntryPrice": "62000.00",
        "currentPrice": "65240.00",
        "unrealizedPnl": "486.00"
      },
      {
        "asset": "ETH",
        "totalQuantity": "1.50000000",
        "averageEntryPrice": "3400.00",
        "currentPrice": "3530.00",
        "unrealizedPnl": "195.00"
      }
    ]
  }
  ```

---

### 🔔 Notifications

#### `GET /api/v1/notifications`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Fetches user alerts and notifications with keyset cursor pagination.
* **Query Parameters:**
  - `limit` (int, optional): Default `20`, max `100`
  - `cursor_time` (string, optional): RFC3339 creation timestamp cursor
  - `cursor_id` (string, optional): Notification ID tiebreaker (must be supplied together with `cursor_time`)
  - `type` (string, optional): Filter by notification type (`INFO`, `TRADE_FILL`, `SYSTEM`, `ACCOUNT`)
* **Response:** `200 OK`
  ```json
  {
    "notifications": [
      {
        "id": "01923e22-1111-7000-8800-001122334455",
        "userId": "01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
        "title": "Order Filled",
        "message": "Your buy order for 0.01000000 BTC has been filled at 65,240.00 USDT.",
        "type": "TRADE_FILL",
        "referenceId": "01923e20-7b3c-70e1-b441-df0a8f89e1a2",
        "referenceType": "ORDER",
        "isRead": false,
        "readAt": "",
        "createdAt": "2026-09-26T14:14:22Z"
      }
    ],
    "nextCursorTime": "2026-09-26T14:14:22Z",
    "nextCursorId": "01923e22-1111-7000-8800-001122334455",
    "hasMore": false,
    "unreadCount": 1
  }
  ```

#### `POST /api/v1/notifications/{id}/read`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Marks a specific notification as read.
* **Path Parameters:**
  - `id` (string, required): Notification UUID
* **Response:** `200 OK`
  ```json
  {
    "notificationId": "01923e22-1111-7000-8800-001122334455",
    "isRead": true,
    "readAt": "2026-09-26T14:16:00Z"
  }
  ```

#### `POST /api/v1/notifications/read-all`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Marks all pending unread notifications as read in bulk for the authenticated user.
* **Request Body:** None
* **Response:** `200 OK`
  ```json
  {
    "markedCount": 5
  }
  ```

#### `GET /api/v1/notifications/unread-count`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Returns the total count of unread notifications for badge rendering in the frontend navbar/header.
* **Response:** `200 OK`
  ```json
  {
    "unreadCount": 5
  }
  ```

---

## 2. 🔌 API Gateway (WebSocket Protocol)

**Endpoint:** `GET /ws`  
**Connection URL:**  
- Public connection: `ws://localhost:8080/ws`  
- Authenticated connection: `ws://localhost:8080/ws?token=<accessToken>` *(or HTTP header `Authorization: Bearer <accessToken>` during upgrade handshake)*

> ⚠️ **Protocol Rules:**
> - There is **no in-band `{ "action": "auth" }` message**. Auth must occur during the HTTP upgrade handshake.
> - Streams are strictly colon-delimited (`market:<type>:<id>` or `user:<type>:<UUID>`).
> - The client subscription frame uses `event` (not `action`) and `streams` array (not `stream` string).

### Client Inbound Frames (Client → Server)

#### 1. Subscribe to Streams
```json
{
  "event": "subscribe",
  "streams": [
    "market:orderbook:BTC-USDT",
    "market:ticker:BTC-USDT",
    "market:trades:BTC-USDT",
    "user:notifications:01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
    "user:portfolio:01923e1f-45b0-7d12-8e43-9821a2c3b4d5"
  ]
}
```

#### 2. Unsubscribe from Streams
```json
{
  "event": "unsubscribe",
  "streams": [
    "market:orderbook:BTC-USDT"
  ]
}
```

#### 3. Client Ping (Heartbeat / Latency probe)
```json
{
  "event": "ping"
}
```

---

### Server Outbound Envelopes (Server → Client)

All streaming updates are wrapped in an envelope containing `stream` and `data`:

```json
{
  "stream": "<stream_name>",
  "data": { ... }
}
```

#### Stream 1: `market:orderbook:<market_id>` (L2 Depth Snapshot / Update)
```json
{
  "stream": "market:orderbook:BTC-USDT",
  "data": {
    "marketId": "BTC-USDT",
    "bids": [
      ["65240.00", "0.45000000"],
      ["65230.00", "1.20000000"]
    ],
    "asks": [
      ["65250.00", "0.30000000"],
      ["65260.00", "0.80000000"]
    ],
    "sequence": 104523,
    "timestamp": 1727360062000
  }
}
```

#### Stream 2: `market:trades:<market_id>` (Public Executed Trade Fill)
```json
{
  "stream": "market:trades:BTC-USDT",
  "data": {
    "tradeId": "01923e25-8c01-7000-8812-4521bcde3344",
    "marketId": "BTC-USDT",
    "price": "65240.00",
    "quantity": "0.02500000",
    "side": "BUY",
    "sequence": 104524,
    "executedAt": 1727360062124
  }
}
```

#### Stream 3: `market:ticker:<market_id>` (24h Rolling Ticker Stats)
```json
{
  "stream": "market:ticker:BTC-USDT",
  "data": {
    "marketId": "BTC-USDT",
    "lastPrice": "65240.00",
    "high24h": "66100.00",
    "low24h": "64500.00",
    "volume24h": "142.35",
    "quoteVolume24h": "9287310.00",
    "priceChange24hPercent": "1.15",
    "timestamp": 1727360062000
  }
}
```

#### Stream 4: `user:notifications:<user_uuid>` (Private User Alert)
```json
{
  "stream": "user:notifications:01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
  "data": {
    "id": "01923e22-1111-7000-8800-001122334455",
    "userId": "01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
    "title": "Order Filled",
    "message": "Your limit buy order was filled at 65,240.00 USDT",
    "type": "TRADE_FILL",
    "createdAt": 1727360062124
  }
}
```

#### Server Control Frames
- Pong reply to client ping:
  ```json
  { "event": "pong" }
  ```
- Stream validation or permission error:
  ```json
  {
    "event": "error",
    "code": "STREAM_VALIDATION_FAILED",
    "message": "unauthorized stream access"
  }
  ```

---

## 3. 💳 Wallet Top-Up Service (Direct HTTP)

**Base URL:** `http://localhost:8091` *(Direct port, NOT routed through API Gateway)*  
**Authentication Header:** `Authorization: Bearer <accessToken>`

#### `GET /health`
* **Access:** Public
* **Explanation:** Liveness and readiness probe for the top-up service.
* **Response:** `200 OK`
  ```json
  {
    "ok": true
  }
  ```

#### `POST /api/v1/topups`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Initiates an INR fiat deposit order. Calculates equivalent USDT amount based on current exchange rate, checks daily limit, and generates a payment provider transaction.
* **Mandatory Headers:**
  - `Authorization: Bearer <accessToken>`
  - `X-Idempotency-Key: <uuid>` *(Required! Prevents double charging)*
* **Request Body:**
  ```json
  {
    "inrAmount": 100000
  }
  ```
  *(Note: Accepts both `inrAmount` and `inr_amount`)*
* **Response:** `201 Created`
  ```json
  {
    "topupId": "01923e30-abcd-7000-8800-010203040506",
    "userId": "01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
    "inrAmount": 100000,
    "usdtAmount": "100.00000000",
    "exchangeRate": 1000,
    "status": "INITIATED",
    "provider": "MOCK",
    "providerOrderId": "mock_order_1001",
    "expiresAt": "2026-09-26T14:40:00Z",
    "createdAt": "2026-09-26T14:10:00Z"
  }
  ```

#### `GET /api/v1/topups/daily-usage`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Fetches the authenticated user's daily top-up limit utilization, reserved amounts, consumed amounts, remaining headroom, and reset timestamp.
* **Response:** `200 OK`
  ```json
  {
    "userId": "01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
    "usageDate": "2026-09-26",
    "limitInr": 1000000,
    "reservedInr": 100000,
    "consumedInr": 200000,
    "remainingInr": 700000,
    "resetsAt": "2026-09-27T00:00:00Z"
  }
  ```

#### `GET /api/v1/topups/{id}`
* **Access:** Protected (Bearer JWT)
* **Explanation:** Retrieves the real-time status (`INITIATED`, `PAYMENT_CONFIRMED`, `COMPLETED`, `FAILED`) of an existing top-up order.
* **Path Parameters:**
  - `id` (string, required): TopUp UUID
* **Response:** `200 OK`
  ```json
  {
    "topupId": "01923e30-abcd-7000-8800-010203040506",
    "userId": "01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
    "inrAmount": 100000,
    "usdtAmount": "100.00000000",
    "exchangeRate": 1000,
    "status": "COMPLETED",
    "provider": "MOCK",
    "providerOrderId": "mock_order_1001",
    "expiresAt": "2026-09-26T14:40:00Z",
    "createdAt": "2026-09-26T14:10:00Z"
  }
  ```

#### `POST /api/v1/webhooks/payment/{provider}`
* **Access:** Webhook Signature Verification
* **Explanation:** Asynchronous callback endpoint for external payment providers (Razorpay, mock providers). Confirms settlement and automatically triggers Wallet deposit.

---

## 4. 🛡️ Admin Service (Direct HTTP)

**Base URL:** `http://localhost:8090` *(Direct port, NOT routed through API Gateway)*  
**Authentication Header:** `Authorization: Bearer <adminJwt>` *(Must contain `role: admin` claim)*  
**Idempotency Header:** All mutation endpoints require `Idempotency-Key: <uuid>`.

### Monitoring & System Health
* `GET /health` — Liveness probe (`{"status":"ok"}`)
* `GET /ready` — Readiness check verifying DB and gRPC dependencies
* `GET /metrics` — Prometheus metrics scrape endpoint
* `GET /api/v1/admin/system/health` — Full diagnostic report across all 13 TradeDrift microservices

### Admin Mutations
All mutation endpoints accept a JSON body `{"reason": "Audit justification"}` and return an `OperationResponseDTO`:

```json
{
  "operation_id": "01923e40-bbbb-7000-9999-1234567890ab",
  "admin_id": "01923e00-0000-7000-0000-admin0000001",
  "operation_type": "USER_SUSPENSION",
  "target_id": "01923e1f-45b0-7d12-8e43-9821a2c3b4d5",
  "status": "COMPLETED",
  "created_at": "2026-09-26T14:20:00Z"
}
```

* `POST /api/v1/admin/users/{user_id}/suspend` — Suspends user account and instantly revokes all active JWT sessions.
* `POST /api/v1/admin/users/{user_id}/unsuspend` — Re-enables a suspended user.
* `POST /api/v1/admin/users/{user_id}/wallets/{asset}/freeze` — Locks a specific asset wallet for a user (e.g., fraud prevention).
* `POST /api/v1/admin/users/{user_id}/wallets/{asset}/unfreeze` — Unlocks a frozen asset wallet.
* `POST /api/v1/admin/markets/{market_id}/halt` — Halts trading on a market (circuit breaker). Rejects new orders.
* `POST /api/v1/admin/markets/{market_id}/resume` — Resumes normal matching on a halted market.

### Incident & Intelligence Endpoints
* `GET /api/v1/admin/incidents` — Lists system and risk incidents.
* `GET /api/v1/admin/incidents/stats` — Summary counts by severity and status.
* `GET /api/v1/admin/incidents/{incident_id}` — Details for a specific incident.
* `GET /api/v1/admin/incidents/{incident_id}/correlated` — Returns related events sharing correlation tokens.
* `POST /api/v1/admin/incidents/{incident_id}/resolve` — Resolves an incident.
* `GET /api/v1/admin/analytics/overview` — Operational metrics (volume, fees, active traders).
* `GET /api/v1/admin/analytics/risk-signals` — Anomaly detection and wash trading risk flags.
* `GET /api/v1/admin/topology` — Real-time microservice dependency graph status.

---

## 5. ⚙️ Internal Microservices (gRPC & Event Engines)

*These services are internal to the cluster. The frontend does not call them directly.*

### 5.1 Auth Service (gRPC)
* **Port:** `:50051` | **Proto:** `tradedrift.auth.v1`
* **Methods:**
  - `Register` — Inserts user in `PENDING_VERIFICATION` state.
  - `VerifyEmail` — Verifies OTP, activates user, returns JWT tokens.
  - `ResendVerificationCode` — Dispatches a new OTP.
  - `Login` — Verifies credentials, returns JWT tokens.
  - `RefreshToken` — Issues fresh token pair.
  - `ForgotPassword` — Sends password reset code.
  - `ResetPassword` — Changes password and revokes previous tokens.
  - `Logout` — Invalidates current session in Redis.
  - `LogoutAll` — Invalidates all sessions for user.
  - `ChangePassword` — Updates password with existing authentication.
  - `InvalidateUserSessions` — Internal method called by Admin service during suspension.
  - `SuspendUser` / `UnsuspendUser` — Internal status toggle.
  - `ListSuspendedUsers` — Internal audit reconciliation.

### 5.2 Wallet Service (gRPC)
* **Port:** `:50052` | **Proto:** `tradedrift.wallet.v1`
* **Methods:**
  - `GetSupportedAssets` — Lists tradeable assets and decimal configurations.
  - `GetBalance` / `GetBalances` — Returns available and reserved funds.
  - `InitializeWallet` — Provisions wallets with seed balances for new accounts.
  - `ReserveFunds` — Locks funds when an order is placed.
  - `ReleaseFunds` — Unlocks reserved funds on order cancellation.
  - `SettleTrade` — Atomically moves funds between buyer and seller.
  - `DepositFunds` — Credits wallet upon fiat top-up webhook verification.
  - `FreezeWallet` — Internal admin control for asset freezing.
  - `Health` — gRPC health check.

### 5.3 Order Service (gRPC)
* **Port:** `:50053` | **Proto:** `tradedrift.order.v1`
* **Methods:**
  - `CreateOrder` — Coordinates fund reservation with Wallet, persists order, publishes to Kafka.
  - `CancelOrder` — Emits cancellation command to Kafka.
  - `GetOrder` — Returns single order state.
  - `ListOrders` — Returns order history with filtering.
  - `CancelAllOrders` — Mass cancellation stub (reserved for market halt saga).

### 5.4 Market Service (gRPC)
* **Port:** `:50054` | **Proto:** `tradedrift.market.v1`
* **Methods:**
  - `ListMarkets` — Returns active market pairs.
  - `GetMarket` — Single market metadata.
  - `GetTicker` — 24h rolling price, high, low, volume statistics.
  - `GetCandles` — OHLCV candlestick aggregation.
  - `GetMarketsOverview` — Batch markets overview with trend sparklines.

### 5.5 Trade Service (gRPC)
* **Port:** `:50057` | **Proto:** `tradedrift.trade.v1`
* **Methods:**
  - `GetTrade` — Fetches trade details with party ownership authorization.
  - `ListUserTrades` — Paginated user fill history.
  - `ListMarketTrades` — Public market trade tape (anonymized).

### 5.6 Portfolio Service (gRPC)
* **Port:** `:50058` | **Proto:** `tradedrift.portfolio.v1`
* **Methods:**
  - `GetPortfolioSummary` — Computes net equity, cash balance, and realized/unrealized PnL.
  - `GetPortfolioHoldings` — Computes per-asset weighted average cost and PnL.

### 5.7 Notification Service (gRPC)
* **Port:** `:50059` | **Proto:** `tradedrift.notification.v1`
* **Methods:**
  - `CreateNotification` — Internal dispatch for system, trade, and account alerts.
  - `GetNotifications` — Keyset-paginated alert history.
  - `MarkAsRead` / `MarkAllAsRead` — Read state toggles.
  - `GetUnreadCount` — Unread count for badge indicators.

### 5.8 Matching Engine (Kafka / Redis)
* **Interface:** Event-driven Kafka consumer/producer + Redis writer (no HTTP/gRPC API).
* **Kafka Consumed:** `orders.commands` (partition key = `MarketID`, events: `order.created`, `order.cancel`).
* **Kafka Produced:** `trades.executed`, `orders.cancelled.v1`.
* **Redis Written:** L2 depth snapshots written directly to Redis (`orderbook:depth:<market_id>`), which the API Gateway streams over WebSocket.

### 5.9 Settlement Service (Kafka / gRPC)
* **Interface:** Event-driven Kafka consumer.
* **Kafka Consumed:** `trades.executed`.
* **Downstream Calls:** Calls `WalletService.SettleTrade` over gRPC to settle cash and asset balances between counter-parties.

### 5.10 Liquidity Engine (Internal MM)
* **Interface:** Autonomous background worker.
* **Downstream Calls:** Calls `OrderService.CreateOrder`, `OrderService.CancelOrder`, and `WalletService` to maintain two-sided liquidity and order book depth.

---

## 6. 📋 Summary Matrix

| # | Service | Interface | Port | Frontend Access Point | Description |
|---|---------|-----------|------|-----------------------|-------------|
| 1 | **API Gateway** | HTTP REST & WS | `:8080` | **Primary Entrypoint** | Single unified door for REST APIs & real-time WebSocket feeds |
| 2 | **Auth Service** | gRPC | `:50051` | Via Gateway (`/api/v1/auth/*`) | Identity, password hashes, OTP verification & session tokens |
| 3 | **Wallet Service** | gRPC | `:50052` | Via Gateway (`/api/v1/wallet/*`) | Double-entry ledger, reservations, balances, asset metadata |
| 4 | **Order Service** | gRPC | `:50053` | Via Gateway (`/api/v1/orders/*`) | Order validation, placement, lifecycle, and history |
| 5 | **Market Service** | gRPC | `:50054` | Via Gateway (`/api/v1/markets/*`) | Trading pair rules, 24h ticker stats, and OHLCV candles |
| 6 | **Trade Service** | gRPC | `:50057` | Via Gateway (`/api/v1/trades/*`) | Executed trade records, user fill history, and public trade tape |
| 7 | **Portfolio Service** | gRPC | `:50058` | Via Gateway (`/api/v1/portfolio/*`) | Real-time valuation, holdings breakdown, realized & unrealized PnL |
| 8 | **Notification Service** | gRPC | `:50059` | Via Gateway (`/api/v1/notifications/*`) | In-app user notifications and trade alerts |
| 9 | **Matching Engine** | Kafka / Redis | — | Via Gateway WS | Ultra-low latency deterministic in-memory order matching book |
| 10 | **Settlement Service** | Kafka / gRPC | — | None (Internal) | Consumes matched trades and triggers atomic wallet settlements |
| 11 | **Liquidity Engine** | gRPC / Kafka | — | None (Internal) | Automated market maker placing bid/ask liquidity orders |
| 12 | **Wallet Top-Up** | HTTP REST | `:8091` | **Direct HTTP** | INR fiat payment gateway integration, daily limits, and credit |
| 13 | **Admin Service** | HTTP REST | `:8090` | **Direct HTTP (Admin Only)** | Account suspension, wallet freeze, market halts, and incident analytics |
