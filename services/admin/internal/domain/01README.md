# Pure Business Domain Subsystem (`internal/domain`)

This document provides a comprehensive architectural and operational manual for the pure domain layer located in [`services/admin/internal/domain/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/).

---

## Table of Contents

1. [Package Overview & Purpose](#1-package-overview--purpose)
2. [What Problems This Package Solves](#2-what-problems-this-package-solves)
3. [File-by-File Deep Dive](#3-file-by-file-deep-dive)
   - [operations.go (Idempotent Operation Ledger)](#operationsgo-idempotent-operation-ledger)
   - [audit.go (Forensic Audit Trail)](#auditgo-forensic-audit-trail)
   - [events.go (Transactional Outbox & Kafka Envelopes)](#eventsgo-transactional-outbox--kafka-envelopes)
   - [saga.go (Distributed Retry Queue & Backoff Schedule)](#sagago-distributed-retry-queue--backoff-schedule)
   - [errors.go (Sentinel Domain Errors)](#errorsgo-sentinel-domain-errors)
   - [uuid.go (Monotonic UUIDv7 Generator)](#uuidgo-monotonic-uuidv7-generator)
4. [Domain State Machines](#4-domain-state-machines)
   - [Operation State Machine](#operation-state-machine)
   - [Outbox State Machine](#outbox-state-machine)
   - [Saga State Machine](#saga-state-machine)
5. [Architectural & Execution Flows](#5-architectural--execution-flows)
   - [Flow 1: Domain Entities Composition in an Atomic Transaction](#flow-1-domain-entities-composition-in-an-atomic-transaction)
   - [Flow 2: Saga Exponential Backoff Schedule Progression](#flow-2-saga-exponential-backoff-schedule-progression)
   - [Flow 3: Domain Error Taxonomy to HTTP Status Mapping](#flow-3-domain-error-taxonomy-to-http-status-mapping)

---

## 1. Package Overview & Purpose

Following the tenets of **Clean Architecture** and **Domain-Driven Design (DDD)**, `internal/domain` is the innermost layer of the Admin Service.

### Pure Domain Characteristics:
1. **Zero External Transport Dependencies**: Has no knowledge of HTTP, gRPC, JSON-RPC, or web frameworks.
2. **Zero Persistence Dependencies**: Has no knowledge of SQL, PostgreSQL, Kafka drivers, or Redis.
3. **High Cohesion & Invariance**: Houses the core business rules, entity models, state machine transitions, immutable event envelopes, sentinel errors, and monotonic ID generators that govern the administrative control plane.

---

## 2. What Problems This Package Solves

| Problem | Failure Scenario Without Pure Domain | How `internal/domain` Solves It |
| :--- | :--- | :--- |
| **Leaky Architectural Abstractions** | HTTP handlers or SQL drivers dictate data structures, causing business logic to break whenever a transport library or database driver updates. | Isolates business rules into pure Go structs with no third-party tags or framework hooks. |
| **State Machine Desynchronization** | Different workers or handlers define conflicting status strings (e.g. `"PENDING"` vs `"pending"` vs `"in_progress"`). | Enforces strongly typed enum types (`OperationStatus`, `OutboxStatus`, `SagaStatus`) with explicit transition rules. |
| **Unbounded Retry Storms** | Workers retry failed remote calls immediately or at a fixed 1-second interval, overloading recovering downstream services. | Defines an explicit, progressive 10-tier `RetrySchedule` in [`saga.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/saga.go) from 30 seconds to 8 hours. |
| **B-Tree Database Index Fragmentation** | Using random UUIDv4 identifiers scatters row inserts across random pages on disk, causing heavy write amplification and cache thrashing. | Provides `MustNewV7()` in [`uuid.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/uuid.go) to guarantee millisecond-ordered monotonic UUIDv7 keys. |
| **Ambiguous Error Handling** | Services return arbitrary string errors (`"user failed"`, `"cannot do that"`), preventing handlers from returning proper HTTP status codes (400 vs 403 vs 409 vs 503). | Centralizes typed sentinel errors in [`errors.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/errors.go). |

---

## 3. File-by-File Deep Dive

### `operations.go` (Idempotent Operation Ledger)
- **File**: [`operations.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/operations.go)
- **Primary Model**: `AdminOperation`

#### 1. Purpose
Defines the state and lifecycle of high-privilege administrative operations (`SUSPEND_USER`, `FREEZE_WALLET`, `HALT_MARKET`, etc.) bound to an `IdempotencyKey`.

#### 2. Models & Types
```go
type OperationStatus string

const (
    OperationStatusPending    OperationStatus = "PENDING"
    OperationStatusProcessing OperationStatus = "PROCESSING"
    OperationStatusCompleted  OperationStatus = "COMPLETED"
    OperationStatusFailed     OperationStatus = "FAILED"
)

type AdminOperation struct {
    ID             string          `json:"id"`              // UUIDv7 operation_id
    AdminID        string          `json:"admin_id"`
    IdempotencyKey string          `json:"idempotency_key"`
    RequestID      string          `json:"request_id"`
    OperationType  string          `json:"operation_type"`
    TargetID       string          `json:"target_id"`
    Reason         string          `json:"reason"`
    Status         OperationStatus `json:"status"`
    ResponseBody   json.RawMessage `json:"response_body,omitempty"`
    CreatedAt      time.Time       `json:"created_at"`
    UpdatedAt      time.Time       `json:"updated_at"`
}
```

#### 3. Methods
- **`IsTerminal() bool`**: Returns `true` if `Status` is `COMPLETED` or `FAILED`. Once an operation reaches a terminal state, subsequent requests with the same idempotency key return the cached `ResponseBody` without re-executing business logic.

---

### `audit.go` (Forensic Audit Trail)
- **File**: [`audit.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/audit.go)
- **Primary Model**: `AuditLog`

#### 1. Purpose
Defines the schema for regulatory audit records capturing who performed an action, against which target, when, and from what IP address.

#### 2. Models & Constants
```go
const (
    ActionSuspendUser    = "SUSPEND_USER"
    ActionUnsuspendUser  = "UNSUSPEND_USER"
    ActionFreezeWallet   = "FREEZE_WALLET"
    ActionUnfreezeWallet = "UNFREEZE_WALLET"
    ActionHaltMarket     = "HALT_MARKET"
    ActionResumeMarket   = "RESUME_MARKET"
)

const (
    TargetTypeUser   = "USER"
    TargetTypeWallet = "WALLET"
    TargetTypeMarket = "MARKET"
)

type AuditLog struct {
    ID          string            `json:"id"`
    AdminID     string            `json:"admin_id"`
    OperationID *string           `json:"operation_id,omitempty"`
    RequestID   string            `json:"request_id"`
    Action      string            `json:"action"`
    TargetType  string            `json:"target_type"`
    TargetID    string            `json:"target_id"`
    Reason      string            `json:"reason"`
    Metadata    map[string]string `json:"metadata"`
    IPAddress   string            `json:"ip_address,omitempty"`
    UserAgent   string            `json:"user_agent,omitempty"`
    CreatedAt   time.Time         `json:"created_at"`
}
```

---

### `events.go` (Transactional Outbox & Kafka Envelopes)
- **File**: [`events.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/events.go)
- **Primary Models**: `OutboxEvent`, `EventEnvelope`

#### 1. Purpose
Defines both the database storage model for pending events (`OutboxEvent`) and the structured Kafka event payload envelope (`EventEnvelope`) streamed across platform services.

#### 2. Models & Constants
```go
const (
    TopicUserSuspended   = "admin.user-suspended.v1"
    TopicUserUnsuspended = "admin.user-unsuspended.v1"
    TopicWalletFrozen    = "admin.wallet-frozen.v1"
    TopicWalletUnfrozen  = "admin.wallet-unfrozen.v1"
    TopicMarketHalted    = "admin.market-halted.v1"
    TopicMarketResumed   = "admin.market-resumed.v1"
)

type OutboxEvent struct {
    ID            string       `json:"id"`           // UUIDv7 event_id
    OperationID   string       `json:"operation_id"`
    Topic         string       `json:"topic"`
    Payload       []byte       `json:"payload"`      // JSON-encoded EventEnvelope
    Published     bool         `json:"published"`
    PublishedAt   *time.Time   `json:"published_at,omitempty"`
    Status        OutboxStatus `json:"status"`       // PENDING | PROCESSING | PUBLISHED | FAILED
    AttemptCount  int          `json:"attempt_count"`
    MaxAttempts   int          `json:"max_attempts"`
    NextAttemptAt time.Time    `json:"next_attempt_at"`
    LastError     *string      `json:"last_error,omitempty"`
    LockedAt      *time.Time   `json:"locked_at,omitempty"`
    LockedBy      *string      `json:"locked_by,omitempty"`
    CreatedAt     time.Time    `json:"created_at"`
    UpdatedAt     time.Time    `json:"updated_at"`
}

type EventEnvelope struct {
    EventID     string            `json:"event_id"`     // UUIDv7
    OperationID string            `json:"operation_id"`
    RequestID   string            `json:"request_id"`
    AdminID     string            `json:"admin_id"`
    Action      string            `json:"action"`
    TargetID    string            `json:"target_id"`
    Reason      string            `json:"reason"`
    Metadata    map[string]string `json:"metadata,omitempty"`
    OccurredAt  time.Time         `json:"occurred_at"`
}
```

---

### `saga.go` (Distributed Retry Queue & Backoff Schedule)
- **File**: [`saga.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/saga.go)
- **Primary Model**: `SagaTask`

#### 1. Purpose
Defines asynchronous cross-service compensation tasks and governs progressive backoff intervals when calling downstream microservices.

#### 2. Models & Progressive Retry Schedule
```go
var RetrySchedule = []time.Duration{
    30 * time.Second, // attempt 1
    1 * time.Minute,  // attempt 2
    2 * time.Minute,  // attempt 3
    5 * time.Minute,  // attempt 4
    15 * time.Minute, // attempt 5
    30 * time.Minute, // attempt 6
    1 * time.Hour,    // attempt 7
    2 * time.Hour,    // attempt 8
    4 * time.Hour,    // attempt 9
    8 * time.Hour,    // attempt 10
}
```
- **`NextDelay(attemptCount int) time.Duration`**: Returns the progressive delay from `RetrySchedule` based on current retry count. Caps at 8 hours.
- **`SagaAuthPayload`**: Typed struct for JSON payloads passed to `AUTH_INVALIDATE_SESSIONS` tasks.

---

### `errors.go` (Sentinel Domain Errors)
- **File**: [`errors.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/errors.go)

#### Categorized Error Definitions

| Category | Sentinel Error | Meaning & Application |
| :--- | :--- | :--- |
| **Authentication & AuthZ** | `ErrUnauthorized`<br>`ErrForbidden`<br>`ErrMissingToken` | Invalid JWT signature, missing claims, or non-admin role. |
| **Idempotency** | `ErrOperationInProgress`<br>`ErrMissingIdempotencyKey`<br>`ErrIdempotencyKeyTooLong` | Concurrent request in-flight with same key, missing header, or invalid key length. |
| **Business Validation** | `ErrInvalidTarget`<br>`ErrInvalidReason`<br>`ErrOperationNotFound` | Missing target ID, reason < 5 characters or > 500 characters, or unknown ID. |
| **Distributed Sagas** | `ErrSagaExhausted` | 10 retry attempts consumed without downstream recovery. |
| **Downstream Outages** | `ErrAuthUnavailable`<br>`ErrWalletUnavailable` | Downstream gRPC service unreachable during synchronous execution. |
| **Worker Concurrency** | `ErrWorkerLeaseLost` | Database lease token expired or acquired by another worker pod. |

---

### `uuid.go` (Monotonic UUIDv7 Generator)
- **File**: [`uuid.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/domain/uuid.go)

```go
func MustNewV7() string {
    u, err := uuid.NewV7()
    if err != nil {
        panic("domain: failed to generate UUIDv7: " + err.Error())
    }
    return u.String()
}
```
- **Why UUIDv7?**
  - UUIDv4 uses 122 bits of pure pseudorandomness. Inserting into B-Trees causes random page writes across memory and disk.
  - UUIDv7 encodes the current 48-bit UNIX timestamp in milliseconds into the high bits, followed by 74 bits of random entropy.
  - **Result**: Naturally monotonic, chronological sorting, eliminating page splits in PostgreSQL and enabling efficient range queries.

---

## 4. Domain State Machines

### Operation State Machine
```
             ┌──────────────┐
             │ [*] In DB    │
             └──────┬───────┘
                    │
                    ▼
             ┌──────────────┐
             │   PENDING    │
             └──────┬───────┘
                    │ Worker / Service Leases
                    ▼
             ┌──────────────┐
             │  PROCESSING  │
             └──────┬───────┘
                    │
        ┌───────────┴───────────┐
 (Success)                      (Failure)
        ▼                               ▼
 ┌──────────────┐                ┌──────────────┐
 │  COMPLETED   │                │    FAILED    │
 └──────────────┘                └──────────────┘
```

### Outbox State Machine
```
             ┌──────────────┐
             │ [*] In Tx    │
             └──────┬───────┘
                    │
                    ▼
             ┌──────────────┐
             │   PENDING    │◄─────────────────┐
             └──────┬───────┘                  │
                    │ SKIP LOCKED Lease        │ (attempt < 10)
                    ▼                          │
             ┌──────────────┐                  │
             │  PROCESSING  │──────────────────┘
             └──────┬───────┘   Transient Error
                    │
        ┌───────────┴───────────┐
 (Kafka ACK)               (Attempts >= 10)
        ▼                               ▼
 ┌──────────────┐                ┌──────────────┐
 │  PUBLISHED   │                │    FAILED    │
 └──────────────┘                └──────────────┘
```

### Saga State Machine
```
             ┌──────────────┐
             │ [*] Suspend  │
             └──────┬───────┘
                    │
                    ▼
             ┌──────────────┐
             │   PENDING    │──(Immediate Success)──┐
             └──────┬───────┘                       │
                    │ (Transient RPC Fail)          │
                    ▼                               │
             ┌──────────────┐                       │
             │   RETRYING   │──(Eventual Success)───┤
             └──────┬───────┘                       │
                    │ (10 Attempts Exhausted)       ▼
                    ▼                        ┌──────────────┐
             ┌──────────────┐                │  COMPLETED   │
             │  EXHAUSTED   │                └──────────────┘
             └──────────────┘
```

---

## 5. Architectural & Execution Flows

### Flow 1: Domain Entities Composition in an Atomic Transaction

```
                   Incoming HTTP Suspension Request
                                  │
                                  ▼
                            AdminService
                                  │
      ┌───────────────────┬───────┴───────────┬───────────────────┐
      ▼                   ▼                   ▼                   ▼
 MustNewV7()         MustNewV7()         MustNewV7()         MustNewV7()
  [opID]              [auditID]           [eventID]           [sagaID]
      │                   │                   │                   │
      ▼                   ▼                   ▼                   ▼
┌──────────────┐    ┌──────────────┐    ┌──────────────┐    ┌──────────────┐
│AdminOperation│    │   AuditLog   │    │ EventEnvelope│    │   SagaTask   │
│Status=PENDING│    │Action=SUSPEND│    │Action=SUSPEND│    │Task=AUTH_INV │
└──────┬───────┘    └──────┬───────┘    └──────┬───────┘    └──────┬───────┘
       │                   │                   │                   │
       │                   │                   ▼                   │
       │                   │            ┌──────────────┐           │
       │                   │            │ OutboxEvent  │           │
       │                   │            │Payload=EnvJSON           │
       │                   │            └──────┬───────┘           │
       │                   │                   │                   │
       └───────────────────┼───────────────────┴───────────────────┘
                           │
                           ▼
          Single Atomic Database Transaction (ACID)
      tx.Commit() -> All 4 Entities Saved Monotonically
```

---

### Flow 2: Saga Exponential Backoff Schedule Progression

```
┌──────────────┐       ┌──────────────┐       ┌──────────────┐       ┌──────────────┐
│  Attempt 1   │──────►│  Attempt 2   │──────►│  Attempt 3   │──────►│  Attempt 4   │
│   Delay: 30s │       │   Delay: 1m  │       │   Delay: 2m  │       │   Delay: 5m  │
└──────────────┘       └──────────────┘       └──────────────┘       └──────────────┘
                                                                             │
┌──────────────┐       ┌──────────────┐       ┌──────────────┐               │
│  Attempt 7   │◄──────│  Attempt 6   │◄──────│  Attempt 5   │◄──────────────┘
│   Delay: 1h  │       │  Delay: 30m  │       │  Delay: 15m  │
└──────┬───────┘       └──────────────┘       └──────────────┘
       │
       ▼
┌──────────────┐       ┌──────────────┐       ┌──────────────┐       ┌──────────────┐
│  Attempt 8   │──────►│  Attempt 9   │──────►│  Attempt 10  │──────►│  EXHAUSTED   │
│   Delay: 2h  │       │   Delay: 4h  │       │   Delay: 8h  │       │Alert Trigger │
└──────────────┘       └──────────────┘       └──────────────┘       └──────────────┘
```

---

### Flow 3: Domain Error Taxonomy to HTTP Status Mapping

```
                        Domain Sentinel Error
                                  │
                                  ▼
                   ┌──────────────────────────────┐
                   │  handler.WriteDomainError()  │
                   └──────────────┬───────────────┘
                                  │
        ┌─────────────────────────┼─────────────────────────┐
        │                         │                         │
        ▼                         ▼                         ▼
┌───────────────┐         ┌───────────────┐         ┌───────────────┐
│ErrUnauthorized│         │  ErrForbidden │         │ErrInvalidTarget
│ErrMissingToken│         │               │         │ErrInvalidReasn│
└───────┬───────┘         └───────┬───────┘         └───────┬───────┘
        ▼                         ▼                         ▼
  HTTP 401 Unauth           HTTP 403 Forbid           HTTP 400 BadReq
        │                         │                         │
        └─────────────────────────┼─────────────────────────┘
                                  │
        ┌─────────────────────────┼─────────────────────────┐
        │                         │                         │
        ▼                         ▼                         ▼
┌───────────────┐         ┌───────────────┐         ┌───────────────┐
│ErrOperationIn-│         │ErrOperation-  │         │ErrAuthUnavail │
│  Progress     │         │  NotFound     │         │ErrWalletUnavail
└───────┬───────┘         └───────┬───────┘         └───────┬───────┘
        ▼                         ▼                         ▼
  HTTP 409 Conf             HTTP 404 NotFnd           HTTP 503 Unavail
```
