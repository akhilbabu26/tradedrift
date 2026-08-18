# `internal/recovery` — Crash Recovery and Engine Replay

**Package:** `recovery`  
**Service:** Matching Engine  
**Last Updated:** August 2026  

---

## 1. What This Package Does

This package is the **startup recovery and durability layer** of the Matching Engine. When the engine restarts after a crash, a deployment, or a planned shutdown, all in-memory order books are lost. The `recovery` package rebuilds every `MarketEngine`'s order book to its exact pre-crash state before any live events are processed.

It does this by:

1. Reading the last committed Kafka offset from Postgres (`kafka_checkpoints`)
2. Capturing the current end of each Kafka partition (the **high water mark**)
3. Creating a dedicated Kafka reader and replaying all events from `savedOffset+1` to `hwm-1` through the engine's Event Loop in `ModeRecovery`
4. Draining the `OutputQueue` (results are discarded — the Publisher is not running)
5. Pushing a fresh depth snapshot to Redis so market data feeds are accurate
6. Calling `engine.SetLive()` — the engine is now ready for live Kafka events

---

## 2. Purpose

The `recovery` package answers one question on every restart:

> What is the minimum set of events to replay so that every in-memory order book is byte-for-byte identical to its pre-crash state?

The answer: **replay every event from the last successfully checkpointed offset forward to the Kafka partition's end.**

---

## 3. Files In This Package

| File | Purpose |
| :--- | :--- |
| `replayer.go` | `Replayer` struct, `ReplayAll()`, `replayEngine()`, `replayTopic()`, `loadCheckpoint()`, `highWatermark()`, `pushFreshDepth()` |
| `README.md` | This file |

---

## 4. Architecture Overview

```
                         ENGINE RESTART
                               │
                               ▼
                     ┌─────────────────────┐
                     │   Create Engines    │  NewMarketEngine() → ModeRecovery
                     └──────────┬──────────┘
                                │
                                ▼
                     ┌─────────────────────┐
                     │  replayer.ReplayAll │
                     └──────────┬──────────┘
                                │  (sequential per market)
                    ┌───────────┴───────────┐
                    │                       │
                    ▼                       ▼
             BTC Engine               ETH Engine
                    │                       │
       ┌────────────┼────────────┐          │  (same steps)
       │            │            │          │
       ▼            ▼            ▼          │
  loadCheckpoint  highWatermark  replayTopic│
       │            │            │          │
       └────────────┘            │          │
       savedOffset+1 → hwm-1    │          │
                                 │          │
                                 ▼          │
                          ModeRecovery      │
                          Match() runs      │
                          (no output to     │
                          Kafka/Redis)      │
                                 │          │
                                 ▼          │
                          OutputQueue       │
                          drained (exact    │
                          count, no block)  │
                                 │          │
                                 ▼          │
                          pushFreshDepth    │
                          → Redis           │
                                 │          │
                                 ▼          │
                           SetLive()        │
                                 │          │
                    └───────────┬───────────┘
                                │
                                ▼
                     ┌─────────────────────┐
                     │  Consumer.Start()   │  ← ONLY after ReplayAll returns
                     └─────────────────────┘
                     ┌─────────────────────┐
                     │  Publisher.Run()    │  ← ONLY after ReplayAll returns
                     └─────────────────────┘
                                │
                                ▼
                            LIVE MODE
```

---

## 5. Structs

### `Replayer`

```go
type Replayer struct {
    brokers []string          // Kafka brokers for replay readers and HWM queries
    db      *pgxpool.Pool     // Postgres — reads kafka_checkpoints
    redis   *redis.Client     // Redis — writes fresh depth after replay
    manager *market.MarketManager  // all registered MarketEngines
}
```

Created via `NewReplayer(brokers, db, rdb, manager)`.

---

## 6. Function Reference

### `ReplayAll(ctx)`

Entry point. Iterates all registered `MarketEngine`s and calls `replayEngine()` for each, **sequentially**.

**Why sequential?** Sequential recovery is deterministic, simple, and has no race conditions. Parallel recovery is an optimization for V2 when startup time becomes a measured bottleneck. For V1 with 3 markets and months of event history, sequential recovery is fast enough.

```go
for _, engine := range r.manager.All() {
    r.replayEngine(ctx, engine)
}
```

**MUST be called before `Consumer.Start()` and `Publisher.Run()`.**

---

### `replayEngine(ctx, engine)`

Recovers one market:

```
Start engine.Run() goroutine (ModeRecovery)
        │
        ▼
replayTopic(orders.submitted)
        │
        ▼
replayTopic(orders.cancel-requested)
        │
        ▼
Drain OutputQueue (exactly N events)
        │
        ▼
pushFreshDepth → Redis
        │
        ▼
engine.SetLive()
```

---

### `replayTopic(ctx, engine, topic)`

Replays one Kafka topic:

1. **Load checkpoint** — reads `savedOffset` from Postgres (or `-1` if no checkpoint exists)
2. **Capture HWM** — calls `highWatermark()` to get the Kafka partition's current end offset
3. **Check if anything to replay** — if `savedOffset+1 >= hwm`, nothing to do
4. **Create dedicated reader** — `kafkago.NewReader` with no `GroupID` (does not touch live consumer group)
5. **Seek** — `reader.SetOffset(savedOffset + 1)` skips already-processed events
6. **Read loop** — fetch and route each message until `msg.Offset >= hwm-1`
7. **Return count** — exact number of events sent to `InputQueue`

---

### `routeMessage(msg, engine, topic)`

Deserializes one Kafka message and sends it to the engine's `InputQueue` if the `market_id` matches. Reuses the exported `kafka.HandleOrderCreated` and `kafka.HandleOrderCancel` functions — **no duplicate deserialization logic**.

```go
sent := false

route := func(marketID string) chan market.InputEvent {
    if marketID == engine.MarketID {
        sent = true        // mark as sent only for the correct market
        return engine.InputQueue
    }
    return nil             // wrong market — skip silently
}
```

**Why the `sent` flag matters:**
Without it, a message for a different market (e.g. ETH event replayed during BTC recovery) would return `nil` to the route function but `routeMessage` would still return `true`. The drain count would be wrong — the drain loop would wait forever for an OutputQueue result that was never produced.

| Outcome | `sent` | Returns |
|---|---|---|
| Event belongs to this engine, sent | `true` | `true, nil` |
| Event belongs to a different engine, skipped | `false` | `false, nil` |
| Corrupt/invalid message | `false` | `false, err` |

---

### `loadCheckpoint(ctx, topic, partition)`

Queries Postgres for the last successfully processed offset:

```sql
SELECT offset FROM kafka_checkpoints
WHERE topic = $1 AND partition = $2
```

Returns `-1` if no row exists (meaning: replay from offset 0, the very beginning).

Uses `pgx.ErrNoRows` sentinel — **not** a string comparison:

```go
if errors.Is(err, pgx.ErrNoRows) {
    return -1, nil
}
```

---

### `highWatermark(topic, partition)`

Connects directly to the Kafka partition leader and reads the latest available offset:

```go
conn, _ := kafkago.DialLeader(ctx, "tcp", broker, topic, partition)
last, _ := conn.ReadLastOffset()
```

The returned value is the offset of the **next message to be produced**. The last available message is at `hwm - 1`. Recovery stops at `hwm - 1` and does not chase new messages during replay.

```
Historical events          HWM           Live events
───────────────────────────┼──────────────────────────
offset 0 → hwm-1           │             hwm →
                           │
                   Recovery stops here
                           │
                     Consumer starts
```

---

### `pushFreshDepth(ctx, engine)`

After replay is complete, the in-memory book is correct but Redis may be stale (Redis could have restarted too). This function:

1. Calls `engine.GetDepth(20)` — reads Top-20 levels from the rebuilt book
2. Serializes to the same JSON format as the Publisher
3. Writes to `depth:{market_id}` in Redis (TTL=0)

Key format matches the Publisher exactly: `depth:BTC-USDT`, `depth:ETH-USDT`, etc.

---

## 7. Critical Architectural Constraints

### V1 Single-Partition Constraint

```go
const partition = 0  // V1: one partition per topic
```

Both `orders.submitted` and `orders.cancel-requested` are configured with **exactly 1 partition** in V1. Recovery only reads partition 0.

> ⚠️ Before increasing Kafka topic partition count, the recovery implementation **must** be updated to iterate all partitions via `kafkago.LookupPartitions()` and replay each independently. Otherwise, events on partitions 1+ will be silently skipped on restart.

### Cross-Topic Replay Order

Recovery replays `orders.submitted` completely before `orders.cancel-requested`. This is safe because:

1. **Live Consumer is also unordered across topics** — in production, two independent goroutines process the two topics with no global ordering guarantee. Recovery is no worse than live.
2. **Causal ordering preserved** — a cancel event can only exist in Kafka _after_ the corresponding order was submitted. Submitted → Cancel is always the correct causal sequence.
3. **Cancel of filled order is a no-op** — if an order was matched and removed during submitted replay, a subsequent cancel for it calls `book.Cancel()` which returns `nil` (order not found) — identical to production behavior.

### Checkpoint is Read-Only During Recovery

The Replayer **reads** the checkpoint to find where to resume. It does **not** write new checkpoints during replay. The checkpoint represents the last externally-acknowledged live processing boundary — recovery must not advance it.

New checkpoints are written by the Publisher **only** after:
1. TradeExecuted events are acknowledged by Kafka
2. Depth snapshot is written to Redis
3. Only then the Postgres checkpoint is UPSERTed

---

## 8. OutputQueue Drain — Why Exact Count

During recovery, `engine.Run()` runs in `ModeRecovery`. `processEvent()` still sends every `MatchResult` to `OutputQueue` — even in recovery mode. The Publisher is not running yet, so nothing consumes the queue.

The drain loop uses an **exact count** rather than "drain until empty":

```go
// CORRECT: drain exactly what we sent
for i := 0; i < totalEvents; i++ {
    <-engine.OutputQueue
}

// WRONG: would block forever waiting for live events that never come
for len(engine.OutputQueue) > 0 {
    <-engine.OutputQueue
}
```

`totalEvents` is the sum of `sent=true` returns from `routeMessage()` across all topics. Messages routed to a different market (wrong `market_id`) do not increment the count because they never enter `InputQueue` and never produce an `OutputQueue` result.

---

## 9. Interaction with Publisher's Monotonic Checkpoint

The Publisher uses a monotonic UPSERT to prevent checkpoint regression:

```sql
ON CONFLICT (topic, partition)
DO UPDATE SET
    offset     = EXCLUDED.offset,
    updated_at = NOW()
WHERE kafka_checkpoints.offset < EXCLUDED.offset   -- only advance, never retreat
```

This protects against the race where:

```
Kafka partition 0:
offset 100 → BTC event
offset 101 → ETH event

ETH Publisher (fast) → checkpoint = 101
BTC Publisher (slow) → checkpoint = 100   ← would regress without WHERE guard
```

With the WHERE guard, BTC's write to 100 is silently ignored. The checkpoint stays at 101.

On recovery, the engine replays from `checkpoint+1`. Replaying one extra event (that was already processed by the faster engine) is safe — it hits `ModeRecovery` and is suppressed.

---

## 10. What This Package Does NOT Do

- Does NOT process live events — that is `../kafka/`
- Does NOT write trade results to Kafka or Redis — that is `../publisher/`
- Does NOT own the order book — that is `../market/` and `../orderbook/`
- Does NOT write checkpoints to Postgres — only reads them
- Does NOT run after startup — Replayer is used once at startup then discarded
- Does NOT run markets in parallel — sequential in V1 by design

---

## 11. V2 Upgrade Path

| Limitation | V1 Behaviour | V2 Upgrade |
| :--- | :--- | :--- |
| Single partition | `const partition = 0` | Iterate `kafkago.LookupPartitions()`, replay all partitions independently |
| Sequential market recovery | One market at a time | Spawn one goroutine per market, use `errgroup` for parallel recovery |
| Full topic replay | Replay from checkpoint to HWM | Add periodic snapshot checkpoints (e.g. every 100k events) to reduce replay window |
| Redis stale detection | Always pushes fresh depth | Only push if Redis key is missing or older than threshold (`updated_at < now-5s`) |
