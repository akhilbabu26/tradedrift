# package `reconciler`

## Purpose

Applies `Diff()` results as real Kafka commands, orchestrates dynamic reference-based repricing, enforces capital exposure caps, and manages the full **PENDING → OS_REGISTERED → RESTING → CANCELLING → STALE** state machine via timeout handlers.

## Problem It Solves

`Diff()` determines *what* desired orders differ from active orders. The reconciler solves *how* to converge those orders safely and efficiently in production:

1. **Zoned & Skew-Preserving Convergence**: Generates desired levels dynamically across LOW, MID, and HIGH zones, preserving tight inside-book liquidity when inventory skew reduces available levels.
2. **Authoritative Reference Price Repricing**: Uses live external reference prices (`platform/refprice`). Handles reference movements gracefully without thrashing Kafka:
   - **Small movement (< SmallBps)**: Keep existing orders.
   - **Moderate movement (SmallBps to LargeBps)**: Selective reprice.
   - **Large movement (>= LargeBps)**: Persistent multi-cycle controlled rebase (HIGH → MID → LOW priority, batched by `MaxBatch`) to prevent large cancellation storms.
   - **STALE reference**: Hold existing liquidity, block new order creations.
   - **PAUSED reference**: Completely halt mutations.
3. **Capital Exposure Protection**: Evaluates total committed quote (USDT) and base inventory before dispatching creates. Skips creates that would breach configured exposure caps.
4. **Slot-Level Order Expiry**: Periodically cancels and reprices orders older than `OrderLifetime` (30m) using the live reference price and latest `RefVersion`.
5. **Robust Identity & Timeout Handling**: Matches orders by exact ID, cancels orphan/stale generation orders in the ME, executes 2-cycle hysteresis on missing resting orders, and safely handles in-flight PENDING and CANCELLING timeouts.

## Package Architecture & File Structure

The reconciler is organized into 7 specialized source files:

- **`reconciler.go`**: Core coordinator. Runs `ReconcileMarket`, `CheckExpiredOrders`, and `CancelStaleGenerationOrders`.
- **`dispatch.go`**: Dispatches Diff actions (`applyEntry`, `applyCreate`, `applyCancel`). Enforces capital exposure caps.
- **`sync.go`**: Synchronizes state with ME snapshots (`syncWithMESnapshot`), manages 2-cycle hysteresis (`handleMissingRestingOrder`), and resyncs from Order Service (`SyncFromOrderService`).
- **`reprice.go`**: Resolves live reference price and version (`currentReference`), executes selective repricing, and coordinates multi-cycle rebase.
- **`refstate.go`**: Tracks persistent reference state (`RefState`), classifies price movements (`ClassifyMovement`), and tracks rebase progress.
- **`timeouts.go`**: Handles stuck PENDING and CANCELLING orders, and confirms RESTING orders against ME snapshots.
- **`test_helpers.go`**: Minimal inspection hooks enabling external test suites to verify internal transitions without polluting production code.

> **Note on Tests**: To prevent file clutter and maintain clear package boundaries, all unit and invariant test suites have been relocated to the dedicated [`test/reconciler/`](../../test/reconciler/) directory (`package reconciler_test`).

---

## Flow: Full Reconcile Cycle

```
engine.runReconcileAll()
         │
         ▼
  reconciler.ReconcileMarket(ctx, marketID, bidCount, askCount)
         │
         ├── Step 1: Authoritative Reference Price Resolution [in reprice.go]
         │     ref, refVer, freshness := currentReference(mc)
         │     │
         │     ├── freshness == FreshnessPaused? → PAUSE (zero mutations, skip snapshot sync, return 0)
         │     │
         │     └── Track / update RefState:
         │           ClassifyMovement(lastRef, ref, freshness, mc.Repricing)
         │           ├── ActionControlledRebase? → activate RebaseActive & record RebaseTargetVer
         │           └── RebaseActive ongoing? → execute next batch (MaxBatch)
         │                 applyRefMovementReprice(mc, targetRef, targetVer, action)
         │
         ├── Step 2: ME Snapshot & Health Verification
         │     meClient.FetchSnapshot(ctx, marketID)
         │     │
         │     ├── ME state != "LIVE"? → Pause cycle, skip diff/apply
         │     │
         │     └── syncWithMESnapshot(ctx, marketID, snap, mc) [in sync.go]
         │           ├── Confirm matched orders as RESTING & sync RemainingQty (INV-MM-08)
         │           ├── Cancel orphan orders in ME (stale gen / untracked) (INV-MM-05)
         │           └── Missing order check with 10s grace period & 2-cycle hysteresis:
         │                 missing >= 2 → handleMissingRestingOrder() (lock slot as CANCELLING)
         │
         ├── Step 3: Zoned Ladder Generation [in pricing]
         │     pricing.GenerateZonedDesired(mc, ref, tracker, bidZones, askZones, rng, bidCount, askCount)
         │     (Produces desired levels with LOW-first skew allocation, jitter, and refVer)
         │
         ├── Step 4: Diff Calculation [in order]
         │     order.Diff(desired, tracker, marketID, cfg) → []DiffEntry
         │     (Protects in-flight and cancelling prices from duplicate collisions)
         │
         └── Step 5: Diff Action Dispatch [in dispatch.go]
               for each DiffEntry:
                 │
                 ├─ DiffCreate ──→ applyCreate()
                 │                  1. Capital Budget Gate: canCreateLevel(mc, desired)
                 │                     - BUY: committedQuote + (Price * Qty) <= MaxBidExposureUSDT
                 │                     - SELL: committedBase + Qty <= MaxAskExposureBase
                 │                     (Skip gracefully if cap breached, returns published=false)
                 │                  2. If freshness == FreshnessStale → Skip (preserve liquidity, no new adds)
                 │                  3. orderSvc.CreateMMOrder() → get orderID
                 │                  4. tracker.SetPending() (storing Zone & RefVersion)
                 │                  5. producer.PublishCreate() → Kafka
                 │                  6. tracker.SetKafkaPublished(true)
                 │
                 ├─ DiffCancel ──→ applyCancel()
                 │                  1. orderSvc.CancelMMOrder() (optional ledger sync)
                 │                  2. producer.PublishCancel() → Kafka
                 │                  3. tracker.SetCancelling() + record inFlightSince
                 │
                 └─ DiffCorrect → applyCancel() + tracker.QueueCorrection()
                                    (replacement created after cancel confirmed via canCreateLevel)
```

---

## Flow: Reference Movement & Controlled Rebase

```
currentReference(mc)
         │
         ▼
ClassifyMovement(prevRef, nextRef, freshness, repricingCfg)
         │
         ├── Freshness == PAUSED  ──> ActionPause (halt all mutations)
         ├── Freshness == STALE   ──> ActionHold (maintain resting orders, block new creations)
         ├── |Δ| < SmallBps (10)  ──> ActionKeep (do nothing, spread absorbs noise)
         ├── SmallBps <= |Δ| < LargeBps ──> ActionSelectiveReprice (reprice affected zone)
         └── |Δ| >= LargeBps (100)──> ActionControlledRebase (persistent multi-cycle rebase)
                                            │
                                            ▼
                           Persistent Multi-Cycle Loop:
                           RefState.RebaseActive = true
                           RebaseTargetVer = latest authoritative version
                           Priority Queue: HIGH Zone → MID Zone → LOW Zone
                           Batch Size: up to MaxBatch (default: 4 orders per cycle)
                           Until all eligible resting orders are rebased.
```

---

## Flow: Slot-Level Order Lifetime Expiry

```
reconcileTicker (or expiry timer) fires
         │
         ▼
CheckExpiredOrders(ctx, marketID)
         │
         ├── 1. Resolve live reference & version via currentReference(mc)
         │
         └── 2. Scan tracker for RESTING orders older than OrderLifetime (30m):
               │
               ├── Select up to MaxBatch oldest expired orders
               │
               ├── Compute new replacement PriceLevel anchored to live ref & version
               │
               ├── tracker.QueueCorrection(levelID, newLevel)
               │
               ├── Publish cancel via producer.PublishCancel()
               │
               └── tracker.SetCancelling(levelID)
                   (Once ME confirms cancel, replacement order is placed automatically)
```

---

## Flow: Pending Timeout Check

```
CheckPendingTimeouts(ctx, marketID) [every PendingTimeout/2]
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

## Flow: Cancelling Timeout Check

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

## Files

### [`reconciler.go`](./reconciler.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `Reconciler` | `struct` | Main coordinator holding tracker, producer, orderSvc, meClient, config, logger, metrics, refStates, and timeout maps. |
| `NewReconciler(...)` | `func` | Constructs a new Reconciler wiring all dependencies. |
| `NewReconcilerWithRNG(...)` | `func` | Constructs Reconciler with deterministic RNG source for reproducible testing. |
| `SetRefProvider(p)` | `func` | Injects the external reference price provider (`platform/refprice`). |
| `ReconcileMarket(ctx, marketID, bidCount, askCount)` | `func` | Orchestrates the full snapshot sync, reference check, zoned ladder generation, diff, and command dispatch. |
| `CheckExpiredOrders(ctx, marketID)` | `func` | Identifies orders exceeding `OrderLifetime` (30m) and initiates cancel-replace with live reference and version. |
| `CancelStaleGenerationOrders(ctx, marketID)` | `func` | Safety cleanup cancelling orders from outdated generations after recovery. |

### [`dispatch.go`](./dispatch.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `canCreateLevel(mc, level)` | `func` (internal) | Final creation safety gate: defensively validates `Price > 0`, `Quantity > 0`, valid `Side`, `MarketID` match, `MinOrderSize`/`LotSize`, and capital exposure caps. |
| `applyEntry(ctx, e, mc)` | `func` (internal) | Routes DiffEntry to `applyCreate`, `applyCancel`, or cancel + QueueCorrection. Returns `(published bool, err error)`. |
| `applyCreate(ctx, e, mc)` | `func` (internal) | Enforces `canCreateLevel`, registers in OS, marks PENDING with metadata, and publishes to Kafka. Returns `(published bool, err error)`. |
| `applyCancel(ctx, e, mc)` | `func` (internal) | Cancels in OS (optional ledger sync) and publishes `OrderCancelRequested` with ME UUID to Kafka. |

### [`sync.go`](./sync.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `parseMERemainingQuantity(meOrder, fallback)` | `func` (internal) | Authoritative remaining quantity parser: accepts `"0"` as valid full fill; rejects negative values. |
| `syncWithMESnapshot(ctx, marketID, snap, mc)` | `func` (internal) | Validates ME atomic snapshot, synchronizes `RemainingQty`, cancels orphan/stale orders, and applies 2-cycle hysteresis to missing orders. |
| `handleMissingRestingOrder(ctx, o, mc)` | `func` (internal) | Handles orders missing from ME after hysteresis: checks OS; if OPEN, initiates cancel and locks slot as CANCELLING. |
| `SyncFromOrderService(ctx, marketID)` | `func` | Pulls authoritative state from OS and seeds highest historical generations (`SetMaxGeneration`). |

### [`reprice.go`](./reprice.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `currentReference(mc)` | `func` (internal) | Resolves the authoritative reference price snapshot (`ReferenceSnapshot`). Preserves provider `FetchedAt` in `RefState.LastUpdated`. On provider failure, persists newly evaluated `STALE` or `PAUSED` into `RefState.Freshness`. |
| `isMarketPaused(marketID)` | `func` (internal) | Consumes authoritative `currentReference()` snapshot semantics to verify if a market is currently PAUSED. |
| `applyRefMovementReprice(ctx, mc, ref, targetVer, action)` | `func` (internal) | Dispatches selective repricing and multi-cycle rebase across zones (HIGH → MID → LOW) in `MaxBatch` batches, using `RemainingQty` as direct replacement authority and capping to `OriginalQty` if violated. |

> **Architectural Invariants & Contracts**:
> - **Authoritative Provider Clock**: `RefState.LastUpdated` strictly records the provider's authoritative `FetchedAt` timestamp. It is never overwritten with the LE reconcile execution timestamp, preventing delayed detection of provider outages.
> - **Unified Reference Snapshot**: `currentReference(mc)` returns an immutable `ReferenceSnapshot{Price, Version, FetchedAt, Freshness}` consumed across `ReconcileMarket()`, `CheckExpiredOrders()`, and `isMarketPaused()`. Provider failure persists derived `STALE`/`PAUSED` directly into `RefState.Freshness`.
> - **Authoritative ME Remaining Quantity**: `parseMERemainingQuantity()` treats `"0"` as authoritative information that an order is fully consumed, preventing resurrection. Negative values are strictly rejected.
> - **Order Service NOT_FOUND Safety**: In `handleCancellingTimeout()`, `orderservice.ErrOrderNotFound` preserves tracked `RemainingQty` rather than assuming a full fill, and does not increment `OrdersFilled`.
> - **Direct RemainingQty Replacement Authority**: Expiry and rebase replacements directly use `replacementQty = RemainingQty`. If `RemainingQty > OriginalQty`, an invariant violation is logged and capped to `OriginalQty`.
> - **Defensive Final Creation Gate**: Central `canCreateLevel` acts as the final gate verifying `Price > 0`, `Quantity > 0`, valid side, market match, dust size, and exposure caps across all order creation paths.
> - **Explicit PAUSED Semantics vs In-Flight Retry**:
>   - **`REFPRICE_PAUSED`** (external reference age $\ge \text{PauseThreshold}$) and **`ME_NOT_LIVE` / `ME_UNAVAILABLE`** (ME disconnected or non-LIVE):
>     - **NO new creates**: Ladder generation is skipped entirely.
>     - **NO new replacements**: Expiry cancel-replace is halted; queued replacements on cancelled orders are dropped.
>     - **NO rebase repricing**: Ladder rebase is halted; resting orders remain resting.
>     - **IN-FLIGHT RETRIES ONLY**: Unconfirmed PENDING orders (`KafkaPublished == false`) may retry their *exact original create command* (preserving `OriginalQty`, `OrderID`, and `ClientOrderID`) to guarantee idempotent create recovery without altering order parameters.

### [`refstate.go`](./refstate.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `RefState` | `struct` | Tracks market reference state: `LastRef`, `LastVersion`, `LastUpdated`, `Freshness`, `RebaseActive`, `RebaseTargetVer`, `RebaseTargetRef`. |
| `ClassifyMovement(prev, next, freshness, cfg)` | `func` | Classifies reference movement into `ActionKeep`, `ActionSelectiveReprice`, `ActionControlledRebase`, `ActionHold`, or `ActionPause`. |

### [`timeouts.go`](./timeouts.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `CheckPendingTimeouts(ctx, marketID)` | `func` | Resolves unconfirmed PENDING orders via Kafka retry or Order Service lookup. |
| `CheckCancellingTimeouts(ctx, marketID)` | `func` | Resolves CANCELLING orders via OS confirmation or cancel retries. |
| `ConfirmRestingFromSnapshot(marketID, snap)` | `func` | Promotes orders to RESTING only when confirmed present in the ME snapshot. |
| `CheckOSRegisteredTimeouts(marketID, timeout, meHealthy)` | `func` | Verifies OS_REGISTERED orders against ME snapshots. |
| `retryCancelOrStale(ctx, o, mc)` | `func` (internal) | Retries cancel commands under limit; escalates to STALE at retry limit. |

### [`test_helpers.go`](./test_helpers.go)

| Symbol | Kind | Purpose |
|:---|:---|:---|
| Exported test hooks | `func` | Safe, minimal getters/setters (`SyncWithMESnapshot`, `CurrentReference`, `GetMissingCycles`, `GetRefState`, etc.) used by external test suites in `test/reconciler/`. |
