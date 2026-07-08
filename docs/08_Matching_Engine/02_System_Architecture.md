# TradeDrift Matching Engine

**Document:** 02_System_Architecture.md  
**Service:** Matching Engine  
**Version:** V1.0  
**Status:** ✅ Design Complete  
**Last Updated:** July 2026

---

# 1. Purpose

This document describes the internal architecture of the TradeDrift Matching Engine.

The Matching Engine is designed around independent **Market Engines**, where each Market Engine exclusively owns a single trading pair and processes all events for that market sequentially.

The architecture is designed so that it can run as:

- **V1:** Single Matching Engine Node
- **Future:** Multiple Matching Engine Nodes (Cluster)

The internal design remains identical in both deployment modes.

---

# 2. Design Goals

The architecture is designed around the following goals.

- Deterministic order matching
- Price-Time Priority
- Low latency
- High throughput
- Fault isolation
- Memory-first processing
- Infrastructure independence
- Horizontal scalability
- Easy recovery
- High observability

---

# 3. Architecture Overview

```
                        Kafka

               OrderCreated
          OrderCancelRequested

                     │

             Kafka Consumer

                     │

      Assigned Kafka Partitions

                     │

──────────────────────────────────────────────

          Matching Engine Node

                     │

          Market Engine Manager

                     │

      Creates Market Engines

      based on assigned partitions

         ┌────────┬────────┬────────┐

         ▼        ▼        ▼

      BTC      ETH      SOL

      Engine   Engine   Engine

         ▼        ▼        ▼

    Output Queue Output Queue Output Queue

──────────────────────────────────────────────

                     │

                     ▼

            Publisher Layer

        ├── Kafka Events

        ├── Redis Projection

        ├── Metrics

        └── Checkpoints
```

---

# 4. Deployment Modes

## V1 (This Version)

```
Matching Engine Node

├── BTC Engine

├── ETH Engine

├── SOL Engine

└── DOGE Engine
```

One node owns all markets.

No distributed coordination is required.

---

## Future (Cluster)

```
Node A

BTC

ETH
```

```
Node B

SOL

DOGE
```

```
Node C

ADA

XRP
```

Markets are distributed across multiple nodes.

No changes are required to Market Engine implementation.

Only deployment changes.

---

# 5. Matching Engine Node

A Matching Engine Node is the deployable application.

Responsibilities

- Join Kafka Consumer Group
- Consume assigned partitions
- Create Market Engines
- Recover Market Engines
- Publish results
- Expose metrics
- Expose health endpoints

The node itself performs **no matching**.

---

# 6. Kafka Consumer

Each Matching Engine Node contains one Kafka Consumer.

Responsibilities

- Read assigned partitions
- Deserialize messages
- Validate event schema
- Forward events to the appropriate Market Engine

The consumer performs **no business logic**.

> **Client library note:** Cooperative Sticky Rebalancing (Section 14) requires a Go Kafka client that actually supports pluggable/cooperative consumer group assignors — e.g. `confluent-kafka-go` (librdkafka-based) or `sarama`. The plain `segmentio/kafka-go` consumer group implementation does not expose the same control and would not deliver the future rebalancing behavior this doc assumes. Not a blocker for V1 (single node, no rebalancing occurs), but the client library choice should be made with this requirement in mind before the cluster mode is built.

---

# 7. Market Engine Manager

The Market Engine Manager controls the lifecycle of Market Engines.

Responsibilities

- Create Market Engine
- Destroy Market Engine
- Recover Market Engine
- Track active engines
- Handle partition assignment changes (future)

The manager never performs matching.

---

# 8. Market Engine

A Market Engine is the smallest independently executable unit.

Each Market Engine owns

- One trading pair
- One Kafka partition
- One Input Queue
- One Event Loop
- One Matching Core
- One Order Book
- One Output Queue

No Market Engine shares state.

---

# 9. Internal Market Engine

```
Input Queue

↓

Event Loop

↓

Matching Core

↓

Order Book

↓

Output Queue
```

The Matching Core is completely isolated from infrastructure.

---

# 10. Event Processing

```
Kafka Consumer

↓

Input Queue

↓

Event Loop

↓

Matching Core

↓

Output Queue

↓

Publisher Layer
```

The Matching Core never communicates with Kafka directly.

---

# 11. Event Loop

Each Market Engine owns exactly one Event Loop.

```
for {

    event := <-inputQueue

    result := matchingCore.Process(event)

    outputQueue <- result

}
```

Only this Event Loop modifies the Order Book.

This guarantees

- deterministic execution
- no locks
- no race conditions

---

# 12. Matching Core

Responsibilities

- Process OrderCreated
- Process OrderCancelRequested
- Match orders
- Update Order Book
- Generate Matching Results

The Matching Core does NOT

- Publish Kafka
- Update Redis
- Record Metrics
- Persist Checkpoints

---

# 13. Publisher Layer

Consumes Output Queues.

Responsibilities

Publish

- TradeExecuted
- OrderFilled
- OrderCancelled
- OrderPartiallyFilled

Update

- Redis
- Metrics
- Recovery metadata

The Matching Core never waits for these operations.

---

# 14. Kafka Design Requirements

TradeDrift relies on Kafka for deterministic event ordering.

## Event Key

Every event must use

```
key = market_id
```

This guarantees all events for a market are written to the same partition.

---

## Partition Invariant

The following topics MUST

- use identical partition counts
- use identical partitioning strategy
- be consumed by the same consumer group

Topics

- OrderCreated
- OrderCancelRequested

This guarantees every Market Engine receives all events for its market in the correct order.

---

## Consumer Group

Every Matching Engine Node joins the same Kafka Consumer Group.

Kafka assigns partitions automatically.

TradeDrift does not implement a custom routing layer.

---

## Rebalancing

### V1

Only one Matching Engine Node exists.

No rebalance occurs.

---

### Future

TradeDrift will use **Cooperative Sticky Rebalancing**.

This ensures only the partitions being reassigned are paused.

Markets that remain on the same node continue matching without interruption.

See Section 6 for the client library requirement this depends on.

---

# 15. Startup

```
Application Starts

↓

Load Configuration

↓

Connect Kafka

↓

Receive Assigned Partitions

↓

Create Market Engines

↓

Recover Order Books

↓

Start Event Loops

↓

Ready
```

---

# 16. Shutdown

```
Stop Kafka Consumer

↓

Finish Current Match

↓

Flush Output Queue

↓

Persist Recovery Metadata

↓

Shutdown
```

---

# 17. Future Cluster Behaviour

Future versions support multiple Matching Engine Nodes.

When a node joins or leaves the Consumer Group

Kafka

↓

Rebalances partitions

↓

Market Engine Manager

↓

Starts or Stops Market Engines

↓

Recovery

↓

Resume Matching

Detailed handoff and partition revocation behaviour is documented in **08_Recovery_Strategy.md**.

---

# 18. Architecture Principles

The architecture follows

- Single Responsibility
- Event-Driven Architecture
- Clean Architecture
- Memory First
- Deterministic Processing
- Infrastructure Isolation
- Fault Isolation
- Horizontal Scalability
- Low Latency

---

# 19. Internal Package Structure

```
matching-engine/

cmd/

internal/

    node/

    kafka/

    market/

        manager.go

        engine.go

        event_loop.go

    matcher/

    orderbook/

    publisher/

    recovery/

    projection/

    metrics/

    config/
```

---

# 20. References

Implementation details are covered in

- 03_Order_Book.md
- 04_Data_Structures.md
- 05_Matching_Algorithm.md
- 06_Event_Contracts.md
- 07_Concurrency_Model.md
- 08_Recovery_Strategy.md
- 09_Redis_Projection.md
- 10_Failure_Handling.md
- 11_Monitoring.md
- 12_Sequence_Diagrams.md
- 13_Future_Enhancements.md