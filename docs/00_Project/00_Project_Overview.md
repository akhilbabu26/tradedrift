# TradeDrift

> **Status:** 🚧 In Design (V1)
>
> A production-inspired cryptocurrency exchange simulator built to demonstrate how real exchanges work internally through a microservices architecture.

---

# Overview

TradeDrift is a production-inspired cryptocurrency exchange simulator engineered to reproduce the technical architecture and operational behavior of a real cryptocurrency exchange while trading exclusively in virtual assets.

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

## 00_Project (Core)
* [README.md](README.md)
* [00_Project_Overview.md](00_Project_Overview.md)
* [01_Project_Vision.md](01_Project_Vision.md)
* [02_Architecture_Blueprint.md](02_Architecture_Blueprint.md)
* [03_System_Architecture.md](03_System_Architecture.md)
* [Glossary.md](Glossary.md)

## 01_Services
* [README.md](../01_Services/README.md)
* [00_System_Flows.md](../01_Services/00_System_Flows.md)
* [04_API_Gateway.md](../01_Services/04_API_Gateway/04_API_Gateway.md)
* [05_Authentication_Service.md](../01_Services/05_Authentication_Service/05_Authentication_Service.md)
* [07_Wallet_Service.md](../01_Services/07_Wallet_Service/07_Wallet_Service.md)
* [08_Order_Service.md](../01_Services/08_Order_Service/08_Order_Service.md)
* [09_Matching_Engine README](../01_Services/09_Matching_Engine/README.md)
* [Settlement_Service.md](../01_Services/Settlement_Service/Settlement_Service.md)
* [Trade_Service.md](../01_Services/Trade_Service/Trade_Service.md)
* [10_Market_Service.md](../01_Services/10_Market_Service/10_Market_Service.md)
* [11_Portfolio_Service.md](../01_Services/11_Portfolio_Service/11_Portfolio_Service.md)
* [12_Notification_Service.md](../01_Services/12_Notification_Service/12_Notification_Service.md)

## 02_Platform (Infrastructure Specifications)
* [README.md](../02_Platform/README.md)
* [13_Event_Driven_Architecture.md](../02_Platform/13_Event_Driven_Architecture.md)
* [14_Fund_Reservation_Contract.md](../02_Platform/14_Fund_Reservation_Contract.md)
* [15_Kafka_Topic_Design.md](../02_Platform/15_Kafka_Topic_Design.md)
* [16_gRPC_Contracts.md](../02_Platform/16_gRPC_Contracts.md)
* [17_Redis_Architecture.md](../02_Platform/17_Redis_Architecture.md)
* [18_PostgreSQL_Design.md](../02_Platform/18_PostgreSQL_Design.md)
* [19_WebSocket_Architecture.md](../02_Platform/19_WebSocket_Architecture.md)
* [20_Deployment.md](../02_Platform/20_Deployment.md)
* [21_Observability.md](../02_Platform/21_Observability.md)
* [22_Disaster_Recovery.md](../02_Platform/22_Disaster_Recovery.md)
* [24_Admin_Workflows.md](../02_Platform/24_Admin_Workflows.md)
* [25_Production_Infrastructure_Architecture.md](../02_Platform/25_Production_Infrastructure_Architecture.md)

## 03_Standards
* [README.md](../03_Standards/README.md)
* [ID_Correlation_Standard.md](../03_Standards/ID_Correlation_Standard.md)
* [02_Shared_Foundation_Design.md](../03_Standards/02_Shared_Foundation_Design.md)

## 04_Audits
* [README.md](../04_Audits/README.md)
* [01_Trading_Lifecycle_Audit.md](../04_Audits/01_Trading_Lifecycle_Audit.md)
* [02_Data_Consistency_Audit.md](../04_Audits/02_Data_Consistency_Audit.md)
* [03_Security_Audit.md](../04_Audits/03_Security_Audit.md)
* [04_Operational_Readiness_Audit.md](../04_Audits/04_Operational_Readiness_Audit.md)
* [05_Disaster_Recovery_Audit.md](../04_Audits/05_Disaster_Recovery_Audit.md)
* [06_Admin_Platform_Audit.md](../04_Audits/06_Admin_Platform_Audit.md)
* [07_Scalability_Audit.md](../04_Audits/07_Scalability_Audit.md)
* [08_Latency_Performance_Audit.md](../04_Audits/08_Latency_Performance_Audit.md)

## 05_Database
* [README.md](../05_Database/README.md)
* [01_Database_Standards.md](../05_Database/01_Database_Standards.md)
* [02_Auth_Database.md](../05_Database/02_Auth_Database.md)
* [03_Wallet_Database.md](../05_Database/03_Wallet_Database.md)
* [04_Order_Database.md](../05_Database/04_Order_Database.md)
* [05_Settlement_Database.md](../05_Database/05_Settlement_Database.md)
* [06_Portfolio_Database.md](../05_Database/06_Portfolio_Database.md)
* [07_Trade_Database.md](../05_Database/07_Trade_Database.md)
* [08_Notification_Database.md](../05_Database/08_Notification_Database.md)
* [09_Market_Database.md](../05_Database/09_Market_Database.md)
* [10_Index_Strategy.md](../05_Database/10_Index_Strategy.md)
* [11_Migration_Order.md](../05_Database/11_Migration_Order.md)

## 06_APIs
* [README.md](../06_APIs/README.md)
* [01_API_Standards.md](../06_APIs/01_API_Standards.md)
* [02_Authentication_API.md](../06_APIs/02_Authentication_API.md)
* [03_Wallet_API.md](../06_APIs/03_Wallet_API.md)
* [04_Order_API.md](../06_APIs/04_Order_API.md)
* [05_Market_API.md](../06_APIs/05_Market_API.md)
* [06_Notification_API.md](../06_APIs/06_Notification_API.md)
* [07_Portfolio_API.md](../06_APIs/07_Portfolio_API.md)
* [08_Admin_API.md](../06_APIs/08_Admin_API.md)
* [09_WebSocket_API.md](../06_APIs/09_WebSocket_API.md)
* [10_Health_API.md](../06_APIs/10_Health_API.md)

## 07_Development
* [README.md](../07_Development/README.md)
* [01_Project_Structure.md](../07_Development/01_Project_Structure.md)
* [02_Coding_Standards.md](../07_Development/02_Coding_Standards.md)
* [03_Branch_Strategy.md](../07_Development/03_Branch_Strategy.md)
* [04_Testing_Strategy.md](../07_Development/04_Testing_Strategy.md)
* [05_Contribution_Guide.md](../07_Development/05_Contribution_Guide.md)

---

# Current Status

### 1. Service Architectures (Design Phase)
* [x] **API Gateway:** ✅ Designed
* [x] **Authentication Service:** ✅ Designed
* [x] **Wallet Service:** ✅ Designed
* [x] **Order Service:** ✅ Designed
* [x] **Matching Engine:** ✅ Designed
* [x] **Settlement Service:** ✅ Designed
* [x] **Trade Service:** ✅ Designed
* [x] **Portfolio Service:** ✅ Designed
* [x] **Market Service:** ✅ Designed
* [x] **Notification Service:** ✅ Designed

### 2. Platform & Standards (Design Phase)
* [x] **Distributed Tracing & Correlation:** ✅ Designed
* [x] **Shared SDK Foundation:** ✅ Designed
* [x] **Database Outbox Engine:** ✅ Designed
* [x] **Kafka Topic Topology:** ✅ Designed

### 3. Database Architecture (Design Phase)
* [x] **Unified DB Standards:** ✅ Designed
* [x] **8 Service Schema Definitions (DDL SQL):** ✅ Designed & Created
* [x] **Composite Index Strategy:** ✅ Designed
* [x] **Migration Dependency Sequence:** ✅ Designed
* [x] **Vector Database ER / Flow Diagrams:** ✅ Generated (SVGs)

### 4. API Design (Design Phase)
* [x] **REST Contract Standards:** ✅ Designed
* [x] **Idempotency & Rate Limit Catalog:** ✅ Designed
* [x] **API Error Codes Registry:** ✅ Designed
* [x] **Kubernetes Health Probe Spec:** ✅ Designed
* [x] **WebSocket Streaming Frame Spec:** ✅ Designed
* [x] **Vector API Routing Diagrams:** ✅ Generated (SVGs)

### 5. Development Guidelines (Design Phase)
* [x] **Multi-Module Monorepo Layout:** ✅ Designed
* [x] **Linter & Coding Standards:** ✅ Designed
* [x] **Git Branch & Review Strategy:** ✅ Designed
* [x] **Mocking & Integration Testing Strategy:** ✅ Designed
* [x] **Architecture Change Request (ACR) Governance:** ✅ Designed

### 6. Source Implementation Phase
* [ ] **Phase 1: Must Fix Before Code (Shared SDK / Wallet ordering):** ⏳ Pending Code
* [ ] **Phase 2: Local Deployment Execution:** ⏳ Pending Code
* [ ] **Phase 3: Production Hardening:** ⏳ Pending Code

---

# License

MIT