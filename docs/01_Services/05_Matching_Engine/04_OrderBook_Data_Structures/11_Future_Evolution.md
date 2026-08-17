# TradeDrift Matching Engine — Future Evolution

**Document:** 04_Data_Structures / 11_Future_Evolution.md
**Service:** Matching Engine
**Version:** V1.0
**Last Updated:** July 2026

---

# 1. Purpose

Planned upgrade paths for the Order Book data structures as TradeDrift grows beyond V1. None of these changes are needed today — they are documented so that upgrades can be made without surprising redesigns.

---

# 2. Upgrade Path Summary

| Upgrade | Trigger | Scope of change |
| --- | --- | --- |
| Sorted Slice → B-Tree | O(n) shift is a measured bottleneck | `Side.sortedPrices` only |
| Price Levels → Skip List | Very high price level count (thousands) | `Side.priceLevels` + `sortedPrices` |
| Recovery → Snapshot + Replay | Kafka replay time grows too long | Startup + serialisation layer |
| Single Node → Sharded | Event volume exceeds single process | Deployment only |
| Allocation → Object Pool | Measurable GC pressure | Insert + Fill/Cancel operations |
| Static Markets → Dynamic | Markets added without ME restart | Market Engine Manager |

---

# 3. Sorted Slice → B-Tree

**Trigger:** Profiling shows the O(n) slice shift on price level insert/remove is a bottleneck. Expected when active price levels per market reach hundreds sustained over time.

**Current:**
```go
sortedPrices  []decimal.Decimal
```

**Upgrade target:**
```go
sortedPrices  *btree.BTreeG[decimal.Decimal]   // github.com/google/btree
```

| Operation | V1 (Slice) | Upgraded (B-Tree) |
| --- | --- | --- |
| Best price | O(1) | O(log n) via Min()/Max() |
| Insert new level | O(n) shift | O(log n) |
| Remove empty level | O(n) shift | O(log n) |
| Depth snapshot | O(d) | O(d) via Ascend/Descend |

> Best-price lookup degrades from O(1) to O(log n) with a B-Tree. To preserve O(1), cache `bestPrice` as a field updated on insert/remove.

**Scope:** Only `Side.sortedPrices` and the functions that insert/remove from it. No changes to OrderNode, PriceLevel, OrderBook, matching algorithms, or published events.

---

# 4. Price Levels → Skip List

**Trigger:** Hash map contention or memory overhead at very high price level counts (tens of thousands — unlikely in V1).

Replace `priceLevels` hash map + `sortedPrices` slice with a single skip list providing sorted iteration and O(log n) lookup.

| Property | Skip List |
| --- | --- |
| Insert | O(log n) average |
| Remove | O(log n) average |
| Best price | O(1) |
| Sorted iteration (snapshot) | O(d) |

---

# 5. Recovery → Snapshot + Replay

**Trigger:** Kafka replay time on restart grows too long as the topic accumulates events.

---

## 5.1 The Core Problem

The V1 recovery strategy replays every `OrderCreated` and `OrderCancelRequested` event from **Kafka offset 0** on every restart. This is correct and simple — but recovery time grows linearly with the total number of events ever written to the topic.

```
Kafka topic after 6 months of trading:

[event 1 .... event 2,000,000 .... event 4,999,900 .... event 5,000,000]
 ▲                                        ▲                    ▲
 replay starts here (offset 0)       checkpoint          crash point

Recovery: replay all 5 million events → minutes of downtime
```

On an active exchange, the topic grows continuously. Every restart forces the engine to walk the entire history, which takes longer every day.

---

## 5.2 The Fix: Snapshot + WAL

Instead of replaying from the beginning every time, the engine periodically **serialises its entire in-memory book state** to durable storage (the snapshot). On restart, it loads the snapshot and replays only the small gap of events written **after** the snapshot was taken (the WAL — Write-Ahead Log equivalent).

```
Kafka topic:

[event 1 ... event 4,999,900] [event 4,999,901 ... event 5,000,000]
                    ▲                                    ▲
              Snapshot saved                       crash point
              (seq = 4,999,900)              WAL = last 100 events only

Recovery:
    1. Load snapshot  (instant — deserialise book state)
    2. Replay only 100 events (WAL)  →  milliseconds
```

**Recovery invariant:**

```
Snapshot(seq = N)  +  WAL events(seq > N)  =  exact pre-crash engine state
```

---

## 5.3 V1 vs Snapshot + WAL Comparison

| Property | V1 (replay from 0) | Snapshot + WAL |
| --- | --- | --- |
| Recovery time | Grows with total history | Always fast (fixed WAL gap) |
| Downtime on crash | Minutes at scale | Seconds |
| Implementation complexity | Simple | Serialisation + snapshot store |
| Extra storage needed | None | Snapshot storage (S3 / Postgres) |
| Correct for V1 volumes? | ✅ Yes | Overkill |
| Required at production scale? | ❌ Too slow | ✅ Required |

---

## 5.4 Real-World Scale Example

A busy exchange processes millions of orders per day. After 1 year of operation:

- **V1 approach:** replay ~365 million events on every restart → hours of downtime per crash
- **Snapshot + WAL:** load last snapshot (e.g. taken every 10 minutes) + replay at most a few thousand events → seconds

The V1 approach is perfectly safe when the Kafka topic is young and small. The Snapshot + WAL upgrade is needed only when measured replay time becomes operationally unacceptable.

---

## 5.5 Current V1 State (explicit scope boundary)

V1 does **not** implement snapshotting. Every recovery is a full replay from Kafka offset 0 up to the checkpoint offset. This is documented explicitly in `08_Recovery_Strategy.md §11`.

The V1 recovery sequence:

```
1. Read checkpoint offset C from Postgres
        │
        ▼
2. Seek Kafka partition to offset 0
        │
        ▼
3. Replay all OrderCreated + OrderCancelRequested
   from offset 0 → C   (RECOVERY mode, output suppressed)
        │
        ▼
4. Transition to LIVE mode at offset C
        │
        ▼
5. Book reconstructed — ready
```

---

## 5.6 Upgrade Target

```
Normal operation (periodic snapshot every N minutes or M events):
        │
        ▼
Serialise OrderBook state → snapshot store (S3 / Postgres / Redis AOF)
Record snapshot Kafka offset S
        │
        ▼
On restart:
        1. Read latest snapshot from store
        2. Deserialise OrderBook, Side, PriceLevel, OrderNode
        3. Replay only Kafka events with offset > S   (WAL)
        4. Transition to LIVE
```

**Scope of change:** Add serialisation/deserialisation for `OrderBook`, `Side`, `PriceLevel`, `OrderNode`. Add snapshot storage and retrieval layer. Modify startup recovery sequence in `08_Recovery_Strategy.md`. No changes to matching algorithms, event contracts, or the Publisher layer.

---

# 6. Single Node → Sharded by Market

**Trigger:** Single-process ME cannot keep up with total event volume across all markets.

**Current (V1):**
```
One ME process
    ├── BTC-USDT goroutine
    ├── ETH-USDT goroutine
    └── SOL-USDT goroutine
```

**Upgrade target:**
```
ME Node A  ──▶  BTC-USDT, ETH-USDT
ME Node B  ──▶  SOL-USDT, BNB-USDT
```

Kafka's consumer group protocol handles partition assignment automatically. Each node's internal design is identical to V1. Only the deployment changes.

---

# 7. Allocation → Object Pool

**Trigger:** Go GC shows high pause times or allocation pressure from OrderNode/list.Element churn.

```go
var nodePool = sync.Pool{
    New: func() interface{} { return &OrderNode{} },
}

// On Insert:
node := nodePool.Get().(*OrderNode)
*node = OrderNode{...}

// On FullFill / Cancel:
*node = OrderNode{}
nodePool.Put(node)
```

**Scope:** Confined to Insert and FullFill/Cancel operations only.

---

# 8. Static Markets → Dynamic Market Creation

**Trigger:** Markets need to be added without restarting the Matching Engine.

**Current (V1):** Market Engine Manager reads `trading_pairs` from the database on startup. Adding a market requires an ME restart.

**Upgrade target:**
```
Market Service publishes  MarketEnabled  event
        │
        ▼
Market Engine Manager consumes event
        │
        ▼
Spawn new goroutine + OrderBook for the new market
Subscribe new Kafka partition
        │
        ▼
Live matching begins for new market
```

---

# 9. References

- `10_Design_Decisions.md` — why V1 choices were made
- `02_System_Architecture.md` — cluster deployment model
- `08_Recovery_Strategy.md` — recovery sequencing
