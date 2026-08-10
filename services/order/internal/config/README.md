# Order Service — Configuration Package (`internal/config`)

> **Package:** `tradedrift/services/order/internal/config`  
> **Directory:** `services/order/internal/config/`  
> **Role:** Environment Configuration Loader & Defaults Gateway

---

## 1. Purpose & Architectural Role

The `config` package provides a centralized, type-safe configuration structure for the Order Service. It isolates environment variable loading, default fallback values, and runtime setting parsing away from business and handler logic.

By enforcing a single point of entry for configuration (`Load()`), the service guarantees that:
- Default database DSNs, ports, and addresses are defined in one location.
- Environment overrides (from `.env` or Docker container environment variables) are applied transparently.
- Failure to specify optional flags falls back to safe local developer defaults (`localhost:5432`, `localhost:50052`, `:50053`).

---

## 2. Files in This Directory

| File | Role |
| :--- | :--- |
| [`config.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/config/config.go) | Defines `Config` struct and the `Load()` initializer function |

---

## 3. Packages & Dependencies Used

| Package | Purpose & Rationale |
| :--- | :--- |
| `tradedrift/platform/config` | Shared platform utility exposing `GetEnv(key, defaultVal)` to parse OS environment variables with fallback values. |

---

## 4. Structs & Functions Breakdown

### 4.1 Struct `Config`

```go
type Config struct {
    PostgresDSN    string // DSN connection string for tradedrift_order PostgreSQL database
    GRPCPort       string // TCP port for Order Service gRPC server (e.g. ":50053")
    MigrationsDir  string // Directory path containing SQL migrations
    WalletGRPCAddr string // Host:Port address for Wallet Service gRPC server (e.g. "localhost:50052")
    LogLevel       string // Zap log level ("debug", "info", "warn", "error")
}
```

* **`PostgresDSN`**: Holds the connection string used by `pgxpool` to establish PostgreSQL connection pools for transaction management.
* **`GRPCPort`**: Binds the gRPC listener in `main.go`.
* **`WalletGRPCAddr`**: Target network address for establishing gRPC dial connections to the Wallet Service (`ReserveFunds` / `ReleaseFunds`).

---

### 4.2 Function `Load() Config`

* **Signature:** `func Load() Config`
* **Purpose:** Reads environment variables using `platformconfig.GetEnv` and returns a fully populated `Config` struct.
* **Problem Solved:** Prevents hardcoded strings across handlers and services. If environment variable `ORDER_POSTGRES_DSN` is set (e.g., in Docker Compose or K8s), it overrides the default local string automatically.

#### Default Fallback Values:
```go
ORDER_POSTGRES_DSN   -> "postgres://postgres:123@localhost:5432/tradedrift_order?sslmode=disable"
ORDER_GRPC_PORT      -> ":50053"
ORDER_MIGRATIONS_DIR -> "migration"
WALLET_GRPC_ADDR     -> "localhost:50052"
LOG_LEVEL            -> "info"
```
