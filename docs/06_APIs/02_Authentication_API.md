# TradeDrift — Authentication API

> **Status:** ✅ Frozen (V1.0)
> **Document:** 02_Authentication_API.md
> **Directory:** docs/06_APIs/
> **Last Updated:** July 2026

---

## 1. Authentication Endpoints

### 1.1 POST `/api/v1/auth/register`
Creates a user account and returns a JWT session pair.
* **Authentication:** Guest (None)
* **Request Body:**
  ```json
  {
    "email": "user@example.com",
    "username": "trader1",
    "password": "SecurePassword123"
  }
  ```
* **Response `201 Created`:**
  ```json
  {
    "userId": "018f673a-4e2b-7f11-80a2-c3bfde34aa5a",
    "email": "user@example.com",
    "username": "trader1",
    "accessToken": "eyJhbGciOi...",
    "refreshToken": "eyJhbGciOi..."
  }
  ```

---

### 1.2 POST `/api/v1/auth/login`
Validates user credentials and returns tokens. Enforces brute-force limits (5 failed attempts locks account for 15 minutes).
* **Authentication:** Guest (None)
* **Request Body:**
  ```json
  {
    "email": "user@example.com",
    "password": "SecurePassword123"
  }
  ```
* **Response `200 OK`:**
  ```json
  {
    "userId": "018f673a-4e2b-7f11-80a2-c3bfde34aa5a",
    "accessToken": "eyJhbGciOi...",
    "refreshToken": "eyJhbGciOi..."
  }
  ```
* **Common Errors:**
  - `401 Unauthorized` / `AUTH_INVALID_CREDENTIALS`
  - `403 Forbidden` / `AUTH_ACCOUNT_LOCKED`

---

### 1.3 POST `/api/v1/auth/refresh`
Rotates the user's refresh token to generate a new pair.
* **Authentication:** Refresh Token Auth (Send `refreshToken` inside body or header)
* **Request Body:**
  ```json
  {
    "refreshToken": "eyJhbGciOi..."
  }
  ```
* **Response `200 OK`:**
  ```json
  {
    "accessToken": "eyJhbGciOi...",
    "refreshToken": "eyJhbGciOi..."
  }
  ```

---

### 1.4 POST `/api/v1/auth/logout`
Revokes the refresh token, blacklisting the associated access token ID in Redis.
* **Authentication:** Bearer Access Token
* **Request Body:**
  ```json
  {
    "refreshToken": "eyJhbGciOi..."
  }
  ```
* **Response `200 OK`:**
  ```json
  {
    "success": true
  }
  ```

---

### 1.5 POST `/api/v1/auth/change-password`
Changes the user's password and invalidates all current refresh token logs.
* **Authentication:** Bearer Access Token
* **Request Body:**
  ```json
  {
    "oldPassword": "SecurePassword123",
    "newPassword": "NewSecurePassword456"
  }
  ```
* **Response `200 OK`:**
  ```json
  {
    "success": true
  }
  ```
