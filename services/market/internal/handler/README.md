# Market Service — gRPC Presentation Layer (`internal/handler`)

> **Package:** `tradedrift/services/market/internal/handler`  
> **Directory:** `services/market/internal/handler/`  
> **Protocol:** gRPC over HTTP/2  
> **Service Interface:** `marketv1.MarketServiceServer`  
> **Primary Design Patterns:** Controller Pattern, Data Transfer Object (DTO) Mapper, Error Adapter

---

## 1. 🎯 Purpose & Architectural Role

The `handler` package serves as the **Northbound Presentation Layer** of the Market Service. In Clean Architecture / Hexagonal Architecture, handlers act as **Driving Adapters** that bridge the gap between external network protocols (gRPC/HTTP/2) and internal core business logic (`internal/service`).

### Why Is This Layer Necessary?
1. **Protocol Decoupling:** The domain business layer (`internal/service`) knows nothing about Protobuf or gRPC status codes. The handler isolates Protobuf contracts so changing internal domain structs never breaks external API contracts.
2. **Input Validation & Sanitization:** Checks incoming RPC messages for missing or invalid parameters before passing them to the service.
3. **Information Shielding:** Translates internal database errors (`pq: connection refused` or `sql: no rows`) into standard, safe gRPC status codes (`codes.NotFound`, `codes.InvalidArgument`) so internal database structures are never leaked to external clients.

---

## 2. 📂 Files in this Package

```
services/market/internal/handler/
├── grpc.go       <-- RPC controller methods (ListMarkets, GetMarket, GetTicker, GetCandles)
├── mapper.go     <-- Domain-to-Protobuf converters & gRPC status error adapter
└── README.md     <-- This comprehensive documentation
```

---

## 3. 🔍 Deep-Dive: File 1 — `grpc.go` (RPC Controller)

### 🏗️ Struct Definition: `GRPCHandler`
```go
type GRPCHandler struct {
    marketv1.UnimplementedMarketServiceServer
    svc service.MarketService
    log *zap.Logger
}
```
* **`UnimplementedMarketServiceServer`:** Embedded for forward compatibility as required by gRPC Go. If new RPC methods are added to `market.proto` in the future, the server compiles without breaking existing implementations.
* **`svc service.MarketService`:** Interface reference to the domain service layer. Handlers depend on abstractions (interfaces), not concrete implementations (Dependency Inversion Principle).
* **`log *zap.Logger`:** Structured logger for error tracking and request diagnostics.

---

### 🛠️ Method-by-Method Detailed Breakdown

#### 1. `ListMarkets(ctx context.Context, req *marketv1.ListMarketsRequest) (*marketv1.ListMarketsResponse, error)`
* **Purpose:** Returns the complete catalog of currency pairs traded on the exchange along with their trading rules.
* **Execution Flow:**
  1. Calls `h.svc.ListMarkets(ctx)`.
  2. If an error occurs, delegates to `ToGRPCError(err, h.log, "ListMarkets")`.
  3. Loops over the returned `[]*domain.Market` entities and converts each entity to `*marketv1.Market` using `ToProtoMarket()`.
  4. Returns `&marketv1.ListMarketsResponse{Markets: protoMarkets}, nil`.

#### 2. `GetMarket(ctx context.Context, req *marketv1.GetMarketRequest) (*marketv1.GetMarketResponse, error)`
* **Purpose:** Fetches configuration rules and status for a single market pair (e.g. `"BTC-USDT"`).
* **Execution Flow:**
  1. Validates that `req.GetMarketId()` is not empty. If empty, returns `status.Error(codes.InvalidArgument, "market_id is required")`.
  2. Calls `h.svc.GetMarket(ctx, req.GetMarketId())`.
  3. If market is not found in database, `ToGRPCError` returns `codes.NotFound`.
  4. Returns `&marketv1.GetMarketResponse{Market: ToProtoMarket(m)}, nil`.

#### 3. `GetTicker(ctx context.Context, req *marketv1.GetTickerRequest) (*marketv1.GetTickerResponse, error)`
* **Purpose:** Returns rolling 24-hour market metrics (High, Low, Total Volume, Quote Volume, Last Traded Price, and Price Change Percentage).
* **Execution Flow:**
  1. Validates `req.GetMarketId()`.
  2. Calls `h.svc.GetTicker(ctx, req.GetMarketId())`.
  3. Maps the domain ticker entity to Protobuf using `ToProtoTicker()`.
  4. Returns `&marketv1.GetTickerResponse{Ticker: protoTicker}, nil`.

#### 4. `GetCandles(ctx context.Context, req *marketv1.GetCandlesRequest) (*marketv1.GetCandlesResponse, error)`
* **Purpose:** Returns historical OHLC candlestick bars for TradingView charts and technical indicator calculations.
* **Execution Flow:**
  1. Validates `req.GetMarketId()`.
  2. Translates Protobuf `req.GetResolution()` enum into internal string (`"1m"`, `"5m"`, `"15m"`, `"1h"`, `"1d"`). If unspecified, returns `codes.InvalidArgument`.
  3. Safely extracts optional `from` and `to` timestamps:
     ```go
     var from, to *time.Time
     if req.GetFrom() != nil {
         t := req.GetFrom().AsTime()
         from = &t
     }
     if req.GetTo() != nil {
         t := req.GetTo().AsTime()
         to = &t
     }
     ```
  4. Calls `h.svc.GetCandles(ctx, req.GetMarketId(), resolutionStr, req.GetLimit(), from, to)`.
  5. Converts the slice of domain candles into `[]*marketv1.Candle` using `ToProtoCandle()`.
  6. Returns `&marketv1.GetCandlesResponse{Candles: protoCandles}, nil`.

---

## 4. 🔍 Deep-Dive: File 2 — `mapper.go` (Converters & Error Adapter)

### 🔄 DTO Converters

#### 1. `ToProtoMarket(m *domain.Market) *marketv1.Market`
* Converts arbitrary-precision decimals (`decimal.Decimal`) to strings via `.String()` to preserve full numerical precision (e.g. `"0.00010000"`).
* Converts Go `time.Time` to `timestamppb.New(m.CreatedAt)`.
* Converts domain status (`"ACTIVE"`, `"HALTED"`) to Protobuf enum integers (`MARKET_STATUS_ACTIVE`).

#### 2. `ToProtoTicker(t *domain.Ticker24h) *marketv1.Ticker24H`
* Maps domain rolling stats directly to Protobuf field definitions:
  * `High_24H`: Highest trade price in the last 24 hours.
  * `Low_24H`: Lowest trade price in the last 24 hours.
  * `Volume_24H`: Total base asset volume traded (e.g. `1245.50 BTC`).
  * `QuoteVolume_24H`: Total quote asset value traded (e.g. `$118,322,500 USDT`).
  * `PriceChange_24HPercent`: 24-hour percentage price difference.

#### 3. `ToProtoCandle(c *domain.Candle) *marketv1.Candle`
* Converts candlestick boundaries (`Open`, `High`, `Low`, `Close`, `Volume`, `QuoteVolume`) to strings.
* Maps `c.StartTime` to `timestamppb.New(c.StartTime)`.

---

### 🛡️ Error Mapping Table (`ToGRPCError`)

The `ToGRPCError` function inspects internal domain errors using `errors.Is(...)` and maps them to standard gRPC status codes:

| Domain / System Error | gRPC Status Code | Client Message Returned | Logged Severity |
| :--- | :--- | :--- | :--- |
| `service.ErrNotFound` | `codes.NotFound` | `"market not found"` | `zap.Warn` |
| `service.ErrInvalidResolution` | `codes.InvalidArgument` | `"invalid candle resolution: ..."` | `zap.Warn` |
| `service.ErrInvalidLimit` | `codes.InvalidArgument` | `"invalid limit: must be between 1 and 500"` | `zap.Warn` |
| `context.Canceled` | `codes.Canceled` | `"request was canceled by client"` | `zap.Debug` |
| `context.DeadlineExceeded` | `codes.DeadlineExceeded`| `"request timed out"` | `zap.Warn` |
| Unexpected / Database error | `codes.Internal` | `"internal server error"` | `zap.Error` (Full stack) |

> **Security Rule:** Raw PostgreSQL SQL errors or connection strings are **never** returned in the gRPC error message to external callers. They are logged privately via Uber Zap for developers, while clients receive a sanitized `codes.Internal` error.

---

## 5. 🛠️ Tools & Packages Used

| Tool / Package | Why Used in Handler Layer |
| :--- | :--- |
| **`google.golang.org/grpc`** | Implements the gRPC server protocol over HTTP/2 with binary serialization. |
| **`google.golang.org/grpc/codes` & `status`** | Creates standardized gRPC error responses with rich status codes. |
| **`google.golang.org/protobuf/types/known/timestamppb`** | Serializes timestamps into standard Google Protobuf timestamp messages. |
| **`go.uber.org/zap`** | Zero-allocation structured logger for method execution auditing and error logs. |
| **`tradedrift/platform/api/gen/market/v1`** | Generated Go code compiled from `proto/market/v1/market.proto`. |
