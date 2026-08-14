# API Gateway — Server Binary & Entrypoint (`cmd/server`)

> **Package:** `tradedrift/services/gateway/cmd/server`  
> **Directory:** `services/gateway/cmd/`  
> **Executable Source:** `services/gateway/cmd/server/main.go`  
> **HTTP Port:** `:8080`  
> **Role:** Process lifecycle management, configuration ingestion, gRPC client pool dialing, middleware stack assembly, routing registration, and OS signal trap for graceful shutdown.

---

## 1. Purpose & Responsibilities

The `cmd/server/main.go` file is the orchestrator and runtime binary entrypoint for the TradeDrift API Gateway. It brings together all foundational platform utilities (`platform/config`, `platform/logger`, `platform/jwt`) and domain handlers (`auth`, `wallet`, `order`, `market`) into an active HTTP/1.1 REST web server.

### Core Responsibilities:
1. **Environment Ingestion**: Loads `.env` variables and validates mandatory secrets (e.g. `JWT_SECRET`).
2. **Client Channel Management**: Dials non-blocking gRPC connection channels to all downstream microservices (`Auth`, `Wallet`, `Order`, `Market`).
3. **Domain Handler Instantiation**: Injects generated gRPC client stubs into domain handler structs.
4. **Middleware Chain Construction**: Wraps public and protected HTTP routes with rate limiting, CORS, panic recovery, structured logging, and JWT authentication.
5. **Clean Lifecycle & Signal Handling**: Catches OS interrupt signals (`SIGINT`, `SIGTERM`) to cleanly terminate active HTTP requests and close gRPC connections.

---

## 2. Step-by-Step 8-Stage Startup Lifecycle

```
                                  🚀 main() Entrypoint
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 0. Load Configuration (.env + Environment)    │
                    └───────────────────────┬───────────────────────┘
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 1. Initialize Uber Zap Structured Logger      │
                    └───────────────────────┬───────────────────────┘
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 2. Parse & Validate Ports, Addrs & JWT Secrets│
                    └───────────────────────┬───────────────────────┘
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 3. Dial gRPC Clients (Auth, Wallet, Order, Mkt│
                    └───────────────────────┬───────────────────────┘
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 4. Instantiate Sub-package Domain Handlers    │
                    └───────────────────────┬───────────────────────┘
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 5. Assemble Global & Protected Middleware     │
                    └───────────────────────┬───────────────────────┘
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 6. Register HTTP Routes on http.ServeMux      │
                    └───────────────────────┬───────────────────────┘
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 7. Launch Non-Blocking HTTP Server (:8080)    │
                    └───────────────────────┬───────────────────────┘
                                            │
                                            ▼
                    ┌───────────────────────────────────────────────┐
                    │ 8. Block on OS Signals (SIGINT/SIGTERM)       │
                    └───────────────────────────────────────────────┘
```

---

## 3. Deep-Dive: Key Architectural Subsystems

### 1. gRPC Connection Management (`grpc.NewClient`)
The Gateway maintains persistent HTTP/2 gRPC client channels to backend microservices:
```go
authConn, _   := grpc.NewClient(authAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
walletConn, _ := grpc.NewClient(walletAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
orderConn, _  := grpc.NewClient(orderAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
marketConn, _ := grpc.NewClient(marketAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
```
* Uses modern non-blocking `grpc.NewClient` (recommended over legacy `grpc.Dial`).
* `defer conn.Close()` guarantees socket resources and network buffers are closed on process termination.

---

### 2. Middleware Chain Assembly
Middleware is applied using standard Go decorator functions:

```go
// 1. Protected wrapper (verifies JWT and injects user_id claim)
protected := func(h http.Handler) http.Handler {
    return authMW(h)
}

// 2. Global wrapper (applied to all public and protected requests)
global := func(h http.Handler) http.Handler {
    h = rateLimiter.Middleware(h)
    h = middleware.CORS(allowedOrigins)(h)
    h = middleware.Recovery(appLogger)(h)
    h = middleware.Logger(appLogger)(h)
    h = middleware.RequestID(h)
    return h
}
```

* **Execution Order for Protected Request:**
  `RequestID` ➔ `Logger` ➔ `Recovery` ➔ `CORS` ➔ `RateLimiter` ➔ `Auth (JWT)` ➔ `Domain Handler`.

---

### 3. Modern Pattern-Matching Routing (Go 1.22+ `http.ServeMux`)
Instead of external heavy frameworks, the gateway leverages Go's built-in, zero-allocation routing:
* **Public Route Example:**
  ```go
  mux.HandleFunc("GET /api/v1/markets/{id}/ticker", marketH.GetTicker)
  ```
* **Protected Route Example:**
  ```go
  mux.Handle("POST /api/v1/orders", protected(http.HandlerFunc(orderH.CreateOrder)))
  ```

---

### 4. Zero-Downtime Graceful Shutdown
When Docker stops a container, it sends `SIGTERM`. The Gateway intercepts this cleanly:

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()

// ... Server runs in background goroutine ...

<-ctx.Done() // Waits for shutdown signal
appLogger.Info("Shutdown signal received")

shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

if err := srv.Shutdown(shutdownCtx); err != nil {
    appLogger.Error("Graceful shutdown failed", zap.Error(err))
}
appLogger.Info("API Gateway stopped cleanly")
```

1. Stops accepting **new** incoming HTTP requests.
2. Allows in-flight trading requests up to **10 seconds** to finish processing.
3. Closes idle keep-alive connections cleanly before exiting.

---

## 4. Operational Environment Variables

| Variable | Default Value | Description |
| :--- | :--- | :--- |
| `GATEWAY_PORT` | `:8080` | Network port for inbound HTTP REST traffic |
| `AUTH_ADDR` | `localhost:50051` | Address of Auth Microservice gRPC endpoint |
| `WALLET_ADDR` | `localhost:50052` | Address of Wallet Microservice gRPC endpoint |
| `ORDER_ADDR` | `localhost:50053` | Address of Order Microservice gRPC endpoint |
| `MARKET_ADDR` | `localhost:50054` | Address of Market Microservice gRPC endpoint |
| `JWT_SECRET` | *(required)* | 32-byte secret for validating HMAC-SHA256 tokens |
| `CORS_ORIGIN` | `http://localhost:5173` | Allowed frontend origin |
| `LOG_LEVEL` | `info` | Zap logging level (`debug`, `info`, `warn`, `error`) |

---

## 5. Running and Testing Locally

### Directly via Go:
```powershell
cd services/gateway
go run cmd/server/main.go
```

### Via Docker Compose:
```powershell
docker compose up -d gateway
```
