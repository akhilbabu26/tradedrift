# TradeDrift Matching Engine — Monitoring

**Document:** 11_Monitoring.md  
**Service:** Matching Engine  
**Version:** V1.0  
**Status:** ✅ Design Complete  
**Last Updated:** July 2026

---

# 1. Purpose

This document defines what the Matching Engine measures, logs, and exposes for health checking—turning the non-functional requirements in `01_Overview.md §10` (low latency, high throughput, fault tolerance, determinism) into concrete, observable signals.

The monitoring strategy is designed around one principle:

> **Every important operational state of the Matching Engine should be externally observable.**

Metrics, structured logs, and health endpoints together allow operators to answer:

- Is the Matching Engine healthy?
- Is a market keeping up with incoming traffic?
- Has matching latency increased?
- Is backpressure active?
- Has recovery completed?
- Has any market halted due to an internal failure?

---

# 2. Metrics

## 2.1 Latency

| Metric | Type | Notes |
| --- | --- | --- |
| `me_match_latency_seconds` | Histogram, labeled by `market_id` | Time from the Event Loop receiving an event from the Input Queue until the Match Loop finishes processing it. This measures the core matching latency. |
| `me_publish_latency_seconds` | Histogram | Time from Output Queue receive until Kafka publish acknowledgement, labeled by event type (`TradeExecuted`, `OrderCancelled`). |
| `me_redis_projection_latency_seconds` | Histogram | Time from receiving a completed `DepthSnapshot` in the Publisher Layer until successful Redis `SET`. Measures serialization and Redis write latency (`09_Redis_Projection.md`). |
| `me_end_to_end_latency_seconds` | Histogram | Time from Kafka `OrderCreated` consume timestamp until `TradeExecuted` publish acknowledgement. Measures the **matching pipeline latency (ME slice only)** — does not include API Gateway → Order Service processing, Order Service → Kafka publish time, Settlement Service processing, or WebSocket broadcast. Labeled by `market_id`. |

---

## 2.2 Throughput

| Metric | Type | Notes |
| --- | --- | --- |
| `me_events_consumed_total` | Counter, labeled by `market_id`, `event_type` | Number of events consumed (`OrderCreated`, `OrderCancelRequested`). |
| `me_trades_executed_total` | Counter, labeled by `market_id` | Number of `TradeExecuted` events successfully published. |
| `me_orders_cancelled_total` | Counter, labeled by `market_id` | Number of `OrderCancelled` events successfully published. |
| `me_orders_resting_total` | Gauge, labeled by `market_id`, `side` | Current number of resting BUY and SELL orders. Maintained incrementally during insert/remove operations to avoid O(n) scans of `orderIndex`. |

---

## 2.3 Order Book State

| Metric | Type | Notes |
| --- | --- | --- |
| `me_best_bid` | Gauge, labeled by `market_id` | Current best bid maintained by the Order Book. Exported in O(1) regardless of the underlying price-index implementation. |
| `me_best_ask` | Gauge, labeled by `market_id` | Current best ask maintained by the Order Book. Exported in O(1) regardless of the underlying price-index implementation. |
| `me_spread` | Gauge, labeled by `market_id` | Difference between best ask and best bid. |
| `me_price_levels_total` | Gauge, labeled by `market_id`, `side` | Number of active price levels. Useful for determining when an alternative price index (e.g. B-Tree) may become beneficial (`04_Data_Structures/11_Future_Evolution.md §3`). |
| `me_book_depth_total_qty` | Gauge, labeled by `market_id`, `side` | Aggregate resting quantity across all price levels for each side of the book. Tracks overall market liquidity. |

---

## 2.4 Queueing and Lag

| Metric | Type | Notes |
| --- | --- | --- |
| `me_input_queue_depth` | Gauge, labeled by `market_id` | Current number of events waiting in the Input Queue. Primary early-warning signal that a market is falling behind. |
| `me_input_queue_utilization` | Gauge (%) | Percentage utilization of the Input Queue (`len(queue) / cap(queue)`). Easier to alert on than raw depth. |
| `me_output_queue_depth` | Gauge, labeled by `market_id` | Number of MatchResults waiting for the Publisher Layer. |
| `me_output_queue_utilization` | Gauge (%) | Percentage utilization of the Output Queue. Sustained high utilization indicates slow downstream publishing. |
| `kafka_consumer_lag` | Gauge, labeled by `market_id`, `partition` | Standard Kafka consumer lag showing how far behind the latest offset the Matching Engine is. **Implementation note:** this metric is typically sourced from the Kafka broker's consumer group metrics or external tooling (e.g. Burrow, `kafka-consumer-groups.sh`). The ME can also re-expose it by querying the Kafka admin API on each scrape cycle, but this is not a zero-cost operation. Deployment teams should decide whether to pull from Kafka-side tooling or have the ME self-report. |

---

## 2.5 Errors and Failures

| Metric | Type | Notes |
| --- | --- | --- |
| `me_panics_recovered_total` | Counter, labeled by `market_id` | Every recovered panic. Should remain zero. Any increment indicates a software defect (`10_Failure_Handling.md §6`). |
| `me_market_halts_total` | Counter, labeled by `market_id` | Number of times a market entered the HALTED state after panic recovery. |
| `me_deadletter_total` | Counter, labeled by `market_id`, `reason` | Events routed to the dead-letter topic. |
| `me_redis_write_failures_total` | Counter, labeled by `market_id` | Failed Redis projection writes. |
| `me_checkpoint_write_failures_total` | Counter, labeled by `market_id` | Failed checkpoint writes. |
| `me_duplicate_order_rejected_total` | Counter, labeled by `market_id` | Duplicate `OrderCreated` events rejected using `orderIndex`. |

---

## 2.6 Recovery

| Metric | Type | Notes |
| --- | --- | --- |
| `me_recovery_in_progress` | Gauge (0/1), labeled by `market_id` | Indicates whether the market is currently rebuilding from replay. |
| `me_recovery_events_replayed_total` | Counter, labeled by `market_id` | Number of Kafka events replayed during the latest recovery. |
| `me_recovery_duration_seconds` | Histogram, labeled by `market_id` | Total wall-clock recovery duration. |

---

## 2.7 Market State

| Metric | Type | Notes |
| --- | --- | --- |
| `me_market_state` | Gauge, labeled by `market_id` | Current Market Engine state. Values: `0 = STARTING` (transient — covers STARTING and LOADING CHECKPOINT; market will proceed to RECOVERY within seconds), `1 = RECOVERY` (replaying Kafka events, output suppressed), `2 = RUNNING` (live, normal operation), `3 = HALTED` (stopped after panic recovery; requires replay before resuming). Operators can distinguish transient startup (`0`) from permanent halt (`3`) without waiting for context. |

---

# 3. Health Endpoints

| Endpoint | Purpose |
| --- | --- |
| `/healthz` | Liveness probe. Confirms the Matching Engine process is running and internal goroutines are alive. Does **not** guarantee markets are processing events. |
| `/readyz` | Readiness probe. Returns success only when all assigned markets have completed recovery and are actively processing live events. Markets in `STARTING`, `RECOVERY`, or `HALTED` prevent readiness. |
| `/metrics` | Prometheus metrics endpoint exposing every metric described in Section 2. |

A node must never report **Ready** while any assigned market remains in `STARTING`, `RECOVERY`, or `HALTED`.

---

# 4. Logging Conventions

The Matching Engine uses structured JSON logging.

Every log entry must include sufficient identifiers to correlate activity across services.

## Standard Fields

| Field | Included when |
| --- | --- |
| `node_id` | Every log line |
| `market_id` | Every log produced by a Market Engine |
| `order_id` | Order insert, cancel, duplicate rejection, panic recovery |
| `trade_id` | Every trade execution |
| `event_type` | Consumer and Publisher logs |

Every log emitted by the Matching Engine identifies both the node (`node_id`) and the market (`market_id`) responsible for the event. This allows failures to be isolated to a specific market during multi-node deployments.

`trace_id` is intentionally **not** included in V1 logs. Distributed tracing is deferred until OpenTelemetry is introduced across the platform (`TradeDrift_ID_Correlation_Standard.md §6`).

---

## Log Levels

| Level | Usage |
| --- | --- |
| `INFO` | Startup, shutdown, recovery start/finish, market assignment, recovery completion |
| `WARN` | Duplicate order detection, malformed events, Redis failures, checkpoint failures |
| `ERROR` | Kafka publish failures after retry exhaustion, Market Halt events |
| `FATAL` | Not used by Market Engines. Panics halt only the affected market rather than terminating the entire Matching Engine process. |

---

# 5. Suggested Alerting Thresholds

| Condition | Severity | Rationale |
| --- | --- | --- |
| `me_market_state == HALTED` | P1 | A market halted after panic recovery. Replay required before trading resumes. |
| `me_panics_recovered_total > 0` | P1 | Indicates a software defect in the matching algorithm. |
| Sustained growth of `kafka_consumer_lag` | P2 | Market is falling behind live trading. |
| Sustained high `me_input_queue_utilization` | P2 | Backpressure active; matching throughput insufficient. |
| Sustained high `me_output_queue_utilization` | P2 | Publisher Layer cannot keep up with completed matches. |
| Continuous increase of `me_redis_write_failures_total` | P3 | Redis projection becoming stale. Trading correctness unaffected. |
| Long `me_recovery_duration_seconds` | P3 | Recovery replay is becoming expensive. Snapshot optimization may be required. |
| `/readyz` not ready within expected startup window | P2 | Recovery or startup dependencies failing. |

Severity definitions:

| Severity | Meaning |
| --- | --- |
| **P1** | Immediate operational response required. Trading correctness or availability impacted. |
| **P2** | Significant service degradation requiring prompt investigation. |
| **P3** | Non-critical degradation. Investigate during normal operational windows. |

Exact thresholds remain deployment-specific.

---

# 6. Suggested Dashboards

## Per-Market Overview

Display one row per active market showing:

- Market State (RUNNING / RECOVERY / HALTED)
- Best Bid
- Best Ask
- Spread
- Resting Orders
- Active Price Levels
- Trade Throughput

---

## Latency Dashboard

Display percentile charts for:

- Match Latency (P50 / P95 / P99)
- Publish Latency
- Redis Projection Latency
- End-to-End Latency

---

## Health Dashboard

Display:

- Input Queue Utilization
- Output Queue Utilization
- Kafka Consumer Lag
- Recovery Progress
- Recovery Duration
- Current Market State

---

## Error Dashboard

Display:

- Panic Count
- Market Halts
- Dead-Letter Events
- Redis Write Failures
- Checkpoint Failures
- Duplicate Order Rejections

---

# 7. Tracing

Distributed tracing is intentionally **not implemented in V1**.

Instead, the Matching Engine relies on:

- `market_id`
- `order_id`
- `trade_id`
- `node_id`

to correlate events across structured logs.

Future versions will integrate OpenTelemetry-based distributed tracing across the entire TradeDrift platform.

---

# 8. References

- `01_Overview.md §10` — Non-functional requirements
- `07_Concurrency_Model.md` — Queue metrics and backpressure
- `08_Recovery_Strategy.md` — Recovery metrics and readiness
- `09_Redis_Projection.md` — Redis projection latency
- `10_Failure_Handling.md` — Failure metrics and Market Halt behavior
- `TradeDrift_ID_Correlation_Standard.md` — Structured logging conventions