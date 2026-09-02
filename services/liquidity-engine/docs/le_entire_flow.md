# Complete End-to-End Architectural Flow of the Liquidity Engine (LE)

This document provides a comprehensive, step-by-step technical breakdown of **every execution flow** within the **TradeDrift Liquidity Engine (LE)**. It explains how startup, reconciliation, order creation, cancellations, trade processing, inventory management, and health monitoring operate seamlessly in production.

---

## Table of Contents
1. [High-Level Architecture & Master Flow](#1-high-level-architecture--master-flow)
2. [Flow 1: Startup, Discovery, & Recovery Flow](#2-flow-1-startup-discovery--recovery-flow)
3. [Flow 2: Core Periodic & Targeted Reconciliation Flow](#3-flow-2-core-periodic--targeted-reconciliation-flow)
4. [Flow 3: Crash-Safe 3-Step Order Creation Flow](#4-flow-3-crash-safe-3-step-order-creation-flow)
5. [Flow 4: Safe Order Correction & Replacement Flow](#5-flow-4-safe-order-correction--replacement-flow)
6. [Flow 5: Real-Time Trade Fill & Inventory Update Flow](#6-flow-5-real-time-trade-fill--inventory-update-flow)
7. [Flow 6: Timeout Handlers & State Machine Transitions](#7-flow-6-timeout-handlers--state-machine-transitions)
8. [Flow 7: ME Health Probing & Fault Isolation Flow](#8-flow-7-me-health-probing--fault-isolation-flow)
9. [Flow 8: Lock-Free HTTP Snapshot & Monitoring Flow](#9-flow-8-lock-free-http-snapshot--monitoring-flow)

---

## 1. High-Level Architecture & Master Flow

```text
┌─────────────────────────────────────────────────────────────────────────────────────────────────┐
│                                 LIQUIDITY ENGINE MASTER ARCHITECTURE                            │
│                                                                                                 │
│   TIMERS / PUMP GOROUTINES              KAFKA CONSUMER (trades.executed)                        │
│   ├── reconcileTicker  (30s) ──┐        │                                                       │
│   ├── walletTicker     (15s) ──┤        │ FetchMessage() → TradeEnvelope{Event, Ack}            │
│   ├── pendingTicker     (5s) ──┼──┐     │                                                       │
│   ├── cancellingTicker (15s) ──┤  │     ▼                                                       │
│   └── resyncTicker     (45s) ──┘  │  e.tradeEvents chan                                         │
│                                   │     │                                                       │
│                                   ▼     ▼                                                       │
│                            e.events chan (loopEvent)                                            │
│                                   │                                                             │
│                                   ▼                                                             │
│             ┌───────────────────────────────────────────────┐                                   │
│             │    SINGLE-THREADED ENGINE EVENT LOOP (Run)    │                                   │
│             │      (Sole writer of all mutable state)       │                                   │
│             └───────┬─────────────┬─────────────┬───────────┘                                   │
│                     │             │             │                                               │
│                     ▼             ▼             ▼                                               │
│              order.Tracker   inventory.Mgr  reconciler.Reconciler                               │
│                     │             │             │                                               │
│                     │             │             ├── 1. GenerateLadder()                         │
│                     │             │             ├── 2. order.Diff()                             │
│                     │             │             └── 3. Apply Actions:                           │
│                     │             │                    ├── gRPC OrderService (CreateMMOrder)    │
│                     │             │                    └── Kafka Producer (orders.commands)     │
│                     │             │                                                             │
│                     ▼             ▼                                                             │
│             publishSnapshot() ──> atomic.Pointer[StatusSnapshot]                                │
│                                             │                                                   │
│                                             ▼                                                   │
│                               HTTP Server (/healthz, /readyz, /status)                          │
└─────────────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Flow 1: Startup, Discovery, & Recovery Flow

When the Liquidity Engine boots up, it transitions through `STARTING → SYNCING → RUNNING` to restore its in-memory tracker from the Order Service before placing any new orders.

```text
[Engine Boot]
   │
   ▼
[State: STARTING]
   │
   ├── 1. Initialize Components:
   │      tracker := order.NewTracker()
   │      inv     := inventory.NewManager(tracker)
   │      rec     := reconciler.NewReconciler(...)
   │      publishes initial StatusSnapshot (StateStarting)
   │
   ├── 2. Start Kafka Consumer in background goroutine (reads trades.executed)
   │
   ├── 3. Fetch Initial Wallet Balances (gRPC walletSvc.GetMMBalances)
   │      - Populates projectedBalances for BTC, ETH, SOL, USDT
   │      - Validates system account MM-001 has required assets
   │
   ▼
[State: SYNCING]
   │
   ├── 4. Authoritative Order Discovery (reconciler.SyncFromOrderService)
   │      - Calls orderSvc.ListMMOrders(marketID) for OPEN & PARTIALLY_FILLED orders
   │      - For each order, parses idempotency_key:
   │          "MM-BTC-USDT-ASK-01-G007" ──> LevelID: "MM-BTC-USDT-ASK-01", Gen: 7
   │      - Seeds tracker in OS_REGISTERED state
   │      - Sets generation baseline (G007) so next order becomes G008
   │
   ├── 5. Matching Engine Liveness Verification (HTTP meclient.CheckAllMarkets)
   │      - Verifies ME HTTP /status endpoint is healthy
   │
   ▼
[State: RUNNING]
   │
   ├── 6. Start 5 Ticker Pump Goroutines:
   │      - reconcileTicker  (every 30s)
   │      - walletTicker     (every 15s)
   │      - pendingTicker    (every 5s)
   │      - cancellingTicker (every 15s)
   │      - resyncTicker     (every 45s)
   │
   └── 7. Trigger Initial Full Reconcile (runReconcileAll)
          - Ladders missing price levels into the Matching Engine
```

---

## 3. Flow 2: Core Periodic & Targeted Reconciliation Flow

Reconciliation compares the **desired market state** against the **actual local tracker state** and emits the minimal set of commands needed to converge them.

```text
reconcileTicker fires (or Debounced Targeted Reconcile after fill)
   │
   ▼
engine.runReconcileMarket(ctx, marketID)
   │
   ├── Step 1: Safety & Staleness Gates
   │   • Is market paused due to ME liveness failure (PAUSED_ME)? ──> SKIP
   │   • Is Order Service sync older than MaxOrderStateStaleness (90s)? ──> SKIP
   │   • Is inventory balance older than MaxBalanceStaleness (60s)? ──> SKIP
   │
   ├── Step 2: Compute Inventory Skew (inventory.ComputeSkew)
   │   • EffectiveBase  = projectedBase - tracker.CommittedBase(marketID)
   │   • EffectiveQuote = projectedUSDT - Σ tracker.CommittedQuote(all markets)
   │   • Decision Matrix:
   │       Effective > MinThreshold      ──> TierNormal   (12 Bids / 12 Asks)
   │       Effective <= MinThreshold     ──> TierLow      (6 Bids / 6 Asks)
   │       Effective <= CriticalThreshold──> TierCritical (0 levels on that side)
   │
   ├── Step 3: Mathematical Ladder Generation (pricing.GenerateLadder)
   │   • Computes geometric spread for i = 1..N:
   │       Bid_i = floor(RefPrice / (1 + SpreadBps * i / 10000), TickSize)
   │       Ask_i = floor(RefPrice * (1 + SpreadBps * i / 10000), TickSize)
   │   • Produces desired []PriceLevel (e.g., 24 levels for BTC-USDT)
   │
   ├── Step 4: Two-Pass Diffing Algorithm (order.Diff)
   │   • Pass 1 (Desired vs Known):
   │       - Missing in tracker? ──> DiffCreate
   │       - RESTING with wrong price or depleted qty? ──> DiffCorrect
   │       - In PENDING / OS_REGISTERED / CANCELLING / STALE? ──> Skip (in-flight)
   │   • Pass 2 (Known vs Desired):
   │       - RESTING but not in desired list? ──> DiffCancel
   │
   └── Step 5: Execute Diff Actions (reconciler.applyEntry)
       • For each DiffCreate  ──> Crash-Safe 3-Step Creation
       • For each DiffCancel  ──> Publish Cancel
       • For each DiffCorrect ──> Cancel Old + Queue Replacement
```

---

## 4. Flow 3: Crash-Safe 3-Step Order Creation Flow

To prevent duplicate orders and orphan states across crashes, new orders follow a strict 3-step sequence:

```text
DiffCreate encountered for LevelID: "MM-BTC-USDT-BID-01"
   │
   ▼
Step 1: Pre-Register in Order Service (gRPC CreateMMOrder)
   • ClientOrderID = tracker.NextGeneration("MM-BTC-USDT-BID-01") ──> "MM-BTC-USDT-BID-01-G008"
   • Calls orderSvc.CreateOrder(UserId=MM-UUID, IdempotencyKey=ClientOrderID, ...)
   • Order Service stores order in PostgreSQL with status OPEN
   • Returns authoritative OrderID (UUID)
   │
   ▼
Step 2: Track Locally in PENDING State (tracker.SetPending)
   • Adds LiveOrder to tracker:
       - Status: PENDING
       - OrderID: returned UUID
       - ClientOrderID: "MM-BTC-USDT-BID-01-G008"
       - Generation: 8
       - KafkaPublished: false
       - PendingSince: time.Now()
   │
   ▼
Step 3: Publish Command to Kafka (producer.PublishCreate)
   • Wraps in CommandEnvelope{EventType: "OrderCreated", MarketID: "BTC-USDT", ...}
   • Sets msg.Key = "BTC-USDT", Partition = 0
   • Writes to topic orders.commands with RequiredAcks = RequireAll
   • tracker.SetKafkaPublished("MM-BTC-USDT-BID-01", true)
```

---

## 5. Flow 4: Safe Order Correction & Replacement Flow

When reference prices move, an existing order must be replaced. Creating a new order before the old one is cancelled risks having two active orders resting at the same level.

```text
DiffCorrect encountered for LevelID: "MM-BTC-USDT-ASK-01"
   │
   ▼
Step 1: Issue Cancel Command
   • producer.PublishCancel(ctx, marketID, partition, existingOrderID)
   • Kafka receives OrderCancelRequested
   │
   ▼
Step 2: Lock Level & Queue Replacement
   • tracker.SetCancelling("MM-BTC-USDT-ASK-01")
   • tracker.QueueCorrection("MM-BTC-USDT-ASK-01", desiredPriceLevel)
   • ⚠️ While in CANCELLING, order.Diff ignores this level (blocks duplicate creation)
   │
   ▼
Step 3: Await Cancellation Confirmation
   • Triggered by either:
     a) CheckCancellingTimeouts (Order Service confirms CANCELLED)
     b) SyncFromOrderService (Old order disappears from OS snapshot)
   │
   ▼
Step 4: Execute Queued Replacement
   • tracker.Remove("MM-BTC-USDT-ASK-01")
   • Retrieves queued desiredPriceLevel
   • Dispatches fresh Crash-Safe 3-Step Creation with Generation G+1 (e.g., G009)
```

---

## 6. Flow 5: Real-Time Trade Fill & Inventory Update Flow

When an external taker order fills a resting MM order in the Matching Engine, the LE updates inventory with **at-least-once delivery guarantees**:

```text
Matching Engine executes match ──> Publishes event to trades.executed
   │
   ▼
kafka.Consumer (Dedicated Goroutine)
   │
   ├── Fetches raw JSON message
   ├── parseTradeMessage() ──> TradeEvent
   │     • Detects if MM is buyer or seller
   │     • MMSide = "BUY" | "SELL"
   │
   ├── Wraps in TradeEnvelope{Event, Ack: make(chan struct{})}
   │
   ├── Sends envelope to e.tradeEvents channel
   │
   └── BLOCKS waiting on <-Ack  (Manual Commit Barrier)
         │
         │  (Single-Threaded Engine Event Loop)
         ▼
engine.handleTrade(env)
   │
   ├── 1. Bounded Deduplication Check:
   │      • Is TradeID in the 1,000-entry ring buffer?
   │        YES ──> Close(env.Ack) & Return immediately (prevents double accounting)
   │        NO  ──> Record TradeID in ring buffer
   │
   ├── 2. Apply Fill to Projected Inventory (inventory.ApplyTrade):
   │      • MM SOLD ──> Projected Base -= Qty,  Projected USDT += (Qty * Price)
   │      • MM BOUGHT ──> Projected Base += Qty, Projected USDT -= (Qty * Price)
   │
   ├── 3. Update In-Memory Tracker (order.Tracker):
   │      • Deducts filled quantity from order's RemainingQty
   │      • If RemainingQty <= 0 ──> tracker.Remove(levelID)
   │
   ├── 4. Release Kafka Commit Barrier:
   │      • Closes env.Ack
   │      • Consumer unblocks and calls reader.CommitMessages() (Guaranteed At-Least-Once)
   │
   └── 5. Schedule Debounced Targeted Reconcile:
          • Resets 200ms debounce timer
          • When timer fires ──> posts evTargetedReconcile to replace filled order
```

---

## 7. Flow 6: Timeout Handlers & State Machine Transitions

To ensure orders never get permanently stuck in transient states, the engine runs dedicated timeout inspection loops:

```text
                    ┌────────────────────────────────────────────────────────┐
                    │               ORDER TIMEOUT RESOLUTION                 │
                    └────────────────────────────────────────────────────────┘

1. PENDING TIMEOUT (CheckPendingTimeouts - Every 5s):
   For each PENDING order where time.Since(PendingSince) > 10s:
   ├── KafkaPublished == false?
   │   └── Retry producer.PublishCreate() with identical OrderID & ClientOrderID
   └── KafkaPublished == true?
       └── Query orderSvc.GetOrderByClientID(clientOrderID)
           ├── OPEN / PARTIALLY_FILLED ──> tracker.SetOSRegistered()
           ├── FILLED                  ──> tracker.Remove() & IncOrdersFilled()
           ├── CANCELLED               ──> tracker.Remove()
           └── NOT_FOUND (3x consecutive) ──> Count towards ME liveness failure

2. OS_REGISTERED TIMEOUT (CheckOSRegisteredTimeouts - Every 5s):
   For each OS_REGISTERED order where time.Since(OSRegisteredSince) > 500ms:
   ├── ME Health Probe == Healthy?
   │   └── tracker.SetResting() (Promoted! Order is live in book, counts toward /readyz)
   └── ME Health Probe == Unhealthy?
       └── Hold in OS_REGISTERED (Do not promote)

3. CANCELLING TIMEOUT (CheckCancellingTimeouts - Every 15s):
   For each CANCELLING order where time.Since(CancellingSince) > 30s:
   └── Query orderSvc.GetOrderByClientID(clientOrderID)
       ├── CANCELLED ──> tracker.Remove() & Apply QueuedCorrection (create replacement)
       ├── FILLED    ──> tracker.Remove() & IncOrdersFilled()
       └── OPEN / CANCELLING:
           ├── retries < 3 ──> Retry producer.PublishCancel() & IncrementCancelRetry()
           └── retries >= 3──> tracker.SetStale() (Lock level until full OS resync)
```

---

## 8. Flow 7: ME Health Probing & Fault Isolation Flow

The engine guarantees fault isolation: if one market fails, the other markets continue quoting normally.

```text
handlePendingCheck() fires (Every 5s)
   │
   ▼
meclient.CheckAllMarkets(ctx) [HTTP GET http://matching-engine:8082/status]
   │
   ├── Response: {"ready": true, "markets": ["BTC-USDT", "ETH-USDT", "SOL-USDT"]}
   │
   ▼
For each configured market (e.g., BTC-USDT):
   │
   ├── Probe Succeeded & Market in List?
   │   • consecutiveMETimeouts["BTC-USDT"] = 0
   │   • marketPaused["BTC-USDT"] = false (Market is RUNNING)
   │
   └── Probe Failed OR Market Missing?
       • consecutiveMETimeouts["BTC-USDT"]++
       • Counter >= 3 (MELivenessThreshold)?
         ├── YES ──> marketPaused["BTC-USDT"] = true (Marked PAUSED_ME)
         │           Engine transitions to DEGRADED
         │           Skips new order creation on BTC-USDT
         │           ETH-USDT and SOL-USDT continue quoting normally ✅
         └── NO  ──> Log warning, keep quoting (Anti-flapping buffer)
```

---

## 9. Flow 8: Lock-Free HTTP Snapshot & Monitoring Flow

External scrapers (Kubernetes probes, Prometheus, operator status dashboards) query the engine without taking mutex locks or interrupting the event loop:

```text
[Engine Event Loop]
   │
   ├── State changes (order placed, fill processed, market paused)
   │
   ▼
publishSnapshot()
   │
   ├── Builds immutable StatusSnapshot struct:
   │   {
   │       state:                StateRunning,
   │       ready:                true,
   │       marketStates:         {"BTC-USDT": "RUNNING", "ETH-USDT": "RUNNING"},
   │       readyBids:            {"BTC-USDT": 12, "ETH-USDT": 12},
   │       readyAsks:            {"BTC-USDT": 12, "ETH-USDT": 12},
   │       inventoryLastRefresh: 2026-09-01T10:00:00Z,
   │   }
   │
   └── atomic.Pointer[StatusSnapshot].Store(snap) ───┐
                                                     │ (Atomic Pointer Swap)
                                                     ▼
                                      [atomic.Pointer[StatusSnapshot]]
                                                     │
                             ┌───────────────────────┼───────────────────────┐
                             │ (Atomic Load)         │ (Atomic Load)         │ (Atomic Load)
                             ▼                       ▼                       ▼
                     GET /healthz            GET /readyz             GET /status
                     (Liveness Probe)        (Readiness Probe)       (Operator Diagnostics)
                     Returns: 200 OK         Evaluates strictly      Returns Full JSON
                     (unless StateStopped)   RESTING orders          Diagnostics Snapshot
```

---

## Summary of Guarantees

| Metric / Requirement | Architectural Guarantee |
| :--- | :--- |
| **Data Loss Prevention** | Event loop single-writer model + manual commit on `TradeEnvelope.Ack`. |
| **Duplicate Order Prevention** | Deterministic `LevelID` + monotonic generation keys (`G008`) + Order Service idempotency. |
| **Crash Recovery** | Zero-DB in-memory recovery from `ListMMOrders` on startup (`SYNCING` state). |
| **High-Throughput Concurrency** | Lock-free `atomic.Pointer[StatusSnapshot]` for external HTTP reads. |
| **Zero Over-Quoting** | Effective capital subtraction (`projected - committed`) + automatic skew trimming. |
| **Fault Isolation** | Per-market pause flags (`PAUSED_ME`) prevent single-pair issues from stopping other pairs. |
