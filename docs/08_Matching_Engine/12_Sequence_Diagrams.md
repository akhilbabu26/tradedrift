# TradeDrift Matching Engine — Sequence Diagrams

**Document:** 12_Sequence_Diagrams.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** ✅ Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

Concrete, end-to-end sequences tying together the individual mechanisms documented across `01`–`11`. These are illustrative walkthroughs, not new design decisions — every step below is already specified elsewhere and cross-referenced.

---

# 2. Limit Order — No Match, Rests on Book

```
Client        Order Service      Kafka        Matching Engine       Redis
  │                 │              │                 │                │
  │  POST /orders   │              │                 │                │
  ├────────────────►│              │                 │                │
  │                 │ generate order_id (UUIDv7)      │                │
  │                 │ ReserveFunds (gRPC → Wallet)     │                │
  │                 │ Save Order + Outbox (status OPEN)│                │
  │◄────────────────┤ 200 Accepted │                 │                │
  │                 │ Outbox Publisher polls           │                │
  │                 ├─────────────►│ OrderCreated    │                │
  │                 │              ├────────────────►│                │
  │                 │              │                 │ Pre-match checks (05 §3)
  │                 │              │                 │ Crossable? No (05 §4)
  │                 │              │                 │ Insert() (04/07 §2)
  │                 │              │                 │ GetDepth() ───►│
  │                 │              │                 │ (no TradeExecuted —
  │                 │              │                 │  nothing to publish)
```

References: `07_Order_Service.md Create Order Flow`, `05_Matching_Algorithm.md §5`, `09_Redis_Projection.md §4`.

---

# 3. Market Order — Immediate Full Fill (IOC)

```
Order Service     Kafka          Matching Engine                Kafka          Settlement Service
      │              │                  │                          │                    │
      │ OrderCreated │                  │                          │                    │
      ├─────────────►│                  │                          │                    │
      │              ├─────────────────►│                          │                    │
      │              │                  │ Match Loop (05 §6):      │                    │
      │              │                  │  ExecuteBest() → maker   │                    │
      │              │                  │  fillQty = min(...)      │                    │
      │              │                  │  trade_id = UUIDv7()     │                    │
      │              │                  │  price = maker.price     │                    │
      │              │                  │  FullFill(maker)         │                    │
      │              │                  │  incoming.remainingQty=0 │                    │
      │              │                  │                          │                    │
      │              │                  ├─────────────────────────►│ TradeExecuted      │
      │              │                  │                          ├───────────────────►│
      │              │                  │                          │      Wallet.SettleTrade(
      │              │                  │                          │        trade_id, buyer_id,
      │              │                  │                          │        seller_id, ...)
      │◄─────────────┼──────────────────┼──────────────────────────┤ TradeExecuted      │
      │  (Order Service consumes its own copy of TradeExecuted,     │                    │
      │   transitions order to FILLED — 07_Order_Service.md)         │                    │
```

References: `05_Matching_Algorithm.md §6, §8`, `06_Event_Contracts.md §3`, `06_Wallet_Service.md SettleTrade`.

---

# 4. Limit Order Sweep — Partial Fill Across Multiple Levels

Scenario from `05_Matching_Algorithm.md §5`: incoming `BUY 3.0 @ 101.00` against asks `[100.50: 1.0], [100.80: 1.5], [101.20: 2.0]`.

```
Matching Core                          Event Loop                    Output Queue                 Publisher
     │                                     │                              │                           │
     │ Step 1: best=100.50 (X, qty 1.0)    │                              │                           │
     │  crosses → Fill{X, 1.0 @ 100.50}    │                              │                           │
     │  FullFill(X)                        │                              │                           │
     │                                     │                              │                           │
     │ Step 2: best=100.80 (Y, qty 1.5)    │                              │                           │
     │  crosses → Fill{Y, 1.5 @ 100.80}    │                              │                           │
     │  FullFill(Y)                        │                              │                           │
     │                                     │                              │                           │
     │ Step 3: best=101.20, does NOT cross │                              │                           │
     │  loop stops. incoming.remaining=0.5 │                              │                           │
     │  Insert(incoming)                   │                              │                           │
     │                                     │                              │                           │
     │ returns fills [Fill #1, Fill #2] ──►│                              │                           │
     │                                     │ GetDepth() → snapshot        │                           │
     │                                     │                              │                           │
     │                                     │ send MatchResult ------------►│                           │
     │                                     │                              │                           │
     │                                     │                              ├──────────────────────────►│ drains MatchResult:
     │                                     │                              │                           │  1. publish TradeExecuted #1 & #2
     │                                     │                              │                           │  2. push single depth snapshot
     │                                     │                              │                           │  3. write checkpoint
```

Note per `09_Redis_Projection.md §5`: even though two trades were produced, only **one** Redis depth push happens — after the whole event (the incoming order) finishes processing, not after each individual fill.

References: `05_Matching_Algorithm.md §5`, `07_Concurrency_Model.md §6`, `09_Redis_Projection.md §5`.

---

# 5. Cancel — No Race, Order Still Resting

```
Client      Order Service        Kafka          Matching Engine
  │              │                  │                  │
  │ DELETE /orders/{id}              │                  │
  ├─────────────►│                  │                  │
  │              │ status → CANCELLING                  │
  │              │ Outbox: OrderCancelRequested          │
  │◄─────────────┤ 202 Accepted     │                  │
  │              ├─────────────────►│ OrderCancelRequested
  │              │                  ├─────────────────►│
  │              │                  │                  │ orderIndex[orderID] lookup — found
  │              │                  │                  │ Cancel() (04/07 §3): O(1)
  │              │                  │                  │ remaining_quantity captured
  │              │                  ├───────────────────┤ OrderCancelled
  │◄─────────────┼──────────────────┼───────────────────┤
  │              │ status → CANCELLED                    │
  │              │ Wallet.ReleaseFunds(order_id)          │
```

References: `06_Event_Contracts.md §4`, `04_Data_Structures/07_Algorithms.md §3`, `07_Order_Service.md Cancel Order Flow`.

---

# 6. Cancel-vs-Fill Race — Fill Wins

Scenario from `07_Order_Service.md §Cancel vs Fill Race Condition`: order gets matched before its cancel request is processed.

```
Order Service                    Kafka (same partition key: market_id)      Matching Engine
     │                                        │                                    │
     │ OrderCreated (order A)                 │                                    │
     ├───────────────────────────────────────►│                                    │
     │                                        ├───────────────────────────────────►│ Insert(A) — rests
     │                                        │                                    │
     │  (user requests cancel of A)           │                                    │
     │ OrderCancelRequested (order A)          │                                    │
     ├───────────────────────────────────────►│                                    │
     │                                        │   ...meanwhile, incoming order B    │
     │                                        │   arrives and crosses against A     │
     │                                        │◄──────────────────────── OrderCreated (B)
     │                                        ├───────────────────────────────────►│ Match(B) fully
     │                                        │                                    │ consumes A
     │                                        │                                    │ FullFill(A)
     │                                        │                                    │ delete(orderIndex, A)
     │                                        ├───────────────────────────────────►│ sends MatchResult{fills: [A+B]}
     │                                        │                                    │ to Output Queue -> Publisher
     │                                        │                                    │
     │                                        ├───────────────────────────────────►│ NOW process
     │                                        │                                    │ OrderCancelRequested(A)
     │                                        │                                    │ orderIndex[A] = nil
     │                                        │                                    │ Cancel() no-op — returns nil
     │                                        │                                    │ (no OrderCancelled published)
     │◄───────────────────────────────────────┼────────────────────────────────────┤ TradeExecuted only
     │  Order Service: order A already FILLED via TradeExecuted; no OrderCancelled  │
     │  ever arrives, and none is expected — rule 1 in 07_Order_Service.md          │
```

**Key point:** because `OrderCreated` and `OrderCancelRequested` share a Kafka partition key, the Event Loop processes them strictly in the order shown — the "race" is fully resolved by partition ordering before it ever reaches matching logic (`07_Concurrency_Model.md §8`).

References: `07_Order_Service.md §Cancel vs Fill Race Condition`, `06_Event_Contracts.md §5`, `07_Concurrency_Model.md §8`.

---

# 7. Node Restart — Recovery Sequence

```
Matching Engine Node          Postgres            Kafka                  OrderBook (in-memory)
       │                          │                  │                          │
       │ starts, loads config     │                  │                          │
       │ joins Consumer Group ────┼─────────────────►│                          │
       │◄─────────────────────────┼── assigned partitions (markets)             │
       │                          │                  │                          │
       │ for each market:         │                  │                          │
       │  read checkpoint row ───►│                  │                          │
       │◄─────────────────────────┤ {topic, partition, offset}                  │
       │  create empty OrderBook  │                  │                          ├── empty
       │  enter RECOVERY mode (Publisher suppressed)  │                          │
       │  seek partition to offset────────────────────►│                          │
       │  replay OrderCreated/OrderCancelRequested ───►│                          │
       │                          │                  ├─────────────────────────►│ Insert/Cancel
       │                          │                  │   (repeated per event)   │   replayed
       │  reached latest offset — exit RECOVERY       │                          │
       │  resume live processing (Publisher active)   │                          ├── matches
       │  report /readyz = true  │                  │                          │   live state
```

References: `08_Recovery_Strategy.md §4`, `02_System_Architecture.md §15`, `11_Monitoring.md §3`.

---

# 8. Panic — Market Halt and Recovery

Triggered when `processWithRecovery` catches a panic via `defer/recover`. See `10_Failure_Handling.md §6` for the full policy.

```
Event Loop (BTC-USDT)       Matching Core          Publisher Layer      Monitoring
       │                          │                      │                 │
       │ processWithRecovery()    │                      │                 │
       ├─────────────────────────┘                      │                 │
       │    panic (nil ptr, logic bug, etc.)           │                 │
       │    defer/recover() catches it                 │                 │
       │                          │                      │                 │
       │ log.Error(market, event, panic)               │                 │
       │ deadLetter(event, reason)                     │                 │
       │ m.haltMarket() ──────────────────────────► Publisher stops   │
       │ ok = false                                    │ for this market │
       │    processWithRecovery returns false           │                 │
       │                          │                      │                 │
       │ Run() sees !ok → return   │                      │                 │
       │ Event Loop goroutine exits│                      │                 │
       │ me_market_state = HALTED  │                      │───────────────┘ P1 alerts fire:
       │                          │                      │   me_panics_recovered_total > 0
       │                          │                      │   me_market_state == HALTED
       │                          │                      │
       │ operator / supervisor triggers restart
       │
       ├──── standard recovery sequence (Section 7) ────────────────────────────────────────────
       │ book rebuilt from Kafka from last checkpoint offset
       │ STARTING → RECOVERY → RUNNING
       │ /readyz = true
```

**Key points:**
- Scope: **only the panicked market halts** — other markets (ETH-USDT, SOL-USDT) continue unaffected (`07_Concurrency_Model.md §7`).
- Book state: **recovery-by-replay guarantees a clean, consistent book** — continuation would risk silently propagating a mid-mutation corrupted state.
- Alert action: P1 requires human investigation of the dead-lettered event and the panic stack trace. This is always a software defect, not a transient fault.

References: `10_Failure_Handling.md §6`, `08_Recovery_Strategy.md §4`, `11_Monitoring.md §5`.

---

# 9. References

- `05_Matching_Algorithm.md` — the business logic each diagram walks through
- `06_Event_Contracts.md` — exact payload shapes referenced in each sequence
- `07_Concurrency_Model.md` — why cancel-vs-fill needs no additional locking
- `08_Recovery_Strategy.md` — full detail behind Section 7's compressed sequence
- `09_Redis_Projection.md` — depth-push timing referenced in Section 4
- `10_Failure_Handling.md` — panic halt and dead-letter policy behind Section 8
