# TradeDrift — Architecture Blueprint

> **Status:** 🚧 In Design (V1)

---

# Purpose

This document describes the high-level architecture of TradeDrift and the reasoning behind its major architectural decisions.

The detailed implementation of each service is documented separately in the service design documents.

---

# Architecture Goals

TradeDrift is designed to achieve:

- Scalability
- Reliability
- Maintainability
- Fault Isolation
- Event-Driven Communication
- Production-style Architecture
- Educational Transparency

---

# Architectural Principles

## Microservices

Each business capability is implemented as an independent service.

Examples:

- Authentication
- Wallet
- Orders
- Matching
- Settlement
- Portfolio

Each service owns:

- Its own database
- Business rules
- gRPC API

---

## Event-Driven Communication

Services communicate asynchronously whenever immediate responses are unnecessary.

Kafka is used for:

- OrderCreated
- TradeExecuted
- TradeSettled
- PortfolioUpdated
- NotificationCreated

Benefits

- Loose coupling
- Better scalability
- Independent deployments

---

## Synchronous Communication

Critical user-facing operations use gRPC.

Examples

Authentication

↓

Wallet Reservation

↓

Health Checks

Benefits

- Immediate feedback
- Lower latency
- Simpler validation

---

## Transactional Outbox

TradeDrift uses the Transactional Outbox Pattern whenever database updates must produce Kafka events.

```
Save Order
+
Save Outbox Event

↓

Commit

↓

Outbox Publisher

↓

Kafka
```

Benefits

- No dual-write problem
- Reliable event publishing

---

## Saga Pattern

TradeDrift uses choreography-based Saga workflows.

Example

```
Reserve Funds

↓

Order Created

↓

Matching Engine

↓

Settlement

↓

Portfolio

↓

Notification
```

No central orchestrator is used.

Each service reacts to events.

---

## Identifier Strategy

Every aggregate uses UUIDv7.

The owning service generates the identifier before persistence.

The same identifier is reused across:

- PostgreSQL
- gRPC
- Kafka
- Logs
- Tracing

---

## Data Ownership

Every service owns its own database.

Services never access another service's database directly.

Communication happens only through:

- gRPC
- Kafka

---

## Service Responsibilities

| Service | Responsibility |
|----------|---------------|
| API Gateway | HTTP entry point |
| Authentication | Identity |
| Order | Order lifecycle |
| Wallet | Balances & reservations |
| Matching Engine | Price-time priority |
| Settlement | Post-match coordination |
| Trade | Trade persistence |
| Portfolio | Holdings & PnL |
| Market | Market data |
| Notification | User notifications |

---

# Design Philosophy

TradeDrift prioritizes:

- Simplicity
- Clear ownership
- Event-driven workflows
- Educational value
- Production-inspired architecture

rather than reproducing every complexity of a real exchange.