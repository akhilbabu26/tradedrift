# Downstream gRPC Client Adapters (`internal/client`)

This document provides a comprehensive architectural and operational guide to the outbound gRPC client adapters located in [`services/admin/internal/client/`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/client/).

---

## Table of Contents

1. [Package Overview & Purpose](#1-package-overview--purpose)
2. [What Problems This Package Solves](#2-what-problems-this-package-solves)
3. [File-by-File Deep Dive](#3-file-by-file-deep-dive)
   - [auth_client.go](#auth_clientgo)
   - [wallet_client.go](#wallet_clientgo)
4. [Core Resilience Patterns & Mechanisms](#4-core-resilience-patterns--mechanisms)
   - [Distributed Tracing Metadata Injection](#distributed-tracing-metadata-injection)
   - [Bounded Call Timeouts (5s Operation / 1s Probe)](#bounded-call-timeouts-5s-operation--1s-probe)
   - [Smart gRPC Error Classification (`IsRetryableGRPCError`)](#smart-grpc-error-classification-isretryablegrpcerror)
   - [Dual-Mode Health Probing (gRPC RPC vs TCP Connectivity)](#dual-mode-health-probing-grpc-rpc-vs-tcp-connectivity)
5. [Architectural & Execution Flows](#5-architectural--execution-flows)
   - [Flow 1: Distributed User Session Invalidation Flow](#flow-1-distributed-user-session-invalidation-flow)
   - [Flow 2: Wallet Asset Freeze / Unfreeze Flow](#flow-2-wallet-asset-freeze--unfreeze-flow)
   - [Flow 3: Dual-Mode Health Probing Flow](#flow-3-dual-mode-health-probing-flow)
   - [Flow 4: gRPC Error Classification Decision Tree](#flow-4-grpc-error-classification-decision-tree)
6. [Testing & Verification](#6-testing--verification)

---

## 1. Package Overview & Purpose

In a microservices ecosystem, the Admin Service acts as a high-privilege supervisory service. To execute actions like suspending users or freezing balances, it must instruct downstream services (Auth and Wallet) over high-performance **gRPC channels**.

The `internal/client` package provides **strongly typed client wrappers** around raw generated Protobuf code (`authv1.AuthServiceClient`, `walletv1.WalletServiceClient`). It encapsulates:
- Connection lifecycle management (`Dial` / `Close`).
- Context deadline enforcement (preventing hanging calls).
- Distributed tracing propagation (`x-request-id`, `x-operation-id`).
- Health probing (`Ping`) for liveness/readiness monitoring.
- Error taxonomy classification (distinguishing transient network blips from permanent business rejections).

---

## 2. What Problems This Package Solves

| Problem | Failure Scenario Without This Adapter | How `internal/client` Solves It |
| :--- | :--- | :--- |
| **Cascading Goroutine Hangs** | Downstream service becomes unresponsive. Without explicit deadlines, gRPC client calls block indefinitely, leaking goroutines and exhausting the HTTP connection pool. | Enforces strict, bounded **5-second operation deadlines** and **1-second health probe deadlines** using `context.WithTimeout`. |
| **Broken Distributed Tracing** | Calls across network boundaries lose correlation IDs, making forensic debugging across log systems impossible during security incidents. | Automatically injects `x-request-id` and `x-operation-id` into outgoing **gRPC Metadata (`metadata.Pairs`)** on every remote call. |
| **Infinite Blind Retry Storms** | Saga worker retries a call that failed due to `InvalidArgument` or `NotFound` (e.g. non-existent user). The worker hammers the downstream service endlessly. | Implements `IsRetryableGRPCError(err)` to differentiate transient failures (`Unavailable`, `DeadlineExceeded`) from permanent non-retryable rejections. |
| **Silent Downstream Outages** | Admin service assumes downstream dependencies are healthy until a live user suspension request fails during an incident. | Exposes dedicated `Ping(ctx)` methods utilized by the background `HealthWorker` every 15 seconds to monitor connectivity proactively. |

---

## 3. File-by-File Deep Dive

### `auth_client.go`
- **File**: [`auth_client.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/client/auth_client.go)
- **Downstream Target**: Auth Microservice (`authv1.AuthServiceClient`)
- **Key Struct**: `AuthClient`

#### 1. Purpose
Wraps communication with the Auth service to revoke user sessions (refresh tokens and active sessions) when an account is suspended or investigated.

#### 2. What Problem It Solves
- **Unauthorized Post-Suspension Access**: When an admin suspends a rogue trader, their active JWT refresh tokens and sessions must be immediately revoked so they cannot execute further orders.
- **Heterogeneous Health Probing**: The Auth Service's protobuf specification does not include a dedicated `Health()` RPC. `auth_client.go` provides a custom two-tier probe (gRPC connection state inspection + active TCP dial probe).

#### 3. Functions Breakdown

| Function | Signature | Purpose & Problem Solved |
| :--- | :--- | :--- |
| `NewAuthClient` | `(grpcAddr string) (*AuthClient, error)` | Dials the target address with `insecure.NewCredentials()` and returns an initialized wrapper. |
| `Close` | `() error` | Closes the underlying `grpc.ClientConn` during application shutdown. |
| `Ping` | `(ctx context.Context) error` | Inspects `conn.GetState()` for `TransientFailure`/`Shutdown`, followed by an active 1-second `net.Dialer` TCP probe to verify network reachability. |
| `InvalidateUserSessions` | `(ctx context.Context, userID, reason, requestID, operationID string) error` | Injects tracing metadata, enforces a 5-second deadline, executes `InvalidateUserSessions` RPC, and validates `resp.Success`. |
| `IsRetryableGRPCError` | `(err error) bool` | Evaluates whether a gRPC error is transient and safe to retry, or a permanent client error that should terminate the saga immediately. |

---

### `wallet_client.go`
- **File**: [`wallet_client.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/client/wallet_client.go)
- **Downstream Target**: Wallet Microservice (`walletv1.WalletServiceClient`)
- **Key Struct**: `WalletClient`

#### 1. Purpose
Wraps communication with the Wallet service to freeze or unfreeze user balances across specific assets (e.g. BTC, USD) and perform deep application health checks.

#### 2. What Problem It Solves
- **Capital Flight During Security Breaches**: Provides immediate, synchronous asset freezing capabilities before suspicious withdrawals can be processed.
- **True Application-Level Health Verification**: Unlike a raw TCP ping, `wallet_client.go` executes an actual `Health()` RPC through the Wallet service's gRPC handler stack to ensure internal components (e.g. database pool, ledger) are functional.

#### 3. Functions Breakdown

| Function | Signature | Purpose & Problem Solved |
| :--- | :--- | :--- |
| `NewWalletClient` | `(grpcAddr string) (*WalletClient, error)` | Dials the target address with `insecure.NewCredentials()` and returns an initialized wrapper. |
| `Close` | `() error` | Closes the underlying `grpc.ClientConn` during application shutdown. |
| `Ping` | `(ctx context.Context) error` | Executes the Wallet service's native `Health` RPC with a strict 1-second timeout. |
| `FreezeWallet` | `(ctx context.Context, userID, asset, reason, requestID, operationID string, freeze bool) (bool, error)` | Injects tracing metadata, enforces a 5-second timeout, calls `FreezeWallet` RPC, and returns the resulting `isFrozen` status. |

---

## 4. Core Resilience Patterns & Mechanisms

### Distributed Tracing Metadata Injection
Every outgoing RPC propagates the originating request ID and operation ID in gRPC metadata:
```go
md := metadata.Pairs(
    "x-request-id", requestID,
    "x-operation-id", operationID,
)
callCtx = metadata.NewOutgoingContext(callCtx, md)
```
This enables downstream services to log identical correlation IDs, unifying logs across microservices in Grafana Loki or Datadog.

---

### Bounded Call Timeouts (5s Operation / 1s Probe)
To prevent network stalls from cascading back to HTTP clients:
- **Operations (`FreezeWallet`, `InvalidateUserSessions`)**:
  ```go
  callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
  defer cancel()
  ```
- **Health Probes (`Ping`)**:
  ```go
  callCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
  defer cancel()
  ```

---

### Smart gRPC Error Classification (`IsRetryableGRPCError`)
The background `SagaWorker` relies on `IsRetryableGRPCError` to decide whether to reschedule a failed task or mark it permanently `FAILED`:

```go
func IsRetryableGRPCError(err error) bool {
    if err == nil {
        return false
    }
    st, ok := status.FromError(err)
    if !ok {
        // Non-gRPC error (TCP disconnect, DNS failure, timeout) -> RETRY
        return true
    }
    switch st.Code() {
    case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
        return true // Transient infrastructure issue -> RETRY
    case codes.Internal:
        return true // Downstream server glitch -> RETRY
    case codes.InvalidArgument, codes.NotFound, codes.PermissionDenied, codes.Unauthenticated, codes.AlreadyExists:
        return false // Client logic error -> DO NOT RETRY (TERMINAL)
    default:
        return false
    }
}
```

---

### Dual-Mode Health Probing (gRPC RPC vs TCP Connectivity)

```
                       ┌─────────────────────────┐
                       │   HealthWorker Probe    │
                       └────────────┬────────────┘
                                    │
            ┌───────────────────────┴───────────────────────┐
            ▼                                               ▼
┌───────────────────────────────┐               ┌───────────────────────────────┐
│     walletClient.Ping()       │               │       authClient.Ping()       │
├───────────────────────────────┤               ├───────────────────────────────┤
│ Executes Native gRPC RPC:     │               │ Two-Tier Connectivity Probe:  │
│   c.client.Health(...)        │               │   1. conn.GetState() check    │
│ Verifies application logic,   │               │   2. net.Dialer TCP socket    │
│ DB pool, and gRPC pipeline.   │               │ Verifies reachability when no │
│ Timeout: 1 Second             │               │ native Health RPC is exposed. │
└───────────────────────────────┘               └───────────────────────────────┘
```

---

## 5. Architectural & Execution Flows

### Flow 1: Distributed User Session Invalidation Flow

```
                         SagaWorker
                             │
                             ▼
                        AuthClient
             (5s Timeout, Injects x-request-id)
                             │
                             ▼
                     Auth Microservice
                             │
         ┌───────────────────┼───────────────────┐
         ▼                   ▼                   ▼
    (Available)        (Unavailable)      (InvalidArgument)
         │                   │                   │
         ▼                   ▼                   ▼
┌─────────────────┐ ┌─────────────────┐ ┌─────────────────┐
│ Response Success│ │ Error: 503 / TO │ │Error: 400 Inval │
│   Task marked   │ │ IsRetryable=TRUE│ │IsRetryable=FALSE│
│    COMPLETED    │ │Saga Retries Exp │ │ Task EXHAUSTED  │
└─────────────────┘ └─────────────────┘ └─────────────────┘
```

---

### Flow 2: Wallet Asset Freeze / Unfreeze Flow

```
                        Administrator
                              │
                              ▼
                        AdminHandler
                    (POST /wallets/freeze)
                              │
                              ▼
                        AdminService
                              │
                              ▼
                        WalletClient
              (5s Timeout, Injects Context MD)
                              │
                              ▼
                     Wallet Microservice
                              │
               ┌──────────────┴──────────────┐
       (Success)                             (Failure)
               ▼                                     ▼
      Wallet State Frozen                    Wallet Unavailable
               │                                     │
               ▼                                     ▼
        AdminHandler                          AdminHandler
         HTTP 200 OK                       HTTP 503 Service Unavail
```

---

### Flow 3: Dual-Mode Health Probing Flow

```
                         HealthWorker
                        (15s Poll Loop)
                              │
        ┌─────────────────────┴─────────────────────┐
        ▼                                           ▼
   AuthClient                                  WalletClient
        │                                           │
  conn.GetState()                             c.client.Health()
 net.Dialer.DialContext                       (1s RPC Deadline)
  (1s Socket Probe)                                 │
        │                                           │
        ▼                                           ▼
┌───────────────┐                           ┌───────────────┐
│ Auth Service  │                           │Wallet Service │
└───────┬───────┘                           └───────┬───────┘
        │                                           │
        └─────────────────────┬─────────────────────┘
                              ▼
                     Prometheus Registry
                 DependencyStatus(auth/wallet)
                     UP (1)  │  DOWN (0)
```

---

### Flow 4: gRPC Error Classification Decision Tree

```
                         Outgoing gRPC Error
                                  │
                                  ▼
                        Is error == nil?
                         /             \
                   (Yes)/               \(No)
                       ▼                 ▼
             ┌─────────────────┐    status.FromError()
             │ Return FALSE:   │         │
             │ No Error        │         ▼
             └─────────────────┘   Valid gRPC Status?
                                   /             \
                            (Yes) /               \(No - Raw TCP/DNS)
                                 ▼                 ▼
                         Check status.Code   ┌─────────────────┐
                                 │           │ Return TRUE:    │
                ┌────────────────┴────────┐  │ Transient NetErr│
                ▼                         ▼  └─────────────────┘
     codes.Unavailable             codes.InvalidArgument
     codes.DeadlineExceeded        codes.NotFound
     codes.ResourceExhausted       codes.PermissionDenied
     codes.Internal                codes.Unauthenticated
                │                         │
                ▼                         ▼
      ┌─────────────────┐       ┌─────────────────┐
      │  Return TRUE:   │       │  Return FALSE:  │
      │    RETRYABLE    │       │  NON-RETRYABLE  │
      └─────────────────┘       └─────────────────┘
```

---

## 6. Testing & Verification

The resilience logic in `internal/client` is verified by automated tests in [`services/admin/test/grpc_test.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/test/grpc_test.go).

### Run Test Suite
```bash
go test -v ./services/admin/test -run TestIsRetryableGRPCError
```

### Verified Test Cases
- `nil_error` $\to$ Returns `false`
- `codes.Unavailable` $\to$ Returns `true`
- `codes.DeadlineExceeded` $\to$ Returns `true`
- `codes.ResourceExhausted` $\to$ Returns `true`
- `codes.Internal` $\to$ Returns `true`
- `codes.InvalidArgument` $\to$ Returns `false`
- `codes.NotFound` $\to$ Returns `false`
- `codes.PermissionDenied` $\to$ Returns `false`
- `codes.Unauthenticated` $\to$ Returns `false`
- `generic_network_error` $\to$ Returns `true`
