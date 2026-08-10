# Order Service — Repository Package (`internal/repository`)

> **Package:** `tradedrift/services/order/internal/repository`  
> **Directory:** `services/order/internal/repository/`  
> **Role:** Persistence Contracts, Domain Models, Sentinel Errors, and Database Access Implementations

---

## 1. Purpose & Architectural Role

The `repository` package owns the **domain entities**, **sentinel error definitions**, and **persistence interfaces** for the Order Service. 

It implements the **Repository Pattern**:
- **Abstraction**: Defines the `OrderRepository` interface so higher layers (`service` package) never execute SQL directly or depend on PostgreSQL driver types.
- **Encapsulation**: Keeps database-specific models, SQL queries, and transaction management inside `repository/postgres/`.
- **Decoupling**: Enables unit testing by mocking the `OrderRepository` interface without needing a live PostgreSQL database.

---

## 2. Files & Directory Structure

```
services/order/internal/repository/
├── README.md                            <-- This documentation file
├── order.go                             <-- Domain Structs, Enums, Errors, & Interface contract
└── postgres/
    └── order_repository.go              <-- PostgreSQL (pgxpool) implementation
```

---

## 3. Packages & Dependencies Used

| Package | Purpose & Rationale |
| :--- | :--- |
| `context` | Manages request cancellation signals and timeouts during database operations. |
| `errors` | Standard library package for defining immutable sentinel errors (`errors.New`). |
| `time` | Timestamps for `created_at` and `updated_at` domain fields. |
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

## 5. Domain Models & Enums (`order.go`)

### 5.1 Enums
* **`OrderSide`**: `SideBuy` (`"BUY"`), `SideSell` (`"SELL"`).
* **`OrderType`**: `TypeLimit` (`"LIMIT"`), `TypeMarket` (`"MARKET"`).
* **`OrderStatus`**: `StatusOpen` (`"OPEN"`), `StatusPartiallyFilled` (`"PARTIALLY_FILLED"`), `StatusFilled` (`"FILLED"`), `StatusCancelling` (`"CANCELLING"`), `StatusCancelled` (`"CANCELLED"`).

### 5.2 Struct `Order`
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

---

## 6. Repository Interface Contract (`order.go`)

```go
type OrderRepository interface {
    FindByIdempotencyKey(ctx context.Context, key string) (*Order, error)
    CreateOrder(ctx context.Context, o *Order, outboxPayload []byte) error
    GetByID(ctx context.Context, orderID, userID string) (*Order, error)
    UpdateStatusToCancelling(ctx context.Context, o *Order, outboxPayload []byte) error
    ListOrders(ctx context.Context, userID, marketID, cursor string, side OrderSide, status OrderStatus, limit int32) ([]*Order, error)
}
```

---

## 7. Function-by-Function Method Analysis (`postgres/order_repository.go`)

### 7.1 `FindByIdempotencyKey(ctx, key)`
* **Purpose**: Looks up an existing order by `idempotency_key`.
* **Behavior**: Runs `SELECT ... FROM orders WHERE idempotency_key = $1`.
* **Special Handling**: If no row matches (`pgx.ErrNoRows`), returns `(nil, nil)` — allowing the caller to distinguish "not found" from actual database connection errors.

### 7.2 `CreateOrder(ctx, order, outboxPayload)`
* **Purpose**: Atomically persists an order row AND an outbox message row inside a single PostgreSQL transaction (`BEGIN ... COMMIT`).
* **Problem Solved (Transactional Outbox Pattern)**: Guarantees that an order event (`OrderCreated`) cannot be published to Kafka unless the order row is committed to the database.
* **Race Condition Guard**: Catches PostgreSQL `*pgconn.PgError` with code `23505` and constraint name `orders_idempotency_key_key`. If two concurrent requests race past `FindByIdempotencyKey`, PostgreSQL rejects the second INSERT, returning `ErrDuplicateIdempotencyKey`.

### 7.3 `GetByID(ctx, orderID, userID)`
* **Purpose**: Retrieves a single order filtered by both `id` AND `user_id`.
* **Security & Isolation**: Multi-tenant protection ensuring User A cannot read User B's order details.

### 7.4 `UpdateStatusToCancelling(ctx, order, outboxPayload)`
* **Purpose**: Transitions order status from `OPEN` / `PARTIALLY_FILLED` $\rightarrow$ `CANCELLING` and inserts an `OrderCancelRequested` outbox event in one atomic transaction.
* **Semantic Error Distinction**: If `res.RowsAffected() == 0`, executes a secondary check `SELECT EXISTS(SELECT 1 FROM orders WHERE id = $1 AND user_id = $2)`:
  - If the order exists (e.g. status is `CANCELLED` or `FILLED`), returns `ErrOrderNotCancellable`.
  - If the order does not exist at all, returns `ErrOrderNotFound`.

### 7.5 `ListOrders(ctx, userID, marketID, cursorStr, side, status, limit)`
* **Purpose**: Serves paginated user order history using **Keyset Cursor Pagination**.
* **Query Pattern**: Constructs dynamic SQL queries filtering by `user_id`, optional `market_id`, `side`, and `status`.
* **Cursor Mechanics**: Decodes the Base64 cursor string into `(created_at, id)`. Appends `AND (created_at, id) < ($N, $N+1)` with `ORDER BY created_at DESC, id DESC LIMIT $M`.
* **Row Check**: Validates `rows.Err()` after scanning to ensure query stream errors are not swallowed.

### 7.6 Helper `decodeCursor(cursorStr)`
* **Purpose**: Decodes a Base64 string formatted as `"2026-08-10T12:00:00Z|order-uuid"`.
* **Error Handling**: Returns `ErrInvalidPaginationCursor` if decoding, delimiter splitting, or RFC3339Nano timestamp parsing fails.
