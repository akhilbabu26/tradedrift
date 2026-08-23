# Settlement Service — Repository Layer (`internal/repository`)

> **Packages:** `tradedrift/services/settlement/internal/repository` · `tradedrift/services/settlement/internal/repository/postgres`  
> **Files:** `repository.go` (interface + domain entity) · `postgres/repository.go` (PostgreSQL implementation)  
> **Design Patterns:** Repository Pattern, Interface Segregation, Short-Lived Transactions

---

## 1. Purpose & Architectural Role

The `repository` package owns two responsibilities:

1. **Domain Entity** — `SettledTrade` is the single struct that represents one row in the `settled_trades` ledger. It carries all state needed by the 3-phase pipeline.
2. **Repository Interface** — `Repository` defines the data access contract. The service layer depends on this interface, not on the concrete PostgreSQL driver, so business logic can be tested with a lightweight in-memory mock.

The concrete implementation lives in `postgres/repository.go` and uses `pgxpool` — but the service never imports that sub-package directly; it receives the interface via dependency injection in `main.go`.

---

## 2. File: `repository.go` — Domain Entity + Interface

### Status Constants

```go
const (
    StatusPending = "PENDING"
    StatusSettled = "SETTLED"
)
```

**Why constants instead of a Go enum?**  
PostgreSQL stores the value as a `VARCHAR(16)` with a `CHECK(status IN ('PENDING','SETTLED'))` constraint. Using plain string constants keeps the round-trip (Go → DB → Go) zero-allocation with no serialization step. An `iota` enum would require a `String()` method and an extra mapping layer.

---

### Domain Entity: `SettledTrade`

```go
type SettledTrade struct {
    TradeID      uuid.UUID
    BuyerID      uuid.UUID
    SellerID     uuid.UUID
    BuyOrderID   uuid.UUID
    SellOrderID  uuid.UUID
    MarketID     string
    BaseAsset    string     // e.g. "BTC"  — parsed from MarketID by service layer
    QuoteAsset   string     // e.g. "USDT" — parsed from MarketID by service layer
    Price        string     // decimal as string — exact representation, no IEEE 754 rounding
    Quantity     string     // decimal as string
    Status       string     // StatusPending | StatusSettled
    ExecutedAt   time.Time  // copied verbatim from TradeExecuted Kafka event
    SettledAt    *time.Time // nil until Phase 3 completes
}
```

**Why `Price` and `Quantity` as `string`?**  
The Matching Engine serializes them as exact decimal strings (e.g. `"50000.12345678"`) to avoid IEEE 754 floating-point rounding. Keeping them as `string` throughout the settlement service preserves that exactness end-to-end. The Wallet Service stores them as `DECIMAL(30,10)` in PostgreSQL.

**Why `SettledAt *time.Time` (pointer)?**  
It is `NULL` in the database until Phase 3 sets it via `NOW()`. A pointer naturally maps to SQL `NULL` — a zero `time.Time` would be ambiguous.

---

### Repository Interface

```go
type Repository interface {
    Insert(ctx context.Context, t *SettledTrade) error
    FindByTradeID(ctx context.Context, id uuid.UUID) (*SettledTrade, error)
    MarkSettled(ctx context.Context, id uuid.UUID) error
    FindStalePending(ctx context.Context, olderThan time.Duration, limit int) ([]*SettledTrade, error)
}
```

#### `Insert(ctx, t) error`
**Purpose:** Phase 1 of the 3-phase pipeline. Records the trade in the ledger as `PENDING` before the gRPC call begins.  
**Why:** Establishes a durable record before any side effects. If the process crashes, the `PENDING` row is proof that Phase 1 completed and Phase 2 needs to be retried.  
**Key property:** Uses `ON CONFLICT (trade_id) DO NOTHING` in the implementation — concurrent inserts of the same `trade_id` (e.g. Kafka partition rebalance) are silently absorbed. No error is returned, no duplicate row is created.

#### `FindByTradeID(ctx, id) (*SettledTrade, error)`
**Purpose:** Idempotency check at the start of every `Settle()` call. Determines which phase to resume from.  
**Why returns `nil, nil` instead of an error on not-found:** Lets the caller use a simple `if existing == nil` check without importing `pgx` error types. Missing row is not an error — it just means Phase 1 hasn't run yet.  
**Decision table:**

| Return value | Meaning |
|---|---|
| `nil, nil` | Trade not in DB — run Phase 1 → Phase 2 → Phase 3 |
| `{Status: PENDING}, nil` | Phase 1 done, Phase 2 failed — skip Phase 1, retry Phase 2 |
| `{Status: SETTLED}, nil` | All phases done — return nil immediately, safe to ACK Kafka |
| `nil, err` | Database error — return error, do not ACK Kafka |

#### `MarkSettled(ctx, id) error`
**Purpose:** Phase 3 of the 3-phase pipeline. Atomically transitions the record from `PENDING` → `SETTLED` and records `settled_at = NOW()`.  
**Why `AND status = 'PENDING'`:** Prevents overwriting an already-settled record if the consumer and recovery goroutine race to Phase 3 concurrently.  
**Why `RowsAffected() == 0` does a follow-up SELECT:** Zero rows affected is ambiguous — the trade could be already `SETTLED` (safe, idempotent) or completely missing (programming bug). The follow-up SELECT distinguishes the two cases. Only a truly missing `trade_id` returns an error.

#### `FindStalePending(ctx, olderThan, limit) ([]*SettledTrade, error)`
**Purpose:** Used by the 60-second recovery goroutine to find trades stuck in `PENDING` state.  
**Why `created_at` not `executed_at`:** `executed_at` is the time the Matching Engine matched the trade — if Kafka delivers a message late (e.g. after a partition lag), the trade immediately appears "stale" even though settlement has not had a chance to start. `created_at` records when *this service* first registered the trade, so "stale" means "stuck in our system for 60+ seconds" — which is the correct signal.  
**Why `FOR UPDATE SKIP LOCKED`:** Prevents two concurrent callers from selecting the same rows. `SKIP LOCKED` means callers skip rows already locked by concurrent `UPDATE` statements — no blocking, no deadlocks.  
**Important limitation:** The row lock is held only for the duration of this `SELECT`. Since `Wallet.SettleTrade` is called after this function returns (outside any DB transaction), the lock is already released before the gRPC call. Duplicate gRPC calls are theoretically possible. **Wallet trade_id idempotency is the authoritative guard against double-settlement**, not `FOR UPDATE SKIP LOCKED`.

---

## 3. File: `postgres/repository.go` — PostgreSQL Implementation

**Package:** `tradedrift/services/settlement/internal/repository/postgres`  
**Driver:** `github.com/jackc/pgx/v5`  
**Pool:** `github.com/jackc/pgx/v5/pgxpool`

### Constructor: `NewRepository(db *pgxpool.Pool) *Repository`

**Purpose:** Creates a new PostgreSQL repository wired to an existing connection pool.  
**Why `*pgxpool.Pool` not `*pgx.Conn`:** A pool manages multiple connections automatically. Settlement holds connections only for brief Phase 1 (INSERT) and Phase 3 (UPDATE) transactions — after each transaction commits, the connection is returned to the pool immediately. This keeps concurrent settlement throughput high even with a small pool (default: 10 connections).  
**Why called once in `main.go` and shared:** `pgxpool` is goroutine-safe by design. Both the Kafka consumer goroutine and the recovery goroutine can call repository methods concurrently without any additional locking.

```go
func NewRepository(db *pgxpool.Pool) *Repository
```

---

### `Insert` — Phase 1 SQL

```sql
INSERT INTO settled_trades (
    trade_id, buyer_id, seller_id, buy_order_id, sell_order_id,
    market_id, base_asset, quote_asset, price, quantity,
    status, executed_at
) VALUES ($1, $2, ..., $12)
ON CONFLICT (trade_id) DO NOTHING
```

**Why `ON CONFLICT DO NOTHING`:** If two goroutines race to insert the same `trade_id` (possible during a Kafka partition rebalance overlap), the second `INSERT` becomes a silent no-op instead of returning a unique-constraint error. Both goroutines safely proceed to Phase 2 where Wallet idempotency absorbs any duplicate.

---

### `FindByTradeID` — Idempotency Query

```sql
SELECT trade_id, buyer_id, seller_id, buy_order_id, sell_order_id,
       market_id, base_asset, quote_asset, price, quantity,
       status, executed_at, settled_at
FROM settled_trades
WHERE trade_id = $1
```

**Why `errors.Is(err, pgx.ErrNoRows)` → `return nil, nil`:** Translates the pgx driver's not-found sentinel into a clean `(nil, nil)` return so the service layer can check `if existing == nil` without importing `pgx`. Keeps the service layer decoupled from the database driver.

---

### `MarkSettled` — Phase 3 SQL

```sql
UPDATE settled_trades
SET    status = 'SETTLED', settled_at = NOW()
WHERE  trade_id = $1
  AND  status = 'PENDING'
```

**Why `AND status = 'PENDING'`:** Guards the `PENDING → SETTLED` state transition. A concurrent retry that already settled the row will affect 0 rows — this is detected via `tag.RowsAffected()`.

**Why the follow-up `FindByTradeID` on 0 rows affected:**
```
RowsAffected == 0
       ↓
FindByTradeID
       ├── row found, Status==SETTLED → return nil   (safe idempotent retry)
       └── row missing               → return error  (programming bug — Phase 1 must always precede Phase 3)
```

---

### `FindStalePending` — Recovery Query

```sql
SELECT ...
FROM settled_trades
WHERE status = 'PENDING'
  AND created_at < NOW() - ($2 || ' seconds')::INTERVAL
ORDER BY created_at ASC
LIMIT $3
FOR UPDATE SKIP LOCKED
```

**Why `($2 || ' seconds')::INTERVAL`:** `pgx` doesn't natively support binding `time.Duration` as a PostgreSQL `INTERVAL`. Converting seconds (an integer) to an interval string is the simplest compatible approach.  
**Why `ORDER BY created_at ASC`:** Processes the oldest stuck trades first (FIFO), minimising the maximum age of unresolved settlements.

---

### Compile-Time Interface Check

```go
var _ repository.Repository = (*Repository)(nil)
```

**Why:** Causes a compile error (not a runtime panic) if `postgres.Repository` is missing any method required by the `repository.Repository` interface. Ensures the implementation stays in sync with the interface contract across refactors.

---

## 4. External Packages Used

| Package | Used In | Why |
|---|---|---|
| `github.com/jackc/pgx/v5` | `postgres/repository.go` | Zero-allocation PostgreSQL driver; `pgx.ErrNoRows` for not-found detection |
| `github.com/jackc/pgx/v5/pgxpool` | `postgres/repository.go` | Connection pooling — goroutine-safe, connections held only for short TX |
| `github.com/google/uuid` | `repository.go`, `postgres/repository.go` | UUID v4/v7 type for all primary keys and foreign keys |
