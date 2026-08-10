# Order Service — Repository Package (`internal/repository`)

> **Package:** `tradedrift/services/order/internal/repository`  
> **Directory:** `services/order/internal/repository/`  
> **Role:** Persistence Contracts, Domain Models, Outbox Models, Sentinel Errors, and Database Access Implementations

---

## 1. Purpose & Architectural Role

The `repository` package owns the **domain entities**, **outbox models**, **sentinel error definitions**, and **persistence interfaces** for the Order Service. 

It implements the **Repository Pattern**:
- **Abstraction**: Defines `OrderRepository` and `OutboxRepository` interfaces so higher layers (`service` and `kafka` packages) never execute SQL directly or depend on PostgreSQL driver types.
- **Encapsulation**: Keeps database-specific models, SQL queries, and transaction management inside `repository/postgres/`.
- **Decoupling**: Enables unit testing by mocking repository interfaces without needing a live PostgreSQL database.

---

## 2. Files & Directory Structure

```
services/order/internal/repository/
├── README.md                            <-- This documentation file
├── order.go                             <-- Domain Structs, Enums, Errors, & Interface contract
├── outbox.go                            <-- Outbox Entity Model & OutboxRepository interface
└── postgres/
    ├── order_repository.go              <-- PostgreSQL order CRUD queries & transactions
    └── outbox_repository.go             <-- PostgreSQL outbox worker atomic claims & backoff retries
```

---

## 3. Packages & Dependencies Used

| Package | Purpose & Rationale |
| :--- | :--- |
| `context` | Manages request cancellation signals and timeouts during database operations. |
| `errors` | Standard library package for defining immutable sentinel errors (`errors.New`). |
| `time` | Timestamps for `created_at`, `updated_at`, `published_at`, and `processing_at` fields. |
| `encoding/base64` | Encodes/decodes URL-safe Base64 strings for keyset pagination cursors. |
| `strconv` | Formats dynamic SQL positional parameters (`$1`, `$2`, `$3`). |
| `strings` | Splitting and parsing cursor token strings (`timestamp|id`). |
| `github.com/jackc/pgx/v5` | High-performance PostgreSQL driver for Go, providing binary protocol support and efficient row scanning. |
| `github.com/jackc/pgx/v5/pgconn` | Low-level PostgreSQL protocol error Inspector (`*pgconn.PgError`) for checking SQL error codes (e.g. `23505` unique violation). |
| `github.com/jackc/pgx/v5/pgxpool` | Thread-safe connection pool manager for PostgreSQL. |
| `go.uber.org/zap` | Structured, high-performance logger for recording database warnings and query failures. |

---

## 4. Sentinel Errors (`order.go`)

| Error Variable | Message | When Triggered |
| :--- | :--- | :--- |
| `ErrOrderNotFound` | `"order not found"` | Returned by `GetByID` or `scanOrder` when `pgx.ErrNoRows` is encountered. |
| `ErrOrderNotCancellable` | `"order cannot be cancelled in its current status"` | Returned by `UpdateStatusToCancelling` when an order exists but is already `CANCELLING`, `CANCELLED`, or `FILLED`. |
| `ErrDuplicateIdempotencyKey` | `"idempotency key already exists"` | Returned when a PostgreSQL unique constraint violation (`23505`) occurs on `orders_idempotency_key_key`. |
| `ErrInvalidPaginationCursor` | `"invalid pagination cursor"` | Returned when a client passes an invalid or malformed Base64 cursor string to `ListOrders`. |

---

## 5. Domain & Outbox Models (`order.go`, `outbox.go`)

### 5.1 Enums (`order.go`)
* **`OrderSide`**: `SideBuy` (`"BUY"`), `SideSell` (`"SELL"`).
* **`OrderType`**: `TypeLimit` (`"LIMIT"`), `TypeMarket` (`"MARKET"`).
* **`OrderStatus`**: `StatusOpen` (`"OPEN"`), `StatusPartiallyFilled` (`"PARTIALLY_FILLED"`), `StatusFilled` (`"FILLED"`), `StatusCancelling` (`"CANCELLING"`), `StatusCancelled` (`"CANCELLED"`).

### 5.2 Struct `Order` (`order.go`)
```go
type Order struct {
    ID                string      // UUIDv7 Primary Key
    UserID            string      // Logical reference to owner (Auth Service)
    MarketID          string      // e.g. "BTC-USDT"
    Side              OrderSide   // BUY | SELL
    OrderType         OrderType   // LIMIT | MARKET
    Price             *string     // Decimal string (nil for pure MARKET orders)
    Quantity          string      // Base asset quantity (Decimal string)
    FilledQuantity    string      // Executed quantity so far (Decimal string)
    RemainingQuantity string      // Unfilled quantity (Decimal string)
    Status            OrderStatus // Current order lifecycle state
    IdempotencyKey    *string     // Client-supplied UUID key (nil for non-idempotent orders)
    CreatedAt         time.Time   // Database creation timestamp
    UpdatedAt         time.Time   // Database last update timestamp
}
```

### 5.3 Struct `OutboxEvent` (`outbox.go`)
```go
type OutboxEvent struct {
    ID           string     // UUID Primary Key
    AggregateID  string     // Order ID
    EventType    string     // "OrderCreated" or "OrderCancelRequested"
    Payload      []byte     // JSON payload
    PartitionKey string     // market_id (e.g. "BTC-USDT")
    PublishedAt  *time.Time // Timestamp when Kafka ACK is received
    ProcessingAt *time.Time // Lease timestamp set during worker claims
    Attempts     int        // Number of retry attempts
    LastError    *string    // Failure error message
    CreatedAt    time.Time  // Outbox creation timestamp
}
```

---

## 6. Repository Interface Contracts

### 6.1 `OutboxRepository` (`outbox.go`)
```go
type OutboxRepository interface {
    GetUnpublishedOutboxEvents(ctx context.Context, limit int) ([]*OutboxEvent, error)
    MarkOutboxEventAsPublished(ctx context.Context, id string) error
    RecordOutboxPublishError(ctx context.Context, id string, errMsg string) error
}
```

### 6.2 `OrderRepository` (`order.go`)
```go
type OrderRepository interface {
    OutboxRepository

    FindByIdempotencyKey(ctx context.Context, key string) (*Order, error)
    CreateOrder(ctx context.Context, o *Order, outboxPayload []byte) error
    GetByID(ctx context.Context, orderID, userID string) (*Order, error)
    UpdateStatusToCancelling(ctx context.Context, o *Order, outboxPayload []byte) error
    ListOrders(ctx context.Context, userID, marketID, cursor string, side OrderSide, status OrderStatus, limit int32) ([]*Order, error)
}
```

---

## 7. Function-by-Function Method Analysis (`postgres/order_repository.go` & `postgres/outbox_repository.go`)

### 7.1 `FindByIdempotencyKey(ctx, key)` (`order_repository.go`)
* **Purpose**: Looks up an existing order by `idempotency_key`. Returns `(nil, nil)` if no row matches.

### 7.2 `CreateOrder(ctx, order, outboxPayload)` (`order_repository.go`)
* **Purpose**: Atomically persists an order row AND an outbox message row inside a single PostgreSQL transaction (`BEGIN ... COMMIT`).

### 7.3 `GetByID(ctx, orderID, userID)` (`order_repository.go`)
* **Purpose**: Retrieves a single order filtered by both `id` AND `user_id`.

### 7.4 `UpdateStatusToCancelling(ctx, order, outboxPayload)` (`order_repository.go`)
* **Purpose**: Transitions order status from `OPEN` / `PARTIALLY_FILLED` $\rightarrow$ `CANCELLING` and inserts an `OrderCancelRequested` outbox event atomically.

### 7.5 `ListOrders(ctx, userID, marketID, cursorStr, side, status, limit)` (`order_repository.go`)
* **Purpose**: Serves paginated user order history using **Keyset Cursor Pagination**.

### 7.6 `GetUnpublishedOutboxEvents(ctx, limit)` (`outbox_repository.go`)
* **Purpose**: Claims up to `limit` unpublished outbox events for worker delivery using an **atomic claim query**:
  ```sql
  UPDATE outbox
  SET processing_at = NOW(), attempts = attempts + 1
  WHERE id IN (
      SELECT id FROM outbox
      WHERE published_at IS NULL
        AND (processing_at IS NULL OR processing_at < NOW())
      ORDER BY created_at ASC
      LIMIT $1
      FOR UPDATE SKIP LOCKED
  )
  RETURNING id, aggregate_id, event_type, payload, partition_key, published_at, processing_at, attempts, last_error, created_at
  ```
* **Multi-Instance Safety**: Uses `FOR UPDATE SKIP LOCKED` inside the `UPDATE` subquery so concurrent replicas never claim the same events. Stale leases (`processing_at < NOW()`) are automatically reclaimed.

### 7.7 `MarkOutboxEventAsPublished(ctx, id)` (`outbox_repository.go`)
* **Purpose**: Updates `published_at = NOW()`, `processing_at = NULL`, and `last_error = NULL` after receiving Kafka delivery ACK.

### 7.8 `RecordOutboxPublishError(ctx, id, errMsg)` (`outbox_repository.go`)
* **Purpose**: Records `last_error = errMsg` and sets `processing_at = NOW() + (INTERVAL '1 second' * LEAST(attempts, 60))`, enforcing **linear retry backoff** (1s, 2s, 3s... up to 60s max).
