# Order Service Client (`internal/clients/orderservice`)

The `orderservice` package encapsulates the gRPC client communicating with the TradeDrift **Order Service** (`tradedrift.order.v1.OrderService`).

---

## 1. Architectural Responsibility

The Order Service client manages the complete remote lifecycle of orders submitted by CT-001. It translates engine-level commands into strongly-typed gRPC requests, guards against nil RPC responses, and provides paginated idempotency recovery to prevent orphaned resting orders.

---

## 2. Asynchronous Nature of `CreateOrder`

A fundamental architectural invariant of the TradeDrift platform is:

$$\text{CreateOrder Success} \neq \text{Order Execution}$$

When Order Service responds with `CreateOrderResponse`:
1. The request passed authentication and initial wallet balance validation.
2. The order was written to the PostgreSQL `orders` table.
3. An event was committed to the transactional outbox to be dispatched to Kafka.
4. **Execution has not yet happened:** The Matching Engine processes the order asynchronously from the Kafka partition stream.

Because of this decoupled pipeline, CTS cannot assume that a successful `CreateOrder` completed execution. Instead, CTS must actively poll and manage the order's residual lifecycle.

```
CTS                     Order Service                   Kafka              Matching Engine
 │                            │                           │                       │
 ├── CreateOrder (gRPC) ─────▶│                           │                       │
 │                            ├── DB Insert               │                       │
 │                            ├── Outbox Insert           │                       │
 │◀── CreateOrderResponse ────┤                           │                       │
 │    (Status: PENDING)       │── Outbox Publisher ──────▶│                       │
 │                            │                           ├── Consume ───────────▶│
 │                            │                           │                       ├── Execute Match
 │                            │◀── TradeExecuted (Kafka) ─┴───────────────────────┤
 │                            ├── Update Order Status                             │
 │── GetOrder (polling) ─────▶│                                                   │
 │◀── GetOrderResponse ───────┤ (Status: PARTIALLY_FILLED)                        │
 │                            │                                                   │
 │── CancelOrder (Residual) ─▶│── CancelOrder Event ─────────────────────────────▶│
 │◀── CancelOrderResponse ────┤                                                   ├── Cancel Resting
 │    (Status: CANCELLED)     │                                                   │
```

---

## 3. Core API Methods

### `CreateCrossingOrder(ctx, marketID, side, priceCap, quantity, idempotencyKey)`
* Submits an aggressive `LIMIT` crossing order for user `account.WalletUUIDStr`.
* Uses `TimeInForce: GTC`.
* Returns an `*OrderResult` containing `OrderID`, `ClientOrderID`, `Status`, `FilledQty`, and `RemainingQty`.
* **Nil-Response Protection:** Verifies that both the `CreateOrderResponse` and the inner `Order` proto are non-nil; returns an explicit error if the gRPC server returns malformed nil envelopes.

### `GetOrder(ctx, orderID)`
* Queries Order Service for the latest status, `filled_quantity`, and `remaining_quantity` of a specific order.
* Used by the worker's post-submission verification loop to inspect fill progress.
* Protected by nil-response checks.

### `CancelOrder(ctx, orderID)`
* Submits a cancellation request for the unfilled residual of an active order.
* Crucial for maintaining the **taker-only invariant**: unfilled remainders must never rest on the order book.
* Protected by nil-response checks.

### `FindOrderByIdempotencyKey(ctx, marketID, idempotencyKey)`
* Queries Order Service for an order previously placed using a specific `idempotencyKey`.
* **Pagination & Ordering:** Order Service sorts orders using `ORDER BY created_at DESC, id DESC`. This method queries with a page size of `50`, following keyset cursor pagination up to `maxPages = 3` (up to 150 recent orders).
* **Return Values:**
  - `(*OrderResult, nil)`: The order was committed in Order Service DB.
  - `(nil, nil)`: The search completed exhaustively across pages and confirmed the order **was not committed**.
  - `(nil, err)`: gRPC or network failure during lookup; outcome remains genuinely unknown.

### `Ping(ctx)`
* Validates client readiness and Order Service availability.
* **Fail-Closed Nil Protection:** If the `Client` instance or its internal `client` stub is `nil`, `Ping()` immediately returns an error (`"order service client is not initialized"`).
* Verifies gRPC transport connectivity state (fails if `TransientFailure` or `Shutdown`).
* Dispatches a lightweight `ListOrdersRequest` (`limit=1`) with a 1.5s timeout to verify that Order Service and its backing database are operational.

---

## 4. Idempotency & Ambiguous Submission Recovery

When an RPC failure occurs during `CreateOrder`, CTS must determine whether the order was committed to the database or if the network dropped before reaching the server.

```
                      CreateOrder RPC Error
                                │
               ┌────────────────┴────────────────┐
               ▼                                 ▼
    Deterministic Rejection              Ambiguous / Timeout
   (InvalidArgument, Precondition)    (DeadlineExceeded, Unavailable)
               │                                 │
               ▼                                 ▼
       Skip cycle cleanly              Idempotency Recovery
      (No recovery needed)          (FindOrderByIdempotencyKey)
                                                 │
                                 ┌───────────────┴───────────────┐
                                 ▼                               ▼
                           Order Found                    Order Not Found
                                 │                               │
                                 ▼                               ▼
                          Recover OrderID                   Fail Closed
                         & Cancel Residual             (Record Failure & Skip)
```

### Deterministic vs. Ambiguous Classification

| gRPC Status Code | Classification | Action |
| :--- | :--- | :--- |
| `InvalidArgument`, `FailedPrecondition`, `NotFound`, `AlreadyExists`, `PermissionDenied`, `Unauthenticated`, `ResourceExhausted` | **Deterministic Rejection** | Order was rejected by validation or balance checks. **No recovery attempted.** Breaker probe released. |
| `DeadlineExceeded`, `Unavailable`, `Canceled` (with living parent context) | **Ambiguous Failure** | Order Service may or may not have committed the order. **Enter idempotency recovery.** |
| `Unknown` (with "timeout", "connection", "eof", "reset", etc.) | **Ambiguous Transport** | Transport broke during flight. **Enter idempotency recovery.** |
| `Unknown` (generic application error) | **Deterministic Failure** | Fails closed without recovery. |

### Upstream `codes.Canceled` vs. CTS Shutdown Cancellation
A critical distinction implemented in `classifySubmissionError`:
* **CTS Shutdown (`parentCtx.Err() != nil`):** CTS initiated graceful shutdown. The order check is performed in a detached context. If committed, CTS cancels the residual before terminating.
* **Upstream RPC `codes.Canceled` (`parentCtx.Err() == nil`):** CTS is healthy, but the gRPC transport or an upstream proxy returned `codes.Canceled`. This is treated as an **Ambiguous Submission**, triggering idempotency lookup and failing closed if uncommitted.
