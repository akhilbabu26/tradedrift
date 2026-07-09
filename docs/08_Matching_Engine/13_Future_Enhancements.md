# TradeDrift Matching Engine — Future Enhancements

**Document:** 13_Future_Enhancements.md
**Service:** Matching Engine
**Version:** V1.0
**Last Updated:** July 2026

---

# 1. Purpose

This document defines the planned evolution of the Matching Engine across versions. Each version is driven by a specific trigger — a measurable signal — not by a calendar date. No changes described here affect V1 design.

V1 is built for correctness and operational simplicity. Each subsequent version adds capability at the specific layer that becomes the bottleneck.

---

# 2. Version Map

```
V1       Production-ready correctness and observability
  │
V1.5     First scaling improvements — no architectural change
  │
V2       High-throughput architectural upgrades
  │
V3       Exchange-scale distributed systems
```

---

# 3. V1 — Production Ready

**Trigger:** Launch readiness. These are not optional.

---

## 3.1 Core Reliability

### Market Circuit Breaker (Panic Recovery)

```go
func (m *MarketEngine) Run() {
    defer func() {
        if r := recover(); r != nil {
            log.Errorf("market %s panic: %v — halting market", m.marketID, r)
            m.halt()    // stop consuming, publish alert
        }
    }()
    for event := range m.inputQueue { ... }
}
```

A panic in one market's Event Loop must not kill the process. Without `defer recover()`, any matching bug in BTC-USDT takes all markets offline.

**Scope:** One `defer recover()` block per Event Loop goroutine.

---

### Graceful Shutdown

```
SIGTERM received
        │
        ▼
Stop Kafka Consumer (no new events enter queues)
        │
        ▼
Each Event Loop drains its Input Queue
        │
        ▼
Results flow to Output Queue → Publisher publishes
        │
        ▼
Wait for all Kafka acks
        │
        ▼
Write final checkpoint
        │
        ▼
Exit cleanly
```

Shutdown must be a drain, not a kill. No in-flight match is lost. No checkpoint is written ahead of its corresponding Kafka publish.

---

### Recovery Replay

On restart:

1. Read checkpoint `{topic, partition, offset}` from Postgres.
2. Enter RECOVERY mode — Publisher output suppressed.
3. Replay `OrderCreated` and `OrderCancelRequested` from checkpoint offset through the full matching algorithm.
4. Exit RECOVERY mode at the checkpoint offset.
5. Resume live matching.

See `08_Recovery_Strategy.md`.

---

### Health and Ready Endpoints

| Endpoint | Returns healthy when |
| --- | --- |
| `GET /healthz` | Process is alive |
| `GET /readyz` | Recovery replay complete, all Event Loops running, Kafka Consumer connected |

Kubernetes liveness and readiness probes depend on these. Without `/readyz`, Kubernetes may route traffic to a node still replaying its Order Book.

---

## 3.2 Observability

### Queue Monitoring

Expose per-market Input Queue and Output Queue depth as Prometheus metrics.

| Metric | Alert threshold |
| --- | --- |
| `me_input_queue_depth{market}` | Warn at 50%, critical at 75%, halt-consider at 90% |
| `me_output_queue_depth{market}` | Warn at 50%, critical at 75% |

---

### Event Loop Latency Metrics

Measure time from event dequeued to results written to Output Queue.

| Metric | Description |
| --- | --- |
| `me_event_loop_latency_p50{market}` | Median processing time |
| `me_event_loop_latency_p99{market}` | 99th percentile — catches sweep latency spikes |
| `me_event_loop_latency_p999{market}` | 99.9th percentile |

---

### Consumer Lag Metrics

Kafka consumer lag per partition. Primary SLO metric for the Matching Engine.

```
consumer_lag{topic, partition, market} = latest_offset - committed_offset
```

Alert: sustained lag > 500 events → scale review.

---

### Memory Monitoring Per Market

| Metric | Description |
| --- | --- |
| `me_resting_orders{market}` | Current count of `OrderNode` objects in the book |
| `me_price_levels{market}` | Current count of PriceLevels (bids + asks) |
| `me_heap_alloc_bytes` | Process-level heap in use |

Monitors against spoofing attacks (millions of fake resting orders exhausting heap).

---

### Structured Logging

All ME log lines must be structured JSON with the following minimum fields:

| Field | Example |
| --- | --- |
| `level` | `INFO`, `WARN`, `ERROR` |
| `market_id` | `BTC-USDT` |
| `event` | `order_matched`, `order_cancelled`, `market_halted` |
| `order_id` | UUID |
| `latency_ms` | `0.23` |
| `ts` | RFC3339 |

---

## 3.3 Configuration

All the following must be runtime-configurable (environment variable or config file), not hardcoded:

| Parameter | Default | Notes |
| --- | --- | --- |
| `INPUT_QUEUE_SIZE` | 1000 | Per-market Input Queue depth |
| `OUTPUT_QUEUE_SIZE` | 5000 | Per-market Output Queue depth (larger than input — Publisher is slower than matching) |
| `CHECKPOINT_INTERVAL_MS` | 500 | How often the checkpoint offset is written to Postgres |
| `CONSUMER_LAG_ALERT_THRESHOLD` | 500 | Events behind before alerting |

> **No sweep limit is imposed.** A market order always fills against all available liquidity at the best prices, regardless of how many price levels that requires. Stopping a fill mid-sweep because an internal level counter was reached would change market behavior — an order that should fully fill would be partially filled or silently truncated. This violates Price-Time Priority correctness and is not acceptable exchange behavior.
>
> Large market orders may temporarily increase Event Loop latency during a deep sweep. This latency is monitored via `me_event_loop_latency_p99`. If sweep latency becomes a measured problem at scale, future versions optimize the matching algorithm — not the trading semantics. See `13_Future_Enhancements.md §5` (V2 performance) and `§6` (V3 ring buffer).

---

# 4. V1.5 — First Scaling Improvements

**Trigger:** BTC-USDT consumer lag sustained above threshold, or cross-market contagion observed in production. No architectural change — only deployment and configuration evolution.

---

## 4.1 Scalability

### One Kafka Consumer Per Partition

**Problem:** The current design has one Kafka Consumer goroutine routing events to all market Input Queues. If BTC-USDT's queue is full, the consumer's channel send blocks — stalling SOL-USDT routing even though SOL-USDT has capacity.

**Fix:** Spawn one consumer goroutine per Kafka partition. Each reads independently.

```
Before:
  One Consumer ──► [BTC queue] [ETH queue] [SOL queue]
  (BTC full → all stall)

After:
  BTC Consumer ──► [BTC queue]
  ETH Consumer ──► [ETH queue]
  SOL Consumer ──► [SOL queue]
  (BTC full → only BTC stalls, ETH and SOL unaffected)
```

**Scope:** Minor change to Kafka Consumer initialization. No change to Event Loop or matching logic.

---

### Dedicated Node Per Hot Market

**Problem:** All markets share one ME process. BTC-USDT consumes most of the CPU.

**Fix:** Assign BTC-USDT to its own Kafka partition and run a dedicated ME node consuming only that partition. No code change — only Kafka partition assignment and deployment config.

```
ME Node A: BTC-USDT partition only
ME Node B: ETH-USDT, SOL-USDT, DOGE-USDT partitions
```

---

### API Rate Limiting

Implement at API Gateway level — before events reach Kafka. Prevents a single client flooding the ME input.

| Limit | Default |
| --- | --- |
| Orders per user per second | 50 |
| Orders per IP per second | 200 |
| Orders per market per second (global) | 10,000 |

---

### Large Output Queue Tuning

If Kafka publish latency spikes (broker overloaded), the Output Queue fills. When it fills, Event Loops block. Tuning `OUTPUT_QUEUE_SIZE` (from §3.3) to absorb typical Kafka latency variance decouples matching throughput from publish throughput.

---

### Market-Level Sustained Failure Circuit Breaker

Distinct from V1's panic recovery. This circuit breaker activates when a market shows sustained degradation — not a single panic, but repeated errors or queue saturation beyond threshold.

```
Repeated failures OR queue > 90% for > 30 seconds
        │
        ▼
Market circuit opens:
    - Stop consuming new events for this market
    - Publish MarketHalted event
    - Alert ops team
    - Other markets continue unaffected
        │
        ▼
After operator review:
    - Market circuit closes
    - Recovery replay from last checkpoint
    - Resume live matching
```

---

### Queue Alerting

Graduated alerting on queue utilization (already defined in §3.2, operationalized here):

| Threshold | Action |
| --- | --- |
| 50% | Informational log |
| 75% | PagerDuty alert |
| 90% | Automatic scaling review + possible rate limit tightening |

---

## 4.2 Performance

### Event Loop Profiling

Run `pprof` CPU and memory profiles against the Event Loop under representative load. Identify actual bottlenecks before optimizing. Do not optimize before profiling.

### Memory Optimization

Based on profiling results, evaluate:
- `sync.Pool` for `OrderNode` allocation (reduces GC pressure from order churn)
- Reducing `OrderNode` struct size (padding, field alignment)
- Reducing `decimal.Decimal` allocation frequency (pre-compute common values)

None of these should be implemented before profiling shows they are necessary.

---

# 5. V2 — High Throughput

**Trigger:** Per-market consumer lag cannot be resolved by dedicated nodes alone. Publish throughput or recovery time becomes a measurable problem.

---

### Per-Market Publisher Goroutine

Replace the single fan-in Publisher with one Publisher goroutine per market.

```
Before:
  All markets → fan-in channel → One Publisher → Kafka

After:
  BTC-USDT Event Loop → BTC Publisher → Kafka (BTC partition)
  ETH-USDT Event Loop → ETH Publisher → Kafka (ETH partition)
```

Kafka slowness for one market no longer blocks publishing for other markets.

---

### Binary Protocol (Protobuf / Avro)

Replace JSON serialization of Kafka events with a binary protocol.

| Option | Schema Registry | Notes |
| --- | --- | --- |
| Avro | Confluent Schema Registry | Most common in Kafka ecosystems |
| Protobuf | Buf / Confluent | Strong tooling, language-neutral |
| FlatBuffers | None needed | Zero-copy reads, no parse step |

JSON serialization overhead is measurable above ~100K events/sec. At V2 scale this becomes worth optimizing.

---

### Auto Scaling via Consumer Lag

Deploy Kubernetes HPA (Horizontal Pod Autoscaler) with a custom metric: Kafka consumer lag per partition.

```
consumer_lag > threshold
        │
        ▼
Kubernetes scales ME deployment
        │
        ▼
New ME node joins consumer group
Kafka rebalances partition assignment
New node begins consuming
```

Requires consumer group rebalancing to be partition-safe (one ME per partition remains enforced).

---

### Order Book Snapshots

Capture in-memory Order Book state periodically.

```
Every X minutes (configurable):
    Serialize: OrderBook → snapshot struct
    Store in: memory (ring buffer of last N snapshots)
```

Enables fast recovery without requiring a full Kafka replay — only events after the snapshot offset need to be replayed.

---

### Persistent Snapshots

Write in-memory snapshots to durable storage.

```
Snapshot taken (in memory)
        │
        ▼
Write to: S3 / Postgres / Redis AOF
Record: snapshot_offset (Kafka offset at snapshot time)
        │
        ▼
On restart:
    Load latest snapshot from storage
    Replay only events after snapshot_offset
    Recovery time capped to last X minutes
```

Combines with Order Book Snapshots — one produces the snapshot, the other persists it.

---

### Automatic Restart and Replay

On process death, the container orchestrator (Kubernetes) automatically restarts the ME pod. On restart, the ME automatically:

1. Reads checkpoint from Postgres.
2. Loads latest persistent snapshot (if available).
3. Replays events from snapshot offset.
4. Resumes live matching without operator intervention.

V1 recovery is automatic at the code level but requires a healthy container to restart — Kubernetes handles that. V2 makes the entire path fully automated including snapshot loading.

---

# 6. V3 — Exchange Scale

**Trigger:** A single market's throughput cannot be handled by a single-threaded Event Loop, even on a dedicated high-core machine. This represents a fundamental architectural constraint being hit.

---

### Multiple Matching Engines Per Market

Run more than one ME instance responsible for the same market's matching. Requires:

- Global sequence numbers on Kafka input events (deterministic ordering across ME instances)
- A coordination layer (or leader-follower where only the leader matches; follower is a hot standby)
- OR price range partitioning (see below)

This is the hardest scaling problem in exchange engineering. Most exchanges solve it by vertical scaling (faster hardware) as long as possible before reaching this.

---

### Ring Buffer — LMAX Disruptor Pattern

Replace Go channels between Event Loop and Publisher with a pre-allocated ring buffer.

```
Before: Event Loop ──chan──► Publisher   (GC pressure, blocking)
After:  Event Loop ──ring buffer──► Publisher   (zero allocation, lock-free)
```

The LMAX Disruptor eliminates garbage collection of channel buffers and reduces inter-goroutine coordination overhead. Used by LMAX, Chronicle, Aeron. Significant implementation complexity — only justified at >1M events/sec per market.

---

### Price Range Partitioning

Split one market's Order Book across multiple ME nodes by price range.

```
BTC-USDT:
  Node A handles: bids 90,000–95,000 | asks 95,000–100,000
  Node B handles: bids 85,000–90,000 | asks 100,000–105,000
```

Extremely difficult because:
- A market order can span multiple price ranges → requires cross-node coordination per fill
- Deterministic ordering across nodes requires a global sequencer
- The coordination overhead may exceed the performance gain at moderate volumes

---

### FPGA Matching

Offload the matching algorithm to dedicated FPGA hardware.

- Latency: nanoseconds vs microseconds for software
- Used by: Nasdaq, Cboe, top HFT venues
- Requires: specialized hardware engineering team, significant capital

Only justified for a venue competing directly with professional HFT firms on latency.

---

### Multi-Region Matching

Deploy ME nodes across US, Europe, and Asia.

```
User in Asia ──► Asia ME ──► Asia Kafka
User in EU   ──► EU ME   ──► EU Kafka
                                │
                         Cross-region replication
                                │
                    Settlement Service (global)
```

The fundamental challenge: one global order book cannot be maintained across regions without introducing either:
- Latency arbitrage opportunities (different regions see different book states)
- Very high cross-region coordination latency

Most exchanges solve this with **separate regional books** (not cross-region matching) or a **primary region** with replicas for read-only data.

---

# 7. Operational Evolution

How the deployment model evolves alongside the technical changes.

---

## V1 Operations

| Aspect | V1 Model |
| --- | --- |
| Deployment | Single ME node, all markets |
| Recovery | Manual restart + automatic Kafka replay |
| Monitoring | Prometheus + Grafana dashboards |
| Incident response | Manual — operator reviews logs, restarts if needed |
| Scaling | Vertical only (bigger machine) |
| Deploy process | Rolling restart (brief recovery replay on startup) |

---

## V1.5 Operations

| Aspect | V1.5 Model |
| --- | --- |
| Deployment | Multiple ME nodes — hot markets on dedicated nodes |
| Recovery | Still manual restart; automatic replay is faster (per-partition consumers) |
| Monitoring | Per-market dashboards; consumer lag as primary SLO |
| Incident response | Market-level circuit breaker reduces blast radius |
| Scaling | Horizontal — add ME nodes for new or hot markets |
| Deploy process | Blue/green per-market (deploy BTC node without touching ETH node) |

---

## V2 Operations

| Aspect | V2 Model |
| --- | --- |
| Deployment | Kubernetes — ME as a StatefulSet (one pod per market) |
| Recovery | Automatic: pod restarts → loads snapshot → replays → resumes |
| Monitoring | Distributed tracing added (trace one order through ME → Settlement) |
| Incident response | SRE runbooks for all common failure modes; PagerDuty integration |
| Scaling | HPA on consumer lag — automatic horizontal scaling |
| Deploy process | Canary deployment per market (5% traffic → validate → 100%) |

---

## V3 Operations

| Aspect | V3 Model |
| --- | --- |
| Deployment | Multi-region Kubernetes clusters; global traffic management |
| Recovery | Fully automated — zero-operator recovery for all standard failure modes |
| Monitoring | Full observability stack: metrics + traces + logs correlated per trade |
| Incident response | Chaos engineering tests validate failure assumptions before incidents |
| Scaling | Automatic at both node and region level |
| Deploy process | Multi-region canary with automated rollback on SLO breach |

---

# 8. What Does NOT Change Across Versions

The following are invariants across all versions — they do not evolve:

| Property | Why it never changes |
| --- | --- |
| Price-Time Priority matching rule | Exchange contract with users — changing it requires market notification |
| `trade_id` generated by ME | Idempotency anchor for Settlement Service |
| Input events: `OrderCreated` + `OrderCancelRequested` | Upstream contract with Order Service |
| Output events: `TradeExecuted` + `OrderCancelled` | Downstream contract with Settlement and Order Service |
| `orderIndex` lookup before cancel | Required for O(1) cancel regardless of architecture |
| Recovery via input event replay | The matching algorithm is deterministic — this property is permanent |

---

# 9. References

- `07_Concurrency_Model.md` — current concurrency model and its known limits
- `04_Data_Structures/11_Future_Evolution.md` — data structure upgrade paths
- `08_Recovery_Strategy.md` — recovery sequencing details
- `11_Monitoring.md` — full metrics catalogue
- `02_System_Architecture.md` — system-level scaling model
