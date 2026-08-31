# `internal/orderservice` — Order Service gRPC Client

**Package:** `orderservice`  
**Service:** Liquidity Engine  
**Last Updated:** August 2026

---

## 1. What This Package Does

This package provides a **read-only gRPC client** for the Order Service. The Liquidity Engine uses it to query the authoritative state of MM-001 orders — it never writes to the Order Service directly. All order creation and cancellation commands go through Kafka (`orders.commands`), not gRPC.

---

## 2. Files In This Package

| File | Purpose |
| :--- | :--- |
| `client.go` | `Client` struct, `OrderState`, `ListMMOrders`, `GetOrderByClientID`, `IsAvailable` |
| `README.md` | This documentation file |

---

## 3. The Golden Rule

> **The LE NEVER writes to the Order Service.**  
> Orders are created/cancelled via Kafka `orders.commands` only.  
> The Order Service gRPC client is strictly read-only.

---

## 4. When the LE Queries the Order Service

| Scenario | Method | Called by |
| :--- | :--- | :--- |
| Startup: populate tracker with existing MM orders | `ListMMOrders` | `reconciler.SyncFromOrderService` |
| Periodic resync (every ~5 min) | `ListMMOrders` | `reconciler.SyncFromOrderService` |
| PENDING timeout: confirm if order was accepted | `GetOrderByClientID` | `reconciler.CheckPendingTimeouts` |
| CANCELLING timeout: confirm if cancel was processed | `GetOrderByClientID` | `reconciler.handleCancellingTimeout` |
| Health check: confirm OS is reachable | `IsAvailable` | health server |

---

## 5. `Client` Struct

```go
type Client struct {
    conn   *grpc.ClientConn
    client orderv1.OrderServiceClient
    logger *zap.Logger
}
```

Uses `insecure.NewCredentials()` — mTLS is handled at the service mesh level in production.

---

## 6. `OrderState` — LE's View of an Order

```go
type OrderState struct {
    OrderID       string          // OS-assigned UUID
    ClientOrderID string          // idempotency_key = LE's client_order_id
    Status        string          // "OPEN" | "PARTIALLY_FILLED" | "FILLED" | "CANCELLING" | "CANCELLED"
    RemainingQty  decimal.Decimal // from order.remaining_quantity
    OriginalQty   decimal.Decimal // from order.quantity
}
```

---

## 7. `ListMMOrders(ctx, marketID) ([]order.OSOrder, error)`

Fetches all **OPEN and PARTIALLY_FILLED** orders for `MM-001` on a given market. Makes two gRPC calls (one per status) and combines the results.

**Parsing & filtering:**

- Orders with an empty `idempotency_key` are skipped (non-MM orders)
- Orders whose `idempotency_key` cannot be parsed as `{LevelID}-G{gen}` are skipped with a warning log
- Invalid decimal strings for `price`, `quantity`, or `remaining_quantity` are skipped

**`client_order_id` parsing:**

```
"MM-BTC-USDT-ASK-01-G003"
    │
    ├── LevelID = "MM-BTC-USDT-ASK-01"
    └── Generation = 3
```

The parser scans for the last `-G` separator to split `LevelID` from `Generation`.

---

## 8. `GetOrderByClientID(ctx, clientOrderID) (*OrderState, error)`

Looks up a specific order by its `idempotency_key`. Used during PENDING and CANCELLING timeout resolution.

**Current implementation:** Fetches all MM-001 orders (no server-side filter by idempotency_key — the OS API doesn't expose one) and scans for a match. Returns `ErrOrderNotFound` if not found.

```go
var ErrOrderNotFound = errors.New("order not found")
```

Callers should check for this sentinel:
```go
if errors.Is(err, orderservice.ErrOrderNotFound) {
    // order never made it to OS — treat as failed create
}
```

---

## 9. Proto Status Mapping

The gRPC proto `OrderStatus` enum is converted to plain strings for internal use:

| Proto Enum | String |
| :--- | :--- |
| `ORDER_STATUS_OPEN` | `"OPEN"` |
| `ORDER_STATUS_PARTIALLY_FILLED` | `"PARTIALLY_FILLED"` |
| `ORDER_STATUS_FILLED` | `"FILLED"` |
| `ORDER_STATUS_CANCELLING` | `"CANCELLING"` |
| `ORDER_STATUS_CANCELLED` | `"CANCELLED"` |
| (default) | `"UNKNOWN"` |

---

## 10. What This Package Does NOT Do

- Does NOT call `CreateOrder`, `CancelOrder`, or any write endpoint on the Order Service
- Does NOT cache responses — every call is a live gRPC query
- Does NOT use Order Service websocket or streaming — polling only
- Does NOT query orders for any account other than `MM-001`
