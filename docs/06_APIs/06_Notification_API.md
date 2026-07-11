# TradeDrift — Notification API

> **Status:** ✅ Frozen (V1.0)
> **Document:** 06_Notification_API.md
> **Directory:** docs/06_APIs/
> **Last Updated:** July 2026

---

## 1. Notification Endpoints

### 1.1 GET `/api/v1/notifications`
Retrieves a paginated list of notification alerts in the user's inbox.
* **Authentication:** Bearer Access Token
* **Query Parameters:**
  - `cursor`: Keyset pagination cursor token.
  - `limit`: Default `20`, Max `100`.
  - `type`: Filter by type (`TRADE_FILL`, `DEPOSIT_CONFIRMED`, `SYSTEM`).
* **Response `200 OK`:**
  ```json
  {
    "data": [
      {
        "id": "018f6749-11ba-7f44-8a1a-fdecba088220",
        "title": "Trade Executed",
        "message": "Your BUY order of 0.0050 BTC on BTC-USDT filled at 65120.00",
        "type": "TRADE_FILL",
        "isRead": false,
        "createdAt": "2026-07-11T13:00:10Z"
      }
    ],
    "nextCursor": "eyJjcmVhdGVkX2F0IjoiMjAyNi0wNy0xMVQxMzowMDoxMFoiLCJpZCI6IjAxOGY2NyJ9"
  }
  ```

---

### 1.2 POST `/api/v1/notifications/:id/read`
Marks a specific alert as read.
* **Authentication:** Bearer Access Token
* **Response `200 OK`:**
  ```json
  {
    "id": "018f6749-11ba-7f44-8a1a-fdecba088220",
    "isRead": true,
    "readAt": "2026-07-11T13:02:15Z"
  }
  ```
* **Common Errors:**
  - `404 Not Found` (Notification ID mismatch or unauthorized access)
