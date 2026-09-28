# Controlled Taker Service — Kafka & Redis Integration

> **Status:** 📐 Designed (V1.1 — Updated with Architectural Feedback)  
> **Service:** Controlled Taker Service (`services/controlled-taker`)  
> **Document:** `06_Kafka_And_Redis.md`  
> **Last Updated:** September 2026  

---

## 1. Kafka Architecture & Topic Map

CTS adheres to the principle of **zero unnecessary infrastructure**:
- **No new Kafka topics are created.**
- Existing topics and partition assignments are fully reused.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                             Kafka Topic Map                                 │
│                                                                             │
│  Topic: orders.commands (Partitions: BTC=0, ETH=1, SOL=2)                   │
│  - Producer: Order Service Outbox Publisher (on behalf of CTS)              │
│  - Consumer: Matching Engine                                                │
│  - Partition Key: MarketID ("BTC-USDT", "ETH-USDT", "SOL-USDT")             │
│                                                                             │
│  Topic: trades.executed (Partitions: BTC=0, ETH=1, SOL=2)                   │
│  - Producer: Matching Engine Publisher                                      │
│  - Consumers: Market Service, Settlement Service, Liquidity Engine          │
│  - Partition Key: MarketID                                                  │
│                                                                             │
│  Topic: trades.settled.v1 (Partition Key: buyer_user_id)                    │
│  - Producer: Wallet Outbox Publisher                                        │
│  - Consumers: Trade Service, Notification Service, Portfolio Service        │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Partition Keying Invariant
In `services/matching-engine/internal/kafka/command.go:51`:
```go
if len(msg.Key) == 0 {
    return false, fmt.Errorf("missing partition key: all orders.commands messages must carry key=market_id")
}
```
All order commands must carry `Key = MarketID`. This ensures that all events for a given market land on the same Kafka partition and are processed strictly in FIFO sequence by the Matching Engine event loop.

---

## 2. Redis Integration & Cumulative Depth Inspection

CTS interacts with Redis solely on the **read path** for high-throughput, non-blocking depth inspection:

```
CTS ──[GET depth:BTC-USDT]──> Redis (In-Memory Key/Value)
     <──[DepthSnapshot JSON]──
```

### Redis Key Contract:
- **Key Pattern:** `depth:{market_id}` (e.g. `depth:BTC-USDT`, `depth:ETH-USDT`, `depth:SOL-USDT`)
- **Data Format:** Top-N L2 depth levels serialized by Matching Engine:
  ```json
  {
    "market_id": "BTC-USDT",
    "sequence": 10425,
    "bids": [
      {"price": "96490.00", "quantity": "0.2500"},
      {"price": "96480.00", "quantity": "0.5000"}
    ],
    "asks": [
      {"price": "96510.00", "quantity": "0.3000"},
      {"price": "96520.00", "quantity": "0.6000"}
    ],
    "snapshot_at": "2026-09-27T18:00:00.123Z"
  }
  ```
- **Cumulative Depth Evaluation:**
  CTS parses the full slice of `bids` and `asks`. This provides the complete multi-level book structure (up to 12 levels maintained by the Liquidity Engine), allowing HIGH profile sweeps to calculate cumulative available volume across levels 1, 2, 3... in a single $< 1\text{ms}$ in-memory operation.

### Staleness Protection:
- CTS parses `snapshot_at`.
- If `time.Since(snapshot_at) > 5 * time.Second`, the depth projection is considered stale (e.g., ME crash or network delay).
- CTS logs a warning, skips the order cycle, and backs off.
