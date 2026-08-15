# TradeDrift

> **Status:** 🔨 In Development — V1 (Phase 2 of 3)  
> **Last Updated:** August 2026
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
* [04_API_Gateway.md](../01_Services/01_API_Gateway/04_API_Gateway.md)
* [05_Authentication_Service.md](../01_Services/02_Authentication_Service/05_Authentication_Service.md)
* [07_Wallet_Service.md](../01_Services/03_Wallet_Service/07_Wallet_Service.md)
* [08_Order_Service.md](../01_Services/04_Order_Service/08_Order_Service.md)
* [09_Matching_Engine README](../01_Services/05_Matching_Engine/README.md)
* [10_Market_Service.md](../01_Services/06_Market_Service/10_Market_Service.md)
* [11_Portfolio_Service.md](../01_Services/07_Portfolio_Service/11_Portfolio_Service.md)
* [12_Notification_Service.md](../01_Services/08_Notification_Service/12_Notification_Service.md)
* [Settlement_Service.md](../01_Services/09_Settlement_Service/Settlement_Service.md)
* [Trade_Service.md](../01_Services/10_Trade_Service/Trade_Service.md)
* [Admin_Service.md](../01_Services/11_Admin_Service/Admin_Service.md)

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
* [09_Final_Design_Readiness_Audit.md](../04_Audits/09_Final_Design_Readiness_Audit.md)
* [09_Final_Design_Readiness_Audit_v1.md](../04_Audits/09_Final_Design_Readiness_Audit_v1.md)

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

> **Last Updated:** August 15, 2026  
> Overall: Phase 1 (Design) ✅ Complete · Phase 2 (Core Services) 🔨 In Progress · Phase 3 (Matching Engine + Settlement) ⏳ Up Next

---

## Phase 1 — Design (Complete ✅)

### 1.1 Service Architecture Design
| Service | Design Status |
| :--- | :--- |
| API Gateway | ✅ Complete |
| Authentication Service | ✅ Complete |
| Wallet Service | ✅ Complete |
| Order Service | ✅ Complete |
| Matching Engine | ✅ Complete (18 docs) |
| Market Service | ✅ Complete |
| Settlement Service | ✅ Complete |
| Trade Service | ✅ Complete |
| Portfolio Service | ✅ Complete |
| Notification Service | ✅ Complete |
| Admin Service | ✅ Complete |

### 1.2 Platform & Standards Design
| Area | Design Status |
| :--- | :--- |
| Distributed Tracing & Correlation IDs | ✅ Complete |
| Shared Platform SDK Design | ✅ Complete |
| Kafka Topic Topology | ✅ Complete |
| Database Schema Definitions (all 8 services) | ✅ Complete |
| Composite Index Strategy | ✅ Complete |
| gRPC Contract Specifications | ✅ Complete |
| REST API Contracts | ✅ Complete |
| WebSocket Streaming Spec | ✅ Complete |
| Trading Lifecycle & Data Consistency Audits | ✅ Complete |
| Security & Operational Readiness Audits | ✅ Complete |

---

## Phase 2 — Core Services Implementation (In Progress 🔨)

### 2.1 Platform / Shared SDK (`platform/`)
| Package | Status | What It Provides |
| :--- | :--- | :--- |
| `platform/config` | ✅ Implemented | `.env` loader, `GetEnv`, `GetEnvOrError` |
| `platform/logger` | ✅ Implemented | Structured Zap logger |
| `platform/postgres` | ✅ Implemented | `pgxpool` connection factory, Goose migrations runner |
| `platform/redis` | ✅ Implemented | Redis client wrapper |
| `platform/jwt` | ✅ Implemented | HMAC JWT sign/validate, context helpers |
| `platform/errors` | ✅ Implemented | gRPC error mapping utilities |
| `platform/uuid` | ✅ Implemented | UUIDv7 generator |
| `platform/api/gen` | ✅ Implemented | Generated gRPC stubs for auth, wallet, order, market |

### 2.2 Auth Service (`services/auth/`)
| Component | Status | Notes |
| :--- | :--- | :--- |
| gRPC Handler | ✅ Implemented | Register, Login, Logout, Refresh, Verify, Resend, ForgotPassword, ResetPassword, ChangePassword |
| Service Layer | ✅ Implemented | Full session lifecycle, JWT rotation, OTP flows |
| Repository Layer | ✅ Implemented | Users, sessions, OTP tables |
| Mail Integration | ✅ Implemented | Verification + password reset email flows |
| OTP System | ✅ Implemented | Time-bounded OTP generation and validation |
| Database Migrations | ✅ Implemented | Goose-managed schema |
| Docker Deployment | ✅ Running | `tradedrift-auth` container live |

### 2.3 Wallet Service (`services/wallet/`)
| Component | Status | Notes |
| :--- | :--- | :--- |
| gRPC Handler | ✅ Implemented | GetBalance, GetBalances, GetSupportedAssets, ReserveFunds, ReleaseFunds, SettleTrade, InitializeWallet |
| Service Layer | ✅ Implemented | Reserve/release, settlement, wallet init |
| Repository Layer | ✅ Implemented | Balances, reservations, outbox |
| Transactional Outbox | ✅ Implemented | `settle_trade.go` — atomic balance update + outbox write |
| Database Migrations | ✅ Implemented | Goose-managed schema |
| Docker Deployment | ✅ Running | `tradedrift-wallet` container live |

### 2.4 Order Service (`services/order/`)
| Component | Status | Notes |
| :--- | :--- | :--- |
| gRPC Handler | ✅ Implemented | CreateOrder, GetOrder, ListOrders, CancelOrder |
| Service Layer | ✅ Implemented | Validation, fund reservation via Wallet gRPC, idempotency |
| Kafka Publisher | ✅ Implemented | Outbox publisher for `order.created.v1` topic |
| Repository Layer | ✅ Implemented | Orders, outbox tables |
| Database Migrations | ✅ Implemented | Goose-managed schema |
| Docker Deployment | ✅ Running | `tradedrift-order` container live |
| Postman Tested | ✅ Tested | CreateOrder, GetOrder, ListOrders, CancelOrder verified |

### 2.5 Market Service (`services/market/`)
| Component | Status | Notes |
| :--- | :--- | :--- |
| gRPC Handler | ✅ Implemented | ListMarkets, GetMarket, GetTicker, GetCandles |
| Service Layer | ✅ Implemented | Market config, ticker aggregation, candle queries |
| Kafka Consumer | ✅ Implemented | Consumes `trade.executed.v1` to build candles & tickers |
| Repository Layer | ✅ Implemented | Markets, market_trades, ohlc_candles tables |
| Database Migrations | ✅ Implemented | `00001` (base schema) + `00002` (open/close trade columns) |
| Docker Deployment | ✅ Running | `tradedrift-market` container live, Kafka connected on `kafka:29092` |
| Postman Tested | ✅ Tested | ListMarkets, GetMarket, GetTicker, GetCandles verified |

### 2.6 API Gateway (`services/gateway/`)
| Component | Status | Notes |
| :--- | :--- | :--- |
| HTTP Router | ✅ Implemented | Go 1.22 `net/http` ServeMux with method+path routing |
| Auth Middleware | ✅ Implemented | JWT Bearer token validation |
| Rate Limiter | ✅ Implemented | Token bucket — 20 req/s per IP |
| CORS Middleware | ✅ Implemented | Configurable origin allowlist |
| Request ID | ✅ Implemented | UUID per request injected into context |
| Logger Middleware | ✅ Implemented | Structured request/response logging |
| Recovery Middleware | ✅ Implemented | Panic recovery with 500 response |
| Auth Routes | ✅ Implemented | 10 endpoints (public + protected) |
| Wallet Routes | ✅ Implemented | 3 endpoints (public + protected) |
| Order Routes | ✅ Implemented | 4 endpoints (all protected) |
| Market Routes | ✅ Implemented | 4 endpoints (all public) |
| Docker Deployment | ✅ Running | `tradedrift-gateway` on port 8080 |
| End-to-End Tested | ✅ Tested | All routes verified through Postman |

### 2.7 Infrastructure
| Component | Status | Notes |
| :--- | :--- | :--- |
| Docker Compose | ✅ Running | All 5 services + Redis + Kafka orchestrated |
| Kafka (KRaft mode) | ✅ Running | Dual listeners: `kafka:29092` (internal), `localhost:9092` (host) |
| Redis | ✅ Running | Auth session store |
| PostgreSQL | ✅ Running | 4 separate databases (auth, wallet, order, market) on host |

---

## Phase 3 — Matching Engine + Downstream Services (⏳ Up Next)

| Service | Status | Planned Start |
| :--- | :--- | :--- |
| **Matching Engine** | ⏳ Implementation Pending | **Next** |
| Settlement Service | ⏳ Pending | After Matching Engine |
| Trade Service | ⏳ Pending | After Settlement |
| Portfolio Service | ⏳ Pending | After Settlement |
| Notification Service | ⏳ Pending | After Trade/Portfolio |
| Admin Service | ⏳ Pending | After core is stable |
| WebSocket Gateway | ⏳ Pending | After Notification |

---

# License

MIT