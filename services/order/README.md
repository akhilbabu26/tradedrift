# TradeDrift — Order Service (`services/order`)

> **Service:** Order Service  
> **Directory:** `services/order/`  
> **Database:** `tradedrift_order` (PostgreSQL)  
> **gRPC Port:** `:50053`  
> **Status:** ✅ Production Ready (V1.0 Implementation Complete)

---

## 1. Executive Summary & Purpose

The **Order Service** is the central entrypoint for trading operations on the TradeDrift spot exchange. It manages order lifecycle state machines, enforces idempotency, performs arbitrary-precision decimal financial validation, coordinates synchronous balance reservations with the **Wallet Service**, and persists transactional outbox events for asynchronous matching by the in-memory **Matching Engine**.

### Core Invariants:
1. **Quantity Standard**: `Order.Quantity` **ALWAYS** represents base asset quantity across all 4 order types (`BUY LIMIT`, `BUY MARKET`, `SELL LIMIT`, `SELL MARKET`).
2. **Arbitrary Financial Precision**: Uses `shopspring/decimal` for all calculations — **zero binary floating-point representation bugs**.
3. **Idempotency Guarantee**: Client-supplied `idempotency_key` guarantees that retried requests with identical parameters return the existing order, while altered parameters return `ErrDuplicateIdempotencyKey`.
4. **Saga Compensation**: If database insertion fails post-wallet reservation, an immediate compensating call to `Wallet.ReleaseFunds` releases locked funds, preventing orphaned reservations.
5. **Transactional Outbox Pattern**: Orders and outbox events (`OrderCreated`, `OrderCancelRequested`) are written inside a single PostgreSQL transaction (`BEGIN ... COMMIT`).

---

## 2. Directory & Architecture Map

```
services/order/
├── README.md                            <-- This main service documentation
├── .env                                 <-- Environment configuration
├── go.mod                               <-- Go module definition
├── go.sum                               <-- Dependency checksums
├── cmd/
│   └── server/
│       └── main.go                      <-- Server entrypoint & dependency wiring
├── internal/
│   ├── config/                          <-- Env loader & defaults
│   │   ├── README.md
│   │   └── config.go
│   ├── domain/                          <-- Domain models & shared contracts
│   ├── repository/                      <-- Persistence contracts & Postgres implementation
│   │   ├── README.md
│   │   ├── order.go                     <-- Domain structs, enums, errors, interface
│   │   └── postgres/
│   │       └── order_repository.go      <-- Postgres (pgxpool) queries & keyset pagination
│   ├── service/                         <-- Core business logic, decimal math, Saga
│   │   ├── README.md
│   │   ├── errors.go                    <-- Service-level sentinel errors
│   │   └── service.go                   <-- Order orchestration logic
│   ├── handler/                         <-- gRPC API handler & error mapping
│   │   ├── README.md
│   │   └── grpc.go                      <-- gRPC server endpoint implementation
│   └── wallet/                          <-- Inter-service gRPC client adapter
│       ├── README.md
│       └── client.go                    <-- Wallet Service gRPC client wrapper
└── migration/
    └── 001_create_orders.sql            <-- DDL migration script for Postgres
```

---

## 3. Technology Stack & Packages Used

| Package / Tool | Purpose & Architectural Rationale |
| :--- | :--- |
| **Go 1.26+** | Service programming language offering low-latency, high-concurrency goroutine scheduling. |
| **PostgreSQL** | Primary persistent store for orders and outbox queues (`tradedrift_order` DB). |
| **`github.com/jackc/pgx/v5`** | High-performance PostgreSQL driver and pool manager (`pgxpool`). Provides protocol-level error code checking (`pgconn.PgError`). |
| **`github.com/shopspring/decimal`** | Arbitrary-precision decimal arithmetic. Prevents floating-point representation loss during price $\times$ quantity calculations. |
| **`github.com/google/uuid`** | Generates timestamp-ordered **UUIDv7** primary keys in the application layer. |
| **`go.uber.org/zap`** | Zero-allocation structured logger. |
| **`google.golang.org/grpc`** | gRPC protocol transport for inter-service communication (Wallet Service) and external API serving. |

---

## 4. Money Flow & Order Execution Lifecycle

```
    Client HTTP / gRPC Request
               │
               ▼
   ┌──────────────────────┐
   │    gRPC Handler      │  1. Request boundary check (user_id, market_id, quantity)
   │  (internal/handler)  │  2. Map Protobuf enums -> Domain types
   └───────────┬──────────┘
               │
               ▼
   ┌──────────────────────┐  3. Check idempotency key & parameter equality
   │    Order Service     │  4. Parse canonical pair "BTC-USDT"
   │  (internal/service)  │  5. Validate positive decimal quantity & price
   └───────┬───────┬──────┘  6. Calculate quote reservation: (Price × Quantity)
           │       │         7. Generate UUIDv7 order ID & serialize Outbox payload
           │       │
           │       └─────────────────────────────┐
           │ (gRPC Network Call)                 │ (PostgreSQL Tx)
           ▼                                     ▼
 ┌───────────────────┐               ┌───────────────────────┐
 │  Wallet Service   │               │   PostgreSQL DB       │
 │   ReserveFunds    │               │ (internal/repository) │
 └─────────┬─────────┘               └───────────┬───────────┘
           │                                     │
    Success│                               Failure│ (Saga Compensation)
           ▼                                     ▼
┌─────────────────────┐             ┌────────────────────────┐
│ Persistent DB Tx    │             │ Compensating Action:   │
│ - INSERT INTO orders│             │ Wallet.ReleaseFunds    │
│ - INSERT INTO outbox│             └────────────────────────┘
└─────────────────────┘
```

---

## 5. End-to-End Method Matrix

```
───────────────────────────────────────────────────────────────────────────────────────────────────
Endpoint / Method | gRPC Method          | Database Operations               | Wallet Inter-Service
───────────────────────────────────────────────────────────────────────────────────────────────────
CreateOrder       | CreateOrder          | SELECT FindByIdempotencyKey       | ReserveFunds (gRPC)
                  |                      | INSERT INTO orders (Tx)           | Compensate ReleaseFunds
                  |                      | INSERT INTO outbox (Tx)           | if DB INSERT fails
───────────────────────────────────────────────────────────────────────────────────────────────────
CancelOrder       | CancelOrder          | SELECT GetByID                    | None (Funds released
                  |                      | UPDATE orders SET status='CANCEL' | post-Matching Engine
                  |                      | INSERT INTO outbox (Tx)           | execution)
───────────────────────────────────────────────────────────────────────────────────────────────────
GetOrder          | GetOrder             | SELECT orders WHERE id=$1 AND u=$2| None
───────────────────────────────────────────────────────────────────────────────────────────────────
ListOrders        | ListOrders           | SELECT orders WHERE (created_at,id)| None
                  |                      | < ($cursor) ORDER BY created_at   |
                  |                      | DESC, id DESC LIMIT $limit        |
───────────────────────────────────────────────────────────────────────────────────────────────────
```

---

## 6. How to Run Locally

### 1. Provision PostgreSQL Database
```powershell
psql -U postgres -h localhost -p 5432 -f "scripts\create_all_databases.sql"
```

### 2. Verify `.env` Configuration
Ensure `services/order/.env` contains:
```env
ORDER_POSTGRES_DSN=postgres://postgres:123@localhost:5432/tradedrift_order?sslmode=disable
ORDER_GRPC_PORT=:50053
ORDER_MIGRATIONS_DIR=services/order/migration
WALLET_GRPC_ADDR=localhost:50052
LOG_LEVEL=info
```

### 3. Build and Run
```powershell
cd services/order
go build ./...
go run cmd/server/main.go
```
