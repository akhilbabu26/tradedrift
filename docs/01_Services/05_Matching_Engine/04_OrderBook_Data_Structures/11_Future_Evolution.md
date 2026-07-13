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

**Current (V1):**
```
Read checkpoint offset from Postgres
    │
    ▼
Replay OrderCreated + OrderCancelRequested from that offset
    │
    ▼
Book reconstructed
```

**Upgrade target:**
```
Periodic snapshot
    │
    ▼
Serialise OrderBook state to snapshot store (S3 / Postgres / Redis AOF)
Record snapshot timestamp + Kafka offset
    │
    ▼
On restart:
    Load latest snapshot
    Replay only events after snapshot Kafka offset
```

**Scope:** Add serialisation/deserialisation for OrderBook, Side, PriceLevel, OrderNode. Add snapshot storage + retrieval. Modify startup recovery sequence. No changes to matching algorithms or event contracts.

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
