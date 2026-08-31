# `internal/engine` — Liquidity Engine Orchestrator & Event Loop

**Package:** `engine`  
**Service:** Liquidity Engine  
**Last Updated:** August 2026

---

## 1. What This Package Does

This package is the **top-level orchestrator** of the Liquidity Engine. It owns the single-goroutine event loop that serialises all state mutations — no other goroutine may mutate tracker or inventory state.

It coordinates five subsystems:
- **Kafka Consumer** (`kafka.Consumer`) — pumps trade events into the event channel
- **Inventory Manager** (`inventory.Manager`) — tracks effective available balances
- **Order Tracker** (`order.Tracker`) — in-memory map of live MM level state
- **Reconciler** (`reconciler.Reconciler`) — diffs desired vs. actual and publishes commands
- **Kafka Producer** (`kafka.Producer`) — writes `OrderCreated` / `OrderCancelRequested` to Kafka

---

## 2. Files In This Package

| File | Purpose |
| :--- | :--- |
| `engine.go` | `Engine` struct, `EngineState` machine, event loop, all handler methods |
| `README.md` | This documentation file |

---

## 3. Concurrency Model

```
┌─────────────────────────────────────────────────────────────────┐
│                      Engine.Run() goroutine                     │
│                                                                 │
│  Owns: tracker, inventory, reconciler, state                    │
│  All state mutation happens here — ONLY here                    │
└───────────────────────────┬─────────────────────────────────────┘
                            │  reads from e.events channel
          ┌─────────────────┼──────────────────────────┐
          │                 │                          │
  kafka.Consumer       pumpTicker()              pumpTicker()
  (goroutine)          (goroutine x5)             (goroutine x5)
      │                    │                          │
      └────────────────────┴──────────────────────────┘
               All write to e.events (chan loopEvent, 256)
               NEVER mutate state directly
```

**Rule:** `e.events` is the only channel goroutines outside the Run loop may write to. They may never call methods that mutate `tracker` or `inventory` directly.

---

## 4. Engine State Machine

```
STARTING
   │
   ▼
SYNCING ─────── SyncFromOrderService fails ──► error (Run returns)
   │
   ▼
RUNNING ◄──────────────────────────────────────────────────────┐
   │                                                            │
   ├── inventory skew detected ──► DEGRADED ──────────────────►┘
   │
   ├── ME liveness timeout (consecutiveMETimeouts >= threshold)
   │        │
   │        ▼
   │      PAUSED ◄── wallet balance stale ◄──────────────────────
   │
   └── ctx.Done() ──► STOPPED (graceful shutdown)
```

| State | Description |
| :--- | :--- |
| `STARTING` | Service is initializing |
| `SYNCING` | Discovering existing MM orders from Order Service |
| `RUNNING` | Nominal two-sided market-making operation |
| `DEGRADED` | Running but inventory skew active on at least one market |
| `PAUSED` | ME unresponsive or critical balance failure; zero command generation |
| `STOPPED` | Graceful shutdown complete |

---

## 5. Event Kinds & Handlers

| Event | Source | Handler | Guard |
| :--- | :--- | :--- | :--- |
| `evTrade` | `kafka.Consumer` | `handleTrade()` | — |
| `evReconcileTick` | `reconcileTicker` (30s) | `runReconcileAll()` | RUNNING or DEGRADED |
| `evWalletTick` | `walletTicker` (5m) | `handleWalletRefresh()` | — |
| `evPendingCheck` | `pendingTicker` (PendingTimeout/2) | `handlePendingCheck()` | — |
| `evCancellingCheck` | `cancellingTicker` (CancellingTimeout/2) | `handleCancellingCheck()` | RUNNING or DEGRADED |
| `evResyncTick` | `resyncTicker` (ReconcileInterval×10) | `syncAllMarkets()` | RUNNING or DEGRADED |

---

## 6. Structs & Interfaces

### `Engine`

```go
type Engine struct {
    cfg                   *config.Config
    tracker               *order.Tracker
    inv                   *inventory.Manager
    reconciler            *reconciler.Reconciler
    producer              *kafka.Producer
    consumer              *kafka.Consumer
    metrics               EngineMetrics
    logger                *zap.Logger
    events                chan loopEvent        // internal event bus (buffered 256)
    state                 EngineState
    stateMu               sync.RWMutex         // protects state (State() is goroutine-safe)
    consecutiveMETimeouts map[string]int        // marketID → consecutive pending timeout count
}
```

### `EngineMetrics` interface

```go
type EngineMetrics interface {
    reconciler.ReconcilerMetrics
    SetEngineState(state string)
    SetLevelCount(marketID, side string, count int)
    ObserveReconcileDuration(marketID string, ms float64)
    IncMELivenessTimeout(marketID string)
}
```

---

## 7. Startup Sequence

```
Run(ctx) called
    │
    ├── 1. setState(STARTING)
    ├── 2. go consumer.Run(ctx)          ← Kafka consumer in background
    ├── 3. setState(SYNCING)
    ├── 4. syncAllMarkets(ctx)           ← fetch real MM orders from Order Service
    ├── 5. Start reconcileTicker, walletTicker, pendingTicker, cancellingTicker, resyncTicker
    ├── 6. go pumpTicker(ctx, ...) × 5  ← post tick events to e.events channel
    ├── 7. setState(RUNNING)
    ├── 8. runReconcileAll(ctx)          ← immediate first reconcile pass
    └── 9. EVENT LOOP (blocks until ctx.Done())
```

---

## 8. Handler Detail

### `handleTrade(ev kafka.TradeEvent)`

- Filters out trades where `ev.MMSide == ""` (MM not involved — skip)
- Calls `inv.ApplyTrade(ev)` to update in-memory balance view
- Finds the matching order in `tracker.All(ev.MarketID)` and updates `RemainingQty` / `FilledQty`
- Resets `consecutiveMETimeouts[ev.MarketID] = 0` (trade confirms ME is alive)
- Does **not** trigger reconcile — the next `evReconcileTick` will detect the missing level

### `handleWalletRefresh()`

- Calls `inv.IsStale(MaxBalanceStaleness)`
- If stale → logs error and transitions to `PAUSED`

### `handlePendingCheck(ctx)`

- Calls `reconciler.CheckPendingTimeouts(ctx, marketID)` per market
- If `consecutiveTimeouts >= MELivenessThreshold` → increments timeout counter, emits metric, and transitions to `PAUSED`

### `handleCancellingCheck(ctx)`

- Calls `reconciler.CheckCancellingTimeouts(ctx, marketID)` per market
- Delegates resolution entirely to the reconciler (retry or STALE)

### `runReconcileAll(ctx)`

- Skips if `inv.IsStale(MaxBalanceStaleness)` — stale inventory means unsafe quoting
- Computes `effectiveBase` and `effectiveQuote` per market
- Calls `inventory.ComputeSkew()` to get `BidCount`, `AskCount` for this cycle
- Calls `reconciler.ReconcileMarket(ctx, marketID, bidCount, askCount)` per market
- Transitions between RUNNING ↔ DEGRADED based on `skew.BaseTier` / `skew.QuoteTier`

---

## 9. Public API (used by health server)

| Method | Description |
| :--- | :--- |
| `State() EngineState` | Returns current engine state; goroutine-safe |
| `ReadyBids(marketID) int` | Number of RESTING bid orders for a market |
| `ReadyAsks(marketID) int` | Number of RESTING ask orders for a market |
| `IsReady() bool` | True if all markets have ≥ MinReadyBids and MinReadyAsks resting |

---

## 10. What This Package Does NOT Do

- Does NOT publish commands directly — delegated to `reconciler`
- Does NOT read from Kafka directly — `kafka.Consumer` runs in its own goroutine
- Does NOT manage balances — delegated to `inventory.Manager`
- Does NOT make pricing decisions — delegated to `pricing.GenerateLadder`
- Does NOT have a database or persistent state
