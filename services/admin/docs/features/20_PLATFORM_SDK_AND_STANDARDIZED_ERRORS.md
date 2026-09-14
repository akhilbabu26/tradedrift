# Feature 20: Platform SDK Integration & Standardized Error Architecture

## 1. What This Feature Does
The **Platform SDK Integration & Standardized Error Architecture** ensures that the Admin Service adheres to strict company-wide infrastructure patterns:
* **Platform SDK Adoption**: Leverages shared packages (`tradedrift/platform/*`) for logging, JWT cryptographic validation, PostgreSQL connection pooling, database migrations, and environment variable resolution.
* **Canonical Error Decoupling**: Business logic returns transport-agnostic canonical error codes (`NOT_FOUND`, `PERMISSION_DENIED`, `FAILED_PRECONDITION`, `INVALID_ARGUMENT`, `INTERNAL`, `UNAVAILABLE`).
* **Deterministic HTTP Mapping**: The transport handler translates canonical errors into predictable HTTP status codes and structured JSON response envelopes.

---

## 2. Why We Need It
In large-scale distributed systems, ad-hoc error handling causes chaos:
1. **Preventing Information Leakage**: Returning raw SQL errors (e.g. `pq: duplicate key value violates unique constraint`) exposes internal schema names and database architectures to potential attackers.
2. **Consistent Client Error Handling**: Frontend apps and API clients need machine-readable error codes (e.g. `ERR_MARKET_HALTED`) rather than parsing variable English error strings.
3. **DRY Platform Standards**: Eliminates copy-pasting JWT parsers or database connection pool wrappers across 9 distinct microservices.

---

## 3. Where We Used It in Code

| Component | Package / File Path | Key Usage |
| :--- | :--- | :--- |
| **Shared JWT** | [`tradedrift/platform/jwt`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/platform/jwt) | `platformjwt.Validator`, `platformjwt.Claims` HMAC assertion |
| **Shared Logging** | [`tradedrift/platform/logger`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/platform/logger) | `platformlogger.New` structured JSON Zap logger |
| **Shared Postgres** | [`tradedrift/platform/pg`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/platform/pg) | `platformpg.NewPool`, `platformpg.RunMigrations` with Goose |
| **Shared Config** | [`tradedrift/platform/config`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/platform/config) | `platformconfig.LoadEnv` handles `.env` precedence |
| **Error Mapping** | [`services/admin/internal/handler/admin_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/admin_handler.go) | `mapDomainErrorToHTTP` converts domain errors to HTTP responses |

---

## 4. How We Achieve This Feature

### Canonical Domain Error Mapping Matrix

| Canonical Domain Error | HTTP Status Code | Description | Client Action |
| :--- | :---: | :--- | :--- |
| `ErrInvalidArgument` | `400 Bad Request` | Malformed UUID, invalid reason string, missing headers. | Correct parameters and retry. |
| `ErrUnauthenticated` | `401 Unauthorized` | Missing, expired, or invalid Bearer JWT. | Re-authenticate with Auth service. |
| `ErrPermissionDenied` | `403 Forbidden` | JWT does not possess `role == "admin"`. | Request elevated privileges. |
| `ErrNotFound` | `404 Not Found` | Target entity or incident ID does not exist. | Verify target ID. |
| `ErrFailedPrecondition` | `422 / 409 Conflict` | Market is already halted; wallet is frozen. | Verify state before retrying. |
| `ErrInternal` | `500 Internal Error` | Unhandled database panic or corruption. | Log alert; do not retry immediately. |
| `ErrUnavailable` | `503 Service Unavail`| Redis, Kafka, or downstream gRPC unreachable. | Back off and retry with jitter. |

---

## 5. Execution Flow

```
                           DOMAIN / BUSINESS LOGIC LAYER
                                         │
                                         ▼
                     ┌───────────────────────────────────────┐
                     │ Business Logic Returns Canonical Err: │
                     │ return ErrMarketHalted                │
                     │ (codes.FailedPrecondition)            │
                     └───────────────────┬───────────────────┘
                                         │
                                         ▼
                     ┌───────────────────────────────────────┐
                     │          HTTP TRANSPORT LAYER         │
                     │         mapDomainErrorToHTTP()        │
                     └───────────────────┬───────────────────┘
                                         │
                                         ▼
                     ┌───────────────────────────────────────┐
                     │ Evaluates Canonical Mapping:          │
                     │ - Status Code: HTTP 409 Conflict      │
                     │ - Structured JSON Error Envelope:     │
                     │   {                                   │
                     │     "error": "market is halted",      │
                     │     "code": "ERR_MARKET_HALTED",      │
                     │     "request_id": "0191eb70-..."      │
                     │   }                                   │
                     └───────────────────┬───────────────────┘
                                         │
                                         ▼
                     ┌───────────────────────────────────────┐
                     │ CLIENT / POSTMAN RECEIVES CLEAN DTO   │
                     │ Zero internal SQL/stack-trace leakage!│
                     └───────────────────────────────────────┘
```
