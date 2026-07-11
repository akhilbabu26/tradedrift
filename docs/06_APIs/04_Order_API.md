# TradeDrift — Order API

> **Status:** ✅ Frozen (V1.0)
> **Document:** 04_Order_API.md
> **Directory:** docs/06_APIs/
> **Last Updated:** July 2026

---

## 1. Order Endpoints

### 1.1 POST `/api/v1/orders`
Submits a spot limit or market order. 
* **Authentication:** Bearer Access Token
* **Headers:** `Idempotency-Key` (Required UUID)
* **Request Body:**
  ```json
  {
    "marketId": "BTC-USDT",
    "side": "BUY",
    "orderType": "LIMIT",
    "price": "65000.0000000000",
    "quantity": "0.0050000000"
  }
  ```
* **Response `201 Created`:**
  ```json
  {
    "orderId": "018f6741-2a3b-7f12-88a1-c9fbde02aa88",
    "marketId": "BTC-USDT",
    "status": "OPEN"
  }
  ```
* **Common Errors:**
  - `400 Bad Request` / `INSUFFICIENT_FUNDS` (Cannot reserve order quotes or base keys)
  - `503 Service Unavailable` / `MARKET_HALTED`

---

### 1.2 DELETE `/api/v1/orders/:id`
Submits a cancellation request. Returns `202 Accepted` because matching loops handle cancels asynchronously.
* **Authentication:** Bearer Access Token
* **Response `202 Accepted`:**
  ```json
  {
    "orderId": "018f6741-2a3b-7f12-88a1-c9fbde02aa88",
    "status": "CANCELLING"
  }
  ```
* **Common Errors:**
  - `404 Not Found` / `ORDER_NOT_FOUND`
  - `400 Bad Request` / `ORDER_ALREADY_FILLED`

---

### 1.3 GET `/api/v1/orders/:id`
Queries current state metadata for a single order.
* **Authentication:** Bearer Access Token
* **Response `200 OK`:**
  ```json
  {
    "orderId": "018f6741-2a3b-7f12-88a1-c9fbde02aa88",
    "userId": "018f673a-4e2b-7f11-80a2-c3bfde34aa5a",
    "marketId": "BTC-USDT",
    "side": "BUY",
    "orderType": "LIMIT",
    "price": "65000.0000000000",
    "quantity": "0.0050000000",
    "filledQuantity": "0.0020000000",
    "remainingQuantity": "0.0030000000",
    "status": "PARTIALLY_FILLED",
    "createdAt": "2026-07-11T12:50:00Z",
    "updatedAt": "2026-07-11T12:51:15Z"
  }
  ```

---

### 1.4 GET `/api/v1/orders`
Retrieves a paginated list of user orders.
* **Authentication:** Bearer Access Token
* **Query Parameters:**
  - `cursor`: Keyset pagination cursor token.
  - `limit`: Default `20`, Max `100`.
  - `status`: Filter by status (`OPEN`, `PARTIALLY_FILLED`, `FILLED`, `CANCELLED`).
  - `marketId`: Filter by pair (e.g. `BTC-USDT`).
  - `side`: Filter by direction (`BUY`, `SELL`).
  - `from` / `to`: ISO-8601 timestamps defining time bounds.
* **Response `200 OK`:**
  ```json
  {
    "data": [
      {
        "orderId": "018f6741-2a3b-7f12-88a1-c9fbde02aa88",
        "marketId": "BTC-USDT",
        "side": "BUY",
        "orderType": "LIMIT",
        "price": "65000.0000000000",
        "quantity": "0.0050000000",
        "filledQuantity": "0.0050000000",
        "status": "FILLED",
        "createdAt": "2026-07-11T12:50:00Z"
      }
    ],
    "nextCursor": "eyJjcmVhdGVkX2F0IjoiMjAyNi0wNy0xMVQxMjo1MDowMFoiLCJpZCI6IjAxOGY2NyJ9"
  }
  ```
