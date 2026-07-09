# TradeDrift Matching Engine — Performance Benchmarking

**Document:** 14_Performance_Benchmarking.md  
**Service:** Matching Engine  
**Version:** V1.0  
**Status:** 📋 Design Phase (Benchmarks Pending)  
**Last Updated:** July 2026

---

# 1. Purpose

This document defines the benchmarking strategy for the Matching Engine.

The goal is to validate that implementation decisions are based on measured performance rather than theoretical complexity.

Benchmarks will be executed after the Matching Engine implementation is complete.

---

# 2. Benchmark Goals

The benchmark suite measures:

- Order insertion latency
- Order cancellation latency
- Matching latency
- Best Bid / Best Ask lookup latency
- Price level insertion/removal latency
- Memory allocation
- CPU utilization
- Throughput (orders/sec)
- Tail latency (P95/P99)

---

# 3. Benchmark Levels

The project contains three benchmark stages.

| Stage | Scope | Status |
|--------|-------|--------|
| Data Structure Benchmarks | Individual order book components | Planned |
| Matching Engine Benchmarks | Core matching engine | Planned |
| End-to-End Exchange Benchmarks | Complete exchange pipeline | Planned |

---

# 4. Data Structure Benchmarks

These benchmarks evaluate the performance of the internal order book data structures.

## Planned Operations

| Benchmark | Description |
|-----------|-------------|
| BenchmarkInsertPriceLevel | Insert a new price level |
| BenchmarkRemovePriceLevel | Remove an empty price level |
| BenchmarkBestBid | Retrieve highest bid |
| BenchmarkBestAsk | Retrieve lowest ask |
| BenchmarkFindPriceLevel | Lookup price level |
| BenchmarkAddOrder | Insert order into FIFO queue |
| BenchmarkCancelOrder | Remove order by Order ID |
| BenchmarkMatchOrder | Execute order matching |

---

# 5. Future Data Structure Comparison

TradeDrift V1 uses a Sorted Slice as the primary price index.

Future versions may compare alternative implementations.

| Price Index | Status |
|-------------|--------|
| Sorted Slice | V1 |
| B-Tree | Planned |
| Skip List | Planned |

The matching algorithm will remain unchanged.

Only the underlying price index implementation will be replaced to ensure a fair comparison.

---

# 6. Matching Engine Benchmarks

The matching engine benchmark measures processing performance under realistic workloads.

## Planned Test Scenarios

- 10,000 Orders
- 100,000 Orders
- 1,000,000 Orders
- Mixed Buy/Sell workload
- Partial fills
- Large market orders
- High cancellation rate

### Measured Metrics

| Metric | Description |
| --- | --- |
| Orders per second | Throughput under sustained load |
| Trades per second | Fill output rate |
| Average latency | Mean event loop processing time |
| P95 latency | 95th percentile event loop latency |
| P99 latency | 99th percentile — primary SLO target |
| Maximum latency | Worst-case sweep latency |
| CPU usage | Core utilization per market goroutine |
| Memory usage | Heap allocation per order, per price level |

---

# 7. End-to-End Benchmarks

The complete exchange will be benchmarked using the production event pipeline.

```
Trader
    ↓
API Gateway
    ↓
Kafka
    ↓
Matching Engine
    ↓
Trade Service
    ↓
Wallet Service
    ↓
Portfolio Service
    ↓
WebSocket
```

### Measured Metrics

| Metric | Description |
| --- | --- |
| API response latency | Gateway receives request → response returned |
| Kafka publish latency | Order Service → Kafka ack |
| Kafka consume latency | Kafka → ME Event Loop start |
| Matching latency | Event Loop start → MatchResult produced |
| Settlement latency | TradeExecuted published → Wallet updated |
| WebSocket broadcast latency | Kafka → client receives depth update |
| End-to-end trade completion | Request received → portfolio updated |

---

# 8. Benchmark Environment

The benchmark environment will be documented with every published result.

Environment includes:

- CPU
- RAM
- Operating System
- Go Version
- Kafka Version
- PostgreSQL Version
- Redis Version

This ensures benchmark results are reproducible.

---

# 9. Benchmark Tools

TradeDrift uses Go's built-in benchmarking framework.

Example command:

```bash
go test -bench=. -benchmem
```

Collected metrics include:

- ns/op
- B/op
- allocs/op

Profiling tools:

- CPU Profile
- Memory Profile
- Goroutine Profile
- Execution Trace

---

# 10. Planned Benchmark Results

This section will be updated after implementation.

| Operation | Sorted Slice | B-Tree | Skip List |
|-----------|-------------:|-------:|----------:|
| Best Bid | TBD | TBD | TBD |
| Best Ask | TBD | TBD | TBD |
| Insert Price Level | TBD | TBD | TBD |
| Remove Price Level | TBD | TBD | TBD |
| Cancel Order | TBD | TBD | TBD |
| Match Order | TBD | TBD | TBD |
| Orders/sec | TBD | TBD | TBD |
| Memory Usage | TBD | TBD | TBD |

---

# 11. Design Decision

TradeDrift V1 adopts a Sorted Slice as the primary price index.

Reasons:

- Simpler implementation
- Excellent CPU cache locality
- Low memory overhead
- Efficient for the expected number of active price levels
- Easier to understand for educational purposes

Although B-Trees provide O(log n) insertion and deletion, production performance depends on workload characteristics such as cache locality, pointer traversal, allocation behavior, and the number of active price levels.

Future versions will benchmark Sorted Slice, B-Tree, and Skip List implementations using identical workloads to validate the optimal design based on empirical performance rather than theoretical complexity.

---

# 12. Future Work

Future releases may include:

- Multi-symbol benchmark suite
- Multi-core scaling benchmarks
- Kafka throughput benchmarks
- Recovery performance benchmarks
- Snapshot generation benchmarks
- Order book reconstruction benchmarks
- Distributed deployment benchmarks
