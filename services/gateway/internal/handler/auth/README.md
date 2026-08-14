# Gateway Handler — Authentication (`internal/handler/auth`)

> **Package:** `tradedrift/services/gateway/internal/handler/auth`  
> **Directory:** `services/gateway/internal/handler/auth/`  
> **Role:** Public & protected HTTP endpoints for user registration, email verification, session login, JWT refresh, and password management.

---

## 1. Purpose

The `auth` handler acts as the external API Gateway reverse proxy for the **Authentication Microservice** (`services/auth`). It translates JSON HTTP requests from browsers and mobile apps into binary gRPC requests (`authv1.AuthServiceClient`) and converts responses back to JSON.

---

## 2. Files in this Directory

| File | Purpose |
| :--- | :--- |
| [`handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/auth/handler.go) | HTTP handler methods implementing user registration, verification, login, token rotation, and password lifecycle. |
| [`dto.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/auth/dto.go) | Request/Response Data Transfer Objects (DTOs) and Protobuf-to-JSON mapper functions. |

---

## 3. Endpoints, Functions & Protection Level

| HTTP Route | Handler Function | Auth Level | Why Protected or Public? |
| :--- | :--- | :--- | :--- |
| `POST /api/v1/auth/register` | `Register` | **Public** | Anyone must be able to sign up. |
| `POST /api/v1/auth/verify` | `VerifyEmail` | **Public** | Unverified users do not yet have a valid JWT. They authenticate with an email OTP. |
| `POST /api/v1/auth/resend` | `ResendVerification` | **Public** | Users who didn't receive their code need to request a new one without logging in. |
| `POST /api/v1/auth/login` | `Login` | **Public** | Exchanging email/username + password for initial JWT tokens. |
| `POST /api/v1/auth/refresh` | `RefreshToken` | **Public** | Called when an Access Token is expired, presenting the Refresh Token. |
| `POST /api/v1/auth/forgot-password` | `ForgotPassword` | **Public** | Users locked out of their accounts request a reset OTP. |
| `POST /api/v1/auth/reset-password` | `ResetPassword` | **Public** | Users submit their reset OTP and new password without an active JWT. |
| `POST /api/v1/auth/logout` | `Logout` | 🔒 **Protected** | Requires a valid JWT to identify the user session to revoke in Redis. |
| `POST /api/v1/auth/logout-all` | `LogoutAll` | 🔒 **Protected** | Requires a valid JWT to revoke all active refresh tokens for that user ID. |
| `POST /api/v1/auth/change-password` | `ChangePassword` | 🔒 **Protected** | Requires active authentication to modify credentials while logged in. |

---

## 4. Middlewares Used & Rationale

1. **`Auth(jwtValidator)` (Protected routes):**
   * **Why:** Parses and verifies the `Authorization: Bearer <token>` HMAC-SHA256 JWT header. Extracts `user_id` and places it in `r.Context()`. Rejects invalid or expired tokens with `401 Unauthorized`.
2. **`RateLimiter` (Global):**
   * **Why:** Enforces token-bucket rate limiting (20 requests/sec per IP) to protect sensitive endpoints (`/login`, `/register`, `/verify`) against brute-force attacks and credential stuffing.
3. **`CORS` (Global):**
   * **Why:** Restricts frontend access to authorized origins (`http://localhost:5173`) and allows standard headers (`Authorization`, `Content-Type`, `Idempotency-Key`).
4. **`Recovery` & `Logger` (Global):**
   * **Why:** Captures crashes gracefully and logs structured audit trails for all authentication activity.

---

## 5. Tools & Libraries Used

* **`google.golang.org/grpc`**: Downstream RPC communication with Auth Service.
* **`tradedrift/platform/api/gen/auth/v1`**: Compiled protobuf client stubs.
* **`encoding/json`**: Request body decoding and response marshaling.
* **`tradedrift/services/gateway/internal/response`**: Standardized JSON envelope helper.
