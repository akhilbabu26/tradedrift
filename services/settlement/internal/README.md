# Settlement Service — Internal Architecture (`internal/`)

> **Directory:** `services/settlement/internal/`  
> **Visibility:** Go's `internal/` enforcement — cannot be imported by any package outside `services/settlement/`  
> **Architecture Pattern:** Hexagonal (Ports & Adapters) with interface-driven dependency injection

---

## 1. Purpose

The `internal/` directory contains all private business logic, data persistence, event streaming, external client, and configuration code for the Settlement Service. The `internal` directory name is enforced by the Go toolchain — no other service or external module can import these packages.

Each sub-package has a single, well-defined responsibility and communicates with its neighbours only through **interfaces** — never through direct struct coupling.

---

## 2. Hexagonal Architecture Diagram

```
                    External World
              (Kafka Brokers / Wallet gRPC)
                           │
              ┌────────────┴────────────┐
              │                         │
              ▼                         ▼
     ┌─────────────────┐       ┌─────────────────┐
     │  Driving Adapter │       │  Driven Adapter  │
     │  internal/kafka  │       │  internal/client │
     │   (Consumer)     │       │  (Wallet gRPC)   │
     └────────┬────────┘       └────────┬─────────┘
              │                         │
              │    ┌──────────────┐      │
              └───▶│   Service    │◀─────┘
                   │  Layer       │
                   │ internal/    │
                   │ service      │
                   └──────┬───────┘
                          │
              ┌───────────┴───────────┐
              │                       │
              ▼                       ▼
     ┌─────────────────┐    ┌──────────────────┐
     │   Repository    │    │     Config        │
     │ internal/       │    │ internal/config   │
     │ repository &    │    │ (env vars)        │
     │ repository/     │    └──────────────────┘
     │ postgres        │
     └─────────────────┘
```

**Dependency rule:** Arrows point inward. The service layer depends on interfaces — it never imports `kafka`, `client`, or `postgres` directly.

---

## 3. Sub-Package Reference

| Sub-Package | Responsibility | Key Types / Functions | README |
|---|---|---|---|
| [`config/`](./config/README.md) | Reads env vars, validates them, returns typed `Config` struct | `Config`, `Load() (Config, error)` | [→](./config/README.md) |
| [`kafka/`](./kafka/README.md) | Manual-commit Kafka consumer, drives `service.Settle()` per message, handles poison pills | `Consumer`, `NewConsumer`, `Start`, `Close` | [→](./kafka/README.md) |
| [`repository/`](./repository/README.md) | Domain entity (`SettledTrade`), status constants, `Repository` interface, and full PostgreSQL implementation | `SettledTrade`, `Repository`, `Insert`, `FindByTradeID`, `MarkSettled`, `FindStalePending` | [→](./repository/README.md) |
| [`client/`](./client/README.md) | Wraps generated Wallet gRPC stub; defines `SettleRequest`; satisfies `service.WalletSettler` | `WalletClient`, `SettleRequest`, `SettleTrade`, `Close` | [→](./client/README.md) |
| [`service/`](./service/README.md) | 3-phase settlement pipeline, input validation, background recovery | `Service`, `TradeExecutedEvent`, `WalletSettler`, `Settle`, `RecoverStalePending` | [→](./service/README.md) |

---

## 4. Data Flow Through `internal/`

```
Kafka broker
     │
     │  JSON: TradeExecutedEvent
     ▼
internal/kafka.Consumer.Start()
     │
     │  json.Unmarshal → TradeExecutedEvent
     ▼
internal/service.Service.Settle(ctx, event)
     │
     ├── VALIDATE (uuid.Parse, decimal, buyer≠seller, market format)
     │
     ├── PHASE 1: internal/repository.Insert(PENDING)
     │               └── pgxpool → PostgreSQL settled_trades
     │
     ├── PHASE 2: internal/client.WalletClient.SettleTrade(ctx, req)
     │               └── gRPC → Wallet Service (idempotent on trade_id)
     │
     └── PHASE 3: internal/repository.MarkSettled(SETTLED)
                     └── pgxpool → PostgreSQL settled_trades
                              │
                              ▼
                     kafka.Consumer.commitMsg()  ← ACK offset
```

---

## 5. Interface Boundaries

| Interface | Defined In | Implemented By | Purpose |
|---|---|---|---|
| `repository.Repository` | `repository/repository.go` | `repository/postgres.Repository` | Decouples service from pgx driver |
| `service.WalletSettler` | `service/service.go` | `client.WalletClient` | Decouples service from gRPC — enables mock injection in tests |

---

## 6. Testing Strategy

The `service` package has **16 unit tests** in `service_test.go`. Because the service depends only on interfaces, all tests run with in-memory mocks — no Kafka, no PostgreSQL, no gRPC dial required. Tests run in ~2 seconds and can be run offline.

```bash
go test ./internal/service/... -v
```

Coverage includes:
- Full happy path (PENDING → SETTLED)
- Already-SETTLED idempotency (no Wallet call)
- Phase 2 gRPC failure (PENDING preserved, no ACK)
- Phase 3 DB failure (error returned, no ACK)
- **Crash-between-Phase2-and-Phase3 redeliver** (Wallet absorbs duplicate)
- All validation paths (bad UUID, buyer=seller, negative price, bad market)
