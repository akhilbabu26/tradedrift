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
 ├── Wallet
 ├── Market
 └── Notification

Order
 │
 ├── Wallet (Reserve)
 └── Kafka

Matching Engine
 │
 └── Kafka

Settlement
 │
 ├── Wallet
 ├── Trade
 ├── Portfolio
 ├── Market
 └── Notification
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

TradeExecuted

↓

Settlement

↓

Trade Service

↓

Wallet

↓

Portfolio

↓

Market

↓

Notification
```

---

## Cancel Order

```
Client

↓

Order Service

↓

Outbox

↓

Kafka

↓

Matching Engine

↓

OrderCancelled

↓

Settlement

↓

Wallet Release
```

---

# Infrastructure

## PostgreSQL

- Users
- Orders
- Wallets
- Trades
- Portfolio

---

## Redis

- Rate limiting
- JWT blacklist
- Order book cache
- Sessions

---

## Kafka

Core topics

- OrderCreated
- OrderCancelRequested
- OrderCancelled
- TradeExecuted
- TradeRecorded
- TradeSettled
- PortfolioUpdated
- NotificationCreated

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