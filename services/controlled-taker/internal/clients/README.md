# Clients Package (`internal/clients`)

The `clients` package isolates and encapsulates all external network dependencies for the Controlled Taker Service (CTS).

---

## 1. Architectural Philosophy

CTS requires interaction with two critical platform components:
1. **Redis Depth Stream (`redisdepth`):** Top-of-book L2 market depth for dynamic order sizing.
2. **Order Service (`orderservice`):** gRPC endpoint for order submission, status verification, idempotency recovery, and residual cancellation.

The `clients` package establishes an abstraction boundary between the execution engine (`internal/engine`) and these external services:

```
┌────────────────────────────────────────────────────────┐
│                   CTS Engine Core                      │
│                  (internal/engine)                     │
│    Uses interfaces: DepthReader, OrderSubmitter        │
└───────────────────────────┬────────────────────────────┘
                            │
            ┌───────────────┴───────────────┐
            ▼                               ▼
┌────────────────────────┐      ┌────────────────────────┐
│  clients/redisdepth    │      │  clients/orderservice  │
│  (Read-Only Redis)     │      │  (gRPC Client)         │
└───────────┬────────────┘      └───────────┬────────────┘
            │                               │
            ▼                               ▼
      Redis Server                    Order Service
  ("depth:<market_id>")             (OrderService gRPC)
```

### Why Engine Code Does Not Directly Talk to External Services
* **Decoupling & Testability:** The engine operates strictly against Go interfaces (`DepthReader`, `OrderSubmitter`). This enables comprehensive unit and integration testing using in-memory mock clients without spinning up external Docker infrastructure or network dependencies.
* **Network & Error Isolation:** Raw transport errors, gRPC status codes, connection drops, JSON parsing, and schema drift are caught, sanitized, and classified at the client boundary rather than leaking into trading decision logic.
* **Fail-Closed Principle:** Any ambiguity, timeout, dropped connection, or malformed payload received from an external dependency causes CTS to **fail closed**:
  - Unreadable or stale depth $\rightarrow$ skip execution cycle, do not guess liquidity.
  - Ambiguous submission result $\rightarrow$ query idempotency store; if still ambiguous, halt trading on that market and trip the circuit breaker.

---

## 2. Subpackages

| Package | Technology | Direction | Purpose |
| :--- | :--- | :--- | :--- |
| [`orderservice`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/controlled-taker/internal/clients/orderservice/README.md) | gRPC (`orderv1`) | Bidirectional RPC | Submits aggressive crossing limit orders, verifies terminal execution states, executes residual cancellations, and powers idempotency recovery. |
| [`redisdepth`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/controlled-taker/internal/clients/redisdepth/README.md) | Redis Client (`go-redis/v9`) | Read-Only | Queries cached L2 order-book depth snapshots (`depth:<market_id>`), enforcing strict staleness and book integrity checks. |

---

## 3. Critical Dependency Invariants

1. **Strict Read-Only Redis:** CTS only reads from Redis (`GET depth:<marketID>`). It never writes or publishes order-book states.
2. **Mandatory Order Service Authority:** CTS never communicates directly with the Matching Engine or Kafka. All orders must pass through Order Service for balance verification and outbox sequencing.
3. **Fail-Closed Recovery:** An ambiguous error from Order Service must never be treated as "order failed." Idempotency recovery must be executed to protect the book from unintended resting maker orders.
