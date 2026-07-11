# TradeDrift — Authentication Database Design

> **Status:** ✅ Frozen (V1.0)
> **Document:** 02_Auth_Database.md
> **Directory:** docs/07_Database/
> **Last Updated:** July 2026

---

## 1. Purpose

The Authentication Database houses user credential records, session tracking, brute-force lockout stats, and persistent JWT blacklist mappings.

---

## 2. Table Schemas

### 2.1 Table: `users`
```sql
CREATE TABLE users (
    id                     UUID PRIMARY KEY,                      -- UUIDv7
    email                  VARCHAR(255) NOT NULL UNIQUE,
    username               VARCHAR(64) NOT NULL UNIQUE,
    password_hash          VARCHAR(255) NOT NULL,                 -- bcrypt hash (cost 10)
    status                 VARCHAR(20) NOT NULL DEFAULT 'ACTIVE', -- 'ACTIVE', 'SUSPENDED', 'BANNED'
    failed_login_attempts  INT NOT NULL DEFAULT 0,
    locked_until           TIMESTAMPTZ,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at          TIMESTAMPTZ,
    last_login_ip          VARCHAR(45),
    last_login_ua          TEXT
);
```

### 2.2 Table: `refresh_tokens`
```sql
CREATE TABLE refresh_tokens (
    id          UUID PRIMARY KEY,                      -- UUIDv7
    user_id     UUID NOT NULL REFERENCES users(id),
    token_hash  VARCHAR(255) NOT NULL,
    status      VARCHAR(20) NOT NULL DEFAULT 'ACTIVE', -- 'ACTIVE', 'ROTATED', 'REVOKED'
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### 2.3 Table: `blacklisted_tokens`
```sql
CREATE TABLE blacklisted_tokens (
    jti         UUID PRIMARY KEY,                      -- Access token JWT ID
    user_id     UUID NOT NULL,                         -- Logical reference
    expires_at  TIMESTAMPTZ NOT NULL
);
```

---

## 3. Query Design & Expected Patterns

### 3.1 Fetch User by Email (Login Check)
```sql
SELECT id, password_hash, status, failed_login_attempts, locked_until 
FROM users 
WHERE email = $1;
```
*Index support:* Handled by the implicit unique index on `email`.

### 3.2 Update User Failed Login Attempt
```sql
UPDATE users 
SET failed_login_attempts = failed_login_attempts + 1,
    locked_until = CASE WHEN failed_login_attempts + 1 >= 5 THEN NOW() + INTERVAL '15 minutes' ELSE NULL END
WHERE email = $1;
```

### 3.3 Log Out (Revoke Refresh Token & Blacklist Access Token)
```sql
-- Within a single ACID transaction in Auth Service:
UPDATE refresh_tokens 
SET status = 'REVOKED' 
WHERE token_hash = $1;

INSERT INTO blacklisted_tokens (jti, user_id, expires_at) 
VALUES ($2, $3, $4);
```
