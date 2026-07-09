# TradeDrift Matching Engine — Matching Algorithm

**Document:** 05_Matching_Algorithm.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** ✅ Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

This document describes the **business-level matching algorithm** — how the Matching Core turns one incoming order into zero or more trades and (optionally) a resting order.

`04_Data_Structures/07_Algorithms.md` already defines the low-level pseudocode for `Insert`, `Cancel`, `Match`, etc. This document sits one layer above that: it explains order-type dispatch, validation before matching, price determination, maker/taker/buyer/seller resolution, and the edge cases the data-structure pseudocode does not spell out.

---

# 2. Inputs to the Matching Core

The Matching Core only ever receives two event types, already validated and persisted by the Order Service:

| Event | Produces |
| --- | --- |
| `OrderCreated` | A new `OrderNode` candidate — LIMIT or MARKET, BUY or SELL |
| `OrderCancelRequested` | A cancel request for an existing resting `order_id` |

The Matching Core trusts these events completely. It does not re-validate user identity, balance, or trading-pair existence — that already happened upstream (see `07_Order_Service.md §Validation Rules`). See `06_Event_Contracts.md` for exact payload shape.

---

# 3. Pre-Match Checks

Before any matching logic runs, the Matching Core performs two cheap checks:

1. **Market configuration check.** Tick size and lot size for the market were loaded from Market Service at startup (`01_Overview.md §9`). If `price` is not a multiple of the tick size, or `quantity` is not a multiple of the lot size, the order is rejected — the ME publishes an `OrderCancelled {reason: "invalid_order_parameters"}` and logs the rejection. Without publishing this event the Order Service would hold the order in `OPEN` state indefinitely. In practice this check should never fire, since Order Service validates against the same configuration before publishing; it exists as a defensive safety net, not a primary validation path.
2. **Order type dispatch.** Route to the LIMIT flow or the MARKET flow (Sections 5 and 6).

---

# 4. Crossable Check

A match is only attempted while the book is crossable:

```
BUY incoming crosses the book when:   incoming.price >= asks.sortedPrices[0]
SELL incoming crosses the book when:  incoming.price <= bids.sortedPrices[0]
```

For a MARKET order there is no `price` to compare — it is crossable against *any* resting liquidity on the opposite side, by definition (Section 6).

If the opposite side is empty, or the best opposite price does not cross, the match loop (`04_Data_Structures/07_Algorithms.md §8`) exits immediately with zero results.

---

# 5. Limit Order Flow

```
OrderCreated (LIMIT) received
        │
        ▼
Pre-match checks (Section 3)
        │
        ▼
Run Match Loop against opposite side
        │
        ├── Fully filled ─────────────► emit TradeExecuted(s), done
        │
        ├── Partially filled ─────────► emit TradeExecuted(s),
        │                                Insert() remainder as resting order
        │
        └── No match at all ──────────► Insert() the full order as resting
```

A limit order can walk through **multiple price levels** in one incoming event if its price crosses more than one opposing level (a "sweep"). The match loop keeps consuming `ExecuteBest` results until either the incoming order is exhausted or the book stops crossing.

**Example — sweep across two levels:**

```
Incoming: BUY 3.0 @ 101.00

Ask side before:
  100.50 → [Order X: 1.0]
  100.80 → [Order Y: 1.5]
  101.20 → [Order Z: 2.0]

Step 1: best ask 100.50, crosses (101.00 >= 100.50). Fill 1.0 @ 100.50. Order X fully filled.
Step 2: best ask 100.80, crosses. Fill 1.5 @ 100.80. Order Y fully filled.
Step 3: best ask 101.20, does NOT cross (101.00 < 101.20). Loop stops.

Result: 2 trades (1.0 @ 100.50, 1.5 @ 100.80).
Incoming remaining_qty = 0.5 → inserted as a new resting BUY order at 101.00.
```

Each fill in a sweep is a **separate trade** with its own `trade_id` — trades are never merged, even against the same counter-order or the same price.

---

# 6. Market Order Flow (IOC)

```
OrderCreated (MARKET) received
        │
        ▼
Pre-match checks (Section 3, price check skipped — market orders carry no price)
        │
        ▼
Run Match Loop against opposite side, no price ceiling/floor
        │
        ├── Fully filled ─────────────► emit TradeExecuted(s), done
        │
        ├── Partially filled ─────────► emit TradeExecuted(s),
        │                                remainder discarded (IOC) — NOT inserted
        │
        └── No liquidity at all ──────► nothing to fill, order discarded entirely
```

Market orders **never call `Insert`**. This is enforced in `04_Data_Structures/07_Algorithms.md §8`: `if incoming.remainingQty > 0 and incoming.type == LIMIT: Insert(...)` — the condition is `LIMIT` only, so a market order's unfilled remainder simply falls out of scope and is garbage collected.

**No liquidity at all** (opposite side empty) is not an error — it is a valid IOC outcome. The Matching Core does not publish any event in this case; there is nothing to report, since the Order Service already knows the order was accepted and will never receive a fill confirmation for it. From the Order Service's perspective, an order that receives zero `TradeExecuted` events and no `OrderCancelled` simply stays `OPEN` — this is an accepted gap for V1, since market orders are expected to have liquidity in practice. Future versions may introduce an explicit "unfilled IOC" event if this proves confusing downstream (see `13_Future_Enhancements.md`).

**Slippage:** V1 market orders have no price protection — they will walk the book as deep as needed to fill, at whatever prices are resting. There is no maximum-slippage parameter in V1. This is a known simplification; see `13_Future_Enhancements.md`.

---

# 7. Cancel Flow

When the Matching Core receives an `OrderCancelRequested` event:

```
OrderCancelRequested received
        │
        ▼
Cancel(book, orderID) called
        │
        ├── Order found and removed from book
        │       │
        │       ▼
        │   Publish OrderCancelled
        │   { order_id, user_id, market_id,
        │     remaining_quantity: node.remainingQty,
        │     reason: "user_requested",
        │     cancelled_at: now() }
        │
        └── Order not found
                │
                ▼
            No-op — idempotent
            (order already fully filled, or ID unknown)
            Nothing published
```

The "not found" path is silent because it is safe: the Order Service sent the cancel request after the order was already in a terminal state. Emitting an `OrderCancelled` for an already-filled order would cause Settlement Service to receive a conflicting signal. Silence is the correct response.

Low-level data-structure operations for this flow are in `04_Data_Structures/07_Algorithms.md §3`.

---

# 8. Buyer / Seller / Maker / Taker Resolution

Every trade has exactly one maker (the resting order that was already in the book) and one taker (the incoming order that caused the match):

| Role | Definition |
| --- | --- |
| **Maker** | The resting order consumed by `ExecuteBest` — it was already in the book, providing liquidity |
| **Taker** | The incoming order from the current event — it is consuming liquidity |
| **Buyer** | Whichever of maker/taker has `side == BUY` |
| **Seller** | Whichever of maker/taker has `side == SELL` |

Since the incoming order and the resting order are always on opposite sides (that's what makes them crossable), exactly one of {maker, taker} is the buyer and the other is the seller — never ambiguous.

```
buyerOf(incoming, best):
    return incoming.orderID if incoming.side == BUY else best.orderID

sellerOf(incoming, best):
    return incoming.orderID if incoming.side == SELL else best.orderID
```

---

# 9. Trade Price Rule

**A trade always executes at the maker's price — never the taker's.**

```
trade.price = best.price   (best = the resting/maker order)
```

Rationale: the maker's price is the price that was publicly resting on the book and is what the crossable check compared against. If the taker is a marketable limit order priced better than the maker (e.g. BUY @ 101 crossing an ASK resting @ 100.50), the trade executes at 100.50 — the taker gets **price improvement**, never a worse price than what they specified. This is standard price-time-priority exchange behavior and matches what `04_Data_Structures/07_Algorithms.md §8` already encodes (`price: best.price` in `MatchResult`).

Market orders have no price of their own, so this rule is their *only* price source — they always take the maker's resting price.

---

# 10. Fill Quantity Rule

```
fillQty = min(incoming.remainingQty, best.remainingQty)
```

This is symmetric — whichever side has less remaining quantity is fully consumed by this fill, and the other side's remainder (if any) either continues matching (against the next price level, for the incoming order) or stays resting (for the maker, if the incoming used less than the maker had).

---

# 11. Self-Trade

**V1 does not implement Self-Trade Prevention (STP).** If the same `user_id` happens to be both the maker and the taker of a match (their own resting order matches their own incoming order), the trade executes normally — same as any other counterparty pair. This is a conscious V1 simplification, not an oversight; STP is scoped for a future version (`01_Overview.md §12`, `13_Future_Enhancements.md`).

---

# 12. Determinism

Given the same ordered sequence of `OrderCreated` / `OrderCancelRequested` events, the algorithm above always produces:
- the same trades, in the same order,
- the same final book state.

The `trade_id`s produced during a single live run are **stable** — they are published to Kafka, consumed by Settlement Service, and never change. Recovery replay generates new UUIDs for the same logical trades, but those are suppressed and never published (see `04_Data_Structures/10_Design_Decisions.md ADR-004`). The settled `trade_id`s are therefore not overwritten. "Determinism" here means: the same input sequence always produces the same logical outcome — not that re-running the algorithm twice would produce byte-identical UUIDs (UUIDv7 includes random bits).

There is no randomness in trade selection, no wall-clock branching, and no external I/O anywhere inside the match loop. This is what makes recovery-by-replay (`08_Recovery_Strategy.md`) correct.

---

# 13. What This Algorithm Does NOT Do

- Does not check user balance — already reserved by Wallet Service before the event reached Kafka.
- Does not calculate fees — V1 is zero-fee (`07_Order_Service.md §V1 Trading Policy`).
- Does not publish events directly — it returns `[]MatchResult` values; the Publisher Layer (`02_System_Architecture.md §13`) turns those into `TradeExecuted` events.
- Does not persist anything — the Order Book is pure in-memory state.

---

# 14. References

- `04_Data_Structures/07_Algorithms.md` — low-level Insert/Cancel/Match/PartialFill/FullFill pseudocode
- `04_Data_Structures/10_Design_Decisions.md` — ADR-004 recovery replay rationale
- `03_Order_Book.md` — Price-Time Priority rules and invariants
- `06_Event_Contracts.md` — exact `OrderCreated` / `TradeExecuted` payload shapes
- `07_Concurrency_Model.md` — how the match loop is invoked from the Event Loop
- `13_Future_Enhancements.md` — STP, slippage protection, and other deferred behavior
