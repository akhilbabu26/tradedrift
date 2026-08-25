# `internal/publisher` — Egress, Trade Publication & Checkpoint Coordination

**Package:** `publisher`  
**Service:** Matching Engine  
**Last Updated:** August 2026  

---

## 1. What This Package Does

This package is the **egress, projection broadcast, and durability coordinator** of the Matching Engine. It consumes `orderbook.MatchResult` outputs produced by each `MarketEngine` and coordinates writes across three infrastructure layers:

1. **Kafka (`trades.executed`)**: Publishes individual matched trade events with deterministic trade IDs and sequences, partitioned by `MarketID`.
2. **Redis (`depth:{market_id}`)**: Pushes real-time Level-2 Top-20 depth snapshots for low-latency market data consumption.
3. **PostgreSQL Checkpoint Coordinator (`internal/checkpoint`)**: Passes completed events (sequences, snapshots with SHA-256 checksums, and source offsets) to advance the contiguous durability watermark in `kafka_checkpoints`, `market_sequences`, and `market_snapshots`.
4. **Snapshot Retention Job**: Runs an hourly background maintenance job that prunes historical snapshots while strictly preserving the recovery anchor snapshot.

---

## 2. The 3-Step Processing Pipeline

Every `MatchResult` is processed via `Publisher.process()` in strict sequence:

```
MatchResult
    │
    ▼
[Step 1: Publish Trades to Kafka]        ← DURABLE EVENT LOG
    ├── Iterate result.Fills
    ├── Marshal tradeExecutedMessage (including Sequence & MarketID)
    ├── Write to "trades.executed" with partition key = fill.MarketID
    └── Error? ──► Fail closed! Call HaltCallback() and stop.
    │
    ▼
[Step 2: Push Depth to Redis]            ← LEVEL-2 PROJECTION
    ├── Marshal result.DepthSnapshot (including Sequence)
    ├── SET "depth:{market_id}" (TTL = 0)
    └── Error? ──► Fail closed! Call HaltCallback() and stop.
    │
    ▼
[Step 3: Advance Checkpoint via Coord]   ← CONTIGUOUS WATERMARK
    ├── Calculate snapshot SHA-256 checksum (if snapshot is non-nil)
    ├── Call checkpointCoordinator.MarkDoneWithSequence(ctx, CompletedEvent)
    └── Error? ──► Return error and abort.
```

---

## 3. Snapshot Retention Query (`runRetention`)

Runs hourly to prevent unbounded disk growth while protecting the recovery anchor snapshot:

```sql
WITH ranked AS (
    SELECT market_id, sequence,
           ROW_NUMBER() OVER (PARTITION BY market_id ORDER BY sequence DESC) as rn
    FROM market_snapshots
),
anchors AS (
    SELECT DISTINCT ON (ms.market_id) ms.market_id, ms.sequence
    FROM market_snapshots ms
    JOIN kafka_checkpoints kc ON kc.partition = ms.partition AND kc.topic = 'orders.commands'
    WHERE ms.offset <= kc.offset
    ORDER BY ms.market_id, ms.offset DESC
)
DELETE FROM market_snapshots ms
WHERE NOT EXISTS (
    SELECT 1 FROM ranked r
    WHERE r.market_id = ms.market_id AND r.sequence = ms.sequence AND r.rn <= 3
)
AND NOT EXISTS (
    SELECT 1 FROM anchors a
    WHERE a.market_id = ms.market_id AND a.sequence = ms.sequence
);
```

---

## 4. What This Package Does NOT Do

- Does NOT run order matching or modify the `OrderBook` — handled by `../matcher/`
- Does NOT ingest or parse input messages from Kafka — handled by `../kafka/`
- Does NOT replay historical Kafka events on startup — handled by `../recovery/`
- Does NOT settle balances or calculate trading fees — handled by Wallet Service
