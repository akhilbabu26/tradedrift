# Order Service — gRPC Handler Package (`internal/handler`)

> **Package:** `tradedrift/services/order/internal/handler`  
> **Directory:** `services/order/internal/handler/`  
> **Role:** gRPC API Boundary Interceptor, Request Validator, & Error Status Mapper

---

## 1. Purpose & Architectural Role

The `handler` package serves as the **gRPC server entrypoint** for the Order Service. It implements the generated protobuf interface `orderv1.OrderServiceServer` (from `platform/api/gen/order/v1`).

Key responsibilities:
1. **Request Boundary Validation**: Validates that gRPC requests are non-nil and contain required identity strings (`user_id`, `market_id`, `order_id`, `quantity`) before passing execution to the service layer.
2. **Type & Enum Mapping**: Maps generated Protobuf enums (`orderv1.OrderSide`, `orderv1.OrderType`, `orderv1.OrderStatus`) to clean internal domain types (`repository.OrderSide`, `repository.OrderType`, `repository.OrderStatus`).
3. **Cursor Token Encoding**: Encodes the last item's `(created_at, id)` into an opaque, URL-safe Base64 token for keyset pagination.
4. **Sanitized Error Mapping (`mapServiceError`)**: Converts domain/service errors into standard gRPC error status codes (`codes.InvalidArgument`, `codes.NotFound`, `codes.AlreadyExists`, `codes.FailedPrecondition`, `codes.Internal`), preventing internal database tracebacks from leaking to API callers.

---

## 2. Files in This Directory

| File | Role |
| :--- | :--- |
| [`grpc.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/handler/grpc.go) | Implements `GRPCHandler` struct and RPC endpoint server methods |
| [`mapper.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/handler/mapper.go) | Error status mapping (`mapServiceError`), Base64 cursor encoding (`encodeCursor`), and Protobuf $\leftrightarrow$ Domain entity converters |

---

## 3. Packages & Dependencies Used

| Package | Purpose & Rationale |
| :--- | :--- |
| `context` | Propagates RPC deadlines and cancellation contexts. |
| `encoding/base64` | Encodes pagination cursors into URL-safe Base64 strings. |
| `errors` | Evaluates service error instances using `errors.Is`. |
| `fmt` | Formats timestamp-ID strings for cursor encoding (`"created_at|id"`). |
| `time` | Formats timestamps into RFC3339Nano for cursor encoding. |
| `go.uber.org/zap` | Structured logging for RPC entrypoints and failures. |
| `google.golang.org/grpc/codes` | Standard gRPC error status codes (`InvalidArgument`, `NotFound`, `AlreadyExists`, `FailedPrecondition`, `Internal`, `Unimplemented`). |
| `google.golang.org/grpc/status` | Constructs gRPC status errors (`status.Error(code, message)`). |
| `google.golang.org/protobuf/types/known/timestamppb` | Converts Go `time.Time` to Protobuf `google.protobuf.Timestamp`. |
| `tradedrift/platform/api/gen/order/v1` | Compiled protobuf stubs (`orderv1`). |

---

## 4. Function & Method Analysis (`grpc.go` & `mapper.go`)

### 4.1 `NewGRPCHandler(svc, logger)` (`grpc.go`)
* **Signature:** `func NewGRPCHandler(svc service.Service, logger *zap.Logger) *GRPCHandler`
* **Purpose:** Constructs the `GRPCHandler` instance.

---

### 4.2 `CreateOrder(ctx, req)` (`grpc.go`)
* **Signature:** `(h *GRPCHandler) CreateOrder(ctx context.Context, req *orderv1.CreateOrderRequest) (*orderv1.CreateOrderResponse, error)`
* **Boundary Checks**: Validates `req != nil`, `req.UserId != ""`, `req.MarketId != ""`, and `req.Quantity != ""`.
* **Flow**: Maps proto enums, invokes `h.svc.CreateOrder(ctx, params)`, maps response struct to proto via `toProtoOrder`, and maps errors via `mapServiceError`.

---

### 4.3 `CancelOrder(ctx, req)` (`grpc.go`)
* **Signature:** `(h *GRPCHandler) CancelOrder(ctx context.Context, req *orderv1.CancelOrderRequest) (*orderv1.CancelOrderResponse, error)`
* **Boundary Checks**: Validates `req != nil`, `req.OrderId != ""`, `req.UserId != ""`.
* **Flow**: Invokes `h.svc.CancelOrder`, returns `orderv1.CancelOrderResponse` with order status set to `CANCELLING`.

---

### 4.4 `GetOrder(ctx, req)` (`grpc.go`)
* **Signature:** `(h *GRPCHandler) GetOrder(ctx context.Context, req *orderv1.GetOrderRequest) (*orderv1.GetOrderResponse, error)`
* **Boundary Checks**: Validates `req != nil`, `req.OrderId != ""`, `req.UserId != ""`.
* **Flow**: Retrieves order metadata and converts it to `orderv1.GetOrderResponse`.

---

### 4.5 `ListOrders(ctx, req)` (`grpc.go`)
* **Signature:** `(h *GRPCHandler) ListOrders(ctx context.Context, req *orderv1.ListOrdersRequest) (*orderv1.ListOrdersResponse, error)`
* **Boundary Checks**: Validates `req != nil`, `req.UserId != ""`.
* **Cursor & Timestamp Extraction**: Converts `req.From` and `req.To` timestamps to `*time.Time`. Encodes the last order's `(CreatedAt, ID)` into `next_cursor`.

---

### 4.6 `CancelAllOrders(ctx, req)` (`grpc.go`)
* **Signature:** `(h *GRPCHandler) CancelAllOrders(ctx context.Context, req *orderv1.CancelAllOrdersRequest) (*orderv1.CancelAllOrdersResponse, error)`
* **Status**: Returns `codes.Unimplemented` (`"bulk cancel all orders is not implemented yet"`).

---

## 5. Error Mapping Rules (`mapper.go`)

```
──────────────────────────────────────────────────────────────────────────────────────────
Service / Repository Error                           | gRPC Status Code
──────────────────────────────────────────────────────────────────────────────────────────
service.ErrInvalidSide                              | codes.InvalidArgument (400)
service.ErrInvalidType                              | codes.InvalidArgument (400)
service.ErrInvalidMarket                            | codes.InvalidArgument (400)
service.ErrInvalidPrice                             | codes.InvalidArgument (400)
service.ErrInvalidQuantity                          | codes.InvalidArgument (400)
service.ErrInvalidPaginationCursor                  | codes.InvalidArgument (400)
repository.ErrInvalidPaginationCursor               | codes.InvalidArgument (400)
──────────────────────────────────────────────────────────────────────────────────────────
service.ErrOrderNotFound                            | codes.NotFound (404)
repository.ErrOrderNotFound                         | codes.NotFound (404)
──────────────────────────────────────────────────────────────────────────────────────────
service.ErrDuplicateIdempotencyKey                  | codes.AlreadyExists (409)
repository.ErrDuplicateIdempotencyKey               | codes.AlreadyExists (409)
──────────────────────────────────────────────────────────────────────────────────────────
service.ErrInsufficientFunds                        | codes.FailedPrecondition (412)
service.ErrOrderNotCancellable                      | codes.FailedPrecondition (412)
repository.ErrOrderNotCancellable                   | codes.FailedPrecondition (412)
──────────────────────────────────────────────────────────────────────────────────────────
Any unknown or internal database error              | codes.Internal (500)
──────────────────────────────────────────────────────────────────────────────────────────
```

---

## 6. Helper Functions Breakdown (`mapper.go`)

### 6.1 `encodeCursor(createdAt, id)`
* **Signature:** `func encodeCursor(createdAt time.Time, id string) string`
* **Format**: Formats `createdAt.Format(time.RFC3339Nano)` + `"|"` + `id`, then returns `base64.URLEncoding.EncodeToString`.

### 6.2 `toProtoOrder(o)`
* **Signature:** `func toProtoOrder(o *repository.Order) *orderv1.Order`
* **Purpose**: Converts internal domain `repository.Order` into generated protobuf message `orderv1.Order`. Handles nil pointers for `Price` and `IdempotencyKey`. Converts `time.Time` to `timestamppb.New`.

### 6.3 Enum Parsers (`parseProtoSide`, `parseProtoType`, `parseProtoStatus`)
* **Purpose**: Safe conversion from Protobuf enum values to Go domain type strings.

### 6.4 Enum Formatter (`toProtoSideEnum`, `toProtoTypeEnum`, `toProtoStatusEnum`)
* **Purpose**: Safe conversion from Go domain type strings to Protobuf enum values.
