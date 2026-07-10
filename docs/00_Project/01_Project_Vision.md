# TradeDrift — Project Vision

> **Version:** 1.0
>
> **Status:** Active Design
>
> **Document:** 01
>
> **Last Updated:** July 2026

---

# Executive Summary

TradeDrift is a production-inspired cryptocurrency exchange simulator engineered to reproduce the technical architecture and operational behavior of a real cryptocurrency exchange while trading exclusively in virtual assets.

Unlike conventional paper trading platforms that only simulate market prices and portfolio performance, TradeDrift models the complete exchange lifecycle—from authentication and wallet reservation through matching, settlement, portfolio updates, and real-time market data.

The platform combines production-style microservices, event-driven communication, a real-time matching engine, and exchange administration into a realistic learning environment with zero financial risk.

TradeDrift is designed for:

- Traders
- Students
- Software Engineers
- Educators

who want to understand how modern exchanges actually operate.

---

# Vision

To become the most realistic, transparent, and technically credible simulated cryptocurrency exchange available.

TradeDrift aims to expose—not hide—the internal mechanics of a modern exchange by allowing users to observe how orders are validated, funds are reserved, trades are matched, settlements occur, portfolios are updated, and markets evolve in real time.

Rather than simplifying exchange mechanics, TradeDrift reproduces them.

---

# Mission

TradeDrift exists to provide a safe environment where users can:

- Learn cryptocurrency trading
- Understand exchange mechanics
- Study production system architecture
- Explore event-driven distributed systems
- Build fintech engineering skills

while never risking real money.

---

# Objectives

## Realistic Trading

Model real exchange behavior including:

- Matching engine
- Order book
- Partial fills
- Market orders
- Limit orders

---

## Production Architecture

Demonstrate:

- Microservices
- Kafka
- gRPC
- Saga Pattern
- Transactional Outbox
- Settlement
- Redis
- WebSockets

---

## Zero Financial Risk

Every balance and every trade is virtual.

No real cryptocurrency or fiat currency is ever stored or transferred.

---

## Educational Transparency

Every major system should be understandable.

Users should be able to study not only the interface but also the architecture that powers it.

---

## Future Expansion

The architecture should support:

- Advanced order types
- AI coaching
- Risk analysis
- Competitions
- Copy trading

without redesigning the core platform.

---

# Why TradeDrift Exists

Most trading simulators teach only price movement.

They rarely teach:

- Order books
- Matching
- Liquidity
- Exchange operations
- Settlement
- Distributed systems

Real exchanges contain these systems, but they are proprietary and inaccessible.

TradeDrift bridges this gap by providing an open, production-inspired implementation built for learning.

---

# Target Audience

## Traders

Learn:

- Order execution
- Risk-free trading
- Market mechanics
- Portfolio management

---

## Software Engineers

Study:

- Distributed systems
- Kafka
- gRPC
- Matching engines
- Saga workflows
- Transactional Outbox

---

## Students

Understand:

- System Design
- Backend Architecture
- Event-driven systems

---

## Educators

Use TradeDrift as a practical teaching platform for:

- Trading
- Distributed Systems
- Software Architecture

---

# Product Features

## User Management

- Registration
- Authentication
- Profile Management

## Wallet

- Virtual wallets
- Configurable seed balances
- Fund reservation
- Transaction history

## Trading

- Market Orders
- Limit Orders
- Cancellation
- Partial fills

## Matching Engine

- Price-Time Priority
- FIFO
- Order Book

## Settlement

- Atomic trade settlement
- Wallet updates
- Trade persistence
- Portfolio updates

## Portfolio

- Holdings
- Average Entry
- Realized PnL
- Unrealized PnL

## Market

- Live Order Book
- Recent Trades
- OHLC
- Market Statistics

## Notifications

- Trade updates
- Order updates
- WebSocket push

## Administration

- User Management
- Asset Listing
- Trading Pair Management
- Market Maker
- Liquidity Management
- Engine Controls

---

# Exchange Lifecycle

```

Register

↓

Authentication

↓

Virtual Wallet Created

↓

Virtual USDT Seeded

↓

Place Order

↓

Reserve Funds

↓

Matching Engine

↓

Settlement Service

↓

Wallet Updated

↓

Trade Recorded

↓

Portfolio Updated

↓

Market Updated

↓

Notification Sent

↓

Trade History Available

```

---

# Simulation Mode

TradeDrift is permanently operated in simulation mode.

- Virtual Assets
- Virtual USDT
- Virtual Wallets
- No Deposits
- No Withdrawals
- No Blockchain Integration

---

# System Market Maker

Every newly listed market begins with zero liquidity.

TradeDrift automatically provisions a System Market Maker that:

- Receives configurable seed balances
- Places buy orders
- Places sell orders
- Provides initial liquidity
- Gradually yields to organic trading

---

# Future Roadmap

## V1

Exchange Core

## V2

Infrastructure

## V3

Professional Trading

## V4

AI Intelligence

---

# Functional Requirements

See detailed service documents.

---

# Non-Functional Requirements

TradeDrift is designed around:

- Low latency
- High availability
- Eventual consistency
- Horizontal scalability
- Service independence
- Idempotent event processing
- Observability
- Security

---

# Related Documents

- 00_Project_Overview.md
- 02_System_Architecture.md
- API Gateway
- Authentication Service
- Order Service
- Wallet Service
- Matching Engine
- Settlement Service
- Trade Service
- Portfolio Service
- Market Service
- Notification Service