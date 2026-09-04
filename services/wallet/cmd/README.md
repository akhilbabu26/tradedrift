# Wallet Service — Server Executable (`cmd/server`)

> **Package:** `tradedrift/services/wallet/cmd/server`  
> **Directory:** `services/wallet/cmd/server/`  
> **Role:** Entrypoint binary for the Ledger & Balance Management Microservice

---

## 1. Purpose & Responsibilities

The `cmd/server` package contains the main entrypoint (`main.go`) for the Wallet Service. It is responsible for booting the microservice, loading configuration, initializing the PostgreSQL connection pool (`tradedrift_wallet`), running Goose SQL migrations, instantiating repositories/services/handlers, binding TCP gRPC ports (`:50052`), and handling graceful shutdown signals.

---

## 2. Startup Sequence Architecture

```
Main Entrypoint (main.go)
  │
  ├─> 1. Load Environment Configuration
  ├─> 2. Initialize Zap Logger
  ├─> 3. Connect to PostgreSQL Pool (tradedrift_wallet)
  ├─> 4. Run Goose DB Migrations (migration/)
  ├─> 5. Instantiate Repositories and Services
  ├─> 6. Bind TCP Port (:50052)
  ├─> 7. Register gRPC Server (walletv1.WalletServiceServer)
  ├─> 8. Start Outbox Publisher (trades.settled.v1 & portfolio.user.trades.v1)
  └─> 9. Graceful Shutdown Signal Trap (SIGTERM / SIGINT)
```


---

## 3. How to Run

```powershell
cd services/wallet
go run cmd/server/main.go
```
