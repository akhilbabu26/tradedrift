# Deterministic UUIDv5 Trade ID & Downstream Idempotency Architecture

**Service:** Matching Engine (`services/matching-engine`)  
**Documentation:** `DETERMINISTIC_TRADE_ID.md`  
**Topic:** Deterministic UUIDv5 Hashing, At-Least-Once Kafka Republishing, and Multi-Service Settlement Idempotency  
**Package References:** 
* [`internal/matcher/matcher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L162-L164)
* [`internal/orderbook/result.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/orderbook/result.go#L12-L26)  
**Last Updated:** August 2026  

---

## 1. Executive Summary & Purpose

In a distributed exchange, an edge crash can occur **after** a trade fill is emitted to Kafka (`trades.executed`), but **before** the PostgreSQL checkpoint offset is committed.

When the Matching Engine reboots, it replays the uncommitted Kafka offset and **re-executes the trade**.

If the Matching Engine generated random UUIDs (UUIDv4) for trades:
* Replaying the trade would produce a **brand-new random Trade ID**.
* Downstream **Settlement and Wallet Services** would think it is a new trade and **transfer balances a second time** (Double-Spend / Double-Credit).

The **Deterministic Trade ID** mechanism ([`internal/matcher/matcher.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L162-L164)) solves this by generating a mathematically deterministic **RFC 4122 UUIDv5** derived directly from the immutable matching inputs.

```
       [ Matching Inputs ] ──► [ UUIDv5 SHA-1 Hashing ] ──► DETERMINISTIC TRADE ID
       • EventID                                             (100% Identical on Replay)
       • MakerOrderID                                                    │
       • TakerOrderID                                                    ▼
       • FillIndex                                          [ Downstream DB Idempotency ]
                                                            ON CONFLICT (trade_id) DO NOTHING
```

---

## 2. Problems Solved, How Solved & Implementing Functions Matrix

| Problem Solved | Danger / Failure Scenario | How It Is Solved | Implementing Function(s) & Code Location |
| :--- | :--- | :--- | :--- |
| **1. Double-Settlement on Crash Replay** | Crash occurs after trade published to Kafka but before checkpoint commits. On reboot, re-executed trade generates a new random ID and double-credits balances in Wallet Service. | Generates deterministic UUIDv5 from matching inputs. Replaying the trade produces the exact same `TradeID`, enabling downstream DB deduplication (`ON CONFLICT DO NOTHING`). | [`TradeID`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L162-L164), [`Match`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L189-L197) |
| **2. Multi-Level Book Sweep ID Collisions** | A large taker market order matches against 5 different maker orders in the same transaction. If IDs only hashed event ID, all 5 fills would get the same ID. | Incorporates `fillIndex` ($0, 1, 2 \dots$) and `makerOrderID` into the hash, guaranteeing every individual fill gets a distinct, reproducible UUID. | [`TradeID(eventID, makerID, takerID, fillIndex)`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L162-L164) |
| **3. Non-Deterministic Random Number Generators** | Relying on `rand` or system clocks causes trades to differ depending on reboot execution timing. | Uses pure cryptographic SHA-1 namespace hashing (`uuid.NewSHA1`) with zero reliance on system clocks or RNG states. | [`uuid.NewSHA1`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher.go#L163) |

---

## 3. The UUIDv5 Hash Generation Algorithm

The deterministic `TradeID` is defined as:

$$\text{TradeID} = \text{UUIDv5}(\text{NamespaceDNS}, \text{EventID} : \text{MakerOrderID} : \text{TakerOrderID} : \text{FillIndex})$$

```go
// internal/matcher/matcher.go
func TradeID(eventID uuid.UUID, makerID uuid.UUID, takerID uuid.UUID, fillIndex int) uuid.UUID {
    return uuid.NewSHA1(
        uuid.NameSpaceDNS, 
        []byte(fmt.Sprintf("%s:%s:%s:%d", eventID, makerID, takerID, fillIndex)),
    )
}
```

### Why These 4 Parameters Are Used:
1. **`eventID`**: The immutable UUID of the incoming taker command from Kafka.
2. **`makerID`**: The UUID of the resting order matched on the opposite side of the book.
3. **`takerID`**: The UUID of the incoming aggressive order.
4. **`fillIndex`**: The 0-indexed execution counter within the current sweep (e.g. 0 for first fill, 1 for second fill).

---

## 4. The Crash Window Lifecycle & Downstream Deduplication

```
 ┌─────────────────────────────────────────────────────────────────────────────────────────────┐
 │ 1. LIVE OPERATION: MATCH OCCURS                                                             │
 │    • Matcher executes Order 100 ──► Generates TradeID: "a3b8c9d0-..."                       │
 │    • Emits fill to Kafka topic `trades.executed`                                            │
 │    • Downstream Settlement Service consumes fill:                                           │
 │        INSERT INTO settlements (trade_id, buyer, seller, amount) VALUES ("a3b8c9d0-...", ...)│
 └──────────────────────────────────────┬──────────────────────────────────────────────────────┘
                                        │
                                        ▼ (💥 SERVER CRASHES BEFORE POSTGRES CHECKPOINT!)
 ┌─────────────────────────────────────────────────────────────────────────────────────────────┐
 │ 2. CRASH REBOOT & DELTA REPLAY                                                              │
 │    • Matching Engine restarts and replays Order 100 from Kafka                              │
 │    • Matcher re-executes Order 100 against the same maker order                             │
 │    • Calls TradeID(eventID, makerID, takerID, 0)                                            │
 │    • Result: Generates the EXACT SAME TradeID: "a3b8c9d0-..."                               │
 └──────────────────────────────────────┬──────────────────────────────────────────────────────┘
                                        │
                                        ▼ (Publishes to Kafka during Live Recovery)
 ┌─────────────────────────────────────────────────────────────────────────────────────────────┐
 │ 3. DOWNSTREAM IDEMPOTENT SETTLEMENT                                                         │
 │    • Settlement Service receives fill with TradeID: "a3b8c9d0-..."                          │
 │    • Executes SQL:                                                                          │
 │        INSERT INTO settlements (trade_id, ...) VALUES ("a3b8c9d0-...", ...)                 │
 │        ON CONFLICT (trade_id) DO NOTHING;                                                   │
 │    • Result: Duplicate fill is silently ignored! Zero double-spending! ✅                    │
 └─────────────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 5. Test Verification in Codebase

The deterministic nature of `TradeID` is tested and enforced by two dedicated unit tests:

1. **[`TestMatch_DeterministicTradeIDs`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/matcher/matcher_test.go#L563-L615)**:  
   Runs 3 separate match passes with the same input parameters and asserts:
   ```go
   if id1 != id2 || id2 != id3 {
       t.Fatalf("trade IDs are not deterministic: %s vs %s vs %s", id1, id2, id3)
   }
   ```
2. **[`TestRecovery_CrashAfterTradePublish`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/recovery/replayer_test.go#L400-L450)**:  
   Simulates a crash immediately after trade emission and verifies:
   ```go
   if originalTradeID != replayTradeID {
       t.Errorf("expected trade ID on replay to match original: %s vs %s", originalTradeID, replayTradeID)
   }
   ```

---

## 6. Summary

* **Pure Function:** Deterministic UUIDv5 generation is a pure mathematical calculation with zero side effects or RNG state.
* **Double-Spend Immunity:** Guarantees that at-least-once Kafka re-executions never generate new trade IDs.
* **Downstream Alignment:** Provides a shared, immutable primary key for Settlement, Wallet, and Accounting services across TradeDrift.
