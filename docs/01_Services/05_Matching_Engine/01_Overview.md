# TradeDrift Matching Engine

**Document:** 01_Overview.md  
**Service:** Matching Engine  
**Version:** V1.0  
**Status:** ✅ Design Complete  
**Last Updated:** July 2026

---

# 1. Purpose

The Matching Engine is the core component of the TradeDrift exchange. It is responsible for maintaining the live order book for every trading pair and matching buy and sell orders according to the exchange's trading rules.

Unlike other services in the system, the Matching Engine focuses on one responsibility only:

> **Determine whether an incoming order can be matched against existing liquidity and generate trade executions accordingly.**

The Matching Engine is intentionally designed to be deterministic, stateless with respect to business workflows, and independent from financial operations such as wallet updates or settlement.

---

# 2. Overview

Whenever a user submits an order, the Order Service validates the request, reserves the required funds, persists the order, and publishes an `OrderCreated` event.

The Matching Engine consumes these events in strict market order, updates the in-memory order book, performs matching using Price-Time Priority, and publishes trade execution events for downstream services.

The Matching Engine **does not own business workflows**.

Its responsibility ends once matching is completed and the appropriate events are published.

---

# 3. Responsibilities

The Matching Engine is responsible for:

- Maintaining an in-memory order book for every active trading pair.
- Processing orders sequentially for each market.
- Matching buy and sell orders using Price-Time Priority.
- Supporting:
  - Limit Orders
  - Market Orders (IOC)
  - Partial fills
  - Full fills
  - Order cancellation
- Generating unique `trade_id` values.
- Publishing `TradeExecuted` events.
- Publishing `OrderCancelled` events.
- Publishing order book updates to Redis.
- Consuming administrative commands (Halt/Resume) via Kafka to halt/resume matching per market.
- Recovering the order book after restart.
- Maintaining Kafka processing checkpoints.

---

# 4. Out of Scope

The Matching Engine intentionally does **not** perform any of the following:

### User Management

- Authentication
- Authorization
- User validation

Owned by:

- Authentication Service
- Order Service

---

### Wallet Operations

The Matching Engine never:

- reserves funds
- releases funds
- updates balances
- calculates available balance

Owned by:

- Wallet Service

---

### Settlement

The Matching Engine never transfers assets.

Settlement is completely owned by:

- Settlement Service

---

### Portfolio Updates

Portfolio calculations are handled by:

- Portfolio Service

---

### Notifications

Trade notifications are produced by:

- Notification Service

---

### Market Administration

The Matching Engine does not own:

- trading pairs
- tick sizes
- lot sizes

These are managed by:

- Market Service

The Matching Engine only reads this configuration.

---

# 5. Design Principles

The Matching Engine follows several core architectural principles.

## Single Responsibility

The engine exists only to match orders.

Everything else belongs to another service.

---

## Deterministic Processing

Given the same sequence of orders, the engine must always produce the same result.

There should never be randomness in the matching process.

---

## Sequential Processing Per Market

Each market has exactly one order book.

Only one worker may modify that order book.

This completely avoids race conditions.

---

## Memory First

The active order book lives entirely in memory.

No database queries occur during order matching.

Persistent storage exists only for recovery.

---

## Event Driven

The Matching Engine communicates only through events.

It never calls Wallet Service.

It never calls Settlement Service.

It never calls Portfolio Service.

Instead it publishes events which downstream services consume independently.

---

## Loose Coupling

The Matching Engine knows nothing about downstream consumers.

Whether ten services consume `TradeExecuted` or only one is irrelevant to the Matching Engine.

---

# 6. Position within TradeDrift

```
                    Client
                       │
                       ▼
                API Gateway
                       │
                       ▼
                Order Service
                       │
                OrderCreated
                       │
                 Kafka Topic
                       │
                       ▼
               Matching Engine
             ┌───────────────────┐
             │                   │
             │ In-Memory Books   │
             │                   │
             └───────────────────┘
                │            │
                ▼            ▼
        TradeExecuted   OrderCancelled
                │            │
                ▼            ▼
        Settlement    Order Service
        Service       (status → CANCELLED
                │      ReleaseFunds)
                ▼
         Wallet Service
                │
                ▼
        Portfolio Service
```

---

# 7. High-Level Workflow

The Matching Engine operates as an event consumer.

The typical order lifecycle is:

```
Client

↓

Order Service

↓

Validate

↓

Reserve Funds

↓

Persist Order

↓

Publish OrderCreated

↓

Kafka

↓

Matching Engine

↓

Match Against Order Book

↓

Generate Trade(s)

↓

Publish TradeExecuted

↓

Settlement Service

↓

Wallet Service

↓

Portfolio Service
```

---

# 8. Core Concepts

The Matching Engine is built around several core concepts.

## Order Book

Each trading pair owns its own order book.

Example:

- BTC-USDT
- ETH-USDT
- SOL-USDT

Order books never share state.

---

## Price-Time Priority

Matching follows two rules:

1. Better price wins.
2. If prices are equal, older orders execute first.

This guarantees fair execution.

---

## Resting Orders

Only limit orders can rest on the book.

Market orders never become resting orders.

---

## Immediate-Or-Cancel (IOC)

Market orders execute immediately.

Any remaining quantity is cancelled.

Market orders never wait for future liquidity.

---

## Good Till Cancelled (GTC)

Limit orders remain active until:

- fully filled
- cancelled by the user

---

# 9. Service Interactions

| Direction | Service | Event / Action |
|-----------|---------|----------------|
| ME **consumes from** | Order Service | `OrderCreated`, `OrderCancelRequested` |
| ME **publishes to** | Settlement Service | `TradeExecuted` |
| ME **publishes to** | Order Service | `OrderCancelled` |
| ME **writes to** | Redis | Live order book snapshot (read replica, after each match) |
| ME **reads from** | Market Service | Trading pair configuration (startup only) |
| ME **reads / writes** | PostgreSQL | Kafka checkpoint rows only (`{topic, partition, offset}`) |

---

# 10. Non-Functional Requirements

The Matching Engine is designed with the following goals.

## Low Latency

Order matching should complete within a few milliseconds.

---

## High Throughput

The architecture should support processing thousands of orders per second.

---

## Fault Tolerance

Unexpected crashes must not permanently lose the order book.

Recovery should occur automatically.

---

## Consistency

Order matching must always produce deterministic results.

---

## Scalability

Markets are isolated from one another, allowing horizontal scaling in future versions.

---

# 11. V1 Scope

Version 1 supports:

- Limit Orders
- Market Orders
- GTC
- IOC
- Partial fills
- Full fills
- Order cancellation
- Price-Time Priority
- In-memory order books
- Kafka event processing
- Redis order book projection
- Recovery after restart

---

# 12. Future Scope

Future versions may introduce:

- Stop Loss Orders
- Take Profit Orders
- Iceberg Orders
- Fill-Or-Kill (FOK)
- Post-Only Orders
- Self Trade Prevention
- Matching Engine sharding
- Multi-region deployment
- Dynamic market creation
- Auction-based market opening
- Maker/Taker fee optimization

---

# 13. References

This document provides a high-level overview of the Matching Engine.

Detailed implementation is described in the following documents:

- 02_System_Architecture.md
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
- 13_Flow_Diagrams.md
- 14_State_Diagrams.md
- 15_Design_Invariants.md
- 16_Future_Enhancements.md
- 17_Performance_Benchmarking.md