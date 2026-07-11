# TradeDrift — Kafka Topic Design

> **Status:** ✅ Designed (V1.1)
> **Document:** 15_Kafka_Topic_Design.md
> **Service:** Platform Architecture
> **Version:** V1.1
> **Last Updated:** July 2026
> Revision notes: V1.1 adds tracing capability and durability guidelines: (1) includes correlation_id and causation_id in envelope; (2) introduces versioned topic naming conventions (e.g. trades.executed.v1); (3) documents partition sizing rationale and deployment flexibility; (4) specifies idempotent producer settings; (5) corrects JSON Schema casing syntax (object, string, integer).

---

## 1. Topic Configuration Standards

To ensure high availability, durability, and horizontal scale across our cluster, all Kafka topics in the TradeDrift platform conform to standard infrastructure properties:

```
[ Producer ] ──(acks=all)──► [ Partition Leader ]
                                 │      │
                          (Replicate) (Replicate)
                                 ▼      ▼
                           [ Follower ] [ Follower ]
```

### Global Infrastructure Settings:
* **Replication Factor:** `3` (Required to tolerate the loss of a broker node without data loss).
* **Minimum In-Sync Replicas (`min.insync.replicas`):** `2` (Guarantees at least one follower replica is in-sync before a write is acknowledged).
* **Producer Acknowledgement (`acks`):** `all` (The partition leader must wait for all in-sync replicas to acknowledge the write).
* **Retention Policy (`cleanup.policy`):** `delete` (Standard log segment deletion).
* **Retention Period (`retention.ms`):** `604800000` (7 days retention, providing ample time for downstream consumer recovery).
* **Message Compression:** `producer` / `snappy` (Provides high compression ratio with low CPU overhead).

### 1.1 Idempotent Producer Configuration
To prevent message duplication at the broker level during transient network drops or broker failovers, all TradeDrift producers must be configured with Kafka's **Idempotency** guarantees:
* `enable.idempotence = true` — Instructs the broker to assign a unique Producer ID (PID) and sequence numbers to message batches, ensuring duplicates from producer retries are discarded at the partition level.
* `max.in.flight.requests.per.connection = 5` — Maintains high pipelining throughput while guaranteeing strict message ordering when idempotence is enabled.
* `retries = 2147483647` — Configures infinite producer-level retries (relying instead on transaction/delivery timeouts to abort), guaranteeing the event is never dropped due to temporary broker unreachability.

---

## 2. Topic Catalog & Partitioning Map

Topics are partitioned based on their structural serialization key to distribute write throughput. 

* **Partition Count Selection (Illustrative Default):** Topics default to **12 partitions** in this specification. This is an illustrative configuration suitable for local testing, staging, and baseline Kubernetes deployments (allowing up to 12 replica worker pods in a consumer group to pull from partitions concurrently). 
* **Deployment-Specific Scaling:** In production, this partition count is deployment-specific and can be scaled upwards (e.g. to 24, 48, or more partitions) to match high-volume market activity or user growth without modifying application code.

| Versioned Topic Name | Partitions | Partition Key | Purpose | Message Key |
|---|---|---|---|---|
| `orders.created.v1` | `12` | `market_id` | limit order funding committed | `market_id` |
| `orders.cancel-requested.v1` | `12` | `market_id` | user cancel queue request | `market_id` |
| `orders.cancelled.v1` | `12` | `market_id` | engine order cancellation execution | `market_id` |
| `trades.executed.v1` | `12` | `market_id` | buy/sell order matched in-memory | `market_id` |
| `user-trades.settled.v1` | `12` | `user_id` | wallet balance settlement complete | `user_id` |
| `portfolios.updated.v1` | `12` | `user_id` | holdings and average cost recalculation | `user_id` |
| `admin.user-suspended.v1` | `12` | `user_id` | user account suspended notification | `user_id` |
| `admin.market-halted.v1` | `12` | `market_id` | market trading emergency halt status | `market_id` |
| `admin.market-commands.v1` | `12` | `market_id` | matching engine administrative triggers | `market_id` |
| `wallet.deposit_completed.v1` | `12` | `user_id` | user cash/crypto deposit completed | `user_id` |
| `wallet.withdrawal-initiated.v1` | `12` | `user_id` | outbound transfer check initiated | `user_id` |
| `wallet.withdrawal-completed.v1` | `12` | `user_id` | transfer successfully sent off-chain | `user_id` |

---

## 3. Global Message Envelope Schema

All Kafka events must wrap their service-specific business details in a standardized envelope. This envelope handles trace correlation, message deduplication, and schema version routing.

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "event_id": {
      "type": "string",
      "format": "uuid",
      "description": "Unique UUIDv7 event identifier (used for consumer processed_events deduplication)"
    },
    "correlation_id": {
      "type": "string",
      "format": "uuid",
      "description": "Lifecycle identifier tracing the original user-triggered request (e.g. order_id)"
    },
    "causation_id": {
      "type": "string",
      "format": "uuid",
      "description": "ID of the immediate parent event that triggered this event (enables lineage graphs)"
    },
    "event_type": {
      "type": "string",
      "description": "Uppercase name of the event type (matches topic definition)"
    },
    "event_version": {
      "type": "integer",
      "minimum": 1,
      "description": "Schema version of the payload (starts at 1)"
    },
    "timestamp": {
      "type": "string",
      "format": "date-time",
      "description": "RFC3339 formatted generation timestamp"
    },
    "payload": {
      "type": "object",
      "description": "Service-specific payload carrying business state updates"
    }
  },
  "required": ["event_id", "correlation_id", "causation_id", "event_type", "event_version", "timestamp", "payload"]
}
```

---

## 4. Topic Payload Specifications

To prevent floating-point rounding errors and precision loss across various programming languages (e.g. Go, Java, Python), **all numeric values representing quantities, prices, or balances must be serialized as strings** containing exact decimal representations (matching PostgreSQL `DECIMAL(30,10)` constraints).

### 4.1 `OrderCreated` (Order Service)
Published when an order has passed validation, locked its funds, and is ready for matching engine ingestion.

```json
{
  "order_id": "018f60f3-b780-7798-8422-dfa6b29f44ea",     // UUIDv7 Order ID
  "user_id": "018f60f3-a120-7798-8422-cfb6a29e11aa",      // User ID
  "market_id": "BTC-USDT",                                // Trade pair symbol
  "price": "58200.0000000000",                            // Limit price (String)
  "quantity": "0.1000000000",                             // Total order quantity (String)
  "side": "BUY",                                          // "BUY" | "SELL"
  "type": "LIMIT",                                        // "LIMIT" | "MARKET"
  "created_at": "2026-07-10T18:07:40Z"                    // Order placement time
}
```

---

### 4.2 `OrderCancelRequested` (Order Service)
Published when a user requests to cancel a resting order.

```json
{
  "order_id": "018f60f3-b780-7798-8422-dfa6b29f44ea",     // Target Order ID to cancel
  "market_id": "BTC-USDT",                                // Market pair
  "requested_at": "2026-07-10T18:12:00Z"                  // Timestamp of request
}
```

---

### 4.3 `OrderCancelled` (Matching Engine)
Published when the Matching Engine removes a resting order from the active book.

```json
{
  "order_id": "018f60f3-b780-7798-8422-dfa6b29f44ea",     // Target Order ID
  "market_id": "BTC-USDT",                                // Market pair
  "cancelled_quantity": "0.0600000000",                   // Unfilled quantity cancelled (String)
  "reason": "USER_REQUESTED",                             // "USER_REQUESTED" | "IMMEDIATE_OR_CANCEL" | "SELF_TRADE"
  "cancelled_at": "2026-07-10T18:12:02Z"                  // Time order was removed from book
}
```

---

### 4.4 `TradeExecuted` (Matching Engine)
Published when the Matching Engine successfully executes a buy/sell priority match on the in-memory book.

```json
{
  "trade_id": "018f60f3-c540-7798-8422-efa6b29f1234",      // Unique Trade ID (UUIDv7)
  "market_id": "BTC-USDT",                                // Market pair
  "buyer_id": "018f60f3-a120-7798-8422-cfb6a29e11aa",      // Taker/Maker Buyer User ID
  "seller_id": "018f60f3-d980-7798-8422-dfb6b29f22bb",     // Taker/Maker Seller User ID
  "buy_order_id": "018f60f3-b780-7798-8422-dfa6b29f44ea",  // Buyer's Order ID
  "sell_order_id": "018f60f3-e520-7798-8422-ffa6b29f55cc", // Seller's Order ID
  "price": "58200.0000000000",                            // Match price (String)
  "quantity": "0.0400000000",                             // Match quantity filled (String)
  "executed_at": "2026-07-10T18:07:42Z"                   // In-memory match execution time
}
```

---

### 4.5 `UserTradeSettled` (Wallet Service)
Published after `SettleTrade` commits. **Note: Because this event partitions by `user_id`, a single executed trade produces two separate `UserTradeSettled` messages — one for the buyer (key: buyer_id) and one for the seller (key: seller_id) — to parallelize portfolio rollup pipelines.**

```json
{
  "trade_id": "018f60f3-c540-7798-8422-efa6b29f1234",      // Match Trade ID
  "user_id": "018f60f3-a120-7798-8422-cfb6a29e11aa",       // Recipient User ID (Partition Key)
  "side": "BUYER",                                         // "BUYER" | "SELLER"
  "order_id": "018f60f3-b780-7798-8422-dfa6b29f44ea",      // Recipient Order ID
  "market_id": "BTC-USDT",                                 // Market pair
  "base_asset": "BTC",
  "quote_asset": "USDT",
  "price": "58200.0000000000",                            // Match execution price (String)
  "quantity": "0.0400000000",                             // Match execution quantity (String)
  "settled_at": "2026-07-10T18:07:44Z"                    // Database commit timestamp
}
```

---

### 4.6 `PortfolioUpdated` (Portfolio Service)
Published when a user's holdings positions or average cost parameters are re-calculated.

```json
{
  "user_id": "018f60f3-a120-7798-8422-cfb6a29e11aa",       // Target User ID (Partition Key)
  "asset": "BTC",                                          // Recalculated asset position
  "total_quantity": "0.1400000000",                        // Total asset holdings (String)
  "average_entry_price": "57850.0000000000",               // Recalculated entry average cost (String)
  "realized_pnl": "120.5000000000",                        // Realized PnL (String)
  "updated_at": "2026-07-10T18:07:45Z"                     // Calculation completion timestamp
}
```

---

To isolate failed events without blocking partition streams, every primary topic has a corresponding Dead Letter Queue topic containing versioned names:

### DLQ Topic Names:
* `orders.created.v1-dlq`
* `orders.cancel-requested.v1-dlq`
* `orders.cancelled.v1-dlq`
* `trades.executed.v1-dlq`
* `user-trades.settled.v1-dlq`
* `portfolios.updated.v1-dlq`
* `admin.user-suspended.v1-dlq`
* `admin.market-halted.v1-dlq`
* `admin.market-commands.v1-dlq`
* `wallet.deposit_completed.v1-dlq`
* `wallet.withdrawal-initiated.v1-dlq`
* `wallet.withdrawal-completed.v1-dlq`

### DLQ Properties:
* **Partition Count:** `1` (DLQs only record failure logs; strict ordering is not required).
* **Replication Factor:** `3`.
* **Min In-Sync Replicas:** `2`.
* **Retention Period:** `1209600000` (14 days, providing extended time for operations to audit and replay failed payloads).

### Required DLQ Metadata Headers:
All messages written to DLQs must include custom headers populated by the consumer that encountered the exception:

```ini
x-original-topic: "orders.created.v1"
x-exception-message: "JSON validation failed: quantity must be a string-based numeric format"
x-failed-at: "2026-07-10T18:08:00Z"
x-failed-by-consumer-group: "matching-engine-group"
```

---

## 6. Service Invariants

- **KTD-1 (Numeric Precision):** No float or double types may be serialized in event payloads. All numeric values representing price, quantity, or balance must be serialized as strings.
- **KTD-2 (Replication Standard):** All transactional Kafka topics must run with replication factor = 3 and minimum insync replicas = 2.
- **KTD-3 (DLQ Commitment):** Consumers encountering JSON syntax errors or payload schema validation exceptions must immediately publish the raw message to the DLQ and commit offset.
- **KTD-4 (Envelope Schema Compliance):** Every message published to any Kafka topic must comply with the global schema envelope, containing `event_id`, `event_type`, `event_version`, `timestamp`, and `payload` properties.
