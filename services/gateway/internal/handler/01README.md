# API Gateway — REST Handler Subpackages (`internal/handler`)

> **Directory:** `services/gateway/internal/handler/`  
> **Role:** Domain-specific HTTP REST Handlers, Reverse Proxy Mappers & Data Transfer Objects (DTOs)

---

## 1. Overview

The `handler` package serves as the reverse proxy presentation layer of TradeDrift. It receives public and protected HTTP/JSON requests from browsers, mobile apps, and algorithmic traders, validates incoming payloads, maps them to downstream gRPC microservice calls, and formats standardized JSON responses.

---

## 2. Directory Structure

```
services/gateway/internal/handler/
├── common/             <-- Shared context tracing, timestamp formatting & gRPC error mapping
│   ├── context.go
│   ├── dto.go
│   ├── errors.go
│   └── README.md
├── auth/               <-- User registration, email verification, session management & JWT rotation
│   ├── handler.go
│   ├── dto.go
│   └── README.md
├── wallet/             <-- Asset catalogs & user balances
│   ├── handler.go
│   ├── dto.go
│   └── README.md
├── order/              <-- Order creation, fill tracking, history & cancellation
│   ├── handler.go
│   ├── dto.go
│   └── README.md
└── market/             <-- Trading pair specifications, 24h tickers & OHLC candlestick charts
    ├── handler.go
    ├── dto.go
    └── README.md
```

---

## 3. Global Gateway Middleware Architecture

All incoming requests traverse a centralized middleware pipeline before reaching domain handlers:

```
Incoming HTTP Request
        │
        ▼
1. RequestID Middleware       (Assigns or extracts x-request-id UUID)
        │
        ▼
2. Logger Middleware          (Structured request/response latency logging)
        │
        ▼
3. Recovery Middleware        (Traps panics and returns clean 500 JSON)
        │
        ▼
4. CORS Middleware            (Validates allowed web origins)
        │
        ▼
5. RateLimiter Middleware     (Token bucket rate-limiting per client IP)
        │
        ▼
6. Auth Middleware            (Validates JWT on protected routes, injects user_id)
        │
        ▼
Domain Handler (`auth`, `wallet`, `order`, `market`)
        │
        ▼
gRPC Call to Microservice (via common.OutgoingCtx with x-request-id)
```
