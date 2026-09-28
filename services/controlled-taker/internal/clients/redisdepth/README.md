# Redis Depth Reader (`internal/clients/redisdepth`)

The `redisdepth` package provides a read-only client for inspecting cached Level 2 (L2) order-book depth snapshots stored in Redis.

---

## 1. Architectural Responsibility

CTS never uses fixed or hardcoded order quantities. Every trading decision is derived dynamically from current live top-of-book depth.

Because sizing and price caps directly rely on this snapshot, the `redisdepth.Reader` enforces strict validation on every read. Any anomaly, clock drift, crossed book, or stale cache immediately causes CTS to **fail closed** and abort the cycle.

---

## 2. Redis Data Contract

### Key Format
Snapshots are retrieved using standard key convention:
```
depth:<market_id>
```
*Example:* `depth:BTC-USDT`, `depth:ETH-USDT`, `depth:SOL-USDT`.

### JSON Payload Schema (`depthSnapshotDTO`)
```json
{
  "market_id": "BTC-USDT",
  "sequence": 104592,
  "snapshot_at": "2026-09-28T16:04:05.123456Z",
  "bids": [
    { "price": "96490.00", "quantity": "0.5000" },
    { "price": "96480.00", "quantity": "1.2500" }
  ],
  "asks": [
    { "price": "96500.00", "quantity": "0.4500" },
    { "price": "96510.00", "quantity": "0.8000" }
  ]
}
```

---

## 3. Strict Fail-Closed Validation Pipeline

When `GetDepth(ctx, marketID)` reads a key from Redis, it executes six mandatory validation checks in sequence:

```
Redis GET "depth:<market_id>"
           │
           ▼
    1. Unmarshal JSON
           │
           ▼
    2. Market ID Check (dto.market_id == requested)
           │
           ▼
    3. Timestamp Freshness Check:
       • Missing or unparseable timestamp ──▶ FAIL
       • Age > 5.0 seconds (Stale) ─────────▶ FAIL
       • Future timestamp > 1.0 second ─────▶ FAIL
           │
           ▼
    4. Level Presence Check:
       • len(bids) == 0 OR len(asks) == 0 ──▶ FAIL
           │
           ▼
    5. Structural Monotonicity Checks:
       • Bids strictly descending: P[i] < P[i-1], P > 0, Q > 0 ──▶ FAIL if violated
       • Asks strictly ascending:  P[i] > P[i-1], P > 0, Q > 0 ──▶ FAIL if violated
           │
           ▼
    6. Crossed / Locked Book Validation:
       • bestAsk <= bestBid ──────────────▶ FAIL
           │
           ▼
    Return Validated *DepthSnapshot
```

### Validation Error Table

| Error Variable | Cause | Impact on CTS |
| :--- | :--- | :--- |
| `ErrDepthNotFound` | Key does not exist in Redis (`redis.Nil`). Order book unseeded. | Cycle skipped. Metric incremented (`not_found`). |
| `ErrMarketMismatch` | Payload `market_id` does not match the queried key. | Cycle skipped. Metric incremented (`market_mismatch`). |
| `ErrEmptySnapshotAt` | `snapshot_at` field is missing, empty, or unparseable RFC3339 string. | Cycle skipped. Metric incremented (`missing_timestamp` or `unparseable_timestamp`). |
| `ErrStaleSnapshot` | `time.Since(snapshot_at) > 5 * time.Second`. Indicates market data pipeline stall. | Cycle skipped. Metric incremented (`stale`). |
| `ErrFutureSnapshot` | `snapshot_at.Sub(now) > 1 * time.Second`. Indicates system clock skew. | Cycle skipped. Metric incremented (`future_timestamp`). |
| `ErrIncompleteDepth` | Either `bids` or `asks` array is empty. One-sided book. | Cycle skipped. Metric incremented (`incomplete_depth`). |
| `ErrInvalidOrderBook` | Level price/quantity $\le 0$, bids not strictly descending, or asks not strictly ascending. | Cycle skipped. Metric incremented (`invalid_level` or `ordering`). |
| `ErrCrossedOrderBook` | `bestAsk <= bestBid`. Market data corruption or uncrossed matching engine queue. | Cycle skipped. Metric incremented (`crossed_book`). |

---

## 4. Operational Invariants

1. **Zero Guessing:** If Redis returns stale data (> 5s) or is unreachable, CTS never trades. It does not fall back to previous snapshots.
2. **Read-Only Privilege:** This package only calls `Get` and `Ping`. It never mutates Redis state.
3. **High-Throughput Parsing:** Level strings are parsed directly into `decimal.Decimal` structures to avoid floating-point rounding inaccuracies.
