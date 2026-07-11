# TradeDrift — Portfolio API

> **Status:** ✅ Frozen (V1.0)
> **Document:** 07_Portfolio_API.md
> **Directory:** docs/06_APIs/
> **Last Updated:** July 2026

---

## 1. Portfolio Endpoints

### 1.1 GET `/api/v1/portfolio/summary`
Returns user portfolio values and aggregated PnL revaluations.
* **Authentication:** Bearer Access Token
* **Response `200 OK`:**
  ```json
  {
    "userId": "018f673a-4e2b-7f11-80a2-c3bfde34aa5a",
    "totalValue": "13506.0000000000",
    "realizedPnl": "250.0000000000",
    "unrealizedPnl": "6.0000000000",
    "cashBalance": "10250.0000000000",
    "updatedAt": "2026-07-11T13:00:15Z"
  }
  ```

---

### 1.2 GET `/api/v1/portfolio/holdings`
Returns a list of asset holdings along with average entry costs and current values.
* **Authentication:** Bearer Access Token
* **Response `200 OK`:**
  ```json
  {
    "userId": "018f673a-4e2b-7f11-80a2-c3bfde34aa5a",
    "holdings": [
      {
        "asset": "BTC",
        "totalQuantity": "0.0500000000",
        "averageEntryPrice": "65000.0000000000",
        "currentPrice": "65120.0000000000",
        "unrealizedPnl": "6.0000000000"
      }
    ]
  }
  ```
