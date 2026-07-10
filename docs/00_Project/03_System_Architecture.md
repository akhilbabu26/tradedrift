# TradeDrift — System Architecture

> **Status:** 🚧 In Design (V1)

---

# High-Level Architecture

```
                Web Client
                     │
             REST / WebSocket
                     │
             API Gateway
                     │
             gRPC Services
                     │
 ┌───────────────────────────────────────────────┐
 │                                               │
 │ Authentication Service                        │
 │ Order Service                                 │
 │ Wallet Service                                │
 │ Matching Engine                               │
 │ Settlement Service                            │
 │ Trade Service                                 │
 │ Portfolio Service                             │
 │ Market Service                                │
 │ Notification Service                          │
 └───────────────────────────────────────────────┘
                     │
          PostgreSQL / Redis / Kafka
```

---

# Request Flow

```
Client

↓

API Gateway

↓

Authentication

↓

Order Service

↓

Wallet Reservation

↓

Kafka

↓

Matching Engine

↓

Settlement Service

↓

Trade

↓

Wallet

↓

Portfolio

↓

Market

↓

Notification

↓

WebSocket
```

---

# Service Dependencies

```
Gateway
 │
 ├── Authentication
 ├── Order
 ├── Wallet (read-only: balances, transactions)
 ├── Market
 └── Notification

Order
 │
 ├── Wallet (gRPC: ReserveFunds, ReleaseFunds)
 ├── Market (gRPC: GetMarket)
 └── Kafka (publish: OrderCreated, OrderCancelRequested)

Matching Engine
 │
 ├── Market (gRPC: GetMarket - startup config)
 └── Kafka (consume: OrderCreated, OrderCancelRequested;
          publish: TradeExecuted, OrderCancelled)

Settlement
 │
 ├── Kafka (consume: TradeExecuted)
 └── Wallet (gRPC: SettleTrade)

Wallet
 │
 └── Kafka (publish via outbox: TradeSettled)

Portfolio
 │
 ├── Wallet (gRPC: GetBalances)
 ├── Trade (gRPC: ListUserTrades)
 └── Kafka (consume: TradeSettled; publish: PortfolioUpdated via outbox)

Trade
 │
 └── Kafka (consume: TradeSettled)

Market
 │
 ├── Wallet (gRPC: GetSupportedAssets)
 └── Kafka (consume: TradeExecuted)

Notification
 │
 └── Kafka (consume: TradeSettled, PortfolioUpdated, OrderCancelled, OrderCreated)
```

---

# Data Flow

## Create Order

```
Client

↓

API Gateway

↓

Order Service

↓

Reserve Funds

↓

Save Order

↓

Outbox

↓

Kafka

↓

Matching Engine
```

---

## Execute Trade

```
Matching Engine

↓

Kafka (TradeExecuted)

↓

Settlement Service

↓

Wallet (gRPC: SettleTrade)

↓

Kafka (TradeSettled, published by Wallet via outbox)

↓

Portfolio + Market + Notification (consume independently)
```

---

## Cancel Order

```
Client

↓

Order Service (status → CANCELLING)

↓

Outbox

↓

Kafka (OrderCancelRequested)

↓

Matching Engine

↓

Kafka (OrderCancelled)

↓

Order Service (status → CANCELLED)

↓

Wallet ReleaseFunds (gRPC, called by Order Service)
```

---

# Infrastructure

## PostgreSQL

- Users
- Orders
- Wallets
- Trades
- Portfolio
- Trading pairs / markets  (owned by Market Service — read by ME on startup as config)
- ME Kafka checkpoint  (one row per partition: `{topic, partition, offset}` — updated by Matching Engine for recovery)

---

## Redis

- Rate limiting  (Gateway — Redis token bucket)
- JWT blacklist  (Auth Service — access token revocation on logout)
- Sessions  (refresh token state lives in Postgres, not Redis)
- Order book read replica  (Matching Engine pushes book snapshot to Redis after each match; Market Service and WebSocket read from here — Redis is NOT the source of truth for matching, the in-memory book is)

---

## Kafka

Core topics

- `OrderCreated` — published by Order Service (via outbox), consumed by Matching Engine
- `OrderCancelRequested` — published by Order Service (via outbox), consumed by Matching Engine
- `OrderCancelled` — published by Matching Engine, consumed by Order Service (status update + fund release)
- `TradeExecuted` — published by Matching Engine, consumed by Settlement Service, Market Service
- `TradeSettled` — published by Wallet Service (via outbox, after settlement commit), consumed by **Trade Service**, Portfolio Service, Notification Service
- `PortfolioUpdated` — published by Portfolio Service (via outbox), consumed by Notification Service
- `NotificationCreated` — published by Notification Service, pushed via WebSocket Gateway

---

## WebSocket

Real-time updates

- Order Book
- Trades
- Portfolio
- Notifications

---

# Future Architecture

V2

- Kubernetes
- Distributed Tracing

V3

- Advanced Order Types

V4

- AI Coach
- Behaviour Analysis