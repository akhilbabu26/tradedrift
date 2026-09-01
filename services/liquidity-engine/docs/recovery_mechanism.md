# System Recovery Mechanisms in TradeDrift

This document provides an in-depth technical explanation of how **crash recovery and state reconstruction** are achieved across the **Matching Engine (ME)** and the **Liquidity Engine (LE)**.

---

## 1. Purpose of the Recovery Mechanisms

In a mission-critical financial exchange, services will inevitably restart due to deployments, node failures, or panics. When a crash occurs:
- **No money or assets can be lost or duplicated.**
- **No phantom or duplicate orders can be created.**
- **The in-memory order books and tracker state must be 100% reconstructed to their exact pre-crash state.**
- **The system must resume live trading with zero human intervention.**

Because TradeDrift executes trades in-memory for microsecond-level performance, traditional ACID database transactions cannot be used during order matching. Instead, the architecture achieves durability and consistency through **Event Sourcing, Deterministic Replay, and Authoritative Ingress Synchronization**.

---

## 2. Where Recovery is Happening in the Architecture

Recovery happens in two distinct subsystems, each handling a different layer of the stack:

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                          TradeDrift Recovery Layers                         │
│                                                                             │
│  1. MATCHING ENGINE RECOVERY                                                │
│     Location: services/matching-engine/internal/engine/replayer.go          │
│     Target:   In-Memory Order Books (Bids/Asks B-Trees & FIFO Queues)       │
│     Source:   PostgreSQL Snapshots + Kafka Event Replay                     │
│                                                                             │
│  2. LIQUIDITY ENGINE RECOVERY                                               │
│     Location: services/liquidity-engine/internal/engine/engine.go           │
│               services/liquidity-engine/internal/reconciler/sync.go         │
│     Target:   In-Memory Order Tracker & Generation Counters                 │
│     Source:   Order Service DB (gRPC ListMMOrders) + Health Probes          │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## 3. Matching Engine Recovery Mechanism

The Matching Engine operates as a single-threaded deterministic state machine per market. It does not write to a database on every trade; instead, it checkpoints its state periodically.

### The Recovery Sequence

```text
[ME Boot]
   │
   ▼
1. Read Last Checkpoint & Snapshot
   • PostgreSQL kafka_checkpoints → Last committed offset (e.g. Offset 500)
   • PostgreSQL market_snapshots  → Latest book snapshot (e.g. Offset 400)
   │
   ▼
2. Initialize In-Memory Book from Snapshot
   • Deserializes snapshot at Offset 400 into active OrderBook struct.
   • Restores order IDs, prices, quantities, and sequence counter.
   │
   ▼
3. Query Kafka High Water Mark (HWM)
   • Inspects topic orders.commands partition to find latest offset (e.g. Offset 550).
   │
   ▼
4. Enter ModeRecovery (Deterministic Replay)
   • Replays Kafka messages from Offset 401 → 550.
   • Executes Match() and Cancel() logic identically to live execution.
   • ⚠️ CRITICAL: Side-effects are SUPPRESSED:
     - No TradeExecuted messages sent to Kafka.
     - No outbound notifications dispatched.
   │
   ▼
5. State Convergence & Cache Warmup
   • Drain internal OutputQueue.
   • Push latest order book depth to Redis (depth:{market_id}).
   │
   ▼
6. Transition to ModeLive
   • SetLive() called.
   • Start Kafka consumer for live order processing.
```

### Why ME Recovery is 100% Deterministic:
- The order book logic has zero non-deterministic inputs (no wall-clock timestamps affecting execution price, no random UUID generation inside the matcher).
- Given snapshot $S_{400}$ and event stream $E_{401 \dots 550}$, the final state $\mathbf{S_{550}}$ is mathematically guaranteed to equal the pre-crash state.

---

## 4. Liquidity Engine Recovery Mechanism

The Liquidity Engine intentionally maintains **zero database tables** of its own. It holds its active order tracker entirely in memory. When the LE crashes or restarts, its entire local tracker is wiped clean.

### The Stateless Recovery Sequence

```text
[LE Boot] ──> StateStarting
   │
   ▼
1. Authoritative Wallet Balance Seed
   • gRPC walletSvc.GetMMBalances(account.WalletUUIDStr)
   • Validates MM-001 has BTC, ETH, SOL, and USDT.
   • Seeds inventory.Manager with base projected balances.
   │
   ▼
2. Authoritative Order Discovery (StateSyncing)
   • Calls orderSvc.ListMMOrders(marketID) for OPEN and PARTIALLY_FILLED orders.
   • For each persistent order, parses the idempotency key:
     "MM-BTC-USDT-ASK-01-G007" ──> LevelID: "MM-BTC-USDT-ASK-01", Gen: 7
   • tracker.SyncFromOrders():
     - Populates tracker in OS_REGISTERED state.
     - Sets generations["MM-BTC-USDT-ASK-01"] = 7 (ensures next replacement is G008).
   │
   ▼
3. Matching Engine Liveness Handshake
   • HTTP meclient.CheckAllMarkets(ctx) probes /status.
   • Confirms ME is live and ready before placing any orders.
   │
   ▼
4. Transition to StateRunning & Active Reconcile
   • Starts background event loop and ticker pumps.
   • Executes initial runReconcileAll():
     - Generates desired ladder (12 Bids + 12 Asks).
     - Diffs desired ladder against recovered tracker.
     - Only creates orders for missing levels; does not touch recovered active levels.
```

---

## 5. Failure Scenarios & Edge-Case Recovery

Here is how the recovery architecture handles failures occurring at critical points:

### Scenario A: LE Crashes Between Order Service Registration & Kafka Publish
```text
1. OS Registration (CreateMMOrder) ──> SUCCESS (Saved in PostgreSQL)
2. Tracker Entry (SetPending)      ──> SUCCESS
3. [💥 LE CRASHES before Kafka Publish]
```
- **Recovery Resolution:**
  1. On restart, `ListMMOrders()` discovers the order in the Order Service.
  2. The tracker is populated in `OS_REGISTERED` state.
  3. `CheckPendingTimeouts` detects the order and verifies with the Matching Engine.
  4. If the Matching Engine doesn't have it, the Kafka publish is retried with the exact same `OrderID` and `ClientOrderID`.
  5. **Result:** No duplicate orders created; zero state loss.

---

### Scenario B: Matching Engine Crashes Mid-Execution
```text
1. User submits aggressive Market BUY order.
2. ME matches against MM Ask (Level ASK-01) at offset 120.
3. [💥 ME CRASHES before checkpointing offset 120]
```
- **Recovery Resolution:**
  1. ME restarts, reads last snapshot (e.g. offset 100) and checkpoint (offset 100).
  2. ME enters `ModeRecovery` and replays commands from offset 101 to 120.
  3. The match executes identically, restoring the partially filled/resting state.
  4. Fills are suppressed during replay to prevent sending duplicate `trades.executed` events.
  5. **Result:** Order book accurately restored to offset 120.

---

### Scenario C: Kafka Redelivers Trade Fill (At-Least-Once Replay)
```text
1. ME executes trade T-100 and publishes to trades.executed.
2. LE processes trade and updates inventory balances.
3. LE closes Ack channel.
4. [💥 LE CRASHES before Kafka consumer commits offset]
5. On restart, Kafka redelivers trade T-100.
```
- **Recovery Resolution:**
  1. LE `handleTrade()` checks its in-memory ring buffer: `isTradeProcessed("T-100")`.
  2. If found $\to$ logs duplicate detection, closes `Ack` channel immediately, and commits offset without re-applying balance mutations.
  3. If LE restarted with a wiped ring buffer, the subsequent `walletTicker` (every 15s) calls `RefreshFromWallet()`, which resets projected balances to the authoritative wallet snapshot.
  4. **Result:** Zero double-crediting or double-deduction.

---

## 6. Summary Comparison: ME vs LE Recovery

| Feature | Matching Engine Recovery | Liquidity Engine Recovery |
| :--- | :--- | :--- |
| **Recovery Mode** | Deterministic Event Sourcing Replay | Authoritative State Resync & Re-Laddering |
| **Data Source** | PostgreSQL Snapshots + Kafka Topic | Order Service DB (gRPC) + Wallet Service |
| **Duration** | Sub-second (replays in-memory buffer) | Sub-second (single gRPC list call per market) |
| **Side-Effect Handling** | Output queues drained & suppressed | Idempotency keys prevent duplicate orders |
| **Target State** | Live Order Book & Sequence Numbers | Local Tracker, Skew Tiers, & Generation Maps |
