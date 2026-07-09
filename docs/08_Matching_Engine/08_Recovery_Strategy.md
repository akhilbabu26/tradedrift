# TradeDrift Matching Engine — Recovery Strategy

**Document:** 08_Recovery_Strategy.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** ✅ Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

This document is the authoritative, detailed sequencing for how a Matching Engine Node rebuilds its Order Books after a restart or crash. `03_Order_Book.md §14` and `04_Data_Structures/10_Design_Decisions.md ADR-004` already establish the *approach* (replay input events through the algorithm, in a suppressed mode); this document covers the full sequencing, edge cases, and timing guarantees.

---

# 2. Why Recovery Is Needed at All

The Order Book is intentionally ephemeral (`01_Overview.md §5 Memory First`) — it is never persisted directly, because persisting it would require a database write on every match, which is unacceptable latency for the matching hot path. The tradeoff is that on any restart, the book must be rebuilt from something durable: Kafka.

---

# 3. What Is Durable vs Ephemeral

| State | Durable? | Where |
| --- | --- | --- |
| Order Book contents (bids, asks, resting orders) | No | Rebuilt from Kafka on every restart |
| Kafka checkpoint (`{topic, partition, offset}`) | Yes | PostgreSQL, one row per partition |
| `OrderCreated` / `OrderCancelRequested` history | Yes | Kafka topic retention |
| `TradeExecuted` / `OrderCancelled` history | Yes (for downstream consumers) | Kafka topic retention — but **not read back by the ME itself** during recovery |

---

# 4. Recovery Sequence (Full Detail)

```
1. Application starts, loads config, connects to Kafka and Postgres.
        │
        ▼
2. Kafka Consumer joins the Consumer Group, receives assigned partitions.
        │
        ▼
3. For each assigned partition (= each market):
        │
        ├── Read checkpoint row from Postgres: {topic, partition, offset}
        │
        ├── If no checkpoint row exists (first-ever startup for this partition):
        │       treat as offset 0 — full replay from the beginning of the topic
        │
        ├── Create the Market Engine (empty OrderBook: empty bids, empty
        │   asks, empty orderIndex — 04_Data_Structures/02_Order_Book.md §8)
        │
        ├── Enter RECOVERY mode for this market:
        │       Publisher output is fully suppressed —
        │       no Kafka publish, no Redis snapshot push, no metrics
        │       (03_Order_Book.md §14 step 2)
        │
        ├── Seek the Kafka partition to the checkpoint offset (inclusive —
        │   the event at that offset is re-processed in RECOVERY mode,
        │   which is idempotent since all output is suppressed).
        │
        ├── Replay events from that offset, in order, through the SAME
        │   Event Loop and SAME Matching Core code path as live processing:
        │
        │       OrderCreated          → Match(book, order, RECOVERY)
        │       OrderCancelRequested  → Cancel(book, orderID)
        │
        │   (TradeExecuted is never read back — see Section 5)
        │
        ├── When the replay reaches the current end of the topic for that
        │   partition (i.e. caught up to what Kafka reports as the latest
        │   offset at consumer-group-join time), exit RECOVERY mode.
        │
        └── Market Engine now processes live events normally — Publisher
            output resumes (Kafka publish, Redis projection, checkpoints).
        │
        ▼
4. Once every assigned market has exited RECOVERY mode, the node reports
   itself Ready on its health endpoint (11_Monitoring.md).
```

---

## 4.1 Recovery State Machine

Each Market Engine goroutine moves through four states on every start. States are per-market — BTC-USDT and ETH-USDT run independent state machines in parallel.

```
                        ┌───────────────────────────────────┐
                        │           ME Node Starts           │
                        └──────────────────┬────────────────┘
                                           │
                                           ▼
                              ┌────────────────────────┐
                              │        STARTING         │
                              │                         │
                              │  Connect Kafka/Postgres  │
                              │  Receive partition list  │
                              └────────────┬────────────┘
                                           │ partition assigned
                                           │ (one per market)
                                           ▼
                              ┌────────────────────────┐
                              │   LOADING CHECKPOINT    │
                              │                         │
                              │  Read {topic, partition, │
                              │  offset} from Postgres   │
                              │  (offset = 0 if none)   │
                              └────────────┬────────────┘
                                           │ checkpoint loaded
                                           ▼
               ┌───────────────────────────────────────────────┐
               │                  RECOVERY                      │◄──────────┐
               │                                               │           │
               │  Seek to checkpoint offset (inclusive)         │  crash    │
               │  Replay OrderCreated / OrderCancelRequested    │  during   │
               │                                               │  replay   │
               │  Publisher:  ✗ suppressed                      │           │
               │  Kafka pub:  ✗ off                             │           │
               │  Redis push: ✗ off                             │           │
               │  Checkpoint: ✗ off (no new checkpoint writes)  │           │
               └──────────────┬──────────────────┬─────────────┘           │
                              │                  │                           │
                   caught up  │                  │ crash                     │
                   to end of  │                  └───────────────────────────┘
                   topic at   │                   restart from same offset
                   join time  │
                              ▼
               ┌───────────────────────────────────────────────┐
               │                    LIVE                        │
               │                                               │
               │  Process OrderCreated / OrderCancelRequested   │
               │  in real-time as events arrive                 │
               │                                               │
               │  Publisher:  ✓ active                          │
               │  Kafka pub:  ✓ on (TradeExecuted/Cancelled)    │
               │  Redis push: ✓ on (after Kafka ack)            │
               │  Checkpoint: ✓ once per input event            │
               └──────────────┬────────────────────────────────┘
                              │ crash / SIGTERM
                              ▼
                         Restart → STARTING
                         checkpoint offset = last published + acked event
```

**"Caught up" definition:** RECOVERY exits when the replayed offset reaches the **high-water mark at consumer-group-join time** — not the absolute end of the topic (which keeps growing). Any events that arrived between join time and the moment RECOVERY exits are queued in the Input Queue and processed normally as the first live events.

---

# 5. Why `TradeExecuted` Is Never Replayed

This is `04_Data_Structures/10_Design_Decisions.md ADR-004`, restated here with the operational consequence spelled out:

The matching algorithm is deterministic (`05_Matching_Algorithm.md §12`). Replaying the exact same ordered `OrderCreated`/`OrderCancelRequested` sequence through the exact same algorithm reconstructs the exact same book state and the exact same set of fills — there is no need to separately store or replay the fills themselves. Doing so would also be actively wrong: those trades were already settled by Settlement/Wallet Service on their first (pre-crash) publication. Replaying and republishing them would cause Settlement Service to attempt to settle the same trade twice.

The suppression during RECOVERY mode is precisely what prevents this: new `trade_id` values ARE generated during replay (the algorithm doesn't know it's replaying), but they are discarded, never published, and never reach Settlement Service.

---

# 6. Checkpoint Semantics During Recovery

Per `02_System_Architecture.md §13`, checkpoints are written **after** Kafka publish is acknowledged, and **once per input event** — after all resulting fills for that event are published and acknowledged. This is not once per fill: a single `OrderCreated` that produces N fills (a sweep) writes exactly one checkpoint after the Nth fill is acked. See `07_Concurrency_Model.md §6` for the Publisher-level checkpoint rule. This matters for recovery correctness:

- If the ME crashes after matching but *before* the checkpoint write, the checkpoint still points to the last-confirmed match. On restart, the crashed match's input event(s) are replayed — harmless, since replay is idempotent from the book's perspective (Section 5) and output is suppressed.
- If the ME crashes after the checkpoint write but before some unrelated later event is processed, that later event simply hasn't been consumed yet — Kafka redelivers it normally, no special recovery handling needed since it was never "lost," just not yet reached.

**No double-checkpoint risk:** because the checkpoint write happens only after Kafka ack, recovery never needs to "roll back" a checkpoint — it can always trust the stored offset as fully safe to resume from.

---

# 7. Recovery Time

Recovery time is dominated by how far back the checkpoint is relative to the current end of the topic, i.e. how many `OrderCreated`/`OrderCancelRequested` events must be replayed. Since checkpoints are written per match (a frequent event on an active market), the gap on a clean restart is typically small — at most the events processed since the last checkpoint before the crash.

**Worst case:** a brand-new partition (or checkpoint table wiped) forces a full replay from offset 0. For V1's expected event volumes this is acceptable; if topic retention grows large enough to make this slow, `04_Data_Structures/11_Future_Evolution.md §5 Recovery → Snapshot + Replay` describes the upgrade path (periodic snapshot + replay-from-snapshot instead of replay-from-zero).

---

# 8. Partial Recovery / Partition-Level Independence

Each partition (market) recovers independently — a slow replay on one market (e.g. BTC-USDT with heavy volume) does not block another market (e.g. DOGE-USDT) from finishing recovery and going live sooner. This falls directly out of `07_Concurrency_Model.md §7`: markets share no state, so their recovery goroutines can run in parallel at startup exactly like their Event Loops run in parallel once live.

---

# 9. Recovery in Future Cluster Mode

`02_System_Architecture.md §17` describes rebalancing in future multi-node deployments. When a partition is revoked from one node and assigned to another (node leaves/joins the Consumer Group), the newly-assigned node runs the **exact same recovery sequence** described in Section 4 — reading the checkpoint row, replaying from there. This is why the recovery design is partition-based rather than node-based: it works identically whether the node recovering is the same node that crashed or a different node picking up a rebalanced partition. No V1 code changes are needed to support this; only the deployment topology changes.

---

# 10. Failure During Recovery Itself

If the ME crashes *during* replay (before reaching the checkpoint's "caught up" point), no harm is done — the checkpoint row was never advanced during RECOVERY mode (checkpoints are only written for live, published matches, not suppressed replay matches). On the next restart, recovery simply starts over from the same checkpoint offset. This is safe specifically because replay is idempotent and side-effect-free (no publish, no Redis, no new checkpoint writes) until RECOVERY mode is exited.

---

# 11. Explicitly Out of Scope for V1

- **Snapshotting** — not implemented; every recovery is a full replay-from-checkpoint. See `04_Data_Structures/11_Future_Evolution.md §5`.
- **Cross-region / multi-datacenter recovery** — out of scope; V1 assumes a single region's Kafka and Postgres.
- **Partial-market recovery prioritization** (e.g. recovering high-volume markets first) — V1 recovers all assigned partitions with equal priority, in parallel.

---

# 12. References

- `03_Order_Book.md §14` — high-level recovery sequence and rationale
- `04_Data_Structures/10_Design_Decisions.md ADR-004` — why input-event replay over output-event replay
- `04_Data_Structures/11_Future_Evolution.md §5` — snapshot + replay upgrade path for when full Kafka replay becomes too slow
- `05_Matching_Algorithm.md §12` — determinism property that makes input-event replay correct
- `02_System_Architecture.md §13, §15, §17` — checkpoint timing, startup sequence, cluster rebalancing
- `07_Concurrency_Model.md §6` — checkpoint written once per input event, not once per fill
- `07_Concurrency_Model.md §7` — per-market goroutine independence that enables parallel recovery
- `11_Monitoring.md` — health endpoint / Ready signal during recovery
