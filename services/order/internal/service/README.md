# Order Service — Service Package (`internal/service`)

> **Package:** `tradedrift/services/order/internal/service`  
> **Directory:** `services/order/internal/service/`  
> **Role:** Core Business Logic, Financial Validation, Idempotency Rules, & Saga Orchestration

---

## 1. Purpose & Architectural Role

The `service` package contains the **core domain & business logic** for the Order Service. It acts as the central orchestrator between input parameters, database persistence, and external microservices (Wallet Service).

Key responsibilities:
1. **Financial Precision**: Uses `shopspring/decimal` for all asset calculations (prices, quantities, total quote reservations) — **zero floating-point math**.
2. **Order Lifecycle Validation**: Enforces strict `BUY`/`SELL` direction, `LIMIT`/`MARKET` order constraints, and canonical `BASE-QUOTE` pair validation.
3. **Idempotency Guarantee**: Performs parameter comparison for duplicate idempotency keys to ensure matching requests return existing orders while altered requests return `ErrDuplicateIdempotencyKey`.
4. **Leak-Proof Pre-Reservation**: All CPU/local validation steps (UUID generation, struct creation, JSON serialization) execute *before* making network calls to Wallet Service.
5. **Saga Compensating Action**: If PostgreSQL persistence fails after funds are reserved in Wallet Service, `CreateOrder` executes a compensating `ReleaseFunds` call to prevent orphaned locked balances.

---

## 2. Files in This Directory

| File | Role |
| :--- | :--- |
| [`errors.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/service/errors.go) | Defines domain service sentinel errors (`ErrInvalidSide`, `ErrInsufficientFunds`, etc.) |
| [`service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/service/service.go) | Implements the `Service` interface and order processing logic |

---

## 3. Packages & Dependencies Used

| Package | Purpose & Rationale |
| :--- | :--- |
| `context` | Manages request propagation, deadlines, and cancellations across gRPC/DB calls. |
| `encoding/json` | Serializes order event payloads (`OrderCreated`, `OrderCancelRequested`) into JSON for outbox storage. |
| `errors` | Sentinel error comparison (`errors.Is`). |
| `fmt` | Error wrapping (`fmt.Errorf("%w")`) and string formatting. |
| `strings` | Capitalization and delimiter parsing (`strings.Split(market, "-")`). |
| `time` | Generation of UTC creation timestamps. |
| `github.com/google/uuid` | Generates timestamp-sortable **UUIDv7** primary keys in the application layer. |
| `github.com/shopspring/decimal` | **Arbitrary-precision fixed-point decimal arithmetic**. Eliminates binary floating-point representation bugs (e.g. `0.1 + 0.2 != 0.3`). |
| `go.uber.org/zap` | High-performance structured logging. |
| `google.golang.org/grpc/codes` | Intercepts gRPC status codes from external services (`codes.FailedPrecondition`). |
| `google.golang.org/grpc/status` | Converts gRPC status errors. |

---

## 4. Sentinel Errors (`errors.go`)

| Error Variable | Problem Solved |
| :--- | :--- |
| `ErrOrderNotFound` | Order ID does not exist for the user. |
| `ErrInvalidSide` | Direction is not `"BUY"` or `"SELL"`. |
| `ErrInvalidType` | Order type is not `"LIMIT"` or `"MARKET"`. |
| `ErrInvalidMarket` | Market string is not formatted as `"BASE-QUOTE"` (e.g. `"BTC-USDT"`). |
| `ErrInvalidPrice` | Limit order (or Market Buy) contains an invalid or non-positive price string. |
| `ErrInvalidQuantity` | Quantity is invalid or non-positive. |
| `ErrDuplicateIdempotencyKey` | Idempotency key was reused with different request parameters. |
| `ErrInsufficientFunds` | User's available balance in Wallet Service is lower than the required reservation. |
| `ErrOrderNotCancellable` | Order is already `CANCELLED`, `FILLED`, or `CANCELLING`. |
| `ErrInvalidPaginationCursor` | Cursor decoding failed. |

---

## 5. Money Flow & Order Rules Matrix

```
──────────────────────────────────────────────────────────────────────────────────────────
Order Type   | Quantity (Base Asset) | Price Parameter   | Reserved Asset | Reserved Amount
──────────────────────────────────────────────────────────────────────────────────────────
BUY LIMIT    | e.g. "0.1" (BTC)      | Required (Limit)  | Quote (USDT)   | Price × Quantity
BUY MARKET   | e.g. "0.1" (BTC)      | Required (Max Cap)| Quote (USDT)   | Max Price × Quantity
SELL LIMIT   | e.g. "0.1" (BTC)      | Required (Limit)  | Base (BTC)     | Quantity
SELL MARKET  | e.g. "0.1" (BTC)      | Omitted           | Base (BTC)     | Quantity
──────────────────────────────────────────────────────────────────────────────────────────
```

* **Invariant**: `Order.Quantity` **ALWAYS** represents base asset quantity across all 4 order types.

---

## 6. Function & Method Analysis (`service.go`)

### 6.1 `CreateOrder(ctx, params)`
* **Signature:** `(s *orderService) CreateOrder(ctx context.Context, p *CreateOrderParams) (*repository.Order, error)`
* **Step-by-Step Flow**:
  1. **Validation**: Checks `p.Side` (`BUY`/`SELL`) and `p.OrderType` (`LIMIT`/`MARKET`).
  2. **Idempotency Lookup**: Calls `repo.FindByIdempotencyKey`. If found:
     - Runs `sameRequest(...)` comparing `UserID`, `MarketID`, `Side`, `OrderType`, `Quantity`, and `Price`.
     - Returns existing order if parameters match; returns `ErrDuplicateIdempotencyKey` if altered.
  3. **Market Parsing**: Parses `"BTC-USDT"` into `base = "BTC"`, `quote = "USDT"`.
  4. **Decimal Validation**: Parses `p.Quantity` and `p.Price` using `decimal.NewFromString`.
  5. **Reservation Math**:
     - `BUY` orders: `reserveAsset = quoteAsset`, `reserveAmount = (priceDec * qty).StringFixed(10)`.
     - `SELL` orders: `reserveAsset = baseAsset`, `reserveAmount = qty.StringFixed(10)`.
  6. **UUIDv7 Generation**: Generates application-level `uuid.NewV7()`.
  7. **Pre-Network Struct & Outbox Assembly**: Constructs `repository.Order` and serializes `payloadBytes` (`json.Marshal`).
  8. **Wallet Fund Reservation**: Calls `s.wallet.ReserveFunds(...)` over gRPC. Converts `codes.FailedPrecondition` to `ErrInsufficientFunds`.
  9. **Database Persistence & Saga Compensation**: Executes `s.repo.CreateOrder(...)`.
     - **Compensating Action**: If PostgreSQL insertion fails after funds were reserved, triggers `s.wallet.ReleaseFunds(ctx, orderID.String())` immediately!

### 6.2 `CancelOrder(ctx, orderID, userID)`
* **Signature:** `(s *orderService) CancelOrder(ctx context.Context, orderID, userID string) (*repository.Order, error)`
* **Flow**: Checks that order status is `OPEN` or `PARTIALLY_FILLED`. Constructs `OrderCancelRequested` JSON payload and calls `s.repo.UpdateStatusToCancelling`.

### 6.3 `GetOrder(ctx, orderID, userID)`
* **Signature:** `(s *orderService) GetOrder(ctx context.Context, orderID, userID string) (*repository.Order, error)`
* **Flow**: Fetches order from repository and translates `repository.ErrOrderNotFound` to `service.ErrOrderNotFound`.

### 6.4 `ListOrders(ctx, userID, marketID, cursor, side, status, limit)`
* **Signature:** `(s *orderService) ListOrders(...) ([]*repository.Order, error)`
* **Flow**: Queries repository and converts `repository.ErrInvalidPaginationCursor` to `service.ErrInvalidPaginationCursor`.

### 6.5 Helper `sameRequest(existing, p)`
* **Purpose**: Performs full equality checks on `UserID`, `MarketID`, `Side`, `OrderType`, `Quantity` (via `quantitiesEqual`), and `Price` (via `pricesEqual`).

### 6.6 Helper `pricesEqual(p1, p2)` / `quantitiesEqual(q1, q2)`
* **Purpose**: Compares numeric decimal equality rather than string matching (e.g. `"60000.0000000000"` equals `"60000"`).
