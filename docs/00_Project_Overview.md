# TradeDrift

> **Status:** 🚧 In Design (V1)
>
> A production-inspired cryptocurrency exchange simulator built to demonstrate how real exchanges work internally through a microservices architecture.

---

# Overview

TradeDrift is a production-inspired cryptocurrency paper trading platform that reproduces the architecture and operational behavior of a real cryptocurrency exchange while using entirely virtual assets.

Unlike traditional paper trading applications that only simulate profit and loss, TradeDrift models the complete exchange lifecycle, including:

- Authentication and user management
- Virtual wallet provisioning
- Fund reservation
- Price-time priority order matching
- Trade settlement
- Portfolio management
- Real-time market data
- Event-driven communication
- Exchange administration

The platform is designed to provide a realistic learning environment without exposing users to financial risk.

---

# Goals

TradeDrift has two primary goals.

## Learn Trading

Provide traders with a realistic environment where they can:

- Practice trading without risking money
- Understand how limit and market orders work
- Learn order books
- Understand liquidity
- Observe partial fills
- Build trading discipline

---

## Learn Exchange Architecture

Provide developers with a production-style reference implementation demonstrating:

- Microservices
- gRPC
- Kafka
- Saga Pattern
- Transactional Outbox
- WebSockets
- Matching Engine
- Wallet Reservation
- Settlement
- Portfolio Calculation
- Exchange Operations

---

# Core Features

## Trading

- Spot cryptocurrency trading
- Market Orders
- Limit Orders
- Partial fills
- Order cancellation
- Price-time priority matching

---

## Wallet

- Virtual wallets
- Available balance
- Reserved balance
- Reservation ledger
- Transaction history

---

## Portfolio

- Holdings
- Average entry price
- Realized PnL
- Unrealized PnL

---

## Market

- Live order book
- Live trades
- OHLC candles
- Market statistics

---

## Administration

- User management
- Asset listing
- Trading pair management
- Market Maker management
- Engine controls
- Audit logs

---

# High-Level Architecture

```
                Web / Mobile Client
                        │
                API Gateway (HTTP)
                        │
                  Authentication
                        │
                 Order Service
                        │
               Wallet Reservation
                        │
                 Kafka Events
                        │
               Matching Engine
                        │
              Settlement Service
          ┌───────────┼───────────┐
          │           │           │
       Wallet      Trade     Portfolio
          │           │           │
          └───────Kafka───────────┘
                    │
          Notification Service
                    │
               WebSocket Gateway
                    │
                Connected Clients
```

---

# Technology Stack

## Backend

- Go
- gRPC
- Kafka
- PostgreSQL
- Redis

## Frontend

- React
- TypeScript
- Tailwind CSS

## Infrastructure

- Docker
- Kubernetes (Future)
- Prometheus
- Grafana
- OpenTelemetry

---

# Design Principles

TradeDrift is designed around several engineering principles.

- Event-driven communication
- Microservice architecture
- Domain-driven design
- Eventual consistency
- Saga pattern
- Transactional Outbox
- Stateless services
- Horizontal scalability
- Idempotent event processing

---

# Project Roadmap

## Version 1

Exchange Core

- Authentication
- Wallet
- Orders
- Matching Engine
- Settlement
- Portfolio
- Market
- Notifications

---

## Version 2

Infrastructure Improvements

- Kubernetes
- Distributed tracing
- Better monitoring
- Replay support
- Recovery improvements

---

## Version 3

Professional Trading

- Stop Loss
- Take Profit
- OCO
- Trailing Stop
- Advanced order types

---

## Version 4

AI Intelligence

- AI Coach
- Trade analysis
- Behaviour detection
- Personalized feedback

---

# Documentation

## Core

- Project Overview
- Project Vision
- System Architecture

## Services

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

## Cross-Cutting Architecture

- Kafka Topics
- Saga Pattern
- Transactional Outbox
- Identifier & Correlation Standard
- PostgreSQL Schema
- Redis Layer
- WebSocket Architecture

---

# Current Status

✅ API Gateway Designed

✅ Authentication Service Designed

✅ Order Service Designed

✅ Wallet Service Designed

✅ Matching Engine Designed

✅ Settlement Service Designed

✅ Trade Service Designed

🚧 Portfolio Service

🚧 Market Service

🚧 Notification Service

---

# License

MIT