# TradeDrift — Project Structure

> **Status:** ✅ Frozen (V1.0)
> **Document:** 01_Project_Structure.md
> **Directory:** docs/07_Development/
> **Last Updated:** July 2026

---

## 1. Monorepo Organization

TradeDrift is organized as a single Git **Multi-Module Monorepo** written in Go. This allows developers to work on all services and shared code inside a unified repository while maintaining clean module boundaries.

---

## 2. Directory Layout

The repository is structured as follows:

```
TradeDrift/
│
├── go.work                              # Go multi-module workspace definition
├── go.work.sum                          # Workspace dependency lockfile (committed)
├── .gitignore                           # Ignores: /bin/, *.env, platform/gen/
├── README.md
├── Makefile                             # Top-level shortcuts: make proto, lint, test
│
├── proto/                               # Canonical protobuf source contracts (Frozen)
│   ├── common/v1/common.proto           # Shared types: Money, UUID, Pagination
│   ├── auth/v1/auth.proto
│   ├── wallet/v1/wallet.proto
│   ├── order/v1/order.proto
│   ├── admin/v1/admin.proto
│   ├── market/v1/market.proto
│   ├── portfolio/v1/portfolio.proto
│   ├── trade/v1/trade.proto
│   └── notification/v1/notification.proto
│
├── platform/                            # Shared platform SDK (go.mod)
│   ├── go.mod                           # module: tradedrift/platform
│   ├── go.sum
│   ├── api/
│   │   └── Makefile                     # protoc compilation script → outputs to gen/
│   ├── gen/                             # Compiled Go protobuf/gRPC stubs (auto-generated)
│   │   ├── auth/v1/
│   │   ├── wallet/v1/
│   │   ├── order/v1/
│   │   ├── admin/v1/
│   │   └── common/v1/
│   ├── config/                          # Env/config loader
│   ├── logger/                          # Structured JSON logger (zerolog/zap)
│   ├── tracer/                          # OpenTelemetry + Jaeger initialisation
│   ├── metrics/                         # Prometheus metrics registration
│   ├── health/                          # /live and /ready HTTP handlers
│   ├── errors/                          # Canonical error codes (INSUFFICIENT_FUNDS, etc.)
│   ├── uuid/                            # UUIDv7 generator
│   ├── jwt/
│   │   ├── validator/                   # Generic JWT signature + Redis blacklist check
│   │   └── transport/
│   │       ├── grpc/                    # gRPC unary interceptor
│   │       └── http/                    # HTTP middleware
│   ├── outbox/                          # Transactional outbox engine (FOR UPDATE SKIP LOCKED)
│   │   ├── publisher.go
│   │   └── serializer.go
│   ├── kafka/                           # Producer/consumer wrappers, retry/backoff defaults
│   ├── postgres/                        # pgx connection pool + migration runner
│   │   └── migrate.go                   # RunMigrations(db, migrationsDir) — see §4
│   ├── redis/                           # Redis + Sentinel client setup
│   ├── pagination/                      # Keyset cursor encode/decode (base64)
│   ├── circuitbreaker/                  # Circuit breaker wrapper for gRPC calls
│   └── events/                          # Shared Kafka event envelope + topic name constants
│       ├── envelope.go                  # EventEnvelope struct (event_id, type, version, payload)
│       └── topics.go                    # Topic constants: orders.created.v1, trades.executed.v1, ...
│
├── services/                            # Standalone microservice modules (11 total)
│   │
│   ├── gateway/                         # API Gateway — HTTP routing, CORS, rate-limit, tracing
│   ├── auth/                            # Authentication Service — JWT, bcrypt, Redis blacklist
│   ├── wallet/                          # Wallet & Ledger Service — double-entry, reservation
│   ├── order/                           # Order Lifecycle Service — validation, fund lock, Kafka
│   ├── matching/                        # In-Memory Matching Engine — FIFO red-black tree
│   ├── settlement/                      # Trade Settlement Orchestrator — two-phase saga
│   ├── market/                          # Market Metadata & Ticker Service
│   ├── portfolio/                       # Portfolio & PnL Projector — Kafka consumer
│   ├── notification/                    # Real-Time Notification — WebSocket hub
│   ├── trade/                           # Trade History & Public Feed — read-side projector
│   └── admin/                           # Admin Control Plane — suspend/freeze/halt
│
│   # Every service follows this internal layout.
│   # kafka/ and client/ are OPTIONAL — only created when the service needs them.
│   # Their absence is meaningful: a service with no kafka/publisher/ never publishes.
│   └── <name>/
│       ├── go.mod                       # module: tradedrift/services/<name>
│       ├── go.sum
│       ├── cmd/
│       │   └── server/
│       │       └── main.go              # Entry point: reads env, wires deps, runs migrations
│       ├── internal/
│       │   ├── handler/                 # Transport layer: gRPC server methods or HTTP handlers
│       │   ├── service/                 # Business logic: domain rules, orchestration
│       │   ├── repository/              # Data access: SQL queries only, returns domain models
│       │   ├── model/                   # Domain structs: zero external imports
│       │   ├── kafka/                   # [OPTIONAL] Present only if service uses Kafka
│       │   │   ├── consumer/            # One file per consumed topic
│       │   │   └── publisher/           # Outbox-backed Kafka producer (omit if never publishes)
│       │   └── client/                  # [OPTIONAL] Present only if service makes gRPC calls
│       │       └── <target>/            # One sub-package per downstream service called
│       ├── migrations/                  # SQL migration files for this service's own database
│       └── config/
│           └── config.go
│
├── deployments/                         # Infrastructure deployment manifests
│   ├── docker/
│   │   ├── docker-compose.infra.yml     # Kafka (KRaft), Redis (Sentinel), Postgres (Patroni)
│   │   └── docker-compose.yml           # Full application stack (all 11 services)
│   ├── kubernetes/                      # K8s manifests per service + infra
│   └── helm/                            # Helm chart (tradedrift/)
│
├── tests/                               # Cross-service integration and E2E tests only
│   ├── integration/                     # Multi-service tests (real infra required)
│   └── e2e/                             # Full system scenarios (full trade lifecycle)
│
├── scripts/                             # Developer automation scripts
│   ├── migrate.sh                       # Run goose migrations for a target service
│   ├── seed.sh                          # Insert BTC-USDT market + test users
│   ├── proto-compile.sh                 # Invokes platform/api/Makefile
│   └── lint.sh                          # golangci-lint across workspace
│
├── docs/                                # Frozen architecture documentation
└── bin/                                 # Compiled binaries — .gitignore'd
```

---

## 3. Modular Boundaries Guidelines

* **Shared Platform SDK (`/platform`):** Contains library packages that expose generic, reusable capabilities. This module is completely self-contained and **must never import service packages** to prevent circular reference compilation errors.
* **Microservices (`/services/[name]`):** Each microservice is an independent, runnable module with its own `go.mod`. Services import the shared platform library using Go Workspaces:
  ```go
  import "tradedrift/platform/uuid"
  ```
* **Protobuf Schemas (`/proto`):** Standard protobuf contract templates are managed centrally and compiled into the `/platform/gen/` folder using `make` to maintain schema consistency.
* **Optional Folders:** `kafka/` and `client/` inside `internal/` are only created when the service needs them. Their absence is itself a meaningful signal — a service with no `kafka/publisher/` never publishes events.

### 3.1 Per-Service Optional Folder Topology

This table shows which optional internal folders each service has. It makes cross-service dependencies and event flow visible without reading individual service specs.

| Service | `kafka/consumer/` | `kafka/publisher/` | `client/<target>/` |
|---|---|---|---|
| `gateway` | — | — | `auth/`, `wallet/`, `order/`, `market/`, `portfolio/`, `trade/` |
| `auth` | — | — | — |
| `wallet` | — | ✅ (`user-trades.settled.v1`) | — |
| `order` | ✅ (`orders.cancel-requested.v1`) | ✅ (`orders.created.v1`) | `wallet/` |
| `matching` | ✅ (`orders.created.v1`, `admin.market-commands.v1`) | ✅ (`trades.executed.v1`, `orders.cancelled.v1`) | — |
| `settlement` | ✅ (`trades.executed.v1`) | — | `wallet/` |
| `market` | ✅ (`trades.executed.v1`) | — | — |
| `portfolio` | ✅ (`user-trades.settled.v1`) | ✅ (`portfolios.updated.v1`) | `trade/` |
| `notification` | ✅ (multiple topics) | — | — |
| `trade` | ✅ (`user-trades.settled.v1`) | — | — |
| `admin` | — | ✅ (`admin.user-suspended.v1`, `admin.market-commands.v1`, `admin.market-halted.v1`) | `auth/`, `wallet/` |

> **Reading the table:** `✅` = folder exists. `—` = folder is absent (intentionally). `client/<target>/` lists the downstream gRPC services called.

---

## 4. Migration Boundary Rule

The migration ownership is split across two locations with a strict contract:

### 4.1 What lives in `platform/postgres/`

The `platform/postgres/` package exposes a **migration runner function** — a thin wrapper around `goose.Up()`:

```go
// platform/postgres/migrate.go

package postgres

import (
    "database/sql"
    "github.com/pressly/goose/v3"
)

// RunMigrations applies all pending SQL migrations from the given directory.
// migrationsDir must point to the calling service's own migrations/ folder.
func RunMigrations(db *sql.DB, migrationsDir string) error {
    return goose.Up(db, migrationsDir)
}
```

This is the **only** migration-related code in `platform/`. It contains no SQL files.

### 4.2 What lives in `services/<name>/migrations/`

Every service that owns a database keeps its own SQL migration files inside its module:

```
services/auth/migrations/
  ├── 001_create_users.sql
  └── 002_add_refresh_tokens.sql

services/wallet/migrations/
  ├── 001_create_wallets.sql
  └── 002_create_outbox.sql
```

SQL files **never** move to `platform/postgres/`. Each service's schema evolves independently.

### 4.3 Call pattern at service startup

Each service's `cmd/server/main.go` calls the shared runner, passing its own migrations path:

```go
// services/auth/cmd/server/main.go

db, _ := postgres.NewPool(cfg.Postgres)
if err := postgres.RunMigrations(db.DB, "./migrations"); err != nil {
    log.Fatal("migration failed", err)
}
```

### 4.4 Invariant

> **PMR-1 (Migration Ownership):** SQL migration files are owned by the service whose database they modify. No service migration file may ever reside outside its own `services/<name>/migrations/` directory.
