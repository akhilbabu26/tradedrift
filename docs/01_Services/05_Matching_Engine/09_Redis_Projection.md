# TradeDrift Matching Engine — Redis Projection

**Document:** 09_Redis_Projection.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** ✅ Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

The Matching Engine pushes a read-only depth snapshot of each Order Book to Redis after every match, so that other services (API Gateway, a future WebSocket/market-data service, dashboards) can read live order book depth **without ever querying the Matching Engine directly or touching its in-memory state.**

Redis here is a **projection**, not a source of truth. The Order Book itself remains the only authoritative state, and it lives entirely inside the Matching Engine's memory (`03_Order_Book.md §13`).

---

# 2. What Gets Written

The payload is exactly the `DepthSnapshot` produced by `GetDepth` (`04_Data_Structures/07_Algorithms.md §7`):

```json
{
  "market_id": "BTC-USDT",
  "bids": [
    { "price": "101.50", "quantity": "2.30" },
    { "price": "101.25", "quantity": "0.80" }
  ],
  "asks": [
    { "price": "101.60", "quantity": "1.10" },
    { "price": "101.75", "quantity": "3.00" }
  ],
  "snapshot_at": "2026-07-09T10:15:32.114Z"
}
```

- `bids` / `asks` are the top `depth` price levels only (default `depth = 20`, configurable per deployment), not the full book — matching `GetDepth(book, depth)`'s signature.
- `quantity` at each level is the pre-aggregated `PriceLevel.totalQty` (`04_Data_Structures/04_Price_Level.md`), not a per-order breakdown — individual resting orders and their owners are never exposed to Redis or any consumer of this projection. This is intentional: the depth snapshot is a public market-data artifact, and per-order attribution is private user data that never leaves the Matching Engine / Order Service boundary.

---

# 3. Key Naming Convention

```
orderbook:depth:{market_id}
```

Examples:

```
orderbook:depth:BTC-USDT
orderbook:depth:ETH-USDT
orderbook:depth:SOL-USDT
```

One key per market. The entire snapshot is stored as a single JSON value under this key (not decomposed into a Redis sorted set or hash) — this keeps the write a single `SET`, and the read a single `GET`, with no risk of a consumer observing a torn/partial update across multiple keys.

---

# 4. Write Timing

Per `04_Data_Structures/07_Algorithms.md §7`: *"Called after every match."* More precisely, per `02_System_Architecture.md §13`, the Redis write happens from the **Publisher Layer**, after the Kafka `TradeExecuted`/`OrderCancelled` publish for that event has been acknowledged — never before, and never from the Matching Core itself.

```
Event Loop (owns OrderBook)
        │
        ├── Match(book, event) → fills
        │
        ├── After ALL fills for this input event:
        │       GetDepth(book, depth) → DepthSnapshot
        │       (Event Loop is the ONLY goroutine allowed to call GetDepth —
        │        Publisher has no access to book; see 07_Concurrency_Model.md CI-3)
        │
        └── Bundle {fills, DepthSnapshot} into final MatchResult
                │
                ▼
         Output Queue (chan)
                │
                ▼
         Publisher Layer
                │
                ├── Publish TradeExecuted / OrderCancelled to Kafka
                │       │
                │       ▼ (after Kafka ack)
                ├── Push DepthSnapshot to Redis (this document)
                ├── Write checkpoint (08_Recovery_Strategy.md)
                └── Record metrics (11_Monitoring.md)
```

**Why GetDepth runs in the Event Loop, not the Publisher:** The Publisher goroutine never holds a reference to `*OrderBook` — this is enforced by construction (CI-3 in `07_Concurrency_Model.md §11`). `GetDepth` reads the book's sorted price levels, so it must run in the Event Loop goroutine that exclusively owns the book. The pre-computed snapshot travels through the Output Queue as part of the MatchResult, and the Publisher pushes it to Redis without any book access.

**Consequence:** the Redis projection can lag the true book state by however long Kafka ack + Redis write takes — typically low single-digit milliseconds. Consumers must treat this as an eventually-consistent read replica, never as a synchronous source of truth for order placement decisions (e.g. it must never be read to decide whether an order would currently cross the book — only the ME itself makes that determination).

**Also written after cancels**, not just fills — a cancel changes book depth just as a fill does, so `OrderCancelled` processing triggers the same `GetDepth` → Redis push.

---

# 5. Write Frequency vs Match Frequency

`GetDepth` is called once per *processed event* that changed the book — a resting insert, a fill, or a cancel. All three cases change book depth and must be reflected in the projection:

| Event outcome | Book changes? | Redis push? |
| --- | --- | --- |
| Order rests (no match) | Yes — new order/level added | ✓ |
| Fill (full or partial) | Yes — order removed or quantity reduced | ✓ |
| Cancel | Yes — order removed, level possibly removed | ✓ |

A sweep that produces 5 `TradeExecuted` events from a single incoming order still results in **one** Redis snapshot write reflecting the book's final state after all 5 fills — not 5 separate writes. This avoids unnecessary Redis write amplification on large sweeps while still keeping the projection accurate as of the latest processed event.

---

# 6. Consumers

Redis is read-only from every consumer's perspective — no consumer ever writes to these keys.

| Consumer | Usage |
| --- | --- |
| API Gateway | Serves `GET /markets/{id}/orderbook`-style REST/WebSocket requests by reading the key directly, without calling the ME |
| Future market-data / WebSocket service | Streams depth updates to connected clients by polling or subscribing to key changes |
| Internal dashboards | Read-only visibility into live book depth for operators |

The Matching Engine has no knowledge of, and no dependency on, how many consumers read this projection or how often — this follows the same Loose Coupling principle already established for Kafka consumers in `01_Overview.md §5`.

---

# 7. Failure Handling

**Redis is never on the critical path of matching.** If the Redis write fails (connection error, timeout, Redis unavailable):

- The match itself has already completed and already published to Kafka — nothing about the trade is affected.
- The failed write is logged and a metric is incremented (`11_Monitoring.md`); it is **not retried indefinitely** and does not block the Publisher Layer from processing the next Output Queue item.
- The **next** successful match will push a fresh snapshot that supersedes the missed one — snapshots are not incremental/differential, so a single missed write self-heals on the next update with no special recovery logic needed.
- If Redis is down for an extended period, the projection simply goes stale; consumers reading it should treat a snapshot older than some threshold (e.g. via `snapshot_at`) as potentially stale and can fall back to a "book state unknown" UI treatment. This is a consumer-side concern, not something the ME compensates for.

This mirrors the general principle in `02_System_Architecture.md §12`: *"The Matching Core never waits for these operations."*

---

# 8. No Snapshot on Recovery

During `RECOVERY` mode (`08_Recovery_Strategy.md`), Redis writes are suppressed along with all other Publisher output. This means the Redis projection for a market being recovered continues to show its **last live value** (from before the crash) throughout the replay, then jumps to the correct current state once recovery completes and live processing resumes. Consumers may briefly see a stale-but-not-wrong snapshot during a restart; they never see a partially-reconstructed or inconsistent one, since nothing is written until the book is fully rebuilt.

---

# 9. TTL / Expiry Policy

Keys are **not** set with a TTL. A market's depth key should exist for as long as that market is active, and the Matching Engine (not Redis expiry) is the only thing that should ever cause it to go stale-but-present or be removed (e.g. on permanent market deactivation, handled by an explicit deletion as part of a future `MarketDisabled` handling path — not implemented in V1, since V1 markets are static per `04_Data_Structures/11_Future_Evolution.md §8`).

---

# 10. Explicitly Out of Scope for V1

- Full order-level depth (only aggregated per-price-level quantity is projected).
- Redis Pub/Sub push notifications on change (V1 is poll/read-on-demand only; a subscribe-based push model is a natural future addition for the market-data service but isn't required for V1).
- Historical depth / time-series storage (Redis holds only the latest snapshot per market, not a history).

---

# 11. References

- `04_Data_Structures/07_Algorithms.md §7` — `GetDepth` pseudocode this projection is built from
- `04_Data_Structures/04_Price_Level.md` — `totalQty` pre-aggregation that makes `GetDepth` O(d)
- `02_System_Architecture.md §13` — Publisher Layer responsibilities and checkpoint/publish ordering
- `08_Recovery_Strategy.md` — why Redis writes are suppressed during RECOVERY mode
- `10_Failure_Handling.md` — general failure-handling policy for non-critical-path I/O
