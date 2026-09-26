# package `reconciler`

## Purpose

Applies `Diff()` results as real Kafka commands and manages the full **PENDING → OS_REGISTERED → RESTING → CANCELLING → STALE** state machine via timeout handlers.

## Problem It Solves

Diff() tells us *what* needs to change. The reconciler answers *how* to make that change safely:

1. **CREATE**: An order needs a stable idempotency key, an OS registration (for recovery across restarts), and a Kafka command — in that specific order.
2. **CANCEL/CORRECT**: A cancel must use the ME-assigned order UUID (not the `client_order_id`), must be retried if the OS doesn't confirm in time, and must escalate to STALE if the retry limit is exceeded.
3. **Timeout handling**: PENDING orders that never appear in OS, OS_REGISTERED orders waiting for ME confirmation, and CANCELLING orders that aren't confirmed all require periodic re-examination and specific resolution paths.

## How It Solves It

The reconciler is split into four files:
- **`reconciler.go`**: Runs the reconcile cycle, executes ME snapshot verification, and manages missing order hysteresis.
- **`dispatch.go`**: Applies Diff entries (`applyEntry`, `applyCreate`, `applyCancel`) to the tracker and Kafka/Order Service.
- **`sync.go`**: Pulls authoritative state from the Order Service and recovers highest historical generations.
- **`timeouts.go`**: Handles stuck PENDING, OS_REGISTERED, and CANCELLING orders, and snapshot-based RESTING confirmations.

---

## Flow: Full Reconcile Cycle

```
engine.runReconcileAll()
         │
         ▼
  reconciler.ReconcileMarket(ctx, marketID, bidCount, askCount)
         │
         ├── meClient.FetchSnapshot(ctx, marketID)
         │     │
         │     ├── ME state != "LIVE"? → Pause cycle, skip diff/apply
         │     │
         │     └── syncWithMESnapshot(ctx, marketID, snap, mc)
         │           ├── Confirm matched orders as RESTING & sync RemainingQty (INV-MM-08)
         │           ├── Cancel orphan orders in ME (stale gen / untracked) (INV-MM-05)
         │           └── Missing order check with 10s grace period & 2-cycle hysteresis:
         │                 missing >= 2 → handleMissingRestingOrder() (lock slot as CANCELLING)
         │
         ├── pricing.GenerateLadder(mc, bidCount, askCount) → desired []PriceLevel
         │
         ├── order.Diff(desired, tracker, marketID, cfg) → []DiffEntry
         │
         └── for each DiffEntry:
               │
               ├─ DiffCreate ──→ applyCreate() [in dispatch.go]
               │                  1. orderSvc.CreateMMOrder() → get orderID
               │                  2. tracker.SetPending() + record inFlightSince
               │                  3. producer.PublishCreate() → Kafka
               │                  4. tracker.SetKafkaPublished(true)
               │
               ├─ DiffCancel ──→ applyCancel() [in dispatch.go]
               │                  1. orderSvc.CancelMMOrder() (optional ledger sync)
               │                  2. producer.PublishCancel() → Kafka
               │                  3. tracker.SetCancelling() + record inFlightSince
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

## Flow: OS_REGISTERED Timeout & Snapshot Confirmation

```
CheckOSRegisteredTimeouts(marketID, meConfirmationTimeout, meHealthy)
         │
         ├── meHealthy == false? → hold all OS_REGISTERED, do nothing
         │
         ├── meClient != nil?
         │     → meClient.FetchSnapshot(ctx, marketID)
         │     → ConfirmRestingFromSnapshot(marketID, snap)
         │         (promotes to RESTING only when confirmed present in ME snapshot)
         │
         └── fallback (unit test mode without ME client):
               for each OS_REGISTERED older than meConfirmationTimeout:
                 → SetResting()
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

## Flow: OS Resync & Generation Recovery

```
syncAllMarkets() [startup + every MaxOrderStateStaleness/2]
         │
         ▼
  SyncFromOrderService(ctx, marketID)
         │
         ├── orderSvc.RecoverHighestGenerations() → tracker.SetMaxGeneration() (INV-MM-07)
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
| `Reconciler` | `struct` | Holds tracker, producer, orderSvc, meClient, config, logger, metrics, and hysteresis/in-flight maps. |
| `NewReconciler(...)` | `func` | Wires all dependencies including ME client. |
| `ReconcileMarket(ctx, marketID, bidCount, askCount)` | `func` | Full reconcile: queries ME snapshot → `syncWithMESnapshot` → generate ladder → diff → apply entries. |
| `syncWithMESnapshot(ctx, marketID, snap, mc)` | `func` (internal) | Verifies ME snapshot: matches identity, syncs `RemainingQty`, cancels orphans, and detects missing orders with 2-cycle hysteresis. |
| `handleMissingRestingOrder(ctx, o, mc)` | `func` (internal) | Resolves order confirmed missing from ME: checks OS; if OPEN/PARTIALLY_FILLED, cancels in OS and locks slot as CANCELLING without removing. |

---

### [`dispatch.go`](./dispatch.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `applyEntry(ctx, e, mc)` | `func` (internal) | Routes DiffEntry to `applyCreate`, `applyCancel`, or cancel + QueueCorrection. |
| `applyCreate(ctx, e, mc)` | `func` (internal) | 3-step: OS register → SetPending + record inFlightSince → Kafka publish. Idempotent on retry (same orderID, same COID). |
| `applyCancel(ctx, e, mc)` | `func` (internal) | Cancels in OS (optional ledger sync) and publishes `OrderCancelRequested` with ME UUID to Kafka. Sets tracker to CANCELLING. |

---

### [`sync.go`](./sync.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `SyncFromOrderService(ctx, marketID)` | `func` | Authoritative OS resync. Recovers highest generations (INV-MM-07), pulls OPEN/PARTIALLY_FILLED orders, and resolves missing orders. |

---

### [`timeouts.go`](./timeouts.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `CheckPendingTimeouts(ctx, marketID)` | `func` | Examines PENDING orders past `PendingTimeout`. Retries Kafka publish if not confirmed. Queries OS and transitions state or counts NOT_FOUND toward liveness threshold. |
| `ConfirmRestingFromSnapshot(marketID, snap)` | `func` | Promotes tracked orders to RESTING if and only if confirmed present in ME atomic snapshot. |
| `CheckOSRegisteredTimeouts(marketID, timeout, meHealthy)` | `func` | Verifies OS_REGISTERED orders against ME snapshot via `ConfirmRestingFromSnapshot` (blind auto-promotion removed). |
| `CheckCancellingTimeouts(ctx, marketID)` | `func` | Examines CANCELLING orders past `CancellingTimeout`. Resolves via `handleCancellingTimeout`. |
| `handleCancellingTimeout(ctx, o, mc)` | `func` (internal) | Queries OS for cancel status. Confirms, handles fills, or calls `retryCancelOrStale`. |
| `retryCancelOrStale(ctx, o, mc)` | `func` (internal) | Retries cancel command under the limit; transitions to STALE at the limit. |
