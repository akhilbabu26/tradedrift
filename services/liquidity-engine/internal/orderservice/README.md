# package `orderservice`

## Purpose

Provides a **read-only gRPC client** for the Order Service. The LE uses it for startup discovery, pending order verification, and cancellation confirmation. The LE also uses `CreateMMOrder` to register new orders in the OS before publishing Kafka commands — this is for crash recovery, not for order execution.

## Problem It Solves

If the LE crashes after publishing an `OrderCreated` Kafka command but before storing the order in any durable state, the order would be lost from the LE's tracker on restart. The ME would have it, but the LE would try to create a duplicate with the same level ID on next startup.

The LE solves this by registering every new order in the Order Service (via gRPC `CreateOrder` with an idempotency key) **before** publishing to Kafka. On restart, `ListMMOrders` recovers all order entries, and the `client_order_id` (idempotency key) encodes the `LevelID` and `Generation` so the tracker can be fully reconstructed.

## How It Solves It

```
Order Creation (3-step, crash-safe):
  1. orderSvc.CreateMMOrder()   OS assigns UUID, stores order idempotently
  2. tracker.SetPending()        in-memory state updated
  3. producer.PublishCreate()    Kafka command sent to ME

Crash between step 2 and 3:
  On restart, ListMMOrders() recovers the OS record.
  tracker.SyncFromOrders() sets it to OS_REGISTERED.
  CheckPendingTimeouts() retries the Kafka publish with the same orderID.
```

---

## Flow: Startup Recovery via ListMMOrders

```
engine.Run() StateSyncing
         │
         ▼
  reconciler.SyncFromOrderService(ctx, marketID)
         │
         ▼
  orderSvc.ListMMOrders(ctx, marketID)
         │
         ├── ListOrders(userId=MM-UUID, status=OPEN)
         ├── ListOrders(userId=MM-UUID, status=PARTIALLY_FILLED)
         │
         └── for each order with idempotency_key:
               parseLevelFromClientOrderID("MM-BTC-USDT-ASK-01-G003")
               levelID="MM-BTC-USDT-ASK-01", gen=3
               → []OSOrder
         │
         ▼
  tracker.SyncFromOrders(marketID, osOrders)
    tracker populated with OS_REGISTERED entries
    generation counters restored
```

---

## Flow: Pending Timeout Verification

```
CheckPendingTimeouts() finds a PENDING order past timeout
         │
         ▼
  orderSvc.GetOrderByClientID(ctx, "MM-BTC-USDT-ASK-01-G003")
         │
         ├── extracts marketID from levelID via parseMarketFromLevelID
         ├── ListOrders(userId=MM-UUID, marketID=BTC-USDT)
         └── scans result for matching idempotency_key
               ├── found → return OrderState{Status, OrderID, Qty}
               └── not found → return ErrOrderNotFound
```

---

## Files

### [`client.go`](./client.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `ErrOrderNotFound` | `var error` | Sentinel returned when no order matches the `client_order_id`. |
| `OrderState` | `struct` | LE's view of one OS order: `OrderID`, `ClientOrderID`, `Status`, `OriginalQty`, `RemainingQty`. |
| `Client` | `struct` | Thin wrapper around the Order Service gRPC client. |
| `NewClient(addr, logger)` | `func` | Dials the Order Service (insecure). Returns error if dial fails. |
| `Close()` | `func` | Closes the gRPC connection. |
| `CreateMMOrder(ctx, ...)` | `func` | Registers an MM order in OS idempotently using `clientOrderID` as `IdempotencyKey`. Returns OS-assigned `OrderID`. Safe to retry with the same `clientOrderID`. |
| `ListMMOrders(ctx, marketID)` | `func` | Fetches all OPEN + PARTIALLY_FILLED MM-001 orders. Parses `idempotency_key` to extract `LevelID`/`Generation`. Skips orders with missing or unparseable keys. Returns `[]order.OSOrder`. |
| `GetOrderByClientID(ctx, clientOrderID)` | `func` | Looks up a single order by `client_order_id`. Extracts `marketID` from the level ID for a targeted query. Returns `ErrOrderNotFound` if absent. |
| `IsAvailable(ctx)` | `func` | Lightweight reachability check. Returns `false` on any error. |
| `parseLevelFromClientOrderID(id)` | `func` (internal) | Splits `"MM-BTC-USDT-ASK-01-G003"` into `levelID` and `gen`. Searches for the last `-G` suffix. |
| `parseMarketFromLevelID(levelID)` | `func` (internal) | Extracts `"BTC-USDT"` from `"MM-BTC-USDT-ASK-01"`. |
| `protoStatusToString(s)` | `func` (internal) | Converts Order Service proto `OrderStatus` enum to string. |

---

## Important Notes

- The LE **never calls CancelOrder via gRPC** — cancels are sent as Kafka commands only.
- All OS queries use `account.WalletUUIDStr` (UUID format). The string `"MM-001"` is only used in Kafka payloads.
- `CreateMMOrder` bypasses the Wallet Service `ReserveFunds` path — MM orders are exempt from balance reservation inside the Order Service.
