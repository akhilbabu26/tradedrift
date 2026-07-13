# TradeDrift Platform SDK — Shared Foundation Library

The `platform/` module is a compiled, generic shared library used across all TradeDrift microservices. It standardizes connection pooling, configuration loading, identity generation, error formatting, logging, and request authentication.

---

## 1. Directory Structure

```
platform/
  ├── config/             # Environment configuration parser
  ├── logger/             # High-performance structured Zap logger
  ├── uuid/               # UUIDv7 cryptographic generator
  ├── errors/             # Platform-wide canonical error codes and wrappers
  ├── postgres/           # pgx Connection pool & goose migration runner
  ├── redis/              # Standalone & Sentinel Redis client setup
  ├── jwt/                # Decoupled JWT auth framework & middleware
  └── api/                # Centrally compiled gRPC/Protobuf client & server stubs
```

---

## 2. Package Responsibilities

### 2.1 Configuration Loader (`platform/config`)
* **Purpose:** Provides fail-fast loaders for environment variables, including trim support and parsing helpers for primitives (`string`, `int`, `time.Duration`, `bool`).
* **Why it exists:** Keeps configurations declarative, prevents silent empty configurations, and avoids program execution with corrupted environment variables.

### 2.2 Structured Logger (`platform/logger`)
* **Purpose:** Configures a production-optimized `zap.Logger` that outputs formatted JSON logs.
* **Why it exists:** Standardizes logs across all services for seamless digestion by observability collectors (Grafana, Loki, ELK, Datadog), automatically attaching timestamps, call sites, and call stacks.

### 2.3 Identifiers (`platform/uuid`)
* **Purpose:** Generates millisecond-precision, ordered `UUIDv7` strings using a cryptographically secure random source.
* **Why it exists:** Guarantees chronological index sortability in database structures, prevents ID conflicts, and enables exact distributed request tracking.

### 2.4 Canonical Errors (`platform/errors`)
* **Purpose:** Defines structured, code-carrying platform errors (`PlatformError`) that carry machine-readable string keys alongside standard error objects.
* **Why it exists:** Ensures client-facing API responses carry consistent, cataloged string keys (e.g. `AUTH_INVALID_TOKEN`, `WALLET_INSUFFICIENT_FUNDS`) regardless of which service emitted them.

### 2.5 PostgreSQL Adapter (`platform/postgres`)
* **Purpose:** Provides pooled database connections using native `pgx/v5` and exposes a migration engine using `goose/v3`.
* **Why it exists:** Centralizes database client configurations (idle timeouts, connection bounds) and guarantees that service migrations are applied automatically during service boot sequences.

### 2.6 Redis & Sentinel HA Adapter (`platform/redis`)
* **Purpose:** Bootstraps Redis database connections, natively supporting both standalone instances and high-availability Sentinel failovers.
* **Why it exists:** Decouples services from hardcoding Sentinel address splits, ensuring high availability backplanes are transparently accessed.

### 2.7 JWT Authentication (`platform/jwt`)
* **Purpose:** Provides a fully decoupled JWT validation and token generation engine, featuring zero-allocation context keys, strict algorithms verification, clock-drift leeway configurations, and HTTP/gRPC middlewares.
* **Why it exists:** Implements security verification patterns once, allowing the API Gateway and microservices to secure their endpoints in a plug-and-play fashion without double-implementing crypto logic.

### 2.8 Protobuf API Contracts (`platform/api`)
* **Purpose:** Standardizes, compiles, and registers Protobuf stubs centrally, distributing them as native Go dependencies inside the monorepo workspace.
* **Why it exists:** Prevents version drift, ensures all internal gRPC interfaces match identical request models, and avoids compilation script duplication.

---

## 3. Dependency Guarantees

The `platform/` module enforces strict **unidirectional boundaries**:
* **Service-to-Platform:** Microservice modules (e.g. `services/auth`) are allowed to import any package in `platform`.
* **Platform-to-Service:** The `platform/` module has **zero imports** referencing any user-facing microservice. It is designed to be entirely generic, reusable, and self-contained.
* **Circular references:** No package inside `platform/` may import another package that would create an import loop.
