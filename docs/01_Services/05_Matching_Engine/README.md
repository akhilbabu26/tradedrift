# TradeDrift Matching Engine Documentation Suite

Welcome to the **TradeDrift Matching Engine** service documentation. This directory contains the complete architectural, algorithmic, and operational specifications for the core matching engine of the TradeDrift trading platform.

---

## 1. Purpose
The Matching Engine (ME) is the high-performance, single-threaded execution core of TradeDrift. Its sole purpose is to process incoming orders for a specific market asset pair (e.g., BTC-USDT) sequentially, maintain price-time priority order books, execute trades, and publish outcome events with sub-millisecond latencies.

## 2. Key Responsibilities
* **Deterministic Matching**: Execute limit and market orders using a pure in-memory FIFO queue per price level.
* **Concurrency Isolation**: Enforce single-threaded access to each order book's state using dedicated, independent goroutine event loops.
* **Asynchronous Output Publishing**: Offload heavy disk and network I/O (Kafka, Redis, Postgres) to separate Publisher goroutines.
* **State Reconstruction (Replay-from-Zero)**: Recover state deterministically by replaying historical events from offset 0 up to the postgres-committed checkpoint.
* **Redis Market Depth Projection**: Publish raw L2 market depth snapshots to Redis for downstream read replica synchronization.

### System Components & Data Pipeline
![Component Diagram](../../diagrams/component-diagram.svg)

---

## 3. Current Status
* **Status**: 🟢 **Design Phase Complete / Implementation Pending**
* **Version**: V1.0 (July 2026)

---

## 4. Reading Order (Recommended Path)
For developer onboarding or design reviews, it is highly recommended to read the specifications in the following sequence:

1. **[01_Overview.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/01_Overview.md)**: High-level design goals and philosophy.
2. **[02_System_Architecture.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/02_System_Architecture.md)**: Goroutine topology and layout of the ME nodes.
3. **[03_Order_Book.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/03_Order_Book.md)**: Logical entities, price levels, and basic lifecycle.
4. **[05_Matching_Algorithm.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/05_Matching_Algorithm.md)**: Match loop, sweeping logic, and calculations.
5. **[07_Concurrency_Model.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/07_Concurrency_Model.md)**: Channels, queue separation, and performance properties.
6. **[08_Recovery_Strategy.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/08_Recovery_Strategy.md)**: Replay-from-zero sequence and Postgres checkpoint mechanics.

---

## 5. Document Index

| Filename | Description |
|---|---|
| **[01_Overview.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/01_Overview.md)** | Core objectives, latency targets, and general design constraints. |
| **[02_System_Architecture.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/02_System_Architecture.md)** | Pipeline overview, thread mapping, and division of labor between ME and Publisher. |
| **[03_Order_Book.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/03_Order_Book.md)** | Logical layout of resting orders, levels, and cleanup cycles. |
| **[04_OrderBook_Data_Structures/](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/04_OrderBook_Data_Structures)** | Struct definitions, lists, and pointer maps. |
| **[05_Matching_Algorithm.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/05_Matching_Algorithm.md)** | Detail on order crossability, fill matching, and tick/lot validations. |
| **[06_Event_Contracts.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/06_Event_Contracts.md)** | JSON schemas and structures for input and output events. |
| **[07_Concurrency_Model.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/07_Concurrency_Model.md)** | Core matching loops, channel capacities, and fan-in routing. |
| **[08_Recovery_Strategy.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/08_Recovery_Strategy.md)** | Crash recovery mechanics, replay semantics, and sentinel caught-up logic. |
| **[09_Redis_Projection.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/09_Redis_Projection.md)** | L2 order book replication, key schemas, and update publishing rules. |
| **[10_Failure_Handling.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/10_Failure_Handling.md)** | Panics, state halts, alerting thresholds, and consumer group errors. |
| **[11_Monitoring.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/11_Monitoring.md)** | Core Prometheus metrics, latency tracking, and operational gauges. |
| **[12_Sequence_Diagrams.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/12_Sequence_Diagrams.md)** | Sequence flow of message processing, recovery catch-up, and cancel pipelines. |
| **[13_Flow_Diagrams.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/13_Flow_Diagrams.md)** | Workflow logic flowcharts (SVG) of Limit, Market (IOC), and Cancel pathways. |
| **[14_State_Diagrams.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/14_State_Diagrams.md)** | State transitions for Market Engines, resting orders, and price levels. |
| **[15_Design_Invariants.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/15_Design_Invariants.md)** | System rules governing concurrency, recovery, algorithms, and performance. |
| **[16_Future_Enhancements.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/16_Future_Enhancements.md)** | Scaling plans, performance optimizations, and clustered rebalancing guides. |
| **[17_Performance_Benchmarking.md](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/docs/01_Services/05_Matching_Engine/17_Performance_Benchmarking.md)** | Benchmarking rules, microbenchmarks, profiling setups, and target SLAs. |
