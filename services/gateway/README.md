# TradeDrift — API Gateway (`services/gateway`)

> **Service:** API Gateway  
> **Directory:** `services/gateway/`  
> **HTTP Port:** `:8080`  
> **Role:** Edge Reverse Proxy, REST to gRPC Translation, JWT Auth Interception, & Rate Limiting

---

## 1. Executive Summary & Purpose

The **API Gateway** is the single entrypoint for external HTTP REST clients (web UI, mobile apps, trading bots). It translates HTTP JSON requests into high-performance gRPC calls to internal backend microservices (**Auth**, **Wallet**, **Order**, and **Market**).

### Key Responsibilities:
1. **HTTP REST to gRPC Translation**: Converts incoming JSON REST endpoints into internal gRPC calls over HTTP/2.
2. **JWT Authentication & Claims Context Injection**: Validates JWT Bearer tokens and injects authenticated `user_id` into request contexts.
3. **Token Bucket Rate Limiting**: Protects downstream microservices against DDoS and brute-force attacks.
4. **CORS & Response Standardizer**: Handles cross-origin requests and wraps all HTTP responses in a unified `{ "success": true, "data": ... }` schema.
5. **Distributed Request Tracing**: Extracts or generates `x-request-id` UUIDs and propagates them to downstream gRPC services via metadata.

---

## 2. Directory Structure Map

```
services/gateway/
├── README.md                            <-- Main Gateway architecture documentation
├── Dockerfile                           <-- Container deployment definition
├── go.mod                               <-- Go module definition
├── go.sum                               <-- Dependency checksums
├── cmd/
│   └── server/
│       └── main.go                      <-- HTTP server entrypoint, gRPC dialing & router wiring
└── internal/
    ├── handler/                         <-- Domain-specific HTTP route handlers & DTOs
    │   ├── 01README.md
    │   ├── common/                      <-- Shared context tracing, errors & timestamp utils
    │   │   ├── context.go
    │   │   ├── dto.go
    │   │   ├── errors.go
    │   │   └── README.md
    │   ├── auth/                        <-- Auth REST endpoints & DTOs
    │   │   ├── handler.go
    │   │   ├── dto.go
    │   │   └── README.md
    │   ├── wallet/                      <-- Wallet REST endpoints & DTOs
    │   │   ├── handler.go
    │   │   ├── dto.go
    │   │   └── README.md
    │   ├── order/                       <-- Order REST endpoints & DTOs
    │   │   ├── handler.go
    │   │   ├── dto.go
    │   │   └── README.md
    │   └── market/                      <-- Market REST endpoints & DTOs
    │       ├── handler.go
    │       ├── dto.go
    │       └── README.md
    ├── middleware/                      <-- HTTP middleware pipeline
    │   ├── auth.go                      <-- JWT HMAC-SHA256 authentication middleware
    │   ├── cors.go                      <-- CORS configuration
    │   ├── logger.go                    <-- Structured request/response logging
    │   ├── rate_limit.go                <-- Token bucket rate limiting implementation
    │   ├── recovery.go                  <-- Panic recovery middleware
    │   └── request_id.go                <-- Request ID tracing middleware
    └── response/                        <-- Standardized JSON response formatters
```

---

## 3. Endpoints & Route Table

### 🔐 Authentication (`/api/v1/auth`)
* `POST /api/v1/auth/register` — Register a new account (Public)
* `POST /api/v1/auth/verify` — Verify email OTP (Public)
* `POST /api/v1/auth/resend` — Resend verification OTP (Public)
* `POST /api/v1/auth/login` — Authenticate and receive JWT token pair (Public)
* `POST /api/v1/auth/refresh` — Rotate refresh token (Public)
* `POST /api/v1/auth/forgot-password` — Request password reset code (Public)
* `POST /api/v1/auth/reset-password` — Set new password using reset code (Public)
* `POST /api/v1/auth/logout` — Revoke active session (🔒 Protected)
* `POST /api/v1/auth/logout-all` — Revoke all user sessions (🔒 Protected)
* `POST /api/v1/auth/change-password` — Update password while logged in (🔒 Protected)

### 💰 Wallet (`/api/v1/wallet`)
* `GET /api/v1/wallet/assets` — List all supported exchange currencies (Public)
* `GET /api/v1/wallet/balances` — Get all asset balances for authenticated user (🔒 Protected)
* `GET /api/v1/wallet/balances/{asset}` — Get balance for a specific asset (🔒 Protected)

### 📈 Orders (`/api/v1/orders`)
* `POST /api/v1/orders` — Place a new limit or market order (🔒 Protected)
* `GET /api/v1/orders` — List order history filtered by market & status (🔒 Protected)
* `GET /api/v1/orders/{id}` — Get single order fill status (🔒 Protected)
* `POST /api/v1/orders/{id}/cancel` — Cancel an open order (🔒 Protected)

### 📊 Markets (`/api/v1/markets`)
* `GET /api/v1/markets` — List all active trading pairs and rules (Public)
* `GET /api/v1/markets/{id}` — Get single market details (Public)
* `GET /api/v1/markets/{id}/ticker` — Get live 24h rolling price & volume statistics (Public)
* `GET /api/v1/markets/{id}/candles` — Get historical OHLC candlestick bars (Public)

---

## 4. Configuration Environment Variables

| Variable | Default | Description |
| :--- | :--- | :--- |
| `GATEWAY_PORT` | `:8080` | HTTP port the gateway listens on |
| `AUTH_ADDR` | `localhost:50051` | gRPC address of the Authentication microservice |
| `WALLET_ADDR` | `localhost:50052` | gRPC address of the Wallet microservice |
| `ORDER_ADDR` | `localhost:50053` | gRPC address of the Order microservice |
| `MARKET_ADDR` | `localhost:50054` | gRPC address of the Market microservice |
| `JWT_SECRET` | *(required)* | 32-byte secret key used to verify HMAC-SHA256 JWT tokens |
| `CORS_ORIGIN` | `http://localhost:5173` | Allowed frontend origin for CORS headers |
| `LOG_LEVEL` | `info` | Logging verbosity (`debug`, `info`, `warn`, `error`) |

---

## 5. How to Run Locally

```powershell
cd services/gateway
go run cmd/server/main.go
```
