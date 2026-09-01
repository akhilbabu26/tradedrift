# package `engine`

## Purpose

The `engine` package is the **top-level orchestrator** of the Liquidity Engine. It owns the single goroutine that serialises all state mutations and coordinates every subsystem: inventory, order tracker, reconciler, Kafka consumer, ME health probes, and wallet refresh.

## Problem It Solves

A market-maker must continuously maintain a live ladder of orders in the exchange's order book. This requires coordinating many concurrent inputs:

- **Trade fills** arriving from Kafka at any time.
- **Periodic tickers** firing for reconcile, wallet refresh, pending/cancelling checks.
- **ME health probes** deciding whether it's safe to place new orders.
- **HTTP handlers** needing to read engine state without blocking the event loop.

Without a structured model, all of these would race on shared state (order tracker, inventory, market-paused flags) causing silent data corruption.

## How It Solves It

The engine uses a **single-goroutine event loop**:

1. All tickers post `loopEvent` messages to a buffered `chan loopEvent` via dedicated pump goroutines. They never touch state directly.
2. The Kafka consumer posts `TradeEnvelope` to `chan kafka.TradeEnvelope`.
3. **Only the `Run()` goroutine reads both channels** — it is the sole writer of all mutable state.
4. HTTP handlers read state **lock-free** via `atomic.Pointer[StatusSnapshot]`.

---

## System Flow

```
┌───────────────────────────────────────────────────────────────┐
│                     Liquidity Engine                          │
│                                                               │
│  reconcileTicker ──┐                                          │
│  walletTicker ─────┤  pumpTicker()  ──→ e.events chan         │
│  pendingTicker ────┤  goroutines                              │
│  cancellingTicker ─┘                        │                 │
│                                             ▼                 │
│  kafka.Consumer ──────────────────→ e.tradeEvents chan        │
│                                             │                 │
│                              ┌──────────────┘                 │
│                              ▼                                │
│                    Run() event loop (single goroutine)        │
│                    ├── handleTrade()                          │
│                    ├── runReconcileAll()                      │
│                    ├── handleWalletRefresh()                  │
│                    ├── handlePendingCheck()                   │
│                    ├── handleCancellingCheck()                │
│                    └── publishSnapshot() ──→ atomic.Pointer   │
│                                                    │          │
│  HTTP /healthz ──────────────────────────→ atomic.Load()     │
│  HTTP /readyz  ──────────────────────────→ atomic.Load()     │
│  HTTP /status  ──────────────────────────→ atomic.Load()     │
└───────────────────────────────────────────────────────────────┘
```

---

## State Machine

```
STARTING
   │  initial wallet fetch + Order Service sync
   ▼
SYNCING
   │  sync complete
   ▼
RUNNING ◄──────────────────────────────────┐
   │  inventory skew OR market pause        │  skew resolved AND no pauses
   ▼                                        │
DEGRADED ───────────────────────────────────┘
   │  ctx cancelled
   ▼
STOPPED
```

Market-level pauses (ME liveness failures) cause `DEGRADED` but do NOT stop the engine. Auto-recovery happens when ME becomes healthy.

---

## Files

### [`engine.go`](./engine.go) — Core struct & lifecycle

| Symbol | Kind | Purpose |
|:---|:---|:---|
| `EngineState` | `type int` | Lifecycle states: `STARTING`, `SYNCING`, `RUNNING`, `DEGRADED`, `PAUSED`, `STOPPED` |
| `Engine` | `struct` | Main orchestrator. Holds all subsystem dependencies and internal state. |
| `NewEngine(...)` | `func` | Wires all dependencies. Publishes the initial atomic snapshot. Does NOT start the loop. |
| `setState(s)` | `func` | Updates state, emits a Prometheus gauge, logs the transition, and refreshes the snapshot. **Event-loop only.** |
| `Run(ctx)` | `func` | **The event loop.** Fetches initial balances → syncs OS → probes ME → starts tickers → enters `select` loop. |
| `pumpTicker(ctx, ch, kind)` | `func` | Goroutine bridge: reads `time.Ticker` channel and forwards `loopEvent` to `e.events`. Prevents tickers from mutating state directly. |

---

### [`handlers.go`](./handlers.go) — Per-event processors

| Function | Problem It Solves |
|:---|:---|
| `handleTrade(env)` | Deduplicates fills (bounded 1000-entry ring), applies fill to inventory, updates tracker remaining qty, marks market dirty, schedules targeted reconcile. Closes `Ack` after all mutations — Kafka commits only after this. |
| `scheduleTargetedReconcile()` | Debounces rapid fills (200ms window) into a single `evTargetedReconcile` event to prevent reconcile storms. |
| `handleWalletRefresh(ctx)` | Fetches MM-001 balances. Transitions to DEGRADED on stale balances; recovers to RUNNING when fresh. |
| `refreshWalletBalances(ctx)` | Calls `walletSvc.GetMMBalances` with a 5s timeout. Caller logs and continues on error. |
| `handlePendingCheck(ctx)` | Single ME probe per tick → evaluates consecutive timeout counter per market → pauses markets at threshold. Calls `CheckPendingTimeouts` and `CheckOSRegisteredTimeouts`. |
| `handleCancellingCheck(ctx)` | Re-queries OS for CANCELLING orders. Retries cancel or escalates to STALE. |

---

### [`reconcile.go`](./reconcile.go) — Reconcile flows

| Function | Problem It Solves |
|:---|:---|
| `runReconcileAll(ctx)` | Full reconcile across all 3 markets. Skips paused/stale markets. Computes skew per market → calls `ReconcileMarket` → updates engine state. |
| `runReconcileMarket(ctx, marketID)` | Targeted single-market reconcile triggered after a fill. Same safety gates as full reconcile. |
| `syncAllMarkets(ctx)` | Queries Order Service for all active MM orders and updates the tracker. Called at startup and on `evResyncTick`. |

---

### [`snapshot.go`](./snapshot.go) — Lock-free read interface

| Symbol | Purpose |
|:---|:---|
| `StatusSnapshot` | Immutable struct: engine state, per-market status strings, RESTING bid/ask counts, inventory staleness. |
| `publishSnapshot()` | Builds a fresh snapshot and stores it atomically (`atomic.Store`). **Event-loop only.** |
| `State()` | Returns engine state from snapshot. Lock-free. |
| `ReadyBids(marketID)` | Returns RESTING bid count for a market. Lock-free. |
| `ReadyAsks(marketID)` | Returns RESTING ask count for a market. Lock-free. |
| `MarketStates()` | Returns a **defensive copy** of per-market status strings. Lock-free. |
| `IsReady()` | `true` if RUNNING/DEGRADED and ≥ `MinReadyBids` bids + ≥ `MinReadyAsks` asks are RESTING. Used by `/readyz`. |
| `InventoryLastRefresh()` | Timestamp of last successful wallet fetch. |
| `MaxBalanceStaleness()` | Configured staleness threshold. Exposed for health handler staleness calculation. |
