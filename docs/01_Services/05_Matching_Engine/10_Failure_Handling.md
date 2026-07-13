# TradeDrift Matching Engine — Failure Handling

**Document:** 10_Failure_Handling.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** ✅ Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

This document catalogs every failure mode the Matching Engine must tolerate, and states the exact handling policy for each. Recovery-from-crash is covered in depth in `08_Recovery_Strategy.md`; this document covers failures that occur *while the process is otherwise healthy* — dependency outages, malformed input, and internal errors.

---

# 2. Guiding Principle

**Matching correctness always wins over availability of secondary features.** The Matching Core must never block, corrupt, or skip a match because of a downstream dependency failure (Redis, metrics, even Kafka publish in some cases). Where a failure genuinely cannot be safely absorbed (e.g. Kafka is down and a `TradeExecuted` cannot be published), the ME slows down or stops accepting new work for that market rather than silently losing a trade.

---

# 3. Failure Matrix

| Failure | Detected where | Impact on matching | Handling |
| --- | --- | --- | --- |
| Kafka broker unreachable (consume side) | Kafka Consumer | New events stop arriving | Consumer retries connection with backoff; Event Loops simply idle (empty Input Queue) — no corruption, matching resumes automatically once Kafka is reachable |
| Kafka broker unreachable (publish side) | Publisher Layer | Trades already matched, but cannot be announced | Publisher retries publish with backoff; Output Queue backs up in memory; Event Loop backpressures if Output Queue is bounded and fills (Section 5) — matching **pauses** for that market rather than dropping a `TradeExecuted` |
| Redis unreachable | Publisher Layer | Depth projection goes stale | Log + metric, drop this write, continue — never retried indefinitely, never blocks Kafka publish (`09_Redis_Projection.md §7`) |
| Postgres unreachable (checkpoint write) | Publisher Layer | Checkpoint falls behind | Retry with backoff; on restart, ME replays a larger range from the last successfully-written checkpoint (`08_Recovery_Strategy.md §6`) — never a correctness issue, only a longer recovery replay. **Lag threshold:** `me_checkpoint_lag_events` (`11_Monitoring.md §2.4`) grows while Postgres is down; a sustained value above the configured P2 alert threshold (e.g. 50,000 events) warns operators to restore Postgres before any restart, avoiding a surprise-long recovery. No explicit circuit breaker is implemented in V1; the metric and alert serve as the manual intervention trigger. |
| Malformed / schema-invalid event | Kafka Consumer | Cannot deserialize | Event is routed to a dead-letter topic (Section 4); consumer offset still advances so one bad message never blocks the partition |
| Panic inside the Match Loop | Event Loop | Market matching halts; other markets are unaffected | `defer/recover` catches the panic; event is dead-lettered; market is marked FAILED and the Event Loop stops for that market; recovery-by-replay (`08_Recovery_Strategy.md §4`) rebuilds a clean, consistent book before the market resumes; a P1 alert is raised. **Never continues with potentially corrupted book state** — "matching correctness always wins" (Section 2) |
| Tick/lot size violation on incoming order | Matching Core (`05_Matching_Algorithm.md §3`) | Order can't be matched | Reject silently at the ME level (should already have been caught by Order Service — this is a defensive check, not expected to fire); log + metric |
| Duplicate `OrderCreated` for an existing `order_id` (redelivery) | Matching Core | Would double-insert | `Insert` uses `orderIndex` for uniqueness; a defensive check before insert rejects an `order_id` already present in `orderIndex`, logging a redelivery warning rather than creating a duplicate resting order |
| Node crash (any cause) | N/A | Full state loss (in-memory) | Full recovery-by-replay on restart — see `08_Recovery_Strategy.md` |
| Market Service unreachable at startup | Market Engine Manager | Cannot load trading pair config | Startup blocks / retries with backoff; the ME does not start matching a market it cannot validate tick/lot size for (`01_Overview.md §9`: "reads from Market Service — startup only") |

---

# 4. Dead-Letter Handling

A dedicated dead-letter topic (e.g. `matching-engine.deadletter`) receives:

- Events that fail schema validation/deserialization.
- Events that cause a panic in the Match Loop. No retry is attempted — retrying a logic bug (nil pointer, assertion failure) just panics again. Dead-letter immediately.

Each dead-lettered message carries the original payload plus a failure reason and timestamp, enabling manual reconciliation — consistent with the dead-letter pattern already established for Settlement Service failures in [08_Order_Service.md §8 Saga Pattern and Compensating Actions](../04_Order_Service/08_Order_Service.md#8-saga-pattern-and-compensating-actions). Dead-lettering an event still advances the Kafka consumer offset for that partition (after the checkpoint rules in `08_Recovery_Strategy.md §6` are respected) — one poison-pill message must never permanently stall an entire market.

---

# 5. Backpressure as a Failure-Containment Mechanism

Bounded channels (`07_Concurrency_Model.md §4, §6`) are the primary tool for containing failure blast radius:

- If Kafka publish is failing, the Output Queue fills, which blocks the Event Loop from placing new results on it, which means the Event Loop simply stops pulling new events off its Input Queue.
- This naturally throttles the whole pipeline for that market back to Kafka consumption, without any explicit circuit-breaker code — the channels themselves provide the backpressure.
- **This is a deliberate tradeoff:** matching *pauses* rather than proceeding without being able to announce results. A paused market is visible immediately via consumer lag and Output Queue depth metrics (`11_Monitoring.md`) — a silently dropped trade would not be.

---

# 6. Panic Recovery in the Event Loop

```go
func (m *MarketEngine) Run() {
    for event := range m.inputQueue {
        if !m.processWithRecovery(event) {
            return  // panic occurred — halt this market's Event Loop
        }
    }
}

func (m *MarketEngine) processWithRecovery(event Event) (ok bool) {
    ok = true
    defer func() {
        if r := recover(); r != nil {
            log.Error("match panic", "market", m.marketID, "event", event, "panic", r)
            metrics.IncPanicRecovered(m.marketID)
            deadLetter(event, r)
            m.haltMarket()  // publish MarketHalted event, stop consuming for this market
            ok = false      // signal Run() to exit the Event Loop
        }
    }()
    // processEvent (07_Concurrency_Model.md §5) handles the full event:
    // Match, IOC-cancel detection, GetDepth, and sends ONE MatchResult to
    // the Output Queue. Any panic inside those operations is caught above.
    m.processEvent(event)
    return

}
```

**Why halt instead of continue:** If a panic occurs mid-mutation (e.g. `PartialFill` reduced `Order.remainingQty` to 3, but panicked before `PriceLevel.totalQty` was updated from 5 to 3), the book is in an inconsistent state. The next incoming order would match against corrupted depth. Continuing would silently propagate that corruption through every subsequent match for this market.

Halting the market and triggering recovery-by-replay (`08_Recovery_Strategy.md §4`) rebuilds the book from Kafka from scratch — guaranteed clean, consistent state, no corruption carried forward. This is the correct application of the guiding principle in Section 2: **matching correctness always wins over availability**. One market halted is far better than one market silently producing incorrect trades.

**Scope:** The halt is per-market. Other markets' Event Loops are completely unaffected (`07_Concurrency_Model.md §7`). The node does not crash.

**Alert severity:** Any panic recovery event is a **P1 alert** (`11_Monitoring.md`) — it indicates a logic bug in the matching algorithm that requires immediate investigation, not a routine operational event.

---

# 7. Idempotency as a Failure-Handling Strategy

Rather than trying to prevent every possible redelivery, the ME leans on idempotency wherever possible, matching the pattern already used across the rest of TradeDrift (`07_Order_Service.md §8`: *"All event handlers must be idempotent"*):

| Operation | Idempotency mechanism |
| --- | --- |
| `Insert` (duplicate `OrderCreated`) | `orderIndex` uniqueness check (Section 3) |
| `Cancel` (duplicate `OrderCancelRequested`, or cancel after already-filled) | `orderIndex` lookup miss is a safe no-op (`04_Data_Structures/07_Algorithms.md §3`) |
| Recovery replay | Deterministic algorithm re-derives identical state; suppressed output means no double-publish (`08_Recovery_Strategy.md §5`) |
| `SettleTrade` on the Wallet Service side | `trade_id` as idempotency key (Settlement Service's responsibility, not the ME's, but the ME's `TradeExecuted` payload is what makes it possible — `06_Event_Contracts.md §3` TradeExecuted schema) |

---

# 8. What the Matching Engine Explicitly Does NOT Retry

- It does not retry a failed match — there is no such thing as a "failed match" in the business sense; a match either completes deterministically or the event is dead-lettered due to an infrastructure-level panic (Section 6), which is an operational bug, not a business retry scenario.
- It does not implement its own circuit breaker library — backpressure via bounded channels (Section 5) serves the same containment purpose with less code and fewer failure modes of its own.
- It does not retry Redis writes — a missed snapshot self-heals on the next match (`09_Redis_Projection.md §7`).

---

# 9. References

- `08_Recovery_Strategy.md` — full-node-crash recovery, distinct from the in-flight failures covered here
- `07_Concurrency_Model.md §4, §6` — bounded channels and backpressure mechanics
- `06_Event_Contracts.md` — idempotency keys (`trade_id`, `order_id`) referenced throughout this document
- `11_Monitoring.md` — metrics and alerts for every failure mode listed in Section 3
- [08_Order_Service.md §8](../04_Order_Service/08_Order_Service.md#8-saga-pattern-and-compensating-actions) — cross-service saga compensating-action pattern this document mirrors
