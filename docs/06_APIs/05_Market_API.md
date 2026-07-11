# TradeDrift — Market API

> **Status:** ✅ Frozen (V1.0)
> **Document:** 05_Market_API.md
> **Directory:** docs/06_APIs/
> **Last Updated:** July 2026

---

## 1. Public Market Endpoints

### 1.1 GET `/api/v1/markets`
Returns list of all active trading pairs.
* **Authentication:** Guest (None)
* **Response `200 OK`:**
  ```json
  [
    {
      "id": "BTC-USDT",
      "baseAsset": "BTC",
      "quoteAsset": "USDT",
      "tickSize": "0.0100000000",
      "lotSize": "0.0001000000",
      "isEnabled": true
    },
    {
      "id": "ETH-USDT",
      "baseAsset": "ETH",
      "quoteAsset": "USDT",
      "tickSize": "0.0100000000",
      "lotSize": "0.0010000000",
      "isEnabled": true
    }
  ]
  ```

---

### 1.2 GET `/api/v1/markets/:id`
Returns metadata rules for a specific trading pair.
* **Authentication:** Guest (None)
* **Response `200 OK`:**
  ```json
  {
    "id": "BTC-USDT",
    "baseAsset": "BTC",
    "quoteAsset": "USDT",
    "tickSize": "0.0100000000",
    "lotSize": "0.0001000000",
    "isEnabled": true
  }
  ```

---

### 1.3 GET `/api/v1/markets/:id/ticker`
Returns daily 24-hour summary parameters. Pulled from Redis.
* **Authentication:** Guest (None)
* **Response `200 OK`:**
  ```json
  {
    "marketId": "BTC-USDT",
    "open": "64850.0000000000",
    "high": "66200.0000000000",
    "low": "64200.0000000000",
    "close": "65120.0000000000",
    "volume": "120.4500000000",
    "timestamp": "2026-07-11T13:00:00Z"
  }
  ```

---

### 1.4 GET `/api/v1/markets/:id/orderbook`
Returns L2 orderbook snapshot (asks/bids aggregated by price). Pulled from Redis.
* **Authentication:** Guest (None)
* **Query Parameters:**
  - `depth`: Quantity of levels to return. Default `20`, Max `100`.
* **Response `200 OK`:**
  ```json
  {
    "marketId": "BTC-USDT",
    "bids": [
      ["65100.0000000000", "0.0540000000"],
      ["65090.0000000000", "0.1200000000"]
    ],
    "asks": [
      ["65120.0000000000", "0.0120000000"],
      ["65130.0000000000", "0.2450000000"]
    ],
    "timestamp": "2026-07-11T13:00:05Z"
  }
  ```

---

### 1.5 GET `/api/v1/markets/:id/trades`
Returns public list of execution trade history.
* **Authentication:** Guest (None)
* **Query Parameters:**
  - `cursor`: Keyset pagination cursor token.
  - `limit`: Default `20`, Max `100`.
* **Response `200 OK`:**
  ```json
  {
    "data": [
      {
        "tradeId": "018f6745-77b1-7f33-8a1a-c3fde0aa8a22",
        "price": "65110.0000000000",
        "quantity": "0.0040000000",
        "executedAt": "2026-07-11T12:59:50Z"
      }
    ],
    "nextCursor": "eyJjcmVhdGVkX2F0IjoiMjAyNi0wNy0xMVQxMjo1OTo1MFoiLCJpZCI6IjAxOGY2NyJ9"
  }
  ```
