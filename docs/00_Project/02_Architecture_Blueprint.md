# TradeDrift — Architecture Blueprint

> **Status:** 🚧 In Design (V1)

---

# Purpose

This document describes the high-level architecture of TradeDrift and the reasoning behind its major architectural decisions.

The detailed implementation of each service is documented separately in the service design documents.

---

# Architecture Goals

TradeDrift is designed to achieve:

- Scalability
- Reliability
- Maintainability
- Fault Isolation
- Event-Driven Communication
- Production-style Architecture
- Educational Transparency

---

# Architectural Principles

## Microservices

Each business capability is implemented as an independent service.

Examples:

- Authentication
- Wallet
- Orders
- Matching
- Settlement
- Portfolio

Each service owns:

- Its own database
- Business rules
- gRPC API

---

## Event-Driven Communication

Services communicate asynchronously whenever immediate responses are unnecessary.

Kafka is used for:

- OrderCreated
- TradeExecuted
- TradeSettled
- PortfolioUpdated
- NotificationCreated

Benefits

- Loose coupling
- Better scalability
- Independent deployments

---

## Synchronous Communication

Critical user-facing operations use gRPC.

Examples

Authentication

↓

Wallet Reservation

↓

Health Checks

Benefits

- Immediate feedback
- Lower latency
- Simpler validation

---

## Transactional Outbox

TradeDrift uses the Transactional Outbox Pattern whenever a database update in one service must notify other services via Kafka.

---

### The Problem: Dual-Write Failure

Without the outbox pattern, a service might attempt two separate writes:

```
1. Update database  ✅
2. Publish to Kafka ❌  (crash / network failure)
```

If the service crashes between step 1 and step 2, the database is updated but the Kafka event is lost forever. Other services never learn what happened. The system is now inconsistent with no way to recover.

---

### The Solution: Write Both Inside One DB Transaction

Instead of writing to Kafka directly, the service writes both the business data and the event record into the **same PostgreSQL transaction**:

```
BEGIN TRANSACTION
  UPDATE users SET status = 'VERIFIED' WHERE id = 'user-123'
  INSERT INTO outbox (event_type, payload, status) VALUES ('UserVerified', '...', 'PENDING')
COMMIT
```

Either both writes succeed together, or both are rolled back together. There is no in-between state. The outbox row is the durable record of an event that has not yet been published.

---

### The Outbox Publisher Worker

A background loop running inside each service scans the outbox table for PENDING rows and publishes them to Kafka:

```
Publisher Worker (runs every N seconds)
  │
  ├── SELECT * FROM outbox WHERE status = 'PENDING' ORDER BY created_at ASC LIMIT 100
  │
  ├── For each row:
  │     ├── Publish event to Kafka topic
  │     ├── Kafka broker ACKs receipt (stored on disk, replicated)
  │     └── UPDATE outbox SET status = 'PROCESSED', published_at = NOW()
  │
  └── Sleep, then repeat
```

---

### Event Lifecycle: PENDING → PROCESSED → (cleanup)

Outbox rows are never hard-deleted immediately. They follow a status lifecycle:

| Status    | Meaning                                                   |
|-----------|-----------------------------------------------------------|
| PENDING   | Event has not yet been published to Kafka                 |
| PROCESSED | Event was successfully handed to Kafka (broker ACK received) |
| FAILED    | Event failed after max retries — requires investigation   |

The `PROCESSED` status change happens **the moment Kafka acknowledges receipt**, not when a consumer reads it. Once Kafka has the event, it is stored durably on disk and replicated across Kafka brokers. Consumers can read it at their own pace.

A scheduled cleanup job periodically removes old PROCESSED rows to keep the table small:

```sql
DELETE FROM outbox WHERE status = 'PROCESSED' AND published_at < NOW() - INTERVAL '30 days'
```

---

### FIFO Ordering

The publisher reads rows ordered by `created_at ASC`, so events are published to Kafka in the order they were committed to the database.

Within a Kafka topic, ordering is guaranteed **per partition**. TradeDrift uses the entity's UUID (e.g., `userID`, `orderID`) as the Kafka partition key, so all events for the same entity always arrive in the same partition in order.

If an event fails to publish, the publisher stops and retries that event before moving forward. This preserves strict FIFO ordering per entity at the cost of blocking on failures.

---

### At-Least-Once Delivery and Idempotent Consumers

The outbox pattern guarantees **at-least-once delivery**, not exactly-once. In rare crash scenarios (crash after Kafka ACK but before marking PROCESSED), the same event may be published twice.

All consumer services must therefore be **idempotent** — they must safely handle receiving the same event more than once without causing duplicate side effects.

Example:

```
Notification Service receives UserVerified for user-X

Check: "Have I already sent a welcome email for user-X?"
  → Yes → Skip
  → No  → Send email, record that it was sent
```

---

### When is the Outbox Used in TradeDrift?

| Service     | Event Written to Outbox | Why                                             |
|-------------|------------------------|-------------------------------------------------|
| Auth        | UserVerified           | Notify Wallet, Notification, and Analytics      |
| Order       | OrderCreated           | Trigger Matching Engine                         |
| Matching    | TradeExecuted          | Trigger Settlement and Portfolio                |
| Settlement  | TradeSettled           | Trigger Portfolio update and Notification       |

---

# outbox  use in order created.
Order Service executes Step 1:
  │
  ├── Saves OrderCreated event to its own outbox table  ← OUTBOX doing its job
  │
  └── Outbox publisher pushes it to Kafka
             │
             ▼
       Kafka Topic: "order.created"
             │
             ▼
  Matching Engine receives it        ← SAGA next step begins
  Matching Engine does its work
  Saves TradeExecuted to its outbox  ← OUTBOX doing its job again
             │
             ▼
       Kafka Topic: "trade.executed"
             │
             ▼
  Settlement receives it             ← SAGA next step begins


### Benefits

- No dual-write problem — DB and event are always in sync
- Crash-safe — events survive service restarts
- Kafka independence — service works even when Kafka is temporarily down
- Audit trail — full history of every event ever published

---

## Saga Pattern

---

### What is the Saga Pattern?

A trade in TradeDrift spans multiple services: Wallet, Order, Matching Engine, Settlement, Portfolio, and Notification. Each service owns its own database. There is no single database transaction that can span all of them.

The Saga pattern solves this by breaking a multi-step workflow into a sequence of local transactions. Each service completes its own step and then publishes an event to trigger the next service. If any step fails, previously completed steps are reversed using **compensation functions**.

---

### Choreography vs Orchestration

TradeDrift uses **choreography-based Saga** — there is no central controller that tells each service what to do. Each service independently listens for events on Kafka and knows its own role.

```
Orchestration (NOT used):
  Central controller → tells Wallet → tells Order → tells Matching → ...

Choreography (USED):
  Wallet reacts to UserVerified
  Order reacts to FundsReserved
  Matching Engine reacts to OrderCreated
  Settlement reacts to TradeExecuted
  (each service only knows its own step)
```

This keeps services fully decoupled — Settlement does not import Order code. It only knows: "When I see a TradeExecuted event, I do my job."

---

### The Full Trade Saga — Happy Path

```
User submits a buy order
        │
        ▼
[Step 1] Wallet Service
  DB Change:  reservation = $500  (status: RESERVED)
  Publishes:  FundsReserved ──► Kafka
        │
        ▼
[Step 2] Order Service
  DB Change:  order status = OPEN
  Publishes:  OrderCreated ──► Kafka
        │
        ▼
[Step 3] Matching Engine
  DB Change:  match recorded, both orders FILLED
  Publishes:  TradeExecuted ──► Kafka
        │
        ▼
[Step 4] Settlement Service
  DB Change:  transfers $500 buyer → seller wallet
              transfers asset seller → buyer
  Publishes:  TradeSettled ──► Kafka
        │
        ▼
[Step 5] Portfolio Service
  DB Change:  buyer holdings +1 BTC, seller holdings -1 BTC
  Publishes:  PortfolioUpdated ──► Kafka
        │
        ▼
[Step 6] Notification Service
  Action:     Sends "Your trade was filled!" to both users
```

---

### The Full Trade Saga — Failure Path (Compensation)

If any step fails, **compensation events** flow backwards to undo what was already done.

Example: Settlement Service database is down at Step 4.

```
[Step 4] Settlement Service
  DB is DOWN ❌
  Publishes:  TradeSettlementFailed ──► Kafka
        │
        ▼
[Compensation Step 4] Order Service receives TradeSettlementFailed
  DB Change:  UPDATE orders SET status = 'FAILED'
  Publishes:  OrderFailed ──► Kafka
        │
        ▼
[Compensation Step 3] Matching Engine receives OrderFailed
  DB Change:  Mark match as CANCELLED, reopen both orders
  Publishes:  MatchCancelled ──► Kafka
        │
        ▼
[Compensation Step 1] Wallet Service receives OrderFailed
  DB Change:  UPDATE wallets SET reserved = 0, balance = balance + 500
  Publishes:  FundsReleased ──► Kafka
        │
        ▼
[Notification] User receives: "Trade failed, funds have been returned"
```

---

### Two Types of Functions Per Service

Every service participating in a Saga has two types of functions:

| Type | Triggered By | What It Does |
|------|-------------|--------------|
| **Forward function** | Happy-path event | Makes DB change, advances the workflow |
| **Compensation function** | Failure event | Reverses its own DB change |

Example in the Wallet Service:

```go
// Forward function — normal happy path
func (s *WalletService) OnOrderCreated(event OrderCreatedEvent) {
    db.ReserveFunds(event.UserID, event.Amount)  // DB change
    kafka.Publish("funds.reserved", ...)          // advance to next step
}

// Compensation function — failure path
func (s *WalletService) OnOrderFailed(event OrderFailedEvent) {
    db.ReleaseFunds(event.UserID, event.Amount)  // REVERSE the DB change
    kafka.Publish("funds.released", ...)          // notify others
}
```

---

### Compensation Must Be Idempotent

Because Kafka guarantees at-least-once delivery, a compensation function may be called more than once. It must safely handle being replayed without doubling the effect.

```go
// Safe compensation — checks state before acting
func ReleaseFunds(userID string, amount float64) {
    wallet := db.GetWallet(userID)
    if wallet.Status == "RESERVED" {
        // safe to release
        db.ReleaseFunds(userID, amount)
    } else {
        // already released — skip silently, do nothing
    }
}
```

Without this check, a double delivery would release funds twice — giving the user double their money back.

---

### How the Saga and Outbox Work Together

The Saga defines the workflow. The Outbox makes each step's event delivery reliable.

Every time a service completes a Saga step, it:
1. Writes its DB change AND the next event to its own outbox in a single transaction.
2. The outbox publisher delivers the event to Kafka reliably.
3. The next service in the Saga reacts to the Kafka event.

```
Settlement completes its step:
  BEGIN TRANSACTION
    UPDATE wallets ...          ← Saga forward DB change
    INSERT INTO outbox (TradeSettled, PENDING)  ← Outbox records the event
  COMMIT

  Outbox publisher → Kafka "trade.settled" ← Portfolio Service reacts
```

---

## Identifier Strategy

Every aggregate uses UUIDv7.

The owning service generates the identifier before persistence.

The same identifier is reused across:

- PostgreSQL
- gRPC
- Kafka
- Logs
- Tracing

---

## Data Ownership

Every service owns its own database.

Services never access another service's database directly.

Communication happens only through:

- gRPC
- Kafka

---

## Service Responsibilities

| Service | Responsibility |
|----------|---------------|
| API Gateway | HTTP entry point |
| Authentication | Identity |
| Order | Order lifecycle |
| Wallet | Balances & reservations |
| Matching Engine | Price-time priority |
| Settlement | Post-match coordination |
| Trade | Trade persistence |
| Portfolio | Holdings & PnL |
| Market | Market data |
| Notification | User notifications |

---

# Concurrency Design & Optimizations

TradeDrift handles concurrent requests across all services. This section documents the concurrency model used in each critical area and the specific optimizations applied.

---

## What is Already Optimized

### Matching Engine — Single Goroutine Per Market

The Matching Engine runs one goroutine per trading pair (BTC_USDT, ETH_USDT, etc.):

```
BTC_USDT goroutine → processes all BTC_USDT events sequentially
ETH_USDT goroutine → processes all ETH_USDT events sequentially
```

Because only one goroutine ever reads or writes a given order book, there is zero lock contention inside the Matching Engine. Kafka partition key = `market_id` guarantees all events for one market always arrive at the same goroutine in the correct order.

This is the Go-idiomatic approach: *"Do not communicate by sharing memory; share memory by communicating."*

### JWT Cache-Aside

Every authenticated request checks Redis before PostgreSQL. At high request rates this eliminates millions of unnecessary DB queries per hour.

### pgxpool Connection Pooling

All services use `pgxpool` to reuse PostgreSQL connections. Without this, every request would open a new TCP connection, which is extremely expensive.

---

## Improvement 1 — Wallet: Optimistic Locking

**The Problem:**

The current `SELECT ... FOR UPDATE` approach serializes all concurrent operations on the same wallet row:

```
Request 1: locks wallet row → processes → commits
Request 2: waits...
Request 3: waits...
Request 50: waits...
```

Under high concurrency (e.g. a user scripting 50 orders per second), all requests queue behind each other. Latency spikes significantly.

**The Solution: Optimistic Locking with a Version Column**

Add a `version` column to the wallets table:

```sql
ALTER TABLE wallets ADD COLUMN version INT NOT NULL DEFAULT 1;
```

Read without any lock:

```sql
SELECT balance, version FROM wallets WHERE user_id = ?
-- No FOR UPDATE — no lock held
```

Update only if the version matches what was read:

```sql
UPDATE wallets
SET balance   = balance - 500,
    version   = version + 1
WHERE user_id = ?
  AND version = 7;  -- must match the version we read
```

If `0 rows affected` → another request updated the row between our read and write → retry the transaction.

**Why this is better:**

- No lock is held between the read and the write.
- Retries only happen on actual conflicts, which are rare under normal load.
- All requests proceed optimistically and concurrently instead of queuing.

---

## Improvement 2 — Outbox Publisher: LISTEN/NOTIFY

**The Problem:**

The current outbox publisher uses a sleep-based polling loop:

```
sleep 100ms → scan DB → sleep 100ms → scan DB → ...
```

This adds up to 250ms of unnecessary event latency even when the database is quiet. Under zero load, it wastes CPU running empty scans constantly.

**The Solution: PostgreSQL LISTEN/NOTIFY**

Add a trigger to the outbox table that fires a notification the moment a new row is inserted:

```sql
CREATE OR REPLACE FUNCTION notify_outbox_insert()
RETURNS TRIGGER AS $$
BEGIN
    PERFORM pg_notify('outbox_ready', NEW.id::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER outbox_insert_trigger
AFTER INSERT ON outbox
FOR EACH ROW EXECUTE FUNCTION notify_outbox_insert();
```

The publisher goroutine wakes up immediately on notification instead of sleeping:

```go
// Publisher listens for notifications — wakes instantly on new row
conn.WaitForNotification(ctx)
// Immediately scan and publish
publishPendingRows()
```

**Why this is better:**

- Event latency drops from ~150ms average to ~1ms.
- No wasted CPU scanning an empty table.
- The publisher only wakes up when there is actual work to do.

---

## Improvement 3 — Auth Service: bcrypt Goroutine Semaphore

**The Problem:**

bcrypt password hashing is intentionally slow (cost factor 10 = ~100ms per operation). Under a login flood:

```
100 concurrent logins
→ 100 goroutines all running bcrypt simultaneously
→ CPU fully saturated
→ All other requests (health checks, JWT validation) slow down
```

**The Solution: Goroutine Semaphore**

Limit the number of concurrent bcrypt operations to the number of available CPU cores:

```go
var bcryptSem = make(chan struct{}, runtime.NumCPU())

func comparePassword(hash, password string) error {
    bcryptSem <- struct{}{}        // acquire slot — blocks if all slots in use
    defer func() { <-bcryptSem }() // release slot when done

    return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
```

**Why this is better:**

- Caps CPU usage from bcrypt at a safe maximum.
- Excess login requests wait in the channel queue instead of all running simultaneously.
- The gRPC server goroutines remain responsive for all other requests.
- Queue wait time is predictable — roughly `(concurrent_logins / NumCPU) * 100ms`.

---

## Improvement 4 — Outbox Publisher: Dead Letter Queue

**The Problem:**

The current FIFO design stops all event processing if one event fails after max retries:

```
Event #1 → Kafka ✅
Event #2 → Kafka ✅
Event #3 → Kafka ❌ (fails 5 times)
→ Event #4, #5, #6 ... all blocked

All users affected because of one bad event
```

**The Solution: Dead Letter Queue (DLQ)**

After max retries, move the failed event to `FAILED` status and continue processing the next event. Fire an alert for human investigation.

```go
func (p *Publisher) publishRow(row OutboxRow) error {
    for attempt := 1; attempt <= p.maxRetries; attempt++ {
        err := p.kafka.Publish(row)
        if err == nil {
            p.db.MarkProcessed(row.ID)
            return nil
        }
        time.Sleep(backoff(attempt))
    }

    // Max retries exhausted — move to DLQ, do NOT block next events
    p.db.MarkFailed(row.ID, "max retries exceeded after Kafka unavailability")
    p.alerting.Fire("outbox_event_failed", row.ID)  // PagerDuty / Grafana alert
    return nil  // Return nil so the publisher continues with the next row
}
```

Failed rows stay in the outbox table with `status = FAILED` and a `failed_reason` for investigation. A developer can re-queue them manually:

```sql
UPDATE outbox SET status = 'PENDING', failed_reason = NULL WHERE id = '...';
```

**Why this is better:**

- One bad event does not block thousands of users.
- The publisher continues processing healthy events immediately.
- Failed events are preserved for investigation, not silently dropped.
- Monitoring can alert on FAILED row count to detect Kafka outages early.

---

## Concurrency Summary

| Component | Concurrency Model | Key Protection |
|---|---|---|
| Matching Engine | Single goroutine per market | No locks needed by design |
| Wallet Service | Optimistic locking + version | Retry on conflict, no queue |
| Auth Service | Goroutine semaphore for bcrypt | CPU-bounded concurrency |
| Outbox Publisher | LISTEN/NOTIFY + DLQ | Low latency, non-blocking failures |
| gRPC Servers | One goroutine per request | pgxpool + DB-level locks |
| JWT Validation | Cache-aside (Redis → PostgreSQL) | Reduces DB load under high traffic |

---

# Design Philosophy

TradeDrift prioritizes:

- Simplicity
- Clear ownership
- Event-driven workflows
- Educational value
- Production-inspired architecture

rather than reproducing every complexity of a real exchange.