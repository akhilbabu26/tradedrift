# `internal/market` — Market Engine, Event Loop & Concurrency Actor

**Package:** `market`  
**Service:** Matching Engine  
**Files Covered:** `engine.go`, `event_loop.go`, `manager.go`, `ring_buffer.go`, `snapshot.go`, `engine_test.go`  
**Documentation:** `02READEME.md`  
**Last Updated:** August 2026  

---

## 1. Executive Summary & Purpose

The `internal/market` package implements the **isolated concurrency actor model** for the Matching Engine.

In an ultra-high-throughput financial exchange, managing multiple order books concurrently while guaranteeing absolute price-time deterministic execution, strict monotonic sequence progression, and sub-millisecond latency is a critical architectural challenge.

The `market` package solves this by establishing an **Actor Pattern (Single-Writer Principle)**:
- Every active trading pair (e.g., `BTC-USDT`, `ETH-USDT`, `SOL-USDT`) is assigned its own dedicated [`MarketEngine`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/engine.go#L77-L90) instance.
- Each `MarketEngine` runs a single dedicated Event Loop goroutine ([`Run()`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go#L17-L167)).
- **Zero Mutex Contention**: The underlying order book is exclusively accessed and mutated by this single goroutine.
- Coordinates incoming Kafka commands (`InputQueue`), pre-match financial validation, in-memory deduplication, periodic snapshotting, recovery barrier synchronization, output emission to publishers (`OutputQueue`), and atomic state snapshots for external reconcilers.

---

## 2. Core Problems Solved & Why This Package Is Needed

### 2.1 Eliminating Mutex Contention via Lock-Free Ownership by Construction
Traditional multi-threaded matching engines use mutexes (e.g., `sync.RWMutex`) around order book data structures. Under high concurrency, lock contention, cache-line bouncing, and thread context switching degrade throughput exponentially.
- **The Solution**: Lock-free single-writer goroutines.
- The Kafka Consumer only writes to the buffered `InputQueue` channel.
- The Event Loop exclusively reads `InputQueue`, modifies the `OrderBook`, and writes to `OutputQueue`.
- The Publisher exclusively reads from `OutputQueue`.
- Go channel operations establish strong *happens-before* memory barriers with zero locking on book data structures.

### 2.2 In-Memory Fast-Path Deduplication with O(1) Ring Buffer
Network retries from upstream services (or Kafka rebalances) can redeliver identical command messages.
- `MarketEngine` maintains an in-memory hash set (`processedEvents map[uuid.UUID]bool`) backed by a fixed-capacity 50,000-entry FIFO ring buffer ([`eventRingBuffer`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/ring_buffer.go#L9-L13)).
- Incoming event UUIDs are inserted into the ring buffer. When the buffer reaches 50,000 entries, the oldest UUID is evicted in **$O(1)$ time with zero array reallocation or slice shifting**, and purged from the deduplication map.

### 2.3 Strict Fail-Closed Safeguards (Issue #10)
If a duplicate logical `EventID` arrives during live operation with a different offset (indicating upstream publisher inconsistency or non-idempotent re-issuance), rather than risking inconsistent balance double-fills:
- The engine logs a `FATAL: duplicate logical event_id detected` message.
- Immediately invokes `m.TriggerFatalHalt()`, setting `isFatalHalt` and firing `m.HaltCallback()` to trigger an instant fail-stop and prevent operating on corrupted state.
- Graceful shutdown draining is explicitly skipped during fatal halts to avoid corrupting persisted state.

### 2.4 Pre-Match Financial Validation (Tick & Lot Size)
Every order is checked before entering the matching algorithm:
- **Tick Size (`Price % TickSize == 0`)**: Ensures limit prices respect market decimal increments (e.g., $0.01 increments).
- **Lot Size (`Quantity % LotSize == 0`)**: Ensures order quantities respect minimum divisible units (e.g., 0.0001 BTC).
- If validation fails, the order is cancelled immediately with `reason: "invalid_order_parameters"` without altering order book sequence numbers.

### 2.5 Dual-Trigger Snapshot Generation & Shutdown Flush
To ensure lightning-fast startup recovery times, `MarketEngine` periodically generates order book snapshots:
- **Count-Based**: Every $N$ events (configurable, default: 10,000 events).
- **Time-Based**: Every $T$ duration (configurable, default: 60 seconds).
- **First Event**: Sequence 1 automatically triggers a snapshot.
- **Shutdown Flush**: When `InputQueue` closes or context is cancelled, [`triggerFinalSnapshot()`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go#L169-L184) serializes the final book state at the last applied offset.

### 2.6 Recovery Barrier Synchronization
During crash recovery replay (`internal/recovery`), historical events are replayed without emitting live trades. When replay reaches the partition checkpoint, a special [`EventRecoveryBarrier`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/engine.go#L48) is queued. When the event loop encounters this barrier, it emits a `MatchResult` with `BarrierReached: true`, allowing the replayer to safely confirm that all historical queue events have been drained before switching the engine to `ModeLive`.

### 2.7 Lock-Free Atomic State Observation for Authoritative Snapshot Reconciler
External supervisory subsystems (such as the Liquidity Engine reconciler and HTTP `/status`, `/markets/{id}/snapshot` endpoints) need real-time visibility into resting orders and sequence progression without introducing mutex contention or queue delays to the single-writer matching loop.
- `MarketEngine` maintains an atomic pointer `snapshot atomic.Pointer[MarketSnapshot]`.
- At the end of each processed live event in `Run()` (and during `SetLive()`), the event loop calls [`PublishAtomicSnapshot()`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/snapshot.go#L54-L94).
- It extracts an immutable point-in-time snapshot containing resting Market Maker (`MMUserID`) orders, client order IDs, level IDs, generations, prices, remaining quantities, and current sequence counter.
- External HTTP handlers read this snapshot via `GetSnapshot()` using a single atomic load (`m.snapshot.Load()`) with **zero locks, zero channel operations, and zero impact on matching throughput**.

---
## 3. Goroutine & Data Flow Architecture

```
                                  KAFKA CONSUMER
                                         │
                         routes by MarketID (chan send)
                                         ▼
                             ┌───────────────────────┐
                             │  InputQueue (chan)    │ (Capacity: 1000)
                             └───────────┬───────────┘
                                         │
                                         ▼
                     ┌───────────────────────────────────────┐
                     │         MarketEngine.Run()            │
                     │       (Exclusively owns book)         │
                     │                                       │
                     │  1. Check Offset > lastAppliedOffset  │
                     │  2. Fast Deduplication (eventRing)    │
                     │  3. Pre-Match (Tick & Lot Size)       │
                     │  4. matcher.Match() / matcher.Cancel()│
                     │  5. Periodic / Final Snapshot Check   │
                     │  6. Advance lastAppliedOffset         │
                     │  7. PublishAtomicSnapshot() (atomic)  │ ──► atomic.Pointer[MarketSnapshot]
                     └───────────────────┬───────────────────┘          │
                                         │                              │ (lock-free read)
                                    (chan send)                         ▼
                                         │                      HTTP SERVER / RECONCILER
                                         ▼                      • /status
                             ┌───────────────────────┐          • /markets/{id}/snapshot
                             │  OutputQueue (chan)   │ (Capacity: 1000)
                             └───────────┬───────────┘
                                         │
                                    (chan recv)
                                         ▼
                                     PUBLISHER
                             ┌───────────────────────┐
                             │ 1. Kafka Trade Events │
                             │ 2. Redis Depth Cache  │
                             │ 3. Postgres Checkpoint│
                             └───────────────────────┘
```

---

## 4. External Packages & Dependencies

| External Package | Purpose & Justification |
| :--- | :--- |
| `context` | Manages graceful shutdown lifecycles and draining of input queues during SIGTERM / service shutdown. |
| `fmt` | Constructs structured error outputs and formatting. |
| `log` | Standard logging for operational events, periodic snapshots, fatal duplicate detections, and mutation failures. |
| `sync/atomic` | Lock-free atomic state snapshots (`atomic.Pointer[MarketSnapshot]`) and fail-stop coordination flags (`atomic.Bool`). |
| `time` | Timestamping order arrival (`time.Now()`), measuring snapshot intervals (`time.Since(lastSnapshotTime)`), and configuring snapshot durations. |
| `github.com/google/uuid` | Fast parsing, generating, and tracking RFC 4122 UUIDs for `EventID`, `OrderID`, and `UserID`, and managing the deduplication ring buffer. |
| `github.com/shopspring/decimal` | Exact arbitrary-precision decimal mathematics for financial pricing, quantities, and modulo operations (`Mod`) without IEEE 754 floating-point rounding errors. |
| `tradedrift/.../matcher` | Internal package implementing pure deterministic price-time matching (`Match`), order cancellation (`Cancel`), and depth extraction (`GetDepth`). |
| `tradedrift/.../orderbook` | Internal package providing order book data structures (`OrderBook`, `OrderNode`, `BookSnapshot`, `DepthSnapshot`, `MatchResult`, `CancelOutcome`, `CancelStatus`). |

---

## 5. Detailed Breakdown of Files, Structs & Functions

### 5.1 `engine.go` — Engine Types, Enums & Lifecycle

#### Enums & Constants
- `Mode`: Engine operational mode:
  - `ModeRecovery (0)`: Historical replay phase. Output generation and external side-effects are suppressed.
  - `ModeLive (1)`: Normal live matching phase. Fills and depth changes are emitted to `OutputQueue`.
- `EventType`: Types of input events:
  - `EventOrderCreated (0)`: New order command.
  - `EventOrderCancel (1)`: User-initiated cancellation.
  - `EventRecoveryBarrier (2)`: Synchronization marker used during startup replay.

#### Core Structs
1. **`MarketConfig`**:
   - `MarketID`: Market identifier (e.g., `"BTC-USDT"`).
   - `TickSize`: Minimum price increment (e.g., `0.01`).
   - `LotSize`: Minimum quantity increment (e.g., `0.001`).
   - `Partition`: Kafka partition ID assigned to this market.
   - `SnapshotInterval`: Snapshot frequency by event count.
   - `SnapshotDuration`: Snapshot frequency by elapsed time duration.

2. **`InputEvent`**:
   - Wrapped message container holding `EventID`, `Type`, `OrderCreated` payload pointer, `OrderCancel` payload pointer, and Kafka coordinates (`Topic`, `Partition`, `Offset`).

3. **`OrderCreatedPayload`**:
   - Holds deserialized order creation data: `OrderID`, `UserID`, `MarketID`, `Side`, `OrderType`, `Price`, `Quantity`, and `ClientOrderID`.

4. **`OrderCancelPayload`**:
   - Holds cancellation request coordinates: `OrderID`, `UserID`, and `MarketID`.

5. **`MarketEngine`**:
   - Struct containing `MarketID`, `InputQueue` (chan `InputEvent`), `OutputQueue` (chan `orderbook.MatchResult`), `book` (`*orderbook.OrderBook`), `config`, `mode`, `processedEvents` map, `eventRing` (`eventRingBuffer`), `lastAppliedOffset`, `isFatalHalt` (`atomic.Bool`), `HaltCallback` (`func()`), and `snapshot` (`atomic.Pointer[MarketSnapshot]`).

#### Methods in `engine.go`
- `NewMarketEngine(config MarketConfig) *MarketEngine`: Constructor. Initializes buffered channels (capacity 1000), internal order book, recovery mode, deduplication structures, and seeds initial `MarketSnapshot` with `State: "RECOVERING"`.
- `TriggerFatalHalt()`: Atomically sets `isFatalHalt` via `Swap(true)` and invokes `HaltCallback()`. Prevents event loop from draining on context cancellation.
- `IsFatalHalt() bool`: Returns whether this engine has entered a fatal halt condition.
- `SetLive()`: Transitions engine mode from `ModeRecovery` to `ModeLive` and invokes `PublishAtomicSnapshot()`.
- `GetDepth(n int) orderbook.DepthSnapshot`: Returns Top-N bid/ask depth levels.
- `GetSequence() uint64`: Returns current monotonic sequence counter.
- `SetSequence(seq uint64)`: Restores sequence counter from database baseline.
- `RestoreFromSnapshot(snap orderbook.BookSnapshot, expectedChecksum []byte, checkpoint int64) error`: Calls `orderbook.Restore` to reset and reconstruct the in-memory book from a snapshot, verifying partition, checksum, tick size, and lot size.
- `GetLastAppliedOffset() int64`: Returns the highest Kafka offset applied to the book.
- `Partition() int`: Returns assigned Kafka partition ID.
- `Mode() Mode`: Returns current operating mode (`ModeRecovery` or `ModeLive`).

---

### 5.2 `event_loop.go` — Event Processing, Snapshots & Validation

#### Methods & Functions
- **`Run(ctx context.Context)`**:
  - The main Event Loop goroutine. Exclusively owns and mutates `book`.
  - Continuously checks `isFatalHalt.Load()` to immediately terminate on fatal failures.
  - Listens on `m.InputQueue` and `ctx.Done()`.
  - On `EventRecoveryBarrier`, emits a barrier confirmation result immediately.
  - Skips redundant offsets where `event.Offset <= m.lastAppliedOffset`.
  - Performs deduplication check via `m.processedEvents`. If duplicate detected, triggers `m.TriggerFatalHalt()`.
  - Executes `applyEvent(event)`.
  - Updates deduplication ring buffer with `m.eventRing.add(event.EventID)`.
  - Advances `m.lastAppliedOffset = event.Offset`.
  - Evaluates snapshot triggers (first event, time elapsed, count elapsed); if met, serializes book state and attaches to `MatchResult.Snapshot`.
  - If in `ModeLive`, calls `m.PublishAtomicSnapshot()` to update the lock-free state observer.
  - Emits result to `m.OutputQueue`.
  - On context cancellation (`ctx.Done()`):
    - If `isFatalHalt` is set, skips draining and exits immediately to prevent operating past failure boundaries.
    - Otherwise, drains all remaining buffered events in `InputQueue`, generates a final shutdown snapshot via `triggerFinalSnapshot()`, and closes `m.OutputQueue`.

- **`triggerFinalSnapshot()`**:
  - Serializes order book state at `m.lastAppliedOffset` and pushes a final `MatchResult` with snapshot to `OutputQueue`.

- **`applyEvent(event InputEvent) (*orderbook.MatchResult, error)`**:
  - Determines matcher mode (`ModeRecovery` vs `ModeLive`).
  - **`EventOrderCreated`**:
    - Creates heap-allocated `OrderNode` preserving `ClientOrderID`.
    - Checks for existing order in `m.book.OrderIndex` (idempotent duplicate skip).
    - Checks `validTickAndLot()`. If invalid, generates `CancelledOrder` with `reason: "invalid_order_parameters"`.
    - Calls `matcher.Match(m.book, node, matcherMode, event.EventID)`.
    - If `OrderTypeMarket` has remaining quantity, creates immediate cancel with `reason: "ioc_expired"`.
    - Returns `MatchResult` containing fills, cancels, Top-20 depth snapshot, and Kafka position.
  - **`EventOrderCancel`**:
    - Calls `matcher.Cancel(m.book, p.OrderID)`.
    - Receives `outcome := matcher.Cancel(...)` (`orderbook.CancelOutcome`).
    - If `outcome.Status == orderbook.CancelStatusRemovedFromBook`:
      - Increments monotonic sequence counter (`m.book.Sequence++`).
      - Logs `[cancel] REMOVED_FROM_BOOK`.
    - If `outcome.Status == orderbook.CancelStatusAlreadyAbsent`:
      - Sequence remains UNCHANGED.
      - Logs `[cancel] ALREADY_ABSENT`.
    - Returns a non-nil `CancelledOrder` carrying `outcome.Status`, `outcome.UserID`, `outcome.MarketID`, and `outcome.RemainingQuantity` to `OutputQueue` so downstream Order Service receives authoritative confirmation for database resolution.

- **`validTickAndLot(node *orderbook.OrderNode, config MarketConfig) bool`**:
  - Checks if `Price % TickSize == 0` (for LIMIT orders).
  - Checks if `Quantity % LotSize == 0`.
  - Returns `true` if both validations pass, otherwise `false`.

---

### 5.3 `manager.go` — Multi-Market Registry & Orchestration

- **`MarketManager`**:
  - Encapsulates `engines map[string]*MarketEngine` keyed by `market_id`.
- **`NewMarketManager() *MarketManager`**:
  - Instantiates manager with empty engine map.
- **`Add(config MarketConfig) *MarketEngine`**:
  - Instantiates and registers a new `MarketEngine` for the given market config.
  - *Note*: Does not start `engine.Run()`, allowing startup recovery to restore snapshots and replay logs first.
- **`Get(marketID string) *MarketEngine`**:
  - Returns engine pointer by market ID (used by Kafka Consumer for fast routing).
- **`All() []*MarketEngine`**:
  - Returns slice of all registered engines.
- **`CloseInputQueues()`**:
  - Iterates over all engines and closes their `InputQueue` channels, triggering graceful shutdown draining across all event loops.

---

### 5.4 `ring_buffer.go` — Deduplication Ring Buffer

- **`const ringBufferCapacity = 50_000`**:
  - Fixed capacity of historical event UUIDs preserved for in-memory deduplication.
- **`eventRingBuffer`**:
  - Struct containing fixed-size array `slots [ringBufferCapacity]uuid.UUID`, integer `head` (next write position / oldest entry), and integer `count` (number of live entries).
- **`add(id uuid.UUID) (evicted uuid.UUID)`**:
  - Inserts a new event UUID into the ring buffer.
  - When the buffer reaches capacity, the oldest entry at `head` is evicted in **$O(1)$ time with zero slice allocations or memory shifts**, advancing `head = (head + 1) % capacity`.
  - Returns the evicted `uuid.UUID` (or `uuid.Nil` if not full), which `Run()` immediately removes from the `processedEvents` hash map.

---

### 5.5 `snapshot.go` — Atomic Market Snapshots & Market Maker State

- **`MMUserID`**:
  - Canonical UUID (`00000000-0000-0000-0000-000000000001`) identifying the automated Market Maker / Liquidity Engine actor.
- **`MMOrderSummary`**:
  - JSON-serializable DTO representing a resting MM order: `OrderID`, `ClientOrderID`, `LevelID`, `Generation`, `Side`, `Price`, and `RemainingQuantity`.
- **`MarketSnapshot`**:
  - Coherent point-in-time snapshot:
    - `MarketID`: Market identifier.
    - `State`: `"LIVE"` or `"RECOVERING"`.
    - `Sequence`: Authoritative order book sequence counter.
    - `SnapshotAt`: Coherent point-in-time timestamp.
    - `Orders`: Slice of `MMOrderSummary` (`nil` during recovery, empty slice if no MM orders resting, populated slice when resting).
- **`parseMMClientOrderID(coid string) (levelID string, gen uint64)`**:
  - Parses structured Market Maker client order IDs formatted as `<level_id>-G<generation>` (e.g., `"L1_BUY-G12"` $\to$ `("L1_BUY", 12)`).
- **`PublishAtomicSnapshot()`**:
  - Evaluates current operating mode and scans `book.OrderIndex` for `MMUserID` orders.
  - Constructs an immutable `MarketSnapshot` instance and stores it atomically via `m.snapshot.Store(...)`.
  - Safe to call from the single-threaded event loop or during engine initialization.
- **`GetSnapshot() *MarketSnapshot`**:
  - Lock-free atomic reader returning the latest `MarketSnapshot` via `m.snapshot.Load()`. Utilized by HTTP health and reconciliation endpoints.

---

## 6. The 9 Authoritative Sequence Rules

The Matching Engine sequence counter (`m.book.Sequence`) is strictly monotonic and deterministic. As validated in [`TestSequence_ComprehensiveLifecycle`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/engine_test.go#L37-L290):

| # | Action | Sequence Behavior | Rationale |
| :- | :--- | :--- | :--- |
| 1 | **Resting LIMIT Insertion** | `Sequence++` | State change: order added to book level. |
| 2 | **Order Cancellation (Active)** | `Sequence++` | State change: order removed from book level (`CancelStatus = REMOVED_FROM_BOOK`). |
| 3 | **Validation Reject (Tick/Lot)** | `Sequence UNCHANGED` | No state change: order rejected at validation gate before touching book. |
| 4 | **Cancel Request for Missing Order** | `Sequence UNCHANGED` | No state change: order was already filled/cancelled. Emits `CancelStatus = ALREADY_ABSENT` to publisher for Order Service DB resolution. |
| 5 | **Single Match (1 Maker, 1 Taker)** | `Fill.Seq = Seq++`, `Depth.Seq = Seq` | State change: match execution mutates maker level. |
| 6 | **Multi-Level Sweep + Remainder** | `Fill_1=Seq+1`, `Fill_2=Seq+2`, `Fill_3=Seq+3`, `Remainder=Seq+4` | Each matched level advances sequence; final resting remainder advances sequence. |
| 7 | **Read Idempotency (`GetDepth`)** | `Sequence UNCHANGED` | Pure read operations never mutate sequence state. |
| 8 | **Non-Zero Baseline Recovery** | `Sequence = baseline + mutations` | Restoring from snapshot starts sequence at `snap.Sequence` without resetting to zero. |
| 9 | **Strict Monotonicity** | Always strictly increasing | Every emitted fill and depth snapshot carries a monotonic sequence. |

---

## 7. Unit Test Suite Summary (`engine_test.go`)

| Test Function | Scenarios Validated |
| :--- | :--- |
| `TestSequence_ComprehensiveLifecycle` | End-to-end execution of all 9 sequence rules (resting orders, cancels, validation rejections, sweeps, and pure reads). |
| `TestSequence_NonZeroBaselineRecovery` | Verifies that restoring an engine at baseline sequence 10,000 correctly increments to 10,001 upon live mutation. |
| `TestOrdersCommands_CreateThenCancel` | Verifies basic LIMIT order placement and subsequent successful cancellation (`REMOVED_FROM_BOOK`). |
| `TestOrdersCommands_CreateMatchThenCancel` | Verifies maker placement, taker matching, and asserting that cancelling an already-filled taker produces `ALREADY_ABSENT` with sequence unchanged. |
| `TestOrdersCommands_CreatePartialFillThenCancel` | Verifies taker partial match where remainder rests in book, followed by cancelling the resting remainder. |
| `TestOrdersCommands_DuplicateEventID_FailClosed` | Verifies that redelivering the same `EventID` with a higher offset triggers `HaltCallback()` for fail-closed security. |
| `TestOrdersCommands_SameOrderDifferentEventID` | Verifies that creating and cancelling the same order ID with distinct event IDs executes correctly. |
| `TestOrdersCommands_FatalHaltSkipsDrain` | Verifies that when `TriggerFatalHalt()` is invoked, the event loop stops immediately on `ctx.Done()` without draining residual input queue events. |
| `TestOrdersCommands_GracefulShutdownDrains` | Verifies that during normal graceful shutdown (`ctx.Done()`), the event loop completely drains pending input events, triggers a final snapshot, and closes `OutputQueue`. |at cancelling an already-filled taker is a safe no-op. |
| `TestOrdersCommands_CreatePartialFillThenCancel` | Verifies taker partial match where remainder rests in book, followed by cancelling the resting remainder. |
| `TestOrdersCommands_DuplicateEventID_FailClosed` | Verifies that redelivering the same `EventID` with a higher offset triggers `HaltCallback()` for fail-closed security. |
| `TestOrdersCommands_SameOrderDifferentEventID` | Verifies that creating and cancelling the same order ID with distinct event IDs executes correctly. |
