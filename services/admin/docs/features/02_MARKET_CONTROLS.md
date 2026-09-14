# Feature 02: Market Controls (Halt & Resume)

## 1. What This Feature Does
The **Market Controls** subsystem provides platform operators with an emergency **circuit breaker** to halt or resume trading on specific currency pairs:
* **Halt Market (`POST /api/v1/admin/markets/{market_id}/halt`)**: Stops all new order placement for a trading pair (e.g. `BTC-USDT`) in sub-milliseconds across the platform.
* **Resume Market (`POST /api/v1/admin/markets/{market_id}/resume`)**: Lifts the trading restriction, clears the enforcement cache, and allows normal matching and order ingress to resume.

---

## 2. Why We Need It
During periods of market chaos, pricing oracle malfunction, extreme volatility, or suspected exploit attempts:
1. **Capital Protection**: If an external oracle feeds corrupted price data or a trading pair suffers a flash crash, allowing order execution can result in massive financial loss for users and insolvency for the exchange.
2. **Sub-Millisecond Rejection**: High-frequency order pipelines cannot wait for asynchronous event delivery. Halting must take effect at the order ingress boundary within sub-milliseconds.
3. **Selective Circuit Breakers**: Halting one compromised pair (e.g., `ETH-USDT`) must not disrupt unaffected markets (e.g., `BTC-USDT` or `SOL-USDT`).

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **HTTP Transport** | [`services/admin/internal/handler/admin_handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/admin_handler.go#L201-L273) | `HandleHaltMarket`, `HandleResumeMarket` |
| **Input Validation** | [`services/admin/internal/handler/validation.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/validation.go#L47-L54) | `ValidateMarketID` (Strict `^[A-Z0-9]{2,10}-[A-Z0-9]{2,10}$` regex) |
| **Service Logic** | [`services/admin/internal/service/market_service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/service/market_service.go#L41-L280) | `HaltMarket`, `ResumeMarket`, Redis writes with exponential retries |
| **Telemetry** | [`services/admin/internal/metrics/metrics.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/metrics/metrics.go#L104-L115) | `RecordMarketHalted`, `RecordMarketResumed` Prometheus gauges |
| **Ingress Guard** | [`services/order/internal/service/guard.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/service/guard.go#L26-L58) | `RedisMarketGuard.IsHalted` checks readiness sentinel and halt key |
| **Order Processing**| [`services/order/internal/service/service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/order/internal/service/service.go#L94-L113) | Checks `marketGuard.IsHalted` before reserving funds |

---

## 4. How We Achieve This Feature

1. **Idempotency and Atomic DB Record**:
   Admin verifies `Idempotency-Key` and commits a PostgreSQL transaction writing the operation (`HALT_MARKET`), an immutable audit log, and an outbox event (`admin.market-halted.v1`).
2. **Synchronous Redis Write**:
   Admin synchronously writes `market:halted:{marketID} = "1"` to Redis using 3 retries (50ms, 150ms, 300ms). Redis acts as the hot-path circuit breaker cache.
3. **Metric Tracking**:
   Sets Prometheus gauge `admin_market_halt_status{market_id="BTC-USDT"} = 1`.
4. **Order Service Gatekeeping**:
   When a user submits an order via `POST /api/v1/orders`:
   - `MarketGuard` verifies the readiness sentinel (`market:enforcement:ready == "1"`).
   - `MarketGuard` checks `GET market:halted:{marketID}`.
   - If `"1"`, Order Service immediately rejects the request with `HTTP 409 Conflict` (`"market is currently halted"`), stopping the order before any balance is reserved.

---

## 5. Execution Flow

```
                                 OPERATOR / POSTMAN
                                         │
                                         │ 1. POST /api/v1/admin/markets/{id}/halt
                                         │    (Idempotency-Key + Bearer JWT)
                                         ▼
                      ┌──────────────────────────────────────┐
                      │             ADMIN HANDLER            │
                      │  - Validates JWT (Role == "admin")   │
                      │  - Validates Idempotency-Key header  │
                      │  - Validates Market Format BASE-QUOTE│
                      └──────────────────┬───────────────────┘
                                         │
                                         │ 2. HaltMarket(ctx, req)
                                         ▼
                      ┌──────────────────────────────────────┐
                      │            MARKET SERVICE            │
                      │  Idempotency Check in DB             │
                      └──────────────────┬───────────────────┘
                                         │
                       ┌─────────────────┴─────────────────┐
                       │ Cached Operation Exists           │ Fresh Key
                       ▼                                   ▼
         ┌───────────────────────────┐       ┌───────────────────────────────────┐
         │ HTTP 200 OK               │       │ ExecAdminOperationTx (Atomic SQL) │
         │ Return Cached DTO         │       │ ├── admin_operations (COMPLETED)  │
         │ (Zero duplicate mutation) │       │ ├── admin_audit_log (HALT_MARKET) │
         └───────────────────────────┘       │ └── admin_outbox (Outbox Event)   │
                                             └─────────────────┬─────────────────┘
                                                               │
                                                               │ 3. Synchronous Redis Circuit Breaker
                                                               ▼
                                             ┌───────────────────────────────────┐
                                             │            REDIS CACHE            │
                                             │  SET market:halted:{id} = "1"     │
                                             │  (3 retries: 50ms, 150ms, 300ms)  │
                                             └─────────────────┬─────────────────┘
                                                               │
                                                               │ 4. Telemetry Update
                                                               ▼
                                             ┌───────────────────────────────────┐
                                             │ metrics.RecordMarketHalted(id)    │
                                             │ - Set market halt gauge = 1       │
                                             └─────────────────┬─────────────────┘
                                                               │
                                                               │ 5. Return Operation DTO
                                                               ▼
                                             ┌───────────────────────────────────┐
                                             │         HTTP 200 OK (Halted)      │
                                             └───────────────────────────────────┘
                                                               │
                                                               │ User attempts order placement:
                                                               │ POST /api/v1/orders
                                                               ▼
                                             ┌───────────────────────────────────┐
                                             │        ORDER SERVICE INGRESS      │
                                             │  1. Check market:enforcement:ready│
                                             │     -> "1" (Enforcement active)   │
                                             │  2. Check market:halted:{id}      │
                                             │     -> "1" (Market halted!)       │
                                             │  3. Reject immediately:           │
                                             │     HTTP 409 Conflict             │
                                             └───────────────────────────────────┘
```
