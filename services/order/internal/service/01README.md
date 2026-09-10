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

## 2. Deep Dive: Idempotency & Financial Protection

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
   - PostgreSQL enforces `idempotency_key UUID UNIQUE`. Even if two parallel retries hit two different Order Service microservice instances at the exact same microsecond, PostgreSQL rejects the second INSERT with error code `23505`, guaranteeing **atomic duplicate prevention**.

---

## 3. Files in This Directory

| File | Role |
| :--- | :--- |
| [`errors.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/service/errors.go) | Defines domain service sentinel errors (`ErrInvalidSide`, `ErrInsufficientFunds`, `ErrPriceBandExceeded`, etc.) |
| [`price_filter.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/service/price_filter.go) | Pre-trade price band & slippage guard evaluating orders against live Redis MM quotes |
| [`price_filter_test.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/service/price_filter_test.go) | Comprehensive unit tests for price deviation bounds and filter failure JSON structures |
| [`service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/service/service.go) | Implements the `Service` interface and order processing orchestration logic |
| [`validation.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/service/validation.go) | Parameter validation, decimal price/quantity equality helpers (`sameRequest`, `pricesEqual`), and market format parsers |

---

## 4. Packages & Dependencies Used

| Package | Purpose & Rationale |
| :--- | :--- |
| `context` | Manages request propagation, deadlines, and cancellations across gRPC/DB calls. |
| `encoding/json` | Serializes order event payloads (`OrderCreated`, `OrderCancelRequested`) into JSON for outbox storage. |
| `errors` | Sentinel error comparison (`errors.Is`). |
| `fmt` | Error wrapping (`fmt.Errorf("%w")`) and string formatting. |
| `strings` | Capitalization and delimiter parsing (`strings.Split(market, "-")`). |
| `time` | Generation of UTC creation timestamps. |
| `github.com/google/uuid` | Generates timestamp-sortable **UUIDv7** primary keys in the application layer. |
| `github.com/redis/go-redis/v9` | Low-latency querying of live Market Maker quotes for pre-trade price verification. |
| `github.com/shopspring/decimal` | **Arbitrary-precision fixed-point decimal arithmetic**. Eliminates binary floating-point representation bugs (e.g. `0.1 + 0.2 != 0.3`). |
| `go.uber.org/zap` | High-performance structured logging. |
| `google.golang.org/grpc/codes` | Intercepts gRPC status codes from external services (`codes.FailedPrecondition`). |
| `google.golang.org/grpc/status` | Converts gRPC status errors. |

---

## 5. Sentinel Errors (`errors.go`)

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
| `ErrInvalidPriceBand` | Price filter parameters or MM quotes are invalid/missing. |
| `ErrPriceBandExceeded` | Order price violates the percentage price band deviation limit (`FILTER_FAILURE_PERCENT_PRICE`). |

---

## 6. Money Flow & Order Rules Matrix

```
──────────────────────────────────────────────────────────────────────────────────────────────────
Order Type   | Quantity (Base Asset) | Price Parameter   | Reserved Asset | Reservation Rule
──────────────────────────────────────────────────────────────────────────────────────────────────
BUY LIMIT    | e.g. "0.1" (BTC)      | Required (Limit)  | Quote (USDT)   | RoundCeil(Price × Qty, Decimals)
BUY MARKET   | e.g. "0.1" (BTC)      | Required (Max Cap)| Quote (USDT)   | RoundCeil(Max Price × Qty, Decimals)
SELL LIMIT   | e.g. "0.1" (BTC)      | Required (Limit)  | Base (BTC)     | Truncate(Qty, Decimals)
SELL MARKET  | e.g. "0.1" (BTC)      | Omitted           | Base (BTC)     | Truncate(Qty, Decimals)
──────────────────────────────────────────────────────────────────────────────────────────────────
```

* **Precision Guarantee**: Quote asset reservation amount is rounded **UP** (`RoundCeil`) to the quote asset's defined precision (e.g., 2 decimals for USDT) via `getAssetDecimals()` to guarantee reserved funds cover execution fees and rounding dust.

---

## 7. Function & Method Analysis (`service.go`, `price_filter.go` & `validation.go`)

### 7.1 `CreateOrder(ctx, params)` (`service.go`)
* **Signature:** `(s *orderService) CreateOrder(ctx context.Context, p *CreateOrderParams) (*repository.Order, error)`
* **Step-by-Step Flow**:
  1. **Validation**: Checks `p.Side` (`BUY`/`SELL`) and `p.OrderType` (`LIMIT`/`MARKET`).
  2. **Idempotency Lookup**: Calls `repo.FindByIdempotencyKey`. If found:
     - Runs `sameRequest(...)` comparing `UserID`, `MarketID`, `Side`, `OrderType`, `Quantity`, and `Price`.
     - Returns existing order if parameters match; returns `ErrDuplicateIdempotencyKey` if altered.
  3. **Market Parsing**: Parses `"BTC-USDT"` into `base = "BTC"`, `quote = "USDT"`.
  4. **Decimal Validation**: Parses `p.Quantity` and `p.Price` using `decimal.NewFromString`.
  4.5. **Pre-Trade Price Band & Slippage Protection**: Invokes `s.priceFilter.ValidatePriceBand(ctx, market, side, orderType, priceDec)` if order price is positive. If the price deviates beyond `max_price_deviation` from MM quotes in Redis, halts and returns `FILTER_FAILURE_PERCENT_PRICE`.
  5. **Exact Reservation Math**:
     - `BUY` orders: `reserveAsset = quoteAsset`, `reserveAmount = totalQuote.RoundCeil(decimals).StringFixed(decimals)`.
     - `SELL` orders: `reserveAsset = baseAsset`, `reserveAmount = qty.Truncate(decimals).StringFixed(decimals)`.
  6. **UUIDv7 Generation**: Generates application-level `uuid.NewV7()`.
  7. **Outbox Serialization & MM Account Branching**:
     - Standard User: Serializes `OrderCreated` payload for transactional outbox.
     - System MM Account (`00000000-0000-0000-0000-000000000001`): Skips outbox enqueue (LE publishes directly) and skips wallet `ReserveFunds` (inventory managed in LE).
  8. **Wallet Fund Reservation**: For non-MM accounts, calls `s.wallet.ReserveFunds(...)` over gRPC. Converts `codes.FailedPrecondition` to `ErrInsufficientFunds`.
  9. **Database Persistence & Saga Compensation**: Executes `s.repo.CreateOrder(...)`.
     - **Compensating Action**: If PostgreSQL insertion fails after funds were reserved, triggers `s.wallet.ReleaseFunds(ctx, orderID.String())` immediately!

### 7.2 `PriceFilter` Implementation (`price_filter.go`)
* **Interface `PriceFilter`**:
  ```go
  type PriceFilter interface {
      ValidatePriceBand(ctx context.Context, market string, side repository.OrderSide, orderType repository.OrderType, price decimal.Decimal) error
  }
  ```
* **Redis Key**: Fetches current quotes from Redis key `orderbook:ticker:{market}` or MM stream.
* **Calculation**:
  - `BUY`: Rejects if `price > best_ask * (1 + max_deviation)`.
  - `SELL`: Rejects if `price < best_bid * (1 - max_deviation)`.
* **Structured Error**: Emits error formatted with code `FILTER_FAILURE_PERCENT_PRICE` and metadata for client consumption.

### 7.3 `CancelOrder(ctx, orderID, userID)` (`service.go`)
* **Signature:** `(s *orderService) CancelOrder(ctx context.Context, orderID, userID string) (*repository.Order, error)`
* **Flow**: Checks that order status is `OPEN` or `PARTIALLY_FILLED`. Constructs `OrderCancelRequested` JSON payload and calls `s.repo.UpdateStatusToCancelling`.

### 7.4 `GetOrder(ctx, orderID, userID)` (`service.go`)
* **Signature:** `(s *orderService) GetOrder(ctx context.Context, orderID, userID string) (*repository.Order, error)`
* **Flow**: Fetches order from repository and translates `repository.ErrOrderNotFound` to `service.ErrOrderNotFound`.

### 7.5 `ListOrders(ctx, userID, marketID, cursor, side, status, fromTime, toTime, limit)` (`service.go`)
* **Signature:** `(s *orderService) ListOrders(...) ([]*repository.Order, error)`
* **Flow**: Queries repository and converts `repository.ErrInvalidPaginationCursor` to `service.ErrInvalidPaginationCursor`.

### 7.6 Precision & Equality Helpers (`validation.go`)
* `sameRequest(existing, p)`: Performs full equality checks on `UserID`, `MarketID`, `Side`, `OrderType`, `Quantity` (via `quantitiesEqual`), and `Price` (via `pricesEqual`).
* `pricesEqual(p1, p2)` / `quantitiesEqual(q1, q2)`: Compares numeric decimal equality rather than string matching.
* `parseMarketID(market)`: Parses `"BTC-USDT"` into base asset (`"BTC"`) and quote asset (`"USDT"`).
* `getAssetDecimals(asset)`: Returns canonical precision decimals (USDT: 2, BTC/ETH: 8, SOL: 9).
