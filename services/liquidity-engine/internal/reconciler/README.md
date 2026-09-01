# package `reconciler`

## Purpose

Applies `Diff()` results as real Kafka commands and manages the full **PENDING → OS_REGISTERED → RESTING → CANCELLING → STALE** state machine via timeout handlers.

## Problem It Solves

Diff() tells us *what* needs to change. The reconciler answers *how* to make that change safely:

1. **CREATE**: An order needs a stable idempotency key, an OS registration (for recovery across restarts), and a Kafka command — in that specific order.
2. **CANCEL/CORRECT**: A cancel must use the ME-assigned order UUID (not the `client_order_id`), must be retried if the OS doesn't confirm in time, and must escalate to STALE if the retry limit is exceeded.
3. **Timeout handling**: PENDING orders that never appear in OS, OS_REGISTERED orders waiting for ME confirmation, and CANCELLING orders that aren't confirmed all require periodic re-examination and specific resolution paths.

## How It Solves It

The reconciler is split into three files:
- **`reconciler.go`**: Runs the reconcile cycle and applies Diff entries.
- **`sync.go`**: Pulls authoritative state from the Order Service.
- **`timeouts.go`**: Handles stuck PENDING, OS_REGISTERED, and CANCELLING orders.

---

## Flow: Full Reconcile Cycle

```
engine.runReconcileAll()
         │
         ▼
  reconciler.ReconcileMarket(ctx, marketID, bidCount, askCount)
         │
         ├── pricing.GenerateLadder(mc, bidCount, askCount) → desired []PriceLevel
         │
         ├── order.Diff(desired, tracker, marketID, cfg) → []DiffEntry
         │
         └── for each DiffEntry:
               │
               ├─ DiffCreate ──→ applyCreate()
               │                  1. orderSvc.CreateMMOrder() → get orderID
               │                  2. tracker.SetPending()
               │                  3. producer.PublishCreate() → Kafka
               │                  4. tracker.SetKafkaPublished(true)
               │
               ├─ DiffCancel ──→ applyCancel()
               │                  1. producer.PublishCancel() → Kafka
               │                  2. tracker.SetCancelling()
               │
               └─ DiffCorrect → applyCancel() + tracker.QueueCorrection()
                                  (replacement created after cancel confirmed)
```

---

## Flow: Pending Timeout Check

```
handlePendingCheck() [every PendingTimeout/2]
         │
         ▼
  CheckPendingTimeouts(ctx, marketID)
         │
         └── for each PENDING order older than PendingTimeout:
               │
               ├── KafkaPublished == false?
               │     → retry producer.PublishCreate() with same orderID/COID
               │
               └── KafkaPublished == true?
                     → orderSvc.GetOrderByClientID()
                           │
                           ├── OPEN / PARTIALLY_FILLED → SetOSRegistered()
                           ├── FILLED → Remove() + IncOrdersFilled
                           ├── CANCELLED → Remove()
                           └── NOT_FOUND (3 consecutive) → count liveness failure
```

---

## Flow: OS_REGISTERED Timeout

```
CheckOSRegisteredTimeouts(marketID, meConfirmationTimeout, meHealthy)
         │
         ├── meHealthy == false? → hold all OS_REGISTERED, do nothing
         │
         └── for each OS_REGISTERED older than meConfirmationTimeout:
               → SetResting()
               (ME assumed accepted after sufficient time with healthy ME probe)
```

---

## Flow: Cancelling Timeout

```
CheckCancellingTimeouts(ctx, marketID) [every CancellingTimeout/2]
         │
         └── for each CANCELLING order older than CancellingTimeout:
               → handleCancellingTimeout()
                     │
                     ├── orderSvc.GetOrderByClientID()
                     │         │
                     │         ├── CANCELLED → Remove()
                     │         │     QueuedCorrection? → create replacement
                     │         │
                     │         ├── FILLED → Remove() + IncOrdersFilled
                     │         │
                     │         └── OPEN / CANCELLING → retryCancelOrStale()
                     │
                     └── retryCancelOrStale()
                           retries < limit? → PublishCancel() + IncrementCancelRetry()
                           retries >= limit? → SetStale() + IncStaleOrders
```

---

## Flow: OS Resync

```
syncAllMarkets() [startup + every MaxOrderStateStaleness/2]
         │
         ▼
  SyncFromOrderService(ctx, marketID)
         │
         ├── orderSvc.ListMMOrders() → []OSOrder (OPEN + PARTIALLY_FILLED)
         ├── tracker.SyncFromOrders() → add new, update existing
         │
         └── for each tracked order NOT in OS response:
               ├── RESTING / OS_REGISTERED / STALE → Remove()
               ├── CANCELLING → Remove()
               │     QueuedCorrection? → CreateMMOrder() + SetPending() + PublishCreate()
               └── PENDING → retain (CheckPendingTimeouts handles it)
```

---

## Files

### [`reconciler.go`](./reconciler.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `Reconciler` | `struct` | Holds tracker, producer, orderSvc, config, logger, metrics, and per-level NOT_FOUND counters. |
| `NewReconciler(...)` | `func` | Wires all dependencies. |
| `ReconcileMarket(ctx, marketID, bidCount, askCount)` | `func` | Full reconcile: generate ladder → diff → apply entries. Returns commands published. |
| `applyEntry(ctx, e, mc)` | `func` (internal) | Routes DiffEntry to `applyCreate`, `applyCancel`, or cancel + QueueCorrection. |
| `applyCreate(ctx, e, mc)` | `func` (internal) | 3-step: OS register → SetPending → Kafka publish. Idempotent on retry (same orderID, same COID). |
| `applyCancel(ctx, e, mc)` | `func` (internal) | Publishes `OrderCancelRequested` with the ME-assigned UUID. Sets tracker to CANCELLING. |

---

### [`sync.go`](./sync.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `SyncFromOrderService(ctx, marketID)` | `func` | Authoritative OS resync. Resolves missing orders: remove RESTING/OS_REGISTERED/STALE, handle CANCELLING with queued corrections, retain PENDING. |

---

### [`timeouts.go`](./timeouts.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `CheckPendingTimeouts(ctx, marketID)` | `func` | Examines PENDING orders past `PendingTimeout`. Retries Kafka publish if not confirmed. Queries OS and transitions state or counts NOT_FOUND toward liveness threshold. |
| `CheckOSRegisteredTimeouts(marketID, timeout, meHealthy)` | `func` | Promotes OS_REGISTERED → RESTING after timeout elapses, only if ME is currently healthy. V1 proxy for a direct ME confirmation event. |
| `CheckCancellingTimeouts(ctx, marketID)` | `func` | Examines CANCELLING orders past `CancellingTimeout`. Resolves via `handleCancellingTimeout`. |
| `handleCancellingTimeout(ctx, o, mc)` | `func` (internal) | Queries OS for cancel status. Confirms, handles fills, or calls `retryCancelOrStale`. |
| `retryCancelOrStale(ctx, o, mc)` | `func` (internal) | Retries cancel command under the limit; transitions to STALE at the limit. |
