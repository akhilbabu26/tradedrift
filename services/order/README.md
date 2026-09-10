# TradeDrift — Order Service (`services/order`)

> **Service:** Order Service  
> **Directory:** `services/order/`  
> **Database:** `tradedrift_order` (PostgreSQL)  
> **gRPC Port:** `:50053`  
> **Status:** ✅ Production Ready (V1.0 Implementation Complete)

---

## 1. Executive Summary & Purpose

The **Order Service** is the central entrypoint for trading operations on the TradeDrift spot exchange. It manages order lifecycle state machines, enforces idempotency, performs arbitrary-precision decimal financial validation, coordinates synchronous balance reservations with the **Wallet Service**, and persists transactional outbox events for asynchronous delivery to the **Matching Engine** via Apache Kafka.

### Core Invariants:
1. **Quantity Standard**: `Order.Quantity` **ALWAYS** represents base asset quantity across all 4 order types (`BUY LIMIT`, `BUY MARKET`, `SELL LIMIT`, `SELL MARKET`).
2. **Arbitrary Financial Precision**: Uses `shopspring/decimal` for all calculations — **zero binary floating-point representation bugs**.
3. **Precision Rounding for Reservations**: BUY orders round UP quote reservations (`RoundCeil(decimals)`) to quote asset precision to ensure reserved funds strictly cover execution. SELL orders truncate base asset reservations to base precision.
4. **Idempotency Guarantee**: Client-supplied `idempotency_key` guarantees that retried requests with identical parameters return the existing order, while altered parameters return `ErrDuplicateIdempotencyKey`.
5. **Pre-Trade Price Band & Slippage Protection**: Evaluates limit order prices and market buy prices against live Market Maker quotes/mid prices stored in Redis (`ValidatePriceBand`). Orders violating configured deviation thresholds are rejected with `FILTER_FAILURE_PERCENT_PRICE` (HTTP 422).
6. **Saga Compensation**: If database insertion fails post-wallet reservation (e.g. concurrent idempotency race), an immediate compensating call to `Wallet.ReleaseFunds` releases locked funds, preventing orphaned reservations.
7. **Transactional Outbox Pattern**: Orders and outbox events (`OrderCreated`, `OrderCancelRequested`) are written inside a single PostgreSQL transaction (`BEGIN ... COMMIT`).
8. **Market Maker Account Special Routing**: The system MM account (`00000000-0000-0000-0000-000000000001`) manages inventory locally in the Liquidity Engine. The Order Service acts solely as an authoritative recovery ledger: skipping wallet `ReserveFunds` and skipping outbox Kafka emission (since LE publishes MM orders).
9. **Multi-Instance Safe Outbox Worker**: Background worker uses atomic claim leases (`UPDATE outbox SET processing_at = NOW(), attempts = attempts + 1 ... RETURNING ...`) with linear retry backoff and delivery ACK verification.
10. **Asynchronous Trade Fill Ingestion**: Background Kafka consumer reads `trades.settled` events and atomically updates order fill progress (`filled_quantity`, `remaining_quantity`, and status `PARTIALLY_FILLED` / `FILLED`) deduplicated by the `processed_trades` table.

---

## 2. Deep Dive: Idempotency Architecture & Financial Protection

### 2.1 What is Idempotency?
In computer science and API design, an operation is **idempotent** if performing it **multiple times** produces the **exact same result** as performing it **a single time**, without causing unintended side effects.

* **Real-World Analogy (Elevator Call Button)**: Pressing an elevator's "UP" button 1 time or 10 times results in the elevator coming to your floor **once**. The extra 9 presses do not summon 9 extra elevators or charge you extra.
* **Non-Idempotent Danger (E-Commerce Double-Charge)**: If you click "Pay $100" on a checkout page, and your Wi-Fi flickers so your browser retries 3 times, a non-idempotent server would deduct **$300** ($100 × 3) from your bank account!

---

### 2.2 Why Idempotency is Used in TradeDrift's Order Service
In financial crypto/stock exchanges, **network retries are guaranteed to happen**:
- A trader clicks **"Buy 1 BTC at $60,000"**.
- Their mobile app loses 5G connectivity for 1 second right as the server processes the order.
- The client app or automated trading bot automatically retries sending the request (`CreateOrder`).

#### ❌ Without Idempotency (Financial Disaster):
If the server doesn't track retries, receiving the same request 3 times creates **3 separate orders** in the database, locking **$180,000 USDT** from the trader's wallet instead of $60,000!

#### ✅ With Idempotency (TradeDrift's Implementation):
The client app/bot generates a unique `idempotency_key` (a UUIDv7 string) before making the API call:
`idempotency_key = "018f3a5b-7c9d-7000-8000-000000000001"`

When the Order Service processes `CreateOrder`:

1. **First Attempt (Initial Request)**:
   - Order Service reserves $60,000 USDT in Wallet Service.
   - Inserts order row + outbox event into PostgreSQL.
   - Returns `Order ID: "018f3a5b-..."`.

2. **Second Attempt (Network Retry with Same `idempotency_key`)**:
   - Order Service looks up the key in PostgreSQL (`FindByIdempotencyKey`).
   - Finds the existing order row.
   - Runs parameter verification (`sameRequest`):
     - **If parameters match**: Skips creating a new order and returns the **existing order immediately**. **Zero additional funds are locked!**
     - **If parameters differ** (e.g. key reused but quantity changed from 1 BTC to 5 BTC): Returns `ErrDuplicateIdempotencyKey` (`codes.AlreadyExists`) to reject key tampering.

3. **Database Race Guard (`orders_idempotency_key_key`)**:
   - PostgreSQL enforces `idempotency_key VARCHAR(64) UNIQUE`. Even if two parallel retries hit two different Order Service microservice instances at the exact same microsecond, PostgreSQL rejects the second INSERT with error code `23505`, guaranteeing **atomic duplicate prevention**.

---

### 2.3 Pre-Trade Price Band Validation & Slippage Guard
To protect users and maintain market integrity, the Order Service enforces pre-trade price checks against real-time Market Maker quotes in Redis (`price_filter.go`):
- **Limit Orders & Market Buy Orders**: Evaluates the incoming order's price against the current market reference price fetched from Redis (`orderbook:ticker:{market}` or MM quote stream).
- **Deviation Guard**: If the order price deviates by more than the configured percentage (e.g., `MAX_PRICE_DEVIATION=0.05` for 5%) above the MM ask (for buys) or below the MM bid (for sells), the order is rejected immediately *before* reserving funds.
- **Client Error Format**: Mapped to gRPC `codes.FailedPrecondition` with structured JSON (`FILTER_FAILURE_PERCENT_PRICE`), allowing the API Gateway to return a detailed HTTP `422 Unprocessable Entity` response with `errorCode`, `reason`, `details`, and `timestamp`.

---

### 2.4 Asynchronous Trade Execution Fill Ingestion
The Order Service continuously monitors execution settlements via a background Kafka consumer (`internal/kafka/consumer/consumer.go`):
- **Topic**: Subscribes to `trades.settled` (or `wallet.events`).
- **Idempotency Barrier (`processed_trades`)**: Uses Migration `003_create_processed_trades.sql` with `PRIMARY KEY (trade_id)`. Before updating an order, the consumer executes:
  ```sql
  INSERT INTO processed_trades (trade_id) VALUES ($1) ON CONFLICT (trade_id) DO NOTHING RETURNING trade_id;
  ```
  If no row was inserted, the event has already been applied and is skipped safely (idempotent no-op).
- **Atomic Order Fill Progress**: Updates both the buy order and sell order atomically:
  ```sql
  UPDATE orders
  SET filled_quantity = LEAST(quantity, filled_quantity + $1::numeric),
      remaining_quantity = GREATEST(0, remaining_quantity - $1::numeric),
      status = CASE 
          WHEN remaining_quantity - $1::numeric <= 0 THEN 'FILLED' 
          ELSE 'PARTIALLY_FILLED' 
      END,
      updated_at = NOW()
  WHERE id = $2 AND status IN ('OPEN', 'PARTIALLY_FILLED');
  ```

---

### 2.5 Market Maker Account Execution Routing
- **Account Identity**: `00000000-0000-0000-0000-000000000001` represents the internal Automated Liquidity Engine (LE) bot.
- **Inventory Autonomy**: The Liquidity Engine tracks its inventory locally in memory and in Redis. Thus, MM orders created in the Order Service skip synchronous wallet `ReserveFunds` calls.
- **Publisher Invariant**: The Liquidity Engine is the sole authority publishing MM `OrderCreated` commands directly to Kafka. The Order Service skips enqueuing outbox events for MM orders to prevent duplicate Kafka emissions.

---

## 3. Directory & Architecture Map

```
services/order/
├── README.md                            <-- Main service architecture documentation
├── .env                                 <-- Environment configuration
├── go.mod                               <-- Go module definition
├── go.sum                               <-- Dependency checksums
├── cmd/
│   ├── README.md                        <-- Executable package documentation
│   └── server/
│       └── main.go                      <-- Server entrypoint, gRPC listener, workers & consumer wiring
├── internal/
│   ├── config/                          <-- Env loader & defaults
│   │   ├── README.md
│   │   └── config.go
│   ├── repository/                      <-- Persistence contracts & Postgres implementation
│   │   ├── README.md
│   │   ├── order.go                     <-- Order domain structs, enums, errors, interface
│   │   ├── outbox.go                    <-- Outbox event model & interface
│   │   └── postgres/
│   │       ├── order_repository.go      <-- Postgres order CRUD, fills & keyset pagination
│   │       └── outbox_repository.go     <-- Postgres outbox worker atomic claims & backoff retries
│   ├── service/                         <-- Core business logic, decimal math, Saga
│   │   ├── README.md
│   │   ├── errors.go                    <-- Service-level sentinel errors
│   │   ├── price_filter.go              <-- Pre-trade price band & slippage guard (Redis MM quotes)
│   │   ├── price_filter_test.go         <-- Unit tests for price band calculations
│   │   ├── service.go                   <-- Order creation & cancellation orchestration logic
│   │   └── validation.go                <-- Decimal math comparison & market format helpers
│   ├── handler/                         <-- gRPC API handler & error mapping
│   │   ├── README.md
│   │   ├── grpc.go                      <-- gRPC RPC Endpoint methods
│   │   └── mapper.go                    <-- Error status mapping & Protobuf converters
│   ├── kafka/                           <-- Outbox publisher and event consumer
│   │   ├── README.md
│   │   ├── consumer/
│   │   │   └── consumer.go              <-- Consumes trades.settled, calls ApplyTradeFill
│   │   └── publisher/
│   │       ├── outbox_publisher.go      <-- Polling loop, topic router, linear backoff
│   │       ├── producer.go              <-- Producer interface & LogProducer stub
│   │       ├── kafka_producer.go        <-- Option B Explicit Partition Producer (BTC:0, ETH:1, SOL:2)
│   │       └── kafka_producer_test.go   <-- Unit tests for explicit partition resolution
│   └── wallet/                          <-- Inter-service gRPC client adapter
│       ├── README.md
│       └── client.go                    <-- Wallet Service gRPC client wrapper
└── migration/
    ├── README.md                        <-- Database DDL documentation
    ├── 001_create_orders.sql            <-- DDL migration script for orders & outbox tables
    ├── 002_alter_idempotency_key.sql    <-- Widens idempotency_key to VARCHAR(64)
    └── 003_create_processed_trades.sql  <-- Dedicated idempotency table for trade executions
```

---

## 4. Technology Stack & Packages Used

| Package / Tool | Purpose & Architectural Rationale |
| :--- | :--- |
| **Go 1.26+** | Service programming language offering low-latency, high-concurrency goroutine scheduling. |
| **PostgreSQL** | Primary persistent store for orders, processed trades, and outbox queues (`tradedrift_order` DB). |
| **Redis** (`github.com/redis/go-redis/v9`) | Caches real-time Market Maker quotes and ticker prices for instant sub-millisecond price band validation. |
| **`github.com/jackc/pgx/v5`** | High-performance PostgreSQL driver and pool manager (`pgxpool`). Provides protocol-level error code checking (`pgconn.PgError`). |
| **`github.com/shopspring/decimal`** | Arbitrary-precision decimal arithmetic. Prevents floating-point representation loss during price $\times$ quantity calculations. |
| **`github.com/google/uuid`** | Generates timestamp-ordered **UUIDv7** primary keys in the application layer. |
| **`github.com/segmentio/kafka-go`** | Fast Kafka reader & writer for outbox event delivery and execution stream ingestion. |
| **`go.uber.org/zap`** | Zero-allocation structured logger. |
| **`google.golang.org/grpc`** | gRPC protocol transport for inter-service communication (Wallet Service) and external API serving. |

---

## 5. End-to-End Money Flow & Execution Lifecycle

```
    Client gRPC Request
             │
             ▼
 ┌──────────────────────┐
 │    gRPC Handler      │  1. Validate user_id, market_id, quantity
 │  (internal/handler)  │  2. Map Protobuf enums -> Domain types
 └───────────┬──────────┘
             │
             ▼
 ┌──────────────────────┐  3. Check idempotency key & parameter equality
 │    Order Service     │  4. Parse canonical pair "BTC-USDT" & decimal precision
 │  (internal/service)  │  4.5. Validate Pre-Trade Price Band via Redis MM quotes
 └───────┬───────┬──────┘  5. Calculate exact quote reservation (RoundCeil to decimals)
         │       │         6. Generate UUIDv7 order ID & serialize Outbox payload
         │       │
         │       └─────────────────────────────┐
         │ (gRPC Call: ReserveFunds)           │ (PostgreSQL Tx)
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
└──────────┬──────────┘
           │ (Atomic Claim Lease)
           ▼
┌─────────────────────┐             ┌────────────────────────┐
│   Outbox Worker     │ ──────────> │ Kafka Event Broker     │
│ (internal/kafka)    │             │ Topic: orders.submitted│
└─────────────────────┘   Deliver   └───────────┬────────────┘
                            ACK                 │
                                                ▼
                                    ┌────────────────────────┐
                                    │ Matching Engine &      │
                                    │ Settlement Execution   │
                                    └───────────┬────────────┘
                                                │
                                                ▼ Topic: trades.settled
┌─────────────────────┐             ┌────────────────────────┐
│ Order Ingestion DB  │ <────────── │  Trade Fill Consumer   │
│ - processed_trades  │  Update     │ (kafka/consumer)       │
│ - status: FILLED    │  Fills      └────────────────────────┘
└─────────────────────┘
```

---

## 6. How to Run Locally

### 1. Provision PostgreSQL Database
```powershell
psql -U postgres -h localhost -p 5432 -f "scripts\create_all_databases.sql"
```

### 2. Build and Execute
```powershell
cd services/order
go build ./...
go run cmd/server/main.go
```
