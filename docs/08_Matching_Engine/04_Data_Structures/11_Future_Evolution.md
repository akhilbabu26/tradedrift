# TradeDrift Matching Engine — Future Evolution

**Document:** 04_Data_Structures / 11_Future_Evolution.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

This document describes the planned upgrade paths for the Order Book data structures as TradeDrift grows beyond V1.

None of these changes are needed today. They are documented now so that future contributors understand the architecture is designed with evolution in mind.

---

# 2. Upgrade: Sorted Slice → B-Tree

**Trigger:** Profiling shows that sorted slice insert/remove (O(n) shift) is a bottleneck.

**Expected trigger threshold:** Hundreds of active price levels per market simultaneously, sustained over time.

---

## Current (V1)

```
sortedPrices  []decimal.Decimal
```

Insert and remove include an O(n) slice shift.

---

## Upgrade Target

```
sortedPrices  *btree.BTreeG[decimal.Decimal]
```

Using `github.com/google/btree` with a custom comparator.

| Operation | V1 (slice) | Upgraded (B-Tree) |
|---|---|---|
| Best price | O(1) | O(log n) — Min()/Max() |
| Insert new level | O(n) shift | O(log n) |
| Remove empty level | O(n) shift | O(log n) |
| Depth snapshot | O(d) | O(d) — Ascend/Descend |

**Note:** Best-price lookup degrades from O(1) to O(log n) with a B-Tree. If O(1) best-price must be preserved, combine the B-Tree with a cached `bestPrice` field updated on insert/remove.

**Scope of change:** Only `Side.sortedPrices` and the functions that insert/remove from it. No changes to OrderNode, PriceLevel, OrderBook, matching algorithms, or events.

---

# 3. Upgrade: Price Levels → Skip List

**Trigger:** Profiling shows hash map contention or memory overhead at very high price level counts.

**Expected trigger threshold:** Tens of thousands of active price levels (unlikely in V1 workload).

---

## Upgrade Target

Replace the `priceLevels` hash map + `sortedPrices` slice with a single skip list that provides both sorted iteration and O(log n) lookup.

A skip list provides:

- O(log n) insert, remove, lookup (average)
- O(1) min/max
- Sorted iteration for depth snapshots

This consolidates two structures into one but introduces probabilistic behaviour.

---

# 4. Upgrade: Order Recovery → Snapshot + Replay

**Trigger:** Kafka replay time on restart grows too long as the topic accumulates messages.

---

## Current (V1)

Recovery reads the checkpoint offset from Postgres and replays Kafka events from that offset.

Replay time grows proportionally to the number of events since the last checkpoint.

---

## Upgrade Target

Periodic order book snapshots.

```
Every N minutes:
    Serialize current OrderBook state to a snapshot store
    (Postgres, S3, or Redis with persistence enabled)
    Record snapshot timestamp and corresponding Kafka offset

On restart:
    Load the latest snapshot
    Replay only events after the snapshot's Kafka offset
```

This caps replay time to at most N minutes of events regardless of topic age.

**Scope of change:**
- Add serialisation/deserialisation logic for `OrderBook`, `Side`, `PriceLevel`, `OrderNode`.
- Add snapshot storage and retrieval.
- Modify startup recovery sequence.
- No changes to matching algorithms, events, or external interfaces.

---

# 5. Upgrade: Single Process → Sharded by Market

**Trigger:** Single-process ME cannot keep up with total event volume across all markets.

---

## Current (V1)

One process, one goroutine per market.

All markets are handled by one Matching Engine node.

---

## Upgrade Target

Multiple Matching Engine nodes, each owning a subset of markets (Kafka partitions).

Kafka's consumer group protocol handles partition assignment automatically via Cooperative Sticky Rebalancing.

Each node's internal design is identical to V1. Only the deployment changes.

See `02_System_Architecture.md` Section 14 for Kafka design requirements.

---

# 6. Upgrade: GC Pressure → Object Pool

**Trigger:** Go garbage collector shows high pause times or allocation pressure from OrderNode / list.Element churn.

---

## Upgrade Target

Pre-allocate a pool of `OrderNode` objects using `sync.Pool`.

```go
var nodePool = sync.Pool{
    New: func() interface{} { return &OrderNode{} },
}
```

On insert: `node = nodePool.Get().(*OrderNode)` then populate fields.

On removal: zero the node fields, then `nodePool.Put(node)`.

**Scope:** Confined to the Insert and FullFill/Cancel operations. No other code changes.

---

# 7. Upgrade: Dynamic Market Creation

**Trigger:** Markets need to be added without restarting the Matching Engine.

---

## Current (V1)

The Market Engine Manager reads `trading_pairs` from the database on startup.

Adding a market requires an ME restart.

---

## Upgrade Target

Market Service publishes a `MarketEnabled` Kafka event.

Market Engine Manager consumes it and hot-starts a new Market Engine for the new market.

```
MarketEnabled event
        |
        v
Market Engine Manager
        |
        v
Create new goroutine + OrderBook for new market
        |
        v
Subscribe new Kafka partition
        |
        v
Resume matching for new market
```

No restart required.

---

# 8. Summary

| Upgrade | Trigger | Scope |
|---|---|---|
| Sorted Slice → B-Tree | O(n) shift is measurable bottleneck | Side.sortedPrices only |
| Price Levels → Skip List | Very high price level count | Side.priceLevels + sortedPrices |
| Recovery → Snapshot + Replay | Kafka replay too slow on restart | Startup + serialisation layer |
| Single Node → Sharded | Event volume exceeds single process | Deployment only |
| Allocation → Object Pool | GC pressure | Insert + Fill/Cancel operations |
| Static Markets → Dynamic | Markets added without restart | Market Engine Manager |

All upgrades are isolated to specific components. The matching algorithm, event contracts, and external service interfaces remain unchanged.

---

# 9. References

- 10_Design_Decisions.md — why V1 choices were made
- 02_System_Architecture.md — cluster deployment model
- 08_Recovery_Strategy.md — recovery sequencing
