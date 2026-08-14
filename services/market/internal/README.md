# Market Service — Internal Architecture (`internal/`)

> **Package:** `tradedrift/services/market/internal`  
> **Directory:** `services/market/internal/`  
> **Architecture Pattern:** Clean / Hexagonal Architecture (Domain-Driven Design)

---

## 1. Purpose & Architectural Layers

The `internal/` directory houses the private business logic, data persistence layer, event streaming workers, and protocol handlers for the Market Service. Code inside `internal/` is protected by Go's compiler visibility rules and cannot be imported by external packages or other microservices.

```
                                  External World
                         (gRPC Clients / Kafka Brokers)
                                       │
                                       ▼
                     ┌───────────────────────────────────┐
                     │         Driving Adapters          │
                     │  • internal/handler (gRPC)        │
                     │  • internal/kafka   (Consumer)    │
                     └─────────────────┬─────────────────┘
                                       │
                                       ▼
                     ┌───────────────────────────────────┐
                     │          Service Layer            │
                     │  • internal/service (Logic)       │
                     └─────────────────┬─────────────────┘
                                       │
                                       ▼
                     ┌───────────────────────────────────┐
                     │        Driven Adapters / DB       │
                     │  • internal/repository/postgres   │
                     │  • Database: PostgreSQL           │
                     └───────────────────────────────────┘
```

---

## 2. Sub-Packages Overview

| Sub-Package | Responsibility | Key Files |
| :--- | :--- | :--- |
| [`config/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/market/internal/config/README.md) | Typed configuration loading from environment variables. | `config.go` |
| [`handler/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/market/internal/handler/README.md) | gRPC presentation layer, domain-to-protobuf mapping, and error sanitization. | `grpc.go`, `mapper.go` |
| [`kafka/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/market/internal/kafka/README.md) | Kafka message consumer, poison-pill skipping, and commit-after-DB safety. | `consumer.go` |
| [`service/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/market/internal/service/README.md) | Core business logic, market validation, and trade processing orchestration. | `service.go`, `errors.go` |
| [`repository/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/market/internal/repository/README.md) | Domain models, repository interfaces, and PostgreSQL implementations (CTE ticker, OHLC candles). | `market.go`, `postgres/` |
