# platform/jwt — Decoupled JWT Authentication Package

This package provides generic JWT validation, type-safe context propagation, and transport-specific adapters (HTTP middleware & gRPC interceptors) for the TradeDrift platform.

---

## 1. Directory Structure & File Responsibilities

```
platform/jwt/
  ├── claims.go          # Custom Claims definition mapping JWT fields
  ├── validator.go       # Generic Validator interface definition
  ├── context.go         # Collision-safe context injection & extraction helpers
  ├── redis_validator.go # Concrete Redis-based validator (implements jwt.Validator)
  ├── generator.go       # Access and Refresh token generation functions
  ├── http/
  │     └── middleware.go  # Generic HTTP Auth middleware
  └── grpc/
        └── interceptor.go # Generic gRPC Unary Auth interceptor
```

### File Responsibilities:
* **`claims.go`**: Defines the custom `Claims` struct that holds standard JWT registered claims (expiry, subject, issuer) and TradeDrift specific fields (`UserID`, `Email`, `JTI`).
* **`validator.go`**: Defines the generic `Validator` interface. Transport layers (HTTP/gRPC) import this interface rather than concrete database/Redis packages, enforcing clean dependency boundaries.
* **`context.go`**: Provides collision-safe context keys (using an empty `struct{}`) and package helpers (`WithClaims` and `FromContext`) to inject and retrieve JWT claims from Go's `context.Context`.
* **`redis_validator.go`**: The concrete implementation of `jwt.Validator`. It handles signature verification, cryptographic validation via `HS256`, and live blacklist checks in Redis (verifying single-session revocations and global logouts).
* **`generator.go`**: Contains helper functions (`IssueAccessToken` and `IssueRefreshToken`) used by the Authentication Service to create signed JWT access tokens and secure 256-bit opaque refresh tokens.
* **`http/middleware.go`**: Exposes HTTP middleware that parses incoming headers, validates JWT format, and triggers validation.
* **`grpc/interceptor.go`** (or `intercepter.go`): Exposes gRPC server interceptors that parse gRPC metadata headers and validate JWT credentials.

---

## 2. Design Decisions & Architectural Principles

To ensure production-grade security and maintainability, this package is designed around the following principles:

### 2.1 Dependency Inversion (Interface-Driven Decoupling)
HTTP and gRPC transport layers do not know about Redis, database pools, or crypto signing secrets. Instead, they depend solely on the generic `jwt.Validator` interface:
```go
type Validator interface {
    Validate(ctx context.Context, tokenStr string) (*Claims, error)
}
```
This decouples request verification from token storage, meaning you can swap the validation backing engine (e.g., from Redis to PostgreSQL or an external OAuth provider) without changing any HTTP/gRPC middleware code.

### 2.2 Flatter Layout
Rather than nesting packages under `jwt/transport/http`, we use a flatter layout (`jwt/http` and `jwt/grpc`). This shortens Go import paths for consumers from `tradedrift/platform/jwt/transport/http` to `tradedrift/platform/jwt/http`.

### 2.3 Type-Safe Context Keys
To avoid context collision bugs (where one package accidentally overrides context variables belonging to another), we use a zero-sized, package-private struct as the context key instead of a raw string:
```go
type claimsContextKey struct{}
```
Because the type is private to the `jwt` package, it is impossible for external packages to collide with or manually modify the injected claims.

### 2.4 Defensive Security Checks
The validator implements defensive programming features to prevent exploit vectors:
* **Algorithm Restrictions:** Enforces the use of `HS256` explicitly using `.Alg()`, preventing algorithm confusion attacks.
* **Strict Format Checks:** Middlewares enforce the standard `"Bearer "` prefix. Raw tokens lacking the prefix are rejected before validation.
* **Claims Consistency Verification:** Verifies that the standard `Subject` matches our custom `UserID` claim to guarantee structural token integrity.
* **Future-Dated Token Invalidation:** Automatically invalidates tokens with an issued-at (`iat`) timestamp in the future (permitting a 60-second clock-skew tolerance).
* **Fail-Closed & Obfuscated Error Messages:** To prevent attackers from mapping active accounts, the middlewares return a generic `"invalid or expired token"` response, suppressing internal database and expiration details.

---

## 3. How to Use

### 3.1 Setup concrete Redis Validator (in main.go)
Initialize the concrete validator using the platform Redis client:
```go
import "tradedrift/platform/jwt"

// Initialize
secret := []byte("your-signing-secret")
redisVal := jwt.NewRedisValidator(secret, redisClient) // implements jwt.Validator
```

### 3.2 HTTP Server Setup (using http middleware)
Pass the validator interface to the HTTP middleware:
```go
import "tradedrift/platform/jwt/http"

r := chi.NewRouter()
r.Use(http.AuthMiddleware(redisVal)) // Protects all downstream routes
```

### 3.3 gRPC Server Setup (using grpc interceptor)
Pass the validator interface to the gRPC interceptor:
```go
import "tradedrift/platform/jwt/grpc"

// Map public methods that bypass authentication
publicMethods := map[string]bool{
    "/tradedrift.auth.v1.AuthService/Login":    true,
    "/tradedrift.auth.v1.AuthService/Register": true,
}

s := grpc.NewServer(
    grpc.UnaryInterceptor(grpc.UnaryAuthInterceptor(redisVal, publicMethods)),
)
```

### 3.4 Retrieving Claims in Handlers
Access the validated user claims from the Go context:
```go
import "tradedrift/platform/jwt"

func MyHandler(w http.ResponseWriter, r *http.Request) {
    claims, ok := jwt.FromContext(r.Context())
    if !ok {
        // Handle unauthenticated state
        return
    }
    
    userID := claims.UserID
    email := claims.Email
    // Proceed with business logic...
}
```
