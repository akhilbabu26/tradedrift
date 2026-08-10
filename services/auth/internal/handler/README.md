# Auth Service — Handler Package (`internal/handler`)

> **Package:** `tradedrift/services/auth/internal/handler`  
> **Directory:** `services/auth/internal/handler/`  
> **Role:** gRPC API Handler & Platform Error Status Mapper

---

## 1. Purpose & Responsibilities

The `handler` package implements the generated `authv1.AuthServiceServer` interface. It parses gRPC requests, validates parameters, delegates domain operations to the service layer, and maps platform errors to standardized gRPC status codes.

---

## 2. Files in This Directory

| File | Role |
| :--- | :--- |
| [`handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/auth/internal/handler/handler.go) | gRPC server method implementations (`Register`, `Login`, `VerifyEmail`, `RefreshToken`, `Logout`, etc.) |
| [`mapper.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/auth/internal/handler/mapper.go) | Error mapping utility (`mapToGRPCError`) converting platform errors to gRPC statuses |

---

## 3. Handled gRPC Endpoints

- `Register(ctx, req)` $\rightarrow$ User registration & verification code dispatch
- `VerifyEmail(ctx, req)` $\rightarrow$ Account activation & initial JWT issuance
- `ResendVerificationCode(ctx, req)` $\rightarrow$ OTP re-sending
- `Login(ctx, req)` $\rightarrow$ Credential check & JWT token pair generation
- `RefreshToken(ctx, req)` $\rightarrow$ Rotation of refresh tokens
- `ForgotPassword(ctx, req)` $\rightarrow$ Password reset code dispatch
- `ResetPassword(ctx, req)` $\rightarrow$ Password reset verification & update
- `Logout(ctx, req)` $\rightarrow$ Token revocation
- `LogoutAll(ctx, req)` $\rightarrow$ Mass session revocation
- `ChangePassword(ctx, req)` $\rightarrow$ Authenticated password update
