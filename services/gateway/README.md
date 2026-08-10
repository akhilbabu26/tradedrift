# TradeDrift — API Gateway (`services/gateway`)

> **Service:** API Gateway  
> **Directory:** `services/gateway/`  
> **HTTP Port:** `:8080`  
> **Role:** Edge Reverse Proxy, REST to gRPC Translation, JWT Auth Interception, & Rate Limiting

---

## 1. Executive Summary & Purpose

The **API Gateway** is the single entrypoint for external HTTP REST clients (web UI, mobile apps, trading bots). It translates HTTP JSON requests into high-performance gRPC calls to internal backend microservices (Auth Service, Wallet Service, Order Service).

### Key Responsibilities:
1. **HTTP REST to gRPC Translation**: Converts incoming JSON REST endpoints into internal gRPC calls over HTTP/2.
2. **JWT Authentication & Claims Context Injection**: Validates JWT Bearer tokens and injects authenticated `user_id` into request contexts.
3. **Rate Limiting & Security**: Protects backend services against DDoS attacks with token bucket rate limiters.
4. **CORS & Response Standardizer**: Handles cross-origin requests and wraps all HTTP responses in a unified `{ "success": true, "data": ... }` schema.

---

## 2. Directory Structure Map

```
services/gateway/
├── README.md                            <-- Main Gateway architecture documentation
├── Dockerfile                           <-- Container deployment definition
├── go.mod                               <-- Go module definition
├── go.sum                               <-- Dependency checksums
├── cmd/
│   ├── README.md                        <-- Server binary documentation
│   └── server/
│       └── main.go                      <-- HTTP server entrypoint & router initialization
└── internal/
    ├── handler/                         <-- HTTP route handlers & DTOs
    │   ├── README.md
    │   ├── auth.go                      <-- Auth REST endpoints
    │   ├── auth_dto.go                  <-- Auth request/response JSON structs
    │   ├── wallet.go                    <-- Wallet REST endpoints
    │   ├── wallet_dto.go                <-- Wallet request/response JSON structs
    │   ├── context.go                   <-- User context extraction helpers
    │   └── dto.go                       <-- Common DTO definitions
    ├── middleware/                      <-- Fiber HTTP middleware
    │   ├── 01README.md                  <-- Middleware architecture docs
    │   ├── 02README.md                  <-- Rate limiting & CORS docs
    │   ├── auth.go                      <-- JWT authentication middleware
    │   ├── cors.go                      <-- CORS configuration
    │   ├── logger.go                    <-- Request logging middleware
    │   ├── rate_limit.go                <-- Rate limiting implementation
    │   ├── recovery.go                  <-- Panic recovery middleware
    │   └── request_id.go                <-- Request ID tracing middleware
    └── response/                        <-- Unified HTTP JSON response formatters
        └── README.md
```

---

## 3. How to Run Locally

```powershell
cd services/gateway
go run cmd/server/main.go
```
