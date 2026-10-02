# package `order`

## Purpose

Provides the **in-memory order tracker** and **diff algorithm** that form the operational core of the LE's reconciliation loop. The tracker maintains the LE's working picture of all MM orders; the diff computes the minimal set of actions to converge actual state to desired state.

## Problem It Solves

The LE needs to maintain exactly N bid levels and M ask levels in the ME order book at all times across dynamic volatility zones (LOW, MID, HIGH). The challenge is that order state is spread across three asynchronous systems:

- **Order Service (authoritative ledger)**: knows if an order is OPEN, FILLED, or CANCELLED in PostgreSQL.
- **Matching Engine (execution core)**: knows if an order is actively resting in the in-memory book.
- **LE Tracker (in-memory state coordinator)**: the LE's own low-latency view, tracking zones, reference versions, queued corrections, and capital commitments.

Without a local tracker, every reconcile cycle would require heavy gRPC queries. Without a diff algorithm, every cycle would blindly cancel and replace everything — causing liquidity gaps, high latency, and Kafka command storms.

## How It Solves It

The tracker stores every MM order by its stable `LevelID` (e.g., `MM-BTC-USDT-ASK-01`). Each level has a lifecycle status, mononotically increasing `Generation` counter (`MM-BTC-USDT-ASK-01-G003`), zone classification (`LOW`, `MID`, `HIGH`), and `RefVersion` stamp.

`Diff()` performs a two-pass set comparison:
1. **Desired vs Known**: Creates missing slots (`DiffCreate`) and schedules price/quantity corrections (`DiffCorrect`).
2. **Known vs Desired**: Cancels extraneous resting slots (`DiffCancel`).
3. **Price Collision Protection**: Locks prices belonging to CANCELLING or QueuedCorrection slots in a uniqueness set so new CREATEs never collide with orders pending removal.

---

## Order Lifecycle Flow

```
                 ┌─────────────────────────────────────────────┐
                 │       Diff() produces DiffCreate            │
                 ▼                                             │
            PENDING ←── SetPending()                          │
                 │  (stores Zone, RefVersion, KafkaPublished)  │
                 │                                             │
    ┌────────────┤ CheckPendingTimeouts()                      │
    │            │  OS confirms OPEN                           │
    │            ▼                                             │
    │       OS_REGISTERED ←── SetOSRegistered()               │
    │            │                                             │
    │            │ syncWithMESnapshot() / ConfirmResting()     │
    │            │  (ME snapshot confirms order in book)       │
    │            ▼                                             │
    │         RESTING ←── SetResting()                        │
    │            │        (RemainingQty synced from snapshot) │
    │       ┌────┴─────────────────┐                          │
    │  DiffCancel             DiffCorrect / Reprice / Expiry   │
    │       │                      │                          │
    │       ▼                      ▼                          │
    │   CANCELLING ←── SetCancelling()                        │
    │       │          (QueuedCorrection stored with new price)│
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

### [`order.go`](./order.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `Status` | `type string` | Order lifecycle states: `PENDING`, `OS_REGISTERED`, `RESTING`, `CANCELLING`, `STALE`. |
| `LiveOrder` | `struct` | Represents one active order in the tracker: `LevelID`, `OrderID`, `ClientOrderID`, `Generation`, `Price`, `OriginalQty`, `RemainingQty`, `FilledQty`, `Status`, `Zone` ("LOW"|"MID"|"HIGH"), `RefVersion`, `QueuedCorrection`, and timestamps. |
| `IncrementCancelRetry()` | `func` | Increments cancel retry counter and resets timer on `LiveOrder`. |
| `OSOrder` | `struct` | Order representation received from the Order Service with parsed `LevelID` and `Generation`. |
| `ClientOrderID(levelID, gen)` | `func` | Constructs stable idempotency key (e.g. `"MM-BTC-USDT-ASK-01-G003"`). |
| `Diff(desired, tracker, marketID, cfg)` | `func` | Two-pass comparison between desired `[]PriceLevel` and tracked orders. Protects locked/in-flight prices in the uniqueness set. |

---

### [`tracker.go`](./tracker.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `Tracker` | `struct` | Thread-safe in-memory store for orders, generations, and sync timestamps. Protected by `sync.RWMutex`. |
| `NewTracker()` | `func` | Creates an initialized, empty tracker. |
| `SetPending(levelID, orderID, coid, gen, level)` | `func` | Registers a newly created order in PENDING state, copying `Price`, `Quantity`, `Zone`, and `RefVersion` from desired level. |
| `SetKafkaPublished(levelID, bool)` | `func` | Marks Kafka publication confirmed. Retried by `CheckPendingTimeouts` if false. |
| `SetOSRegistered(levelID, orderID, ...)` | `func` | Transitions order to OS_REGISTERED upon Order Service confirmation. |
| `SetResting(levelID, orderID, orig, rem)` | `func` | Promotes order to RESTING and updates `RemainingQty` from the ME snapshot (INV-MM-08). |
| `SetCancelling(levelID)` | `func` | Transitions order to CANCELLING when a cancel command is emitted. |
| `QueueCorrection(levelID, desired)` | `func` | Attaches a desired replacement `PriceLevel` to a CANCELLING order. |
| `SetStale(levelID)` | `func` | Marks order STALE when cancel retries exceed limit; halts further automated mutations until full resync. |
| `Remove(levelID)` | `func` | Removes order from tracking. Preserves generation counter to guarantee monotonicity. |
| `NextGeneration(levelID)` | `func` | Increments and returns the next generation number for a level slot. |
| `CurrentGeneration(levelID)` | `func` | Reads the current generation without incrementing. |
| `SetMaxGeneration(levelID, gen)` | `func` | Updates generation counter to highest observed historical generation during recovery (INV-MM-07). |
| `CommittedBase(marketID)` | `func` | Calculates the total base currency quantity currently committed in active ask orders. Used for ask exposure cap checks. |
| `CommittedQuote(marketID)` | `func` | Calculates the total quote currency value (`Price × RemainingQty`) committed in active bid orders. Used for bid exposure cap checks. |
| `Get(levelID)` | `func` | Returns pointer to tracked `LiveOrder`, or nil. |
| `All(marketID)` | `func` | Returns snapshot slice of all tracked orders for a market. |
| `AllMarkets()` | `func` | Returns all tracked orders across all configured markets. |
| `ActiveCount(marketID, side)` | `func` | Returns count of active RESTING and OS_REGISTERED orders on a side. |
