# Auth Service — Server Executable (`cmd/server`)

> **Package:** `tradedrift/services/auth/cmd/server`  
> **Directory:** `services/auth/cmd/server/`  
> **Role:** Entrypoint binary for the Authentication Microservice

---

## 1. Purpose & Responsibilities

The `cmd/server` package contains the main entrypoint (`main.go`) for the Auth Service. It is responsible for boot-sequencing, loading configuration, initializing PostgreSQL connection pools, running Goose SQL migrations, instantiating services/handlers, binding TCP gRPC ports (`:50051`), and trapping OS signals (`SIGINT`, `SIGTERM`) for graceful shutdown.

---

## 2. Startup Sequence Architecture

```
Main Entrypoint (main.go)
  │
  ├─> 1. Load Environment Configuration (internal/config)
  ├─> 2. Initialize Zap Logger
  ├─> 3. Connect to PostgreSQL Pool (tradedrift_auth)
  ├─> 4. Run Goose DB Migrations (migrations/)
  ├─> 5. Instantiate Repositories, Services, and Mail/OTP Adapters
  ├─> 6. Bind TCP Port (:50051)
  ├─> 7. Register gRPC Server (authv1.AuthServiceServer)
  └─> 8. Graceful Shutdown Signal Trap (SIGTERM / SIGINT)
```

---

## 3. How to Run

```powershell
cd services/auth
go run cmd/server/main.go
```
