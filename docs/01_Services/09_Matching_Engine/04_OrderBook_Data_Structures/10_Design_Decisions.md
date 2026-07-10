# TradeDrift Matching Engine — Design Decisions

**Document:** 04_Data_Structures / 10_Design_Decisions.md
**Service:** Matching Engine
**Version:** V1.0
**Last Updated:** July 2026

---

# 1. Purpose

Architecture Decision Records (ADRs) for the Order Book data structure choices. Captures what was decided, why, and what was rejected.

---

# 2. ADR-001 — Sorted Slice for the Price Index

**Decision:** Use `[]decimal.Decimal` (sorted slice) as the ordered price index inside `Side`.
**Status:** Accepted

### Alternatives Considered

| Alternative | Best Price | Insert | Remove | Reason Rejected |
| --- | --- | --- | --- | --- |
| AVL / Red-Black Tree | O(log n) | O(log n) | O(log n) | Worse best-price; complex; no stdlib |
| B-Tree (`google/btree`) | O(log n) | O(log n) | O(log n) | Still O(log n) best-price; adds dependency |
| Skip List | O(log n) avg | O(log n) avg | O(log n) avg | Probabilistic; no stdlib |
| Min/Max Heap | O(1) | O(log n) | O(n) lazy | Cannot efficiently remove arbitrary levels |
| Redis Sorted Set | O(1) | O(log n) | O(log n) | Network round-trip per match — unacceptable |

### Decision Rationale

| Property | Sorted Slice |
| --- | --- |
| Best price lookup | O(1) — `sortedPrices[0]` |
| Insert new level | O(log n) search + O(n) shift |
| Remove empty level | O(log n) search + O(n) shift |
| Depth snapshot | O(d) — iterate first d elements |
| Cache locality | Excellent — contiguous memory |
| Go stdlib dependency | None |
| Implementation complexity | Low |

The O(n) shift is acceptable because active price levels are small (tens to low hundreds) in V1, and the sorted slice can be swapped for a B-Tree in one place without changing any other logic.

---

# 3. ADR(Architecture Decision Record)-002 — Doubly-Linked List for Order Queues

**Decision:** Use Go stdlib `container/list` as the FIFO queue at each price level.
**Status:** Accepted

| Alternative | Append | Front | Arbitrary Remove | Decision |
| --- | --- | --- | --- | --- |
| Slice / array | O(1) amortised | O(1) | O(n) | Rejected — cancel is O(n) |
| Ring buffer | O(1) | O(1) | O(n) | Rejected — same reason |
| Doubly-linked list | O(1) | O(1) | O(1) with pointer | **Chosen** |

O(1) arbitrary removal is achieved by storing `node.element` (`*list.Element`) on each `OrderNode`. `list.Remove(node.element)` removes the node without scanning the list.

---

# 4. ADR-003 — orderIndex on OrderBook, not Side

**Decision:** Place `orderIndex map[uuid.UUID]*OrderNode` on `OrderBook`, shared across both sides.
**Status:** Accepted

| Option | Cancel lookup | Trade-off |
| --- | --- | --- |
| `orderIndex` on each `Side` | Two hash lookups (check bids, then asks) | Split ownership; redundant checks |
| `orderIndex` on `OrderBook` | Single hash lookup | Cleaner ownership; one index to maintain |

After the book-level lookup, `node.side` tells the algorithm which side's `priceLevels` to use. No information is lost.

---

# 5. ADR-004 — Recovery via Input Event Replay

**Decision:** On restart, replay only `OrderCreated` and `OrderCancelRequested` through the full matching algorithm. Do not replay `TradeExecuted`.
**Status:** Accepted

| Approach | Events replayed | Recovery code path |
| --- | --- | --- |
| Replay input events through algorithm | `OrderCreated`, `OrderCancelRequested` | Same as live processing (suppressed output) |
| Replay output events manually | `TradeExecuted` + inputs | Separate fill-replay logic; different code path |

The matching algorithm is **deterministic**. The same ordered input sequence always produces the same fills and the same final book state. Replaying via the algorithm uses the same code path as live processing — no separate recovery logic to write or maintain. New `trade_id` UUIDs are generated during replay but are suppressed and never published, since those trades are already settled.

---

# 6. ADR-005 — decimal.Decimal for All Financial Values

**Decision:** Use `github.com/shopspring/decimal` for all prices and quantities. Reject `float64`.
**Status:** Accepted

```
float64:  0.1 + 0.2  =  0.30000000000000004   ← incorrect
decimal:  0.1 + 0.2  =  0.3                   ← correct
```

Float arithmetic loses precision. In a financial system, imprecise arithmetic causes incorrect trade prices, incorrect fill quantities, and potential fund loss.

---

# 7. References

- `05_Side.md` — sortedPrices and priceLevels
- `06_Order_Index.md` — orderIndex placement
- `09_Complexity_Analysis.md` — full complexity table
- `11_Future_Evolution.md` — upgrade path to B-Tree
