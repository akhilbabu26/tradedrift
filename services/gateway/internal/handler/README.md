# API Gateway — REST Handler Package (`internal/handler`)

> **Package:** `tradedrift/services/gateway/internal/handler`  
> **Directory:** `services/gateway/internal/handler/`  
> **Role:** HTTP REST Handlers & Data Transfer Objects (DTOs)

---

## 1. Purpose & Responsibilities

The `handler` package implements HTTP REST request handlers for Fiber. It parses incoming JSON request bodies, converts them to gRPC client messages, dispatches calls to internal microservices, and formats responses back to clients.

---

## 2. Files in This Directory

| File | Role |
| :--- | :--- |
| [`auth.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/auth.go) | Authentication HTTP endpoints (`/api/v1/auth/register`, `/login`, `/verify`, `/refresh`, `/logout`, etc.) |
| [`auth_dto.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/auth_dto.go) | Request & response JSON payload structs for auth endpoints |
| [`wallet.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/wallet.go) | Wallet HTTP endpoints (`/api/v1/wallet/balances`, `/balance`, `/assets`) |
| [`wallet_dto.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/wallet_dto.go) | Request & response JSON payload structs for wallet endpoints |
| [`context.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/context.go) | Helpers to extract user claims from Fiber request contexts |
| [`dto.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/dto.go) | Common DTO definitions |
