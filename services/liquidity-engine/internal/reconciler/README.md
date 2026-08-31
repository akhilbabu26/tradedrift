# `internal/reconciler` — MM Order Reconciliation Engine

**Package:** `reconciler`  
**Service:** Liquidity Engine  
**Last Updated:** August 2026

---

## 1. What This Package Does

This package is the **heart of reliable MM operation**. It takes the diff between the *desired* ladder state and the *actual* tracked state, then publishes the minimum necessary Kafka commands to close the gap. It also manages the full `PENDING → RESTING → CANCELLING → CANCELLED/STALE` order lifecycle with timeout-based resolution via the Order Service.

The core invariant:
> **No diff → No Kafka command.**  
> The LE never blindly re-sends orders. It only acts when the actual state diverges from desired.

---

## 2. Files In This Package

| File | Purpose |
| :--- | :--- |
| `reconciler.go` | `Reconciler` struct, `ReconcileMarket`, all lifecycle handlers |
| `reconciler_test.go` | Unit tests for reconcile cycles |
| `README.md` | This documentation file |

---

## 3. `Reconciler` Struct

```go
type Reconciler struct {
    tracker                    *order.Tracker
    producer                   *kafka.Producer
    orderSvc                   *orderservice.Client
    cfg                        *config.Config
    logger                     *zap.Logger
    metrics                    ReconcilerMetrics
    consecutivePendingTimeouts map[string]int // marketID → timeout count
}
```

### `ReconcilerMetrics` Interface

```go
type ReconcilerMetrics interface {
    IncStaleOrders(marketID string)
    IncReconcileCreate(marketID string)
    IncReconcileCancel(marketID string)
    IncReconcileCorrect(marketID string)
    IncReconcileNoop(marketID string)
    IncOrdersFilled(marketID, side string)
}
```

---

## 4. `ReconcileMarket` — Core Reconcile Cycle

Called per-market on every `evReconcileTick`. Steps:

```
1. pricing.GenerateLadder(mc, bidCount, askCount)
        │  desired []PriceLevel
        ▼
2. order.Diff(desired, tracker, marketID, mc)
        │  []DiffEntry{Action, LevelID, DesiredLevel, ExistingOID, ExistingCOID}
        ▼
3. len(entries) == 0?  →  IncReconcileNoop → return (no commands)
        │
        ▼
4. for each entry → applyEntry(ctx, entry, mc)
        │
        ├── DiffCreate  → applyCreate()
        ├── DiffCancel  → applyCancel()
        └── DiffCorrect → applyCancel() + tracker.QueueCorrection()
```

### Diff Actions

| Action | Condition | Operation |
| :--- | :--- | :--- |
| `DiffCreate` | Level missing from tracker | Publish `OrderCreated`, set PENDING |
| `DiffCancel` | Existing level no longer needed | Publish `OrderCancelRequested`, set CANCELLING |
| `DiffCorrect` | Level exists but at wrong price | Cancel existing + queue replacement for after cancel confirms |

---

## 5. `applyCreate` — Publishing a New MM Order

```
1. tracker.NextGeneration(levelID)       → gen (monotonically increasing)
2. order.ClientOrderID(levelID, gen)     → "MM-BTC-USDT-BID-01-G3"
3. uuid.New()                            → orderID (LE-assigned, becomes OS order ID)
4. producer.PublishCreate(...)           → OrderCreated on orders.commands
5. tracker.SetPending(levelID, ...)      → marks level as PENDING
```

If `PublishCreate` fails, `DecrementGeneration` is called to keep generation counter consistent.

---

## 6. `applyCancel` — Requesting an Order Cancellation

```
1. Validate e.ExistingOID != ""          → must have ME/OS-assigned UUID
2. producer.PublishCancel(...)           → OrderCancelRequested on orders.commands
3. tracker.SetCancelling(levelID)        → marks level as CANCELLING
```

The cancel command targets the **ME-assigned order_id** (UUID), never the `client_order_id`. The ME ignores cancel commands referencing unknown order IDs.

---

## 7. PENDING Timeout Resolution (`CheckPendingTimeouts`)

Called periodically by `engine.handlePendingCheck`. For each PENDING order older than `PendingTimeout`:

```
Query Order Service: GetOrderByClientID(client_order_id)
    │
    ├── Status == "OPEN" / "PARTIALLY_FILLED"
    │       → SetResting(levelID, osState.OrderID, ...)
    │       → reset consecutivePendingTimeouts[marketID] = 0
    │
    ├── Status == "FILLED"
    │       → Remove(levelID)  (reconcile will recreate)
    │       → IncOrdersFilled metric
    │
    ├── Status == "CANCELLED"
    │       → Remove(levelID)  (reconcile will recreate)
    │
    ├── ErrOrderNotFound
    │       → Remove(levelID) + timedOut = true
    │
    └── gRPC error
            → log warning + timedOut = true
```

If `timedOut = true` at end of scan: `consecutivePendingTimeouts[marketID]++`.  
The engine transitions to `PAUSED` when this count reaches `MELivenessThreshold`.

---

## 8. CANCELLING Timeout Resolution (`CheckCancellingTimeouts` → `handleCancellingTimeout`)

Called periodically by `engine.handleCancellingCheck`. For each CANCELLING order older than `CancellingTimeout`:

```
Query Order Service: GetOrderByClientID(client_order_id)
    │
    ├── Status == "CANCELLED"
    │       → Remove(levelID)
    │       └── QueuedCorrection present?
    │               → PublishCreate for replacement
    │
    ├── Status == "FILLED" (fully)
    │       → Remove(levelID) + IncOrdersFilled
    │
    ├── Status == "PARTIALLY_FILLED" (remaining > 0)
    │       → retryCancelOrStale()
    │
    ├── Status == "OPEN" / "CANCELLING"
    │       → retryCancelOrStale()
    │
    └── ErrOrderNotFound
            → treat as CANCELLED, Remove(levelID)
```

### `retryCancelOrStale`

```
cancelRetries >= CancelRetryLimit?
    YES → SetStale(levelID) + IncStaleOrders
    NO  → PublishCancel (retry) + IncrementCancelRetry
```

**STALE** is a terminal state within the reconciler. A STALE order sits in the tracker but the reconciler cannot cancel it. The next periodic `syncAllMarkets` (full resync from Order Service) will clear it if the order no longer appears in the OS response.

---

## 9. `SyncFromOrderService` — Full State Resync

Called at startup (SYNCING state) and on every `evResyncTick` (every `ReconcileInterval × 10`).

```
ListMMOrders(ctx, marketID)           → all active MM orders from Order Service
    │
    ├── tracker.SyncFromOrders(marketID, osOrders)
    │       → adds any OS orders not already in tracker (RESTING status)
    │
    └── For each RESTING order in tracker not in osOrders:
            → Remove(levelID)  (order gone from OS — clean up stale tracker entry)
```

This is the authoritative recovery path. After an ME crash and recovery cycle, this clears any stale RESTING entries that ME no longer knows about.

---

## 10. Concurrency

All `Reconciler` methods must be called from the engine's **single event loop goroutine**. There is no internal locking.

---

## 11. What This Package Does NOT Do

- Does NOT call `pricing.GenerateLadder` outside of `ReconcileMarket` — pricing is requested on each cycle
- Does NOT maintain its own ticker — all invocations are driven by the engine event loop
- Does NOT write to any database — all state is ephemeral in the `order.Tracker`
- Does NOT cancel all orders on shutdown — graceful shutdown is handled at the engine level
