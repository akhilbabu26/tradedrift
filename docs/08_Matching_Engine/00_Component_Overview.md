# TradeDrift Matching Engine — Component Overview

**Document:** 00_Component_Overview.md
**Service:** Matching Engine
**Version:** V1.0
**Status:** ✅ Design Complete
**Last Updated:** July 2026

---

# 1. Purpose

This document provides a high-level architectural overview of the Matching Engine service.

Unlike the detailed design documents that explain algorithms and data structures, this document focuses only on the major runtime components and how they interact.

It is intended to give readers a quick understanding of the service before diving into implementation details.

---

# 2. High-Level Architecture

```text
                    ┌───────────────────────────────┐
                    │        Order Service          │
                    │  Publishes OrderCreated /     │
                    │  OrderCancelRequested Events  │
                    └──────────────┬────────────────┘
                                   │
                                   │ Kafka
                                   ▼
                         ┌────────────────────┐
                         │ orders.submitted   │
                         └─────────┬──────────┘
                                   │
                                   ▼
                     ┌──────────────────────────┐
                     │ Kafka Consumer           │
                     │ (per Market Engine)      │
                     └──────────┬───────────────┘
                                │
                                ▼
                     ┌──────────────────────────┐
                     │ Input Queue              │
                     │ FIFO Channel             │
                     └──────────┬───────────────┘
                                │
                                ▼
                  ┌──────────────────────────────────┐
                  │ Event Loop (Single Goroutine)    │
                  │                                  │
                  │ • Validate Events                │
                  │ • Process Matching               │
                  │ • Update Order Book              │
                  │ • Generate MatchResult           │
                  └──────────┬───────────────────────┘
                             │
          ┌──────────────────┼──────────────────┐
          ▼                  ▼                  ▼
 ┌────────────────┐  ┌────────────────┐  ┌────────────────┐
 │ Matching Core  │  │ Order Book     │  │ Order Index    │
 │ Price-Time     │  │ Price Levels   │  │ O(1) Lookup    │
 │ Matching       │  │ Linked Lists   │  │                │
 └────────────────┘  └────────────────┘  └────────────────┘
                             │
                             ▼
                  ┌──────────────────────────┐
                  │ MatchResult              │
                  │ Trades + Orders + Depth  │
                  └──────────┬───────────────┘
                             │
                             ▼
                     ┌────────────────────┐
                     │ Output Queue       │
                     └─────────┬──────────┘
                               │
                               ▼
                   ┌──────────────────────────┐
                   │ Publisher                │
                   │                          │
                   │ • Publish Kafka Events   │
                   │ • Update Redis           │
                   │ • Persist Checkpoint     │
                   └──────┬─────────┬─────────┘
                          │         │
              ┌───────────┘         └────────────┐
              ▼                                  ▼
      trades.executed                     orderbook.updates
          (Kafka)                             (Redis)
              │                                  │
              ▼                                  ▼
    Settlement Service                  Market Data / WS
```

---

# 3. Component Responsibilities

| Component | Responsibility |
|-----------|----------------|
| **Kafka Consumer** | Reads `OrderCreated` and `OrderCancelRequested` events from Kafka. |
| **Input Queue** | Buffers incoming events and preserves FIFO ordering. |
| **Event Loop** | Single owner of the Order Book. Processes one event at a time. |
| **Matching Core** | Executes the price-time priority matching algorithm. |
| **Order Book** | Stores all resting orders in memory. |
| **Order Index** | Enables O(1) order lookup for cancellations. |
| **MatchResult** | Immutable result of processing one input event. |
| **Output Queue** | Transfers MatchResults to the Publisher without blocking the Event Loop. |
| **Publisher** | Publishes execution events, updates Redis projections, and writes checkpoints. |

---

# 4. Ownership Boundaries

Only one component owns the in-memory Order Book.

```text
                Event Loop
                     │
         Owns every mutable object
                     │
                     ▼
      Order Book / Price Levels / Orders

Publisher

    • Never touches OrderBook memory
    • Consumes immutable MatchResults only
```

This ownership model eliminates data races and allows the matching hot path to operate without mutexes.

---

# 5. Event Flow

```
Kafka

↓

Kafka Consumer

↓

Input Queue

↓

Event Loop

↓

Matching Core

↓

Order Book

↓

MatchResult

↓

Output Queue

↓

Publisher

↓

Kafka + Redis + PostgreSQL
```

---

# 6. Design Principles

The Matching Engine is built around five core principles:

- **Single-threaded matching** — one Event Loop per market eliminates locking.
- **In-memory execution** — matching never depends on databases.
- **Asynchronous publishing** — external I/O never blocks the matching path.
- **Deterministic replay** — recovery reconstructs the Order Book from Kafka events.
- **Strict ownership** — only the Event Loop mutates matching state.

---

# 7. Related Documents

- `01_Overview.md`
- `02_System_Architecture.md`
- `07_Concurrency_Model.md`
- `08_Recovery_Strategy.md`
- `13_Flow_Diagrams.md`
- `14_State_Diagrams.md`
- `15_Design_Invariants.md`