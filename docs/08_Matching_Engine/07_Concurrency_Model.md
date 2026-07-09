# TradeDrift Matching Engine — Concurrency Model

**Document:** 07_Concurrency_Model.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** ✅ Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

This document defines exactly which goroutines exist inside a Matching Engine Node, what each one is allowed to touch, and how they hand data to each other without locks. It formalizes the "one Event Loop per market" claim made in `01_Overview.md` and `02_System_Architecture.md`.

---

# 2. Goroutine Inventory

A single Matching Engine Node runs the following goroutines:

| Goroutine | Count | Owns |
| --- | --- | --- |
| Kafka Consumer | 1 per node | Reading assigned partitions, deserializing, routing to Input Queues |
| Market Engine Event Loop | 1 per active market | One `OrderBook`, all matching for that market |
| Publisher Layer worker | 1 per node (or pooled — see Section 6) | Kafka publish, Redis projection push, checkpoint write, metrics |

For V1 (4 markets: BTC, ETH, SOL, DOGE — `02_System_Architecture.md §4`), that's 1 Kafka Consumer + 4 Event Loops + 1 Publisher = 6 goroutines, plus Go runtime/HTTP-health-endpoint goroutines.

---

# 3. Ownership Rule

> **Exactly one goroutine may ever read or write a given `OrderBook`: that market's own Event Loop.**

This is not enforced by a mutex — it is enforced by construction: no reference to a market's `OrderBook` is ever handed to any other goroutine. The Kafka Consumer only ever touches the market's **Input Queue** (a channel), never the book directly. The Publisher only ever touches the market's **Output Queue** (a channel), never the book directly.

```
Kafka Consumer ──chan──► Input Queue ──► Event Loop ──(exclusive owner)──► OrderBook
                                              │
                                              ▼
                                       Output Queue ──chan──► Publisher Layer
```

Because Go channel send/receive establishes a happens-before relationship (`04_Data_Structures/08_Memory_Model.md §7`), no additional synchronization primitive — no mutex, no atomic, no `sync.RWMutex` — is needed anywhere in the matching path.

---

# 4. Input Queue

Each Market Engine has one buffered channel as its Input Queue.

```go
// Event is an interface satisfied by OrderCreated and OrderCancelRequested.
// In practice this is a discriminated union: {Type EventType; Payload []byte}
// or two separate typed channels — implementation detail, not architecture.
type MarketEngine struct {
    marketID    string
    inputQueue  chan Event        // OrderCreated | OrderCancelRequested
    book        *OrderBook
    outputQueue chan MatchResult  // results from one processed event
}
```

**Routing:** The Kafka Consumer reads `market_id` from each event and sends it to the matching Market Engine's `inputQueue`. Because `market_id` is also the Kafka partition key, all events for one market already arrive at the consumer in order — the consumer only has to fan them out to the right channel, never reorder them.

**Backpressure:** The Input Queue is bounded (e.g. depth 1000, tunable). If an Event Loop falls behind and its queue fills, the Kafka Consumer's send blocks, which in turn stalls consumption of that partition — Kafka's own flow control takes over. This is intentional: a slow market never causes the ME to drop events; it causes consumer lag, which is visible in monitoring (`11_Monitoring.md`).

> **V1.5 note:** V1 uses a single Kafka Consumer goroutine routing events to all market Input Queues. If BTC-USDT's queue saturates, that single goroutine blocks and SOL-USDT routing also stalls. V1.5 replaces this with **one consumer goroutine per Kafka partition** — a backed-up BTC-USDT consumer no longer affects other markets. See `16_Future_Enhancements.md §4.1`.

---

# 5. Event Loop

```go
// Run loops over the Input Queue until the channel is closed (graceful shutdown)
// or a panic causes the market to halt (10_Failure_Handling.md §6).
func (m *MarketEngine) Run() {
    for event := range m.inputQueue {
        if !m.processWithRecovery(event) {
            return  // panic — halt this market's Event Loop
        }
    }
}

// processEvent processes ONE input event and sends exactly ONE MatchResult to
// the Output Queue. This "one-in one-out" contract is the key architectural
// invariant of the pipeline:
//
//   ONE input event → ONE Output Queue message → ONE checkpoint write
//
// Consequences:
//   - Zero fills (resting order): sends MatchResult{fills:nil, snapshot, offset}
//   - Sweep of N fills:           sends one MatchResult containing all N fills
//   - Publisher writes exactly one checkpoint per MatchResult.sourceOffset
//   - No "isLast" flag needed; the MatchResult IS the complete event outcome
//
func (m *MarketEngine) processEvent(event InputEvent) {
    var fills        []Fill
    var cancelResult *OrderCancelled

    switch e := event.payload.(type) {

    case OrderCreated:
        node := buildOrderNode(e)    // heap-allocated *OrderNode (see §2 Insert note)

        // Pre-match checks: Tick and lot size validation (05_Matching_Algorithm.md §3)
        if !validTickAndLot(node, m.config) {
            cancelResult = &OrderCancelled{
                orderID:           node.orderID,
                userID:            node.userID,
                marketID:          node.marketID,
                remainingQuantity: node.originalQty,
                reason:            "invalid_order_parameters",
                cancelledAt:       now(),
            }
        } else {
            fills = m.matchingCore.Match(m.book, node, m.mode)

            // MARKET order IOC: if any remainder exists after the match loop,
            // the unfilled quantity must be signalled so Order Service can
            // transition the order and Wallet Service can release reserved funds.
            // (05_Matching_Algorithm.md §6, 06_Event_Contracts.md §4.2)
            if e.orderType == MARKET && node.remainingQty > 0 {
                cancelResult = &OrderCancelled{
                    orderID:           node.orderID,
                    userID:            node.userID,
                    marketID:          node.marketID,
                    remainingQuantity: node.remainingQty,
                    reason:            "ioc_expired",
                    cancelledAt:       now(),
                }
            }
        }

    case OrderCancelRequested:
        // Cancel returns the removed node (or nil if already filled).
        // The Event Loop — not the Match Loop — is responsible for
        // building the OrderCancelled payload from the returned node.
        cancelledNode := Cancel(m.book, e.orderID)   // O(1) — §3
        if cancelledNode != nil {
            cancelResult = &OrderCancelled{
                orderID:           cancelledNode.orderID,
                userID:            cancelledNode.userID,
                marketID:          cancelledNode.marketID,
                remainingQuantity: cancelledNode.remainingQty,
                reason:            "user_requested",
                cancelledAt:       now(),
            }
        }
        // nil return → silent no-op (order already fully filled)
    }

    // GetDepth is ALWAYS called, even for zero-fill events.
    // Inserts, fills, and cancels all change book depth. The snapshot
    // is accurate only after the full event has been processed.
    snapshot := GetDepth(m.book, defaultDepth)   // O(depth) — §7

    // Exactly ONE item sent to the Output Queue per input event.
    m.outputQueue <- MatchResult{
        fills:         fills,
        cancelResult:  cancelResult,
        depthSnapshot: snapshot,
        sourceOffset:  event.offset,   // Kafka offset carried by InputEvent wrapper
    }
}
```

`m.book` is the only place ever touched by this goroutine. The loop is
single-threaded by construction — no concurrent invocation, no interleaving
of two orders' matching logic, no possibility of two goroutines racing on
`bids.sortedPrices` or `orderIndex`.

**Sequential guarantee:** One event is processed fully (all fills, any
`Insert`, the `GetDepth` call, and the Output Queue send) before the next
event is dequeued from `inputQueue`. This is what makes matching deterministic
per market — `03_Order_Book.md §5 Book Invariants` and `05_Matching_Algorithm.md §12`
both rely on it.

# 6. Output Queue and Publisher Layer

The Publisher Layer is a separate goroutine responsible for all I/O that must
never block matching: Kafka publish, Redis projection update, checkpoint write,
and metrics. The Event Loop sends one `MatchResult` to the Output Queue and
immediately continues — it never waits for I/O to complete.

```
Event Loop ──► Output Queue (chan MatchResult) ──► Publisher
                                                        │
                               ┌────────────────┬──────────────────────┐
                               │                │                      │
                               ▼                ▼                      ▼
                         publishKafka    pushRedisProjection   writeCheckpoint
                      (fills → N ×       (snapshot after        (once per
                      TradeExecuted,      Kafka ack)            MatchResult.sourceOffset)
                       cancelResult →
                       OrderCancelled)
```

**Why a separate goroutine:** Kafka, Redis, and Postgres writes carry latency
and failure modes. `02_System_Architecture.md §12`: *"The Matching Core never
waits for these operations."* A slow Kafka broker delays publishing, not matching.

**Checkpoint rule — one per MatchResult:** Because the Event Loop sends exactly
one `MatchResult` per input event, the Publisher writes exactly one checkpoint
per `MatchResult.sourceOffset`. A zero-fill event (resting order) still produces
a `MatchResult` — its `fills` slice is empty but `sourceOffset` is set — so
its checkpoint is written just as reliably as a sweep that produces 10 fills.
No per-fill counting, no "isLast" flag, no sentinel message required.

**V1 Publisher topology:** One Publisher goroutine per node, fan-in from all
markets' Output Queues. V2 upgrades to one Publisher per market — see
`16_Future_Enhancements.md §5`.

**Fan-in mechanics:** The single Publisher goroutine uses a Go `select`
statement over all markets' `outputQueue` channels. Go's `select` is
pseudo-random when multiple channels are simultaneously ready — this is
intentional and correct: cross-market ordering carries no guarantee (different
markets publish to different Kafka partitions). Within a single market, the
Output Queue channel is FIFO, so fill order is always preserved. A market
whose Output Queue is empty is simply not selected; a market whose Output Queue
is backed up receives more `select` picks until it drains, which is the
desired backpressure behaviour.

**Ordering guarantee:** Within a market, the Publisher drains `outputQueue`
in event-arrival order, preserving the in-order publish guarantee in
`06_Event_Contracts.md §7`. For each `MatchResult`, the Publisher publishes
fills to Kafka in fill order (slice index 0 first), then publishes any
`cancelResult`, then pushes the `depthSnapshot` to Redis.

---

# 7. Cross-Market Parallelism

Different markets' Event Loops are completely independent and require no coordination — no state is shared across markets (`03_Order_Book.md §6 Market Isolation`, `02_System_Architecture.md §8`). Each market has its own Event Loop goroutine, allowing the Go scheduler to execute different markets in parallel when multiple CPU cores are available. On single-core systems, the Event Loops execute concurrently through time-slicing while preserving the same deterministic behavior. This architecture allows the Matching Engine to scale across CPU cores without introducing shared-state contention.

```
┌─────────────────────────────────────────────────────────┐
│                    ME Node (V1)                         │
│                                                         │
│  Kafka Consumer (1 goroutine — V1)                      │
│       │           │           │           │             │
│       ▼           ▼           ▼           ▼             │
│  BTC Queue   ETH Queue   SOL Queue  DOGE Queue          │
│       │           │           │           │             │
│       ▼           ▼           ▼           ▼             │
│  BTC Event   ETH Event   SOL Event  DOGE Event          │
│  Loop (g1)   Loop (g2)   Loop (g3)  Loop (g4)          │
│  ──────────── run in parallel, zero shared state ──────│
│       │           │           │           │             │
│       └───────────┴───────────┴───────────┘             │
│                       │ fan-in                          │
│                       ▼                                 │
│              Publisher Layer (1 goroutine)              │
│                       │                                 │
│                       ▼                                 │
│               Kafka · Redis · Postgres                  │
└─────────────────────────────────────────────────────────┘
```

---

# 8. Cancel-vs-Fill Race — Where Concurrency Actually Matters

`07_Order_Service.md §Cancel vs Fill Race Condition` describes a race between a fill and a cancel for the same order. Because `OrderCreated` and `OrderCancelRequested` share a Kafka partition key and therefore arrive at the **same Input Queue in the same order** they were published, this "race" is fully resolved by the time it reaches the Event Loop — it is not a concurrency problem inside the ME at all. The Event Loop simply processes whatever arrived: if the fill event's processing already removed the order from `orderIndex` before the cancel event is read, `Cancel()` is a harmless no-op lookup miss (`04_Data_Structures/07_Algorithms.md §3`). No lock, no compare-and-swap, no distributed coordination is required — single-threaded sequential processing is sufficient.

---

# 9. Shutdown Concurrency

Per `02_System_Architecture.md §16`:

```
Stop Kafka Consumer (no new events enter any Input Queue)
        │
        ▼
Each Event Loop drains its remaining Input Queue
        │
        ▼
Each Event Loop's final results flow to Output Queue, then closes it
        │
        ▼
Publisher drains all Output Queues, flushes to Kafka
        │
        ▼
Wait for Kafka ack on every flushed message
        │
        ▼
Write final checkpoint (only after all acks — see 08_Recovery_Strategy.md)
```

Shutdown is a drain, not a kill — no goroutine is interrupted mid-operation. This guarantees no in-flight match is lost and no checkpoint is written ahead of its corresponding publish.

---

# 11. Concurrency Invariants

The following invariants are enforced by construction. Any violation is a critical bug detectable by `go test -race`.

| # | Invariant | Enforced by |
| --- | --- | --- |
| CI-1 | Exactly one goroutine reads or writes an `OrderBook` — its own Event Loop | No `*OrderBook` reference ever leaves the Event Loop goroutine |
| CI-2 | The Kafka Consumer never calls any method on `OrderBook` | Consumer only sends to `inputQueue chan Event` |
| CI-3 | The Publisher never calls any method on `OrderBook` | Publisher only reads from `outputQueue chan MatchResult` |
| CI-4 | No two markets share any `OrderBook`, `Side`, or `PriceLevel` | One `MarketEngine` per market — no cross-references |
| CI-5 | The checkpoint is written after all results for one input event are published | `writeCheckpoint` is gated on the last result for that Kafka offset |
| CI-6 | The Input Queue is the only communication path from Consumer to Event Loop | No shared variables, no callbacks, no direct goroutine references |
| CI-7 | Shutdown is always a drain, never an interrupt | Goroutines exit via channel close — never via `os.Exit()` or panic mid-operation |

CI-1 through CI-4 are enforced at runtime by the Go race detector. CI-5 through CI-7 are architectural invariants verified by code review and integration tests.

---

# 12. Testing Implications

- Because the Event Loop is single-threaded per market, unit tests for the Matching Core can call `Process(book, event)` synchronously with no goroutines at all — no test needs to reason about scheduling.
- Integration tests should run with Go's race detector (`go test -race`) enabled to catch any accidental cross-goroutine access to a shared `*OrderBook` — a violation of Section 3 would be a bug, and `-race` catches it deterministically.
- Load tests should size Input Queue depth against realistic burst rates per market to validate the backpressure behavior in Section 4 rather than assuming an unbounded channel.

---

# 13. References

- `02_System_Architecture.md §7-13` — Market Engine Manager, Market Engine, Matching Core, Publisher Layer roles
- `04_Data_Structures/08_Memory_Model.md §7` — no-lock justification via channel happens-before
- `03_Order_Book.md §5 Book Invariants` — only one Event Loop may modify an Order Book
- `08_Recovery_Strategy.md` — checkpoint write ordering relative to Kafka ack
- `11_Monitoring.md` — Input Queue depth / consumer lag as an observability signal
- `16_Future_Enhancements.md` — V1.5 per-partition consumer, V2 per-market Publisher
