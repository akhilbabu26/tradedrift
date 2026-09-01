# package `order`

## Purpose

Provides the **in-memory order tracker** and **diff algorithm** that are at the heart of the LE's reconciliation loop. The tracker maintains the LE's working picture of all MM orders; the diff computes the minimal set of actions to converge actual state to desired state.

## Problem It Solves

The LE needs to maintain exactly N bid levels and M ask levels in the ME order book at all times. The challenge is that order state is spread across three systems:

- **Order Service (authoritative)**: knows if an order is OPEN, FILLED, or CANCELLED.
- **Matching Engine**: knows if an order is in the live order book.
- **LE tracker (working)**: the LE's own in-flight view, which may be ahead of OS/ME due to Kafka lag.

Without a local tracker, every reconcile cycle would need to make gRPC calls to determine what exists. Without a diff algorithm, every reconcile would cancel and recreate everything — producing unnecessary Kafka traffic and gaps in liquidity.

## How It Solves It

The tracker stores every MM order by its stable `LevelID` (e.g., `MM-BTC-USDT-ASK-01`). Each level has a lifecycle status and a monotonically increasing `Generation` counter that survives restarts via the `client_order_id` (`MM-BTC-USDT-ASK-01-G003`).

`Diff()` does a two-pass set comparison: (1) desired vs known → CREATE/CORRECT, (2) RESTING vs desired → CANCEL. Orders in PENDING, CANCELLING, or STALE are excluded from all actions — left to resolve via timeout handlers.

---

## Order Lifecycle Flow

```
                 ┌─────────────────────────────────────────────┐
                 │       Diff() produces DiffCreate            │
                 ▼                                             │
            PENDING ←── SetPending()                          │
                 │  (Kafka published, OS registered)           │
                 │                                             │
    ┌────────────┤ CheckPendingTimeouts()                      │
    │            │  OS confirms OPEN                           │
    │            ▼                                             │
    │       OS_REGISTERED ←── SetOSRegistered()               │
    │            │                                             │
    │            │ CheckOSRegisteredTimeouts()                 │
    │            │  (ME healthy + confirmation window elapsed) │
    │            ▼                                             │
    │         RESTING ←── SetResting()                        │
    │            │                                             │
    │       ┌────┴─────────────────┐                          │
    │  DiffCancel             DiffCorrect                      │
    │       │                      │                          │
    │       ▼                      ▼                          │
    │   CANCELLING ←── SetCancelling()                        │
    │       │          (QueuedCorrection stored)              │
    │       │                                                  │
    │       │ CheckCancellingTimeouts()                        │
    │       │  OS confirms CANCELLED                           │
    │       ├── Remove() ─→ QueuedCorrection? ─→ yes ─────────┘
    │       │
    │       │ retry limit exceeded
    │       ▼
    │     STALE ←── SetStale()
    │       │  (frozen until full resync)
    │       ▼
    │    Remove() ←── SyncFromOrderService()
    └─────────────────────────────────────────────────────────
```

---

## Files

### [`tracker.go`](./tracker.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `Status` | `type string` | Lifecycle states: `PENDING`, `OS_REGISTERED`, `RESTING`, `CANCELLING`, `STALE` |
| `LiveOrder` | `struct` | One tracker entry: level identity, order details (market/side/price/qty), status, and timing fields. |
| `Tracker` | `struct` | Holds `orders`, `generations`, and `lastSync` maps. |
| `NewTracker()` | `func` | Creates an empty tracker. |
| `SetPending(...)` | `func` | Adds a new entry in PENDING state after OS registration and Kafka publish. |
| `SetKafkaPublished(levelID, bool)` | `func` | Marks Kafka dispatch confirmed. If `false`, `CheckPendingTimeouts` retries the publish. |
| `SetOSRegistered(levelID, orderID, ...)` | `func` | PENDING → OS_REGISTERED. OS has the order; ME confirmation window starts. |
| `SetResting(levelID, orderID, ...)` | `func` | OS_REGISTERED → RESTING. ME has accepted the order into the live book. |
| `SetCancelling(levelID)` | `func` | RESTING → CANCELLING. Cancel command published to Kafka. |
| `QueueCorrection(levelID, desired)` | `func` | Stores the replacement level on a CANCELLING order. Applied once cancel is confirmed. |
| `SetStale(levelID)` | `func` | CANCELLING → STALE. Cancel retry limit exceeded; frozen until OS resync. |
| `Remove(levelID)` | `func` | Deletes the tracker entry. Generation counter is preserved for monotonicity. |
| `NextGeneration(levelID)` | `func` | Increments and returns the generation counter. Persists across `Remove()`. |
| `CurrentGeneration(levelID)` | `func` | Returns the current generation without incrementing. |
| `Get(levelID)` | `func` | Returns the `LiveOrder` for a level, or nil. |
| `All(marketID)` | `func` | Returns all tracked orders for one market. Used by Diff and timeout handlers. |
| `AllMarkets()` | `func` | Returns all tracked orders across all markets. |
| `ActiveCount(marketID, side)` | `func` | Count of RESTING + OS_REGISTERED orders for a side. |
| `RestingCount(marketID, side)` | `func` | Count of strictly RESTING orders. Used exclusively by `/readyz`. |
| `PendingCount(marketID)` | `func` | Count of PENDING + OS_REGISTERED orders. |
| `StaleCount(marketID)` | `func` | Count of STALE orders. |
| `CommittedBase(marketID)` | `func` | Sum of `RemainingQty` for active SELL orders. Used by `inventory.EffectiveAvailableBase`. |
| `CommittedQuote(marketID)` | `func` | Sum of `RemainingQty × Price` for active BUY orders. Used by `inventory.EffectiveAvailableQuote`. |
| `RecordSync(marketID)` | `func` | Records a successful `ListMMOrders` sync timestamp. |
| `LastSuccessfulSync(marketID)` | `func` | Returns the last sync time. Stale sync blocks new order creation. |
| `SyncFromOrders(marketID, orders)` | `func` | Populates tracker from OS response. Deduplicates by LevelID (highest generation wins). New entries enter OS_REGISTERED. Returns `(added, duplicates)`. |
| `ClientOrderID(levelID, gen)` | `func` | Constructs `"MM-BTC-USDT-ASK-01-G003"`. Idempotency key for OS and ME. |

---

### [`diff.go`](./diff.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `DiffAction` | `type int` | `DiffCreate`, `DiffCancel`, `DiffCorrect` |
| `DiffEntry` | `struct` | One reconciliation action: action, LevelID, desired level, existing COID/OID. |
| `Diff(desired, tracker, marketID, cfg)` | `func` | **Two-pass diff.** Pass 1: desired vs known → CREATE (missing) or CORRECT (wrong price or qty < MinOrderSize). Pass 2: RESTING not in desired → CANCEL. PENDING/CANCELLING/STALE block CREATE but are excluded from CANCEL/CORRECT. Returns the minimal `[]DiffEntry`. |

#### Diff Decision Matrix

```
                 │  In desired?  │  Not in desired?
─────────────────┼───────────────┼─────────────────
PENDING          │  skip         │  skip
OS_REGISTERED    │  skip         │  skip
RESTING          │  check price  │  CANCEL
                 │  + qty → CORRECT
CANCELLING       │  skip         │  skip
STALE            │  skip         │  skip
Not tracked      │  CREATE       │  —
```
