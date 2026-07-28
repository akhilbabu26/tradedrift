# `repository` Package

This package defines the **data models** and **database contracts** for the auth service.
It is split into two layers:

```
repository/
  token.go              ← interface + model + constants  (no DB dependency)
  user.go               ← interface + model              (no DB dependency)
  postgres/
    token_repository.go ← PostgreSQL implementation of the token interface
    user_repository.go  ← PostgreSQL implementation of the user interface
```

---

## What is in `token.go`?

`token.go` contains three things:

### 1. Token Status Constants

```go
const (
    TokenStatusActive  = "ACTIVE"
    TokenStatusRotated = "ROTATED"
    TokenStatusRevoked = "REVOKED"
)
```

These constants represent every possible value of the `Status` field on a refresh token.
They must match the `CHECK` constraint in the SQL migration:

```sql
CHECK (status IN ('ACTIVE', 'ROTATED', 'REVOKED'))
```

Using constants instead of raw strings prevents typos like `"Rotated"` or `"active"` that the
compiler cannot catch but would silently break hijack detection logic.

---

### 2. `RefreshToken` — the Data Model

```go
type RefreshToken struct {
    ID          string
    UserID      string
    TokenHash   string
    Status      string  // one of TokenStatusActive, TokenStatusRotated, TokenStatusRevoked
    IPAddress   *string
    UserAgent   *string
    DeviceName  *string
    LastUsedAt  *time.Time
    RotatedAt   *time.Time
    ExpiresAt   time.Time
    CreatedAt   time.Time
}
```

This struct is a **plain data container** — it represents one row from the `refresh_tokens`
table. It has no methods and implements no interface. Think of it as the shape of the data.

> `RefreshToken` is the **"what"** — what a token record looks like.

---

### 3. `RefreshTokenRepository` — the Behaviour Interface

```go
type RefreshTokenRepository interface {
    Create(ctx context.Context, t *RefreshToken) error
    GetByHash(ctx context.Context, hash string) (*RefreshToken, error)
    Rotate(ctx context.Context, oldID string, newToken *RefreshToken) error
    Revoke(ctx context.Context, id string) error
    RevokeAll(ctx context.Context, userID string) error
    BlacklistToken(ctx context.Context, jti string, userID string, expiresAt time.Time) error
    IsTokenBlacklisted(ctx context.Context, jti string) (bool, error)
}
```

This interface defines **what operations** can be performed on refresh tokens. It uses
`*RefreshToken` as the data type for those operations.

> `RefreshTokenRepository` is the **"how"** — what actions are allowed on tokens.

---

## What is in `postgres/token_repository.go`?

This file contains the actual **PostgreSQL implementation** of `RefreshTokenRepository`.

```go
type TokenRepository struct {
    db *pgxpool.Pool
}
```

It implements every method from the interface using real SQL queries:

| Method | What it does |
|---|---|
| `Create` | Inserts a new refresh token row |
| `GetByHash` | Looks up a token by its SHA-256 hash |
| `Rotate` | Marks old token as `ROTATED` and inserts new token in one transaction |
| `Revoke` | Marks a specific token as `REVOKED` (single logout) |
| `RevokeAll` | Marks all tokens for a user as `REVOKED` (logout all / password change) |
| `BlacklistToken` | Inserts an access token JTI into the `blacklisted_tokens` table |
| `IsTokenBlacklisted` | Checks if a JTI exists in `blacklisted_tokens` |

---

## How does Go know `TokenRepository` satisfies `RefreshTokenRepository`?

**Go uses implicit interface satisfaction** — there is no `implements` keyword. Go automatically
checks at compile time whether a struct has all the methods an interface requires.

The rule is simple:

> If a struct has **all** the methods defined in an interface (with exactly matching signatures),
> it satisfies that interface — no declaration needed.

### Step-by-step: How the check happens in this project

**Step 1 — Interface defines the required methods** (`token.go`):
```go
type RefreshTokenRepository interface {
    Create(...) error
    GetByHash(...) (*RefreshToken, error)
    // ... 5 more methods
}
```

**Step 2 — `TokenRepository` implements all of them** (`postgres/token_repository.go`):
```go
func (r *TokenRepository) Create(...) error           { /* SQL */ }
func (r *TokenRepository) GetByHash(...) (*RefreshToken, error) { /* SQL */ }
// ... 5 more
```

**Step 3 — The check happens at the moment of assignment** (`cmd/server/main.go`):
```go
tokenRepo := postgresRepo.NewTokenRepository(dbPool)
// ↑ returns *TokenRepository

authService := service.NewService(
    tokenRepo,  // NewService expects RefreshTokenRepository
    ...
)
// Go checks: does *TokenRepository match RefreshTokenRepository?
// All 7 methods match → ✅ compiles fine
```

`NewService` is declared as:
```go
func NewService(tokenRepo repository.RefreshTokenRepository, ...) *Service
```

When `*TokenRepository` is passed in, Go runs a silent checklist:
```
Create?             ✅
GetByHash?          ✅
Rotate?             ✅
Revoke?             ✅
RevokeAll?          ✅
BlacklistToken?     ✅
IsTokenBlacklisted? ✅

→ All 7 match. Contract satisfied.
```

If even **one** method is missing or has the wrong signature → **compile error**:
```
cannot use tokenRepo (type *TokenRepository) as type RefreshTokenRepository:
missing method RevokeAll
```

---

## What if `TokenRepository` has extra methods?

**It still satisfies the interface.** Go only requires `TokenRepository` to have
**at least** all the methods in the interface. Having more is allowed.

```go
func (r *TokenRepository) Create(...)         {} // ✅ required
func (r *TokenRepository) GetByHash(...)      {} // ✅ required
func (r *TokenRepository) DeleteExpired(...)  {} // ✅ extra — totally fine
```

However, extra methods are **invisible through the interface**:

```go
// Held as the interface — only sees 7 defined methods
var repo repository.RefreshTokenRepository = &TokenRepository{}
repo.DeleteExpired(...) // ❌ compile error — interface doesn't expose this

// Held as the concrete type — sees everything
var repo *postgres.TokenRepository = &TokenRepository{}
repo.DeleteExpired(...) // ✅ works
```

---

## Why is the interface and implementation in separate packages?

```
repository/         ← the contract (no DB import)
  token.go
postgres/           ← the fulfillment (imports pgxpool)
  token_repository.go
```

The `Service` only imports the `repository` package (the interface). It never imports
`postgres` directly. This means:

- **Swap databases freely** — replace `postgres.TokenRepository` with a MySQL or SQLite
  implementation without touching any service code.
- **Easy testing** — write a simple in-memory mock that implements the interface, no
  real database needed in tests.
- **Clear separation** — the service layer expresses *what it needs*, not *how it is done*.

---

## The full chain

```
RefreshToken struct            ← the data shape (one token row)
        ↓ used by
RefreshTokenRepository         ← the contract (what operations exist)
        ↑ satisfied by
postgres.TokenRepository       ← the contractor (actual SQL queries)
        ↑ injected into
Service                        ← uses the interface, never knows about postgres
        ↑ wired in
cmd/server/main.go             ← the only place that knows both sides
```
