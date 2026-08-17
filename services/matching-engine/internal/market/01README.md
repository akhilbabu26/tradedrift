# `internal/market` — Market Engine and Event Loop

**Package:** `market`
**Service:** Matching Engine
**Last Updated:** August 2026

---

## 1. What This Package Does

This package is the **goroutine layer** of the Matching Engine. It owns the
lifecycle of each market''s processing loop and acts as the bridge between:

- **Input:** raw Kafka events (from `../kafka/`)
- **Processing:** matching algorithms (from `../matcher/`)
- **Output:** match results sent to the Publisher (from `../publisher/`)

It does NOT implement matching logic itself. That belongs to `../matcher/`.
It does NOT read from Kafka itself. That belongs to `../kafka/`.
It coordinates the goroutines and enforces the ownership rules that make
the Matching Engine lock-free.

---

## 2. Purpose

The `market` package answers one fundamental question:

> How does one market''s order book stay consistent while the rest of the
> system operates concurrently?

**Answer:** By giving each market exactly one goroutine that exclusively
owns its `OrderBook`. No other goroutine ever touches it. No mutex needed.

---

## 3. Files In This Package

| File | Purpose |
| :--- | :--- |
| `engine.go` | `MarketEngine` struct, config types, input/output event types, constructor |
| `event_loop.go` | `Run()` goroutine, `processEvent()`, `validTickAndLot()` |
| `manager.go` | `MarketManager` — creates and indexes all engines |
| `README.md` | This file |

---

## 4. Goroutine Architecture

A single Matching Engine Node runs exactly these goroutines:

```
┌─────────────────────────────────────────────────────────────────┐
│                    Matching Engine Node                         │
│                                                                 │
│  Kafka Consumer (1)                                             │
│       │                                                         │
│       ├──chan──► BTC-USDT InputQueue ──► Event Loop (1) ──► BTC OrderBook
│       │                                       │                 │
│       ├──chan──► ETH-USDT InputQueue ──► Event Loop (1) ──► ETH OrderBook
│       │                                       │                 │
│       └──chan──► SOL-USDT InputQueue ──► Event Loop (1) ──► SOL OrderBook
│                                               │                 │
│                                        OutputQueues             │
│                                               │                 │
│                                    Publisher (1) ──► Kafka + Redis + Postgres
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

For V1 (3 markets): 1 Consumer + 3 Event Loops + 1 Publisher = 5 goroutines.

---

## 5. Ownership Rule (Enforced by Construction, Not Mutex)

```
Kafka Consumer  → touches ONLY InputQueue  (chan send)
Event Loop      → EXCLUSIVELY owns book    (only goroutine that reads/writes)
Publisher       → touches ONLY OutputQueue (chan receive)
```

This is not enforced by a `sync.Mutex`. It is enforced by **construction**:
no reference to a market''s `OrderBook` is ever handed to any other goroutine.

Go channel send/receive establishes a happens-before relationship, which
provides all the memory visibility guarantees needed — without any locks.

---

## 6. Files In Detail

### `engine.go` — MarketEngine

Defines the struct and all types used across the package.

```go
type MarketEngine struct {
    MarketID    string
    InputQueue  chan InputEvent            // Kafka Consumer sends here
    OutputQueue chan orderbook.MatchResult // Publisher reads from here
    book        *orderbook.OrderBook      // ONLY touched by Run() goroutine
    config      MarketConfig              // tick_size, lot_size
    mode        Mode                      // RECOVERY or LIVE
}
```

**`Mode`** controls two phases:

| Mode | What happens |
| :--- | :--- |
| `ModeRecovery` | Events replayed through Match() to rebuild book; all output suppressed |
| `ModeLive` | Normal operation; fills emitted to Publisher |

**`MarketConfig`** holds market-specific trading rules fetched from Market
Service gRPC at startup:

| Field | Purpose |
| :--- | :--- |
| `TickSize` | Minimum price increment (e.g. 0.01 for BTC-USDT) |
| `LotSize` | Minimum quantity increment (e.g. 0.00001 BTC) |

**`InputEvent`** wraps one deserialized Kafka message:

```go
type InputEvent struct {
    Type         EventType             // OrderCreated or OrderCancel
    OrderCreated *OrderCreatedPayload  // non-nil when Type == EventOrderCreated
    OrderCancel  *OrderCancelPayload   // non-nil when Type == EventOrderCancel
    Offset       int64                 // Kafka offset — used for checkpoint
}
```

The `Offset` field is critical — it flows through to `MatchResult.SourceOffset`,
which the Publisher uses to write exactly one checkpoint per processed event.

**`NewMarketEngine(config)`** always creates the engine in `ModeRecovery`.
It never starts in LIVE mode — recovery must complete first.

**`SetLive()`** transitions the engine after recovery replay is complete.
Called by the `recovery` package, not by the engine itself.

---

### `event_loop.go` — Run() and processEvent()

#### `Run()`

```go
func (m *MarketEngine) Run() {
    for event := range m.InputQueue {
        m.processEvent(event)
    }
}
```

Loops over the InputQueue until it is closed (graceful shutdown).
This is the **only goroutine** that calls `processEvent` and therefore
the **only goroutine** that touches `m.book`.

#### `processEvent(event InputEvent)`

Enforces the **one-in one-out invariant**:

> Every single InputEvent produces exactly one MatchResult on the OutputQueue.

This invariant is what makes checkpointing simple — the Publisher writes
one checkpoint per MatchResult, guaranteeing that every processed Kafka
offset is durably recorded.

**For `EventOrderCreated`:**

```
1. Build *OrderNode (heap-allocated — MUST be on heap)
   node.Timestamp = time.Now()   (ME arrival time, NOT Order Service time)

2. validTickAndLot(node, config)?
       No  → send MatchResult{cancelResult: invalid_order_parameters}
            return

3. fills = matcher.Match(book, node, matcherMode)

4. if MARKET and node.RemainingQty > 0:
       cancel = {reason: ioc_expired}

5. send MatchResult{fills, cancel, GetDepth(book, 20), event.Offset}
```

**For `EventOrderCancel`:**

```
1. node = matcher.Cancel(book, orderID)

2. if node != nil:
       cancel = {reason: user_requested, remaining: node.RemainingQty}
   else:
       cancel = nil   (order already filled — silent no-op)

3. send MatchResult{cancel, GetDepth(book, 20), event.Offset}
   (ALWAYS send, even for no-op — so Publisher writes one checkpoint)
```

#### `validTickAndLot(node, config)`

Pre-match validation gate. Checks two rules:

| Check | Condition | Order type |
| :--- | :--- | :--- |
| Tick size | `node.Price % config.TickSize == 0` | LIMIT only |
| Lot size | `node.RemainingQty % config.LotSize == 0` | LIMIT and MARKET |

MARKET orders skip the tick size check because they have no price.

If either check fails, the order is cancelled immediately with
`reason: "invalid_order_parameters"` — it never enters the matching loop.

---

### `manager.go` — MarketManager

Owns all `MarketEngine` instances. Acts as a registry so the Kafka Consumer
can route events to the correct engine by `market_id`.

```go
type MarketManager struct {
    engines map[string]*MarketEngine  // key: marketID
}
```

| Method | Purpose |
| :--- | :--- |
| `Add(config)` | Creates and registers a new engine (does NOT start goroutine) |
| `Get(marketID)` | Returns the engine for a given market — used by Kafka Consumer |
| `All()` | Returns all engines — used by main to start all goroutines |

**Why `Add` does not start the goroutine:**
The goroutine must be started only AFTER the recovery sequence for that
market completes. If the goroutine started before recovery, live Kafka
events could enter the InputQueue while the book is still being rebuilt —
producing wrong matches against an incomplete book state.

The correct startup sequence in `main.go`:
```
For each market:
    engine = manager.Add(config)

For each engine:
    recovery.Replay(engine)     // rebuilds book, then calls engine.SetLive()

For each engine:
    go engine.Run()             // NOW safe to start — book is correct

go consumer.Start()             // NOW safe to start routing live events
```

---

## 7. InputEvent vs Kafka Message

The Kafka Consumer (in `../kafka/`) is responsible for:
1. Reading raw Kafka messages
2. Deserializing JSON into `OrderCreatedPayload` or `OrderCancelPayload`
3. Wrapping them in an `InputEvent` with the Kafka `Offset`
4. Sending to `engine.InputQueue`

The `market` package never sees raw Kafka bytes — it only sees strongly-typed
`InputEvent` structs. This keeps the Event Loop free of serialization concerns.

---

## 8. How matcherMode Is Derived

```go
matcherMode := matcher.ModeRecovery
if m.mode == ModeLive {
    matcherMode = matcher.ModeLive
}
```

The engine''s `Mode` (market-level) maps directly to `matcher.Mode` (algorithm-level).
During RECOVERY, `matcher.Match()` runs the full algorithm but returns nil fills.
During LIVE, it returns the actual fills for publishing.

---

## 9. The One-In One-Out Invariant — Why It Matters

Every InputEvent produces exactly one MatchResult. This is the foundation
of the checkpoint system.

```
Kafka offset 500  →  processEvent()  →  MatchResult{sourceOffset: 500}  →  Publisher
                                                                              │
                                                                    checkpoint: offset 500
```

If the process crashes after processing offset 500 but before writing the
checkpoint, the Publisher will re-process offset 500 on the next startup.
Because RECOVERY mode is idempotent (same input → same book state, no output),
this is safe.

If a "no-op" cancel (order not found) did NOT produce a MatchResult, the
Publisher would never advance the checkpoint past that offset, causing an
infinite loop on restart. That is why even no-op cancels produce a MatchResult.

---

## 10. Backpressure

InputQueue and OutputQueue are both buffered with capacity 1000.

```
If Event Loop falls behind → InputQueue fills → Kafka Consumer''s send blocks
→ Kafka partition consumption stalls → Kafka consumer lag increases
→ Visible in monitoring
```

This is intentional. A slow market never drops events — it applies
backpressure all the way to the Kafka consumer. Events are preserved in
Kafka until the engine catches up.

---

## 11. V1 Limitations and V2 Upgrade Path

### Current V1

| Aspect | V1 Behaviour |
| :--- | :--- |
| Kafka Consumer | Single goroutine routing all markets |
| Goroutine per market | Yes — Event Loop |
| Panic recovery | Not implemented — crash halts that market |
| Metrics | Not instrumented |
| Hot config reload | TickSize/LotSize fixed at startup |

### Future V2 Upgrades

**1. One Kafka Consumer goroutine per partition**

V1 uses one consumer goroutine routing all markets. If BTC-USDT''s InputQueue
fills, the single consumer blocks and SOL-USDT routing also stalls.

V2 fix: one consumer goroutine per Kafka partition. A backed-up BTC-USDT
consumer no longer blocks other markets.

**2. Panic recovery per market**

If processEvent() panics (e.g. nil pointer in a corrupt payload), V1
crashes the entire Event Loop goroutine for that market, halting all
matching for that pair.

V2 fix: wrap processEvent() in a recover() block. On panic, log the error,
publish an alert, and optionally restart the loop from the last checkpoint.

**3. Prometheus metrics**

Instrument:
- `matching_engine_fills_total{market_id}` — counter
- `matching_engine_input_queue_depth{market_id}` — gauge
- `matching_engine_match_duration_seconds{market_id}` — histogram
- `matching_engine_recovery_duration_seconds{market_id}` — gauge

**4. Hot config reload for TickSize/LotSize**

V1 loads TickSize and LotSize at startup from Market Service gRPC.
If the config changes (e.g. tick size reduced), the engine must restart.

V2 fix: subscribe to a Kafka config-change topic and update `m.config`
atomically between event processing iterations (safe because the Event Loop
is single-threaded — no mutex needed, just assign between iterations).

---

## 12. What This Package Does NOT Do

- Does NOT implement matching algorithms — that is `../matcher/`
- Does NOT read from Kafka — that is `../kafka/`
- Does NOT publish to Kafka — that is `../publisher/`
- Does NOT write to Redis — that is `../projection/`
- Does NOT write checkpoints — that is `../recovery/`
- Does NOT validate auth or fund availability — that is Order Service
- Does NOT track order status transitions — that is Order Service
