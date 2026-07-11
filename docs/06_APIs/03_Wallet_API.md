# TradeDrift — Wallet API

> **Status:** ✅ Frozen (V1.0)
> **Document:** 03_Wallet_API.md
> **Directory:** docs/06_APIs/
> **Last Updated:** July 2026

---

## 1. Wallet Endpoints

### 1.1 GET `/api/v1/wallets/balances`
Returns all active asset balances for the authenticated user.
* **Authentication:** Bearer Access Token
* **Response `200 OK`:**
  ```json
  {
    "userId": "018f673a-4e2b-7f11-80a2-c3bfde34aa5a",
    "balances": [
      {
        "asset": "USDT",
        "availableBalance": "10000.0000000000",
        "reservedBalance": "250.0000000000",
        "totalBalance": "10250.0000000000",
        "isFrozen": false
      },
      {
        "asset": "BTC",
        "availableBalance": "0.0500000000",
        "reservedBalance": "0.0000000000",
        "totalBalance": "0.0500000000",
        "isFrozen": false
      }
    ]
  }
  ```

---

### 1.2 GET `/api/v1/wallets/balances/:asset`
Returns single balance metrics for a specific asset.
* **Authentication:** Bearer Access Token
* **Response `200 OK`:**
  ```json
  {
    "asset": "USDT",
    "availableBalance": "10000.0000000000",
    "reservedBalance": "250.0000000000",
    "totalBalance": "10250.0000000000",
    "isFrozen": false
  }
  ```
* **Common Errors:**
  - `404 Not Found` (Unsupported asset code)

---

### 1.3 POST `/api/v1/wallets/deposits`
Executes simulated asset deposits to credit the user's available balance.
* **Authentication:** Bearer Access Token
* **Headers:** `Idempotency-Key` (Required UUID)
* **Request Body:**
  ```json
  {
    "asset": "USDT",
    "amount": "5000.0000000000",
    "referenceId": "ext-txn-99238"
  }
  ```
* **Response `201 Created`:**
  ```json
  {
    "transferId": "018f673d-9e32-7f22-83b1-a9fdeba38a10",
    "referenceId": "ext-txn-99238",
    "asset": "USDT",
    "amount": "5000.0000000000",
    "newAvailableBalance": "15000.0000000000",
    "status": "COMPLETED"
  }
  ```

---

### 1.4 POST `/api/v1/wallets/withdrawals`
Executes simulated asset withdrawals to debit the user's available balance.
* **Authentication:** Bearer Access Token
* **Headers:** `Idempotency-Key` (Required UUID)
* **Request Body:**
  ```json
  {
    "asset": "BTC",
    "amount": "0.0100000000",
    "referenceId": "client-ref-11202"
  }
  ```
* **Response `201 Created`:**
  ```json
  {
    "transferId": "018f673f-1a1a-7b33-85c1-e9f02bb38ea8",
    "referenceId": "client-ref-11202",
    "asset": "BTC",
    "amount": "0.0100000000",
    "newAvailableBalance": "0.0400000000",
    "status": "COMPLETED"
  }
  ```
* **Common Errors:**
  - `400 Bad Request` / `INSUFFICIENT_FUNDS`
  - `403 Forbidden` / `WALLET_FROZEN`
