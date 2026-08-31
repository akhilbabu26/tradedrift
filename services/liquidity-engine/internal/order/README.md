# `internal/order` — MM Order Tracker & Diff Engine

**Package:** `order`  
**Service:** Liquidity Engine  
**Last Updated:** August 2026

---

## 1. What This Package Does

This package provides two things:

1. **`Tracker`** — the in-memory working picture of all active MM-001 orders across all markets. It is the LE's fast local view of what orders currently exist.

2. **`Diff()`** — the core reconciliation algorithm. It compares desired ladder levels (from `pricing.GenerateLadder`) against actual tracked orders and produces the **minimum necessary set** of CREATE / CANCEL / CORRECT actions. If desired == actual, it returns an empty slice — zero Kafka commands.

> **The Tracker is NOT authoritative.** The Order Service is the source of truth. The Tracker is a working cache, populated at startup via `ListMMOrders` and updated continuously by reconcile cycles and trade events.

---

## 2. Files In This Package

| File | Purpose |
| :--- | :--- |
| `tracker.go` | `Tracker`, `LiveOrder`, `OSOrder`, `ClientOrderID()`, all state mutation methods |
| `diff.go` | `Diff()`, `DiffEntry`, `DiffAction` constants |
| `diff_test.go` | Unit tests for all diff scenarios |
| `README.md` | This documentation file |

---

## 3. Order Lifecycle & Status

```
OrderCreate published
        │
        ▼
   PENDING ────────── PendingTimeout expires ──► [Check via Order Service]
        │                                              │
        │                                    ┌─────── ┤
        │                                    ▼        │
        │                                 RESTING ◄───┘ (OS confirms OPEN/PARTIALLY_FILLED)
        │
        ▼ (Order Service returns FILLED before PENDING resolves)
     [Remove from tracker — reconcile rebuilds]
        │
   RESTING ────────── reconciler issues cancel
        │
        ▼
  CANCELLING ────── CancellingTimeout expires ──► [Check via Order Service]
        │                                              │
        │                                    ┌─────── ┤
        │                                    ▼        │
        │                                 CANCELLED ──┘ → Remove
        │                                 FILLED ──────── Remove + IncFilled
        │                                 OPEN → retry cancel
        │
        └── retries >= CancelRetryLimit
                │
                ▼
             STALE  ← terminal; cleared only by periodic resync (SyncFromOrders)
```

### Status Diff Eligibility Rules

| Status | Blocks CREATE? | Eligible for CANCEL? | Eligible for CORRECT? |
| :--- | :---: | :---: | :---: |
| `PENDING` | ✅ Yes | ❌ No | ❌ No |
| `RESTING` | ✅ Yes | ✅ Yes | ✅ Yes |
| `CANCELLING` | ✅ Yes | ❌ No | ❌ No |
| `STALE` | ✅ Yes | ❌ No | ❌ No |

---

## 4. Three-Layer Order Identity

Every MM order has three identities that must be kept consistent:

```
LevelID       = "MM-BTC-USDT-ASK-01"          (stable logical slot — never changes)
Generation    = 3                               (monotonic lifecycle counter)
ClientOrderID = "MM-BTC-USDT-ASK-01-G003"     (idempotency key sent in OrderCreated payload)
OrderID       = "<UUID assigned by ME/OS>"      (authoritative ID for cancel commands)
```

- **LevelID** is the stable logical address in the order book. It is used as the map key in `Tracker.orders`.
- **Generation** increments each time a level transitions through a full fill/cancel/correction lifecycle. It prevents the Order Service from rejecting a new order for the same level as a duplicate.
- **ClientOrderID** = `LevelID + "-G" + zero-padded generation`. The Order Service stores this as `idempotency_key`. Used to look up orders in the OS during PENDING/CANCELLING resolution.
- **OrderID** = the ME/OS-assigned UUID. Used in cancel commands (`OrderCancelRequested.order_id`). Only learned after `SyncFromOrders` or `SetResting`.

---

## 5. `Diff()` Algorithm

```go
func Diff(desired []pricing.PriceLevel, tracker *Tracker, marketID string, cfg *config.MarketConfig) []DiffEntry
```

**Pass 1: desired → CREATE or CORRECT**

```
For each desired level:
    ├── not in knownSet (any status) → DiffCreate
    │
    ├── in knownSet but status != RESTING → skip (let it resolve)
    │
    ├── in knownSet, RESTING, price mismatch → DiffCorrect
    │
    ├── in knownSet, RESTING, remainingQty < MinOrderSize → DiffCorrect (consumed)
    │
    └── in knownSet, RESTING, price OK, qty OK → KEEP (no entry)
```

**Pass 2: RESTING → CANCEL**

```
For each RESTING order not in desiredSet → DiffCancel
```

**Core Invariant:**

> If `len(entries) == 0` → reconciler calls `IncReconcileNoop` and publishes **zero Kafka commands**.  
> The LE is silent when the book is already correct.

---

## 6. Committed Inventory Calculations

The tracker provides two methods used by `inventory.Manager` to compute effective available inventory:

### `CommittedBase(marketID) decimal.Decimal`

Sum of `RemainingQty` for all **RESTING and PENDING SELL** orders in `marketID`.

```
committed_base = Σ RemainingQty  for all SELL orders in {RESTING, PENDING}
```

Uses `RemainingQty` (not `OriginalQty`) to correctly account for partially filled asks.

### `CommittedQuote(marketID) decimal.Decimal`

Sum of `RemainingQty × Price` for all **RESTING and PENDING BUY** orders in `marketID`.

```
committed_quote = Σ (RemainingQty × Price)  for all BUY orders in {RESTING, PENDING}
```

---

## 7. `SyncFromOrders` — Startup & Recovery Resync

```go
func (t *Tracker) SyncFromOrders(marketID string, orders []OSOrder) int
```

Called during SYNCING (startup) and on `evResyncTick`. Merges Order Service state into the tracker:

- **Existing entry found**: Updates `OrderID`, `OriginalQty`, `RemainingQty`. If PENDING or STALE → promotes to RESTING.
- **New entry not in tracker**: Creates a new RESTING `LiveOrder`. Initializes generation to 1.
- **RESTING entry in tracker but NOT in OS response**: Removed by the reconciler's follow-up sweep.

---

## 8. Generation Counter — Idempotency Management

```go
func (t *Tracker) NextGeneration(levelID string) int   // increment + return
func (t *Tracker) DecrementGeneration(levelID string)  // rollback on publish failure
func (t *Tracker) CurrentGeneration(levelID string) int
```

The generation counter persists in `tracker.generations` and **survives `Remove()` calls**. This ensures that if an order for `MM-BTC-USDT-ASK-01` is filled (generation 2) and a new one is placed, it gets `client_order_id = MM-BTC-USDT-ASK-01-G003` — never duplicating a previous key.

`DecrementGeneration` is called when `PublishCreate` fails before writing to the tracker. This allows the next reconcile cycle to retry with the same `client_order_id`, making the retry idempotent at the Order Service level.

---

## 9. `ClientOrderID()` Format

```go
func ClientOrderID(levelID string, gen int) string
// Returns: "MM-BTC-USDT-ASK-01-G003"
```

Format: `{LevelID}-G{gen:03d}`  
The zero-padded 3-digit generation (`%03d`) allows up to 999 lifecycles per level slot.

---

## 10. Concurrency

All `Tracker` mutations must be called from the engine's single event loop goroutine. The only exception is `LiveOrder.IncrementCancelRetry()`, which is protected by `LiveOrder.mu` for the case where a timeout handler runs from a different context.

---

## 11. What This Package Does NOT Do

- Does NOT call the Order Service or Kafka directly — those are in `orderservice` and `kafka`
- Does NOT generate prices — that is `pricing.GenerateLadder`'s job
- Does NOT decide when to reconcile — that is the engine event loop's job
- Does NOT persist state — everything is lost on restart; startup sync rebuilds from OS
