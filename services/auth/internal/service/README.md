# Auth Service — Business Logic Package (`internal/service`)

> **Package:** `tradedrift/services/auth/internal/service`  
> **Directory:** `services/auth/internal/service/`  
> **Role:** Authentication Domain Logic, Password Hashing, & Token Lifecycle Management

---

## 1. Purpose & Responsibilities

The `service` package contains the domain business rules for user authentication, password hashing (Argon2id/Bcrypt), email verification code generation, JWT token pair minting, and refresh token revocation tracking.

---

## 2. Files in This Directory

| File | Role |
| :--- | :--- |
| [`service.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/auth/internal/service/service.go) | Central service struct & constructor |
| [`register.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/auth/internal/service/register.go) | User registration & account creation |
| [`verification.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/auth/internal/service/verification.go) | Email verification code validation |
| [`login.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/auth/internal/service/login.go) | Credential verification & JWT token pair minting |
| [`refresh.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/auth/internal/service/refresh.go) | Refresh token rotation & revocation checking |
| [`logout.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/auth/internal/service/logout.go) | Session & token revocation logic |
| [`password.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/auth/internal/service/password.go) | Forgot password & reset password workflows |
