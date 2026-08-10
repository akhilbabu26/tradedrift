# API Gateway — Server Executable (`cmd/server`)

> **Package:** `tradedrift/services/gateway/cmd/server`  
> **Directory:** `services/gateway/cmd/server/`  
> **Role:** HTTP Server Entrypoint & Router Initialization

---

## 1. Purpose & Responsibilities

The `cmd/server` package contains the main entrypoint (`main.go`) for the API Gateway. It initializes Fiber HTTP framework, registers global middleware (Logger, Recovery, Request ID, CORS, Rate Limiting), establishes gRPC client connections to backend services (Auth Service, Wallet Service, Order Service), binds the HTTP server port (`:8080`), and handles graceful shutdown.

---

## 2. Startup Sequence Architecture

```
Main Entrypoint (main.go)
  │
  ├─> 1. Load Environment Configuration
  ├─> 2. Initialize Zap Logger
  ├─> 3. Establish gRPC Client Pool (Auth, Wallet, Order Services)
  ├─> 4. Initialize Fiber HTTP Engine with Global Middleware
  ├─> 5. Register REST Endpoint Routes (/api/v1/...)
  ├─> 6. Listen on HTTP Port (:8080)
  └─> 7. Graceful Shutdown Signal Trap (SIGTERM / SIGINT)
```

---

## 3. How to Run

```powershell
cd services/gateway
go run cmd/server/main.go
```
