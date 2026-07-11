# TradeDrift — Administrative API

> **Status:** ✅ Frozen (V1.0)
> **Document:** 08_Admin_API.md
> **Directory:** docs/08_APIs/
> **Last Updated:** July 2026

---

## 1. Administrative Endpoints

### 1.1 POST `/api/v1/admin/users/:id/suspend`
Suspends a user, preventing login and rejecting new order placements.
* **Authentication:** Admin Token Auth (Requires administrator role claims)
* **Request Body:**
  ```json
  {
    "reason": "Suspected market manipulation rule breach"
  }
  ```
* **Response `200 OK`:**
  ```json
  {
    "userId": "018f673a-4e2b-7f11-80a2-c3bfde34aa5a",
    "status": "SUSPENDED",
    "suspendedAt": "2026-07-11T13:00:20Z"
  }
  ```

---

### 1.2 POST `/api/v1/admin/wallets/:id/freeze`
Freezes or unfreezes a target user's wallet for a specific asset.
* **Authentication:** Admin Token Auth
* **Request Body:**
  ```json
  {
    "asset": "BTC",
    "freeze": true,
    "reason": "Security investigation lock"
  }
  ```
* **Response `200 OK`:**
  ```json
  {
    "userId": "018f673a-4e2b-7f11-80a2-c3bfde34aa5a",
    "asset": "BTC",
    "isFrozen": true,
    "frozenAt": "2026-07-11T13:00:22Z"
  }
  ```

---

### 1.3 POST `/api/v1/admin/markets/:id/halt`
Halts a trading pair's Matching Engine Event Loop immediately.
* **Authentication:** Admin Token Auth
* **Request Body:**
  ```json
  {
    "reason": "System maintenance pair decommission"
  }
  ```
* **Response `200 OK`:**
  ```json
  {
    "marketId": "BTC-USDT",
    "status": "HALTED",
    "haltedAt": "2026-07-11T13:00:25Z"
  }
  ```

---

### 1.4 POST `/api/v1/admin/markets/:id/resume`
Resumes trading on a halted market.
* **Authentication:** Admin Token Auth
* **Response `200 OK`:**
  ```json
  {
    "marketId": "BTC-USDT",
    "status": "ACTIVE",
    "resumedAt": "2026-07-11T13:00:30Z"
  }
  ```
* **Common Errors:**
  - `404 Not Found` (Market ID mismatch)
