# Gateway Handler — Wallet (`internal/handler/wallet`)

> **Package:** `tradedrift/services/gateway/internal/handler/wallet`  
> **Directory:** `services/gateway/internal/handler/wallet/`  
> **Role:** HTTP endpoints for querying supported exchange currencies and user asset balances.

---

## 1. Purpose

The `wallet` handler exposes REST interfaces for the **Wallet Microservice** (`services/wallet`). It allows clients to discover tradeable currency pairs and inspect both `available_balance` and `reserved_balance` (funds locked in active limit orders).

---

## 2. Files in this Directory

| File | Purpose |
| :--- | :--- |
| [`handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/wallet/handler.go) | HTTP request handlers for asset catalogs and balance queries. |
| [`dto.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/wallet/dto.go) | Response DTOs representing `AssetDTO` (metadata, decimals) and `BalanceDTO` (available/locked amounts). |

---

## 3. Endpoints, Functions & Protection Level

| HTTP Route | Handler Function | Auth Level | Why Protected or Public? |
| :--- | :--- | :--- | :--- |
| `GET /api/v1/wallet/assets` | `GetSupportedAssets` | **Public** | Public catalog of supported assets (`BTC`, `USDT`, `ETH`, `SOL`). Needed by visitors before logging in. |
| `GET /api/v1/wallet/balances` | `GetBalances` | 🔒 **Protected** | Financial privacy: A user can only view their own asset balances. `user_id` is extracted strictly from the verified JWT. |
| `GET /api/v1/wallet/balances/{asset}` | `GetBalance` | 🔒 **Protected** | Financial privacy: Returns the available and reserved amounts for a specific asset (e.g. `USDT`) for the authenticated user. |

---

## 4. Middlewares Used & Rationale

1. **`Auth(jwtValidator)` (Protected Balance routes):**
   * **Security Rule:** The client never passes `user_id` in URL path or query params. Instead, the `Auth` middleware verifies the JWT signature and extracts the `user_id` claim. This guarantees users cannot snoop on other users' wallet balances.
2. **`RateLimiter` (Global):**
   * **Why:** Prevents aggressive polling loops from overwhelming the PostgreSQL database pool.
3. **`CORS` (Global):**
   * **Why:** Permits cross-origin browser requests from the frontend trading interface.

---

## 5. Tools & Libraries Used

* **`google.golang.org/grpc`**: High-performance binary communication with `services/wallet`.
* **`tradedrift/platform/api/gen/wallet/v1`**: Generated gRPC client contracts.
* **`tradedrift/services/gateway/internal/middleware`**: Extracts verified identity claims from request context.
