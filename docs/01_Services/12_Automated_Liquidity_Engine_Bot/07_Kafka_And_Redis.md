# Liquidity Engine — Kafka Event Contracts & Redis

> **Service:** `services/liquidity-engine`
> **Version:** V1.1

---

## Kafka Event Contracts

### Liquidity Engine Publishes (to `orders.commands`):

| Event | Trigger |
| :--- | :--- |
| `OrderCreate` | New ladder level needed |
| `OrderCancel` | Stale / risk-breached level |
| `OrderReplace` | Price shift requiring cancel + create (single atomic command) |

> `OrderReplace` is preferred over `OrderCancel + OrderCreate` as it halves the number of
> events written to `orders.commands`, directly reducing ME recovery replay cost.

### Liquidity Engine Consumes:

| Event | Source Topic | Action |
| :--- | :--- | :--- |
| `OrderAccepted` | `orders.events` | Add to in-memory tracker |
| `OrderRejected` | `orders.events` | Log + retry with backoff |
| `OrderPartiallyFilled` | `orders.events` | Update remaining qty in tracker |
| `OrderFilled` | `orders.events` | Remove from tracker + trigger reconcile |
| `OrderCancelled` | `orders.events` | Remove from tracker |
| `TradeExecuted` | `trades.executed` | Update inventory metrics |
| `ME_RECOVERING` | `system.events` | Transition LE to PAUSED state |
| `ME_LIVE` | `system.events` | Detect epoch change, transition LE to RECONCILING |

---

## Redis Usage

Redis provides the **read-side order book depth cache**:

```
Key:  depth:{market_id}
Type: JSON blob

Liquidity Engine reads:
  - Current best bid
  - Current best ask
  - Full L2 depth snapshot
```

**Important:** Redis is a **read-only projection cache** for the Liquidity Engine.

| Who writes `depth:{market_id}` | Who reads `depth:{market_id}` |
| :--- | :--- |
| Matching Engine Publisher | Liquidity Engine (best bid/ask check) |
| | API Gateway (order book endpoint) |
| | WebSocket Publisher (L2 stream) |

The Liquidity Engine **never writes** to Redis. The Matching Engine owns all Redis depth writes.
