# Gateway Handler — Order (`internal/handler/order`)

> **Package:** `tradedrift/services/gateway/internal/handler/order`  
> **Directory:** `services/gateway/internal/handler/order/`  
> **Role:** Protected HTTP endpoints for placing, inspecting, listing, and cancelling trading orders.

---

## 1. Purpose

The `order` handler acts as the gateway entrypoint for the **Order Microservice** (`services/order`). It validates user order placement requests (e.g. `BUY` / `SELL`, `LIMIT` / `MARKET`), passes client idempotency keys, and proxies lifecycle operations to the matching engine pipeline.

---

## 2. Files in this Directory

| File | Purpose |
| :--- | :--- |
| [`handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/order/handler.go) | HTTP request handlers for order creation, retrieval, filtering, and cancellation. |
| [`dto.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/order/dto.go) | Request/Response DTOs (`CreateOrderRequestDTO`, `OrderDTO`) and Enum mappers for Side and Type. |

---

## 3. Endpoints, Functions & Protection Level

| HTTP Route | Handler Function | Auth Level | Why Protected or Public? |
| :--- | :--- | :--- | :--- |
| `POST /api/v1/orders` | `CreateOrder` | 🔒 **Protected** | Financial action: Places a new limit or market order. Requires a verified user ID to lock funds in the user's wallet. |
| `GET /api/v1/orders` | `ListOrders` | 🔒 **Protected** | Queries order history filtered by market (e.g., `BTC-USDT`) or status (`OPEN`, `FILLED`, `CANCELLED`). Scoped strictly to the authenticated caller. |
| `GET /api/v1/orders/{id}` | `GetOrder` | 🔒 **Protected** | Fetches detailed fill status of a specific order belonging to the caller. |
| `POST /api/v1/orders/{id}/cancel` | `CancelOrder` | 🔒 **Protected** | Initiates order cancellation and triggers release of reserved funds. Caller must own the order. |

---

## 4. Key Mechanisms & Best Practices

### 1. Idempotency Key Forwarding (`Idempotency-Key` Header)
* Traders placing orders during network drops might submit duplicate requests.
* `CreateOrder` reads the `Idempotency-Key` HTTP header and passes it to the Order Service, ensuring that rapid double-clicks or retries never create duplicate order books entries.

### 2. Side & Type Parsing
* Maps incoming JSON string representations (`"BUY"` / `"SELL"`, `"LIMIT"` / `"MARKET"`) to compiled protobuf enums (`orderv1.OrderSide`, `orderv1.OrderType`).

---

## 5. Middlewares Used & Rationale

1. **`Auth(jwtValidator)`:**
   * **Why:** Order operations alter financial state. Identity is authenticated via JWT signature, ensuring no client can submit or cancel orders under another user's identity.
2. **`RateLimiter`:**
   * **Why:** Shields the order service and matching engine from Denial-of-Service (DoS) and algorithmic flooding.
3. **`RequestID` & `Recovery`:**
   * **Why:** Ensures every order placement has a distinct end-to-end trace ID logged across all microservices.

---

## 6. Tools & Libraries Used

* **`google.golang.org/grpc`**: Downstream communication with Order Service on port `:50053`.
* **`tradedrift/platform/api/gen/order/v1`**: Protobuf interface bindings.
* **`encoding/json`**: Stream payload decoders.
