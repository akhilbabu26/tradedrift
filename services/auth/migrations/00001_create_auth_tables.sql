-- +goose Up
-- SQL in this section is executed when the migration is applied.

-- 1. Create Users Table
CREATE TABLE users (
    id                    UUID PRIMARY KEY,
    email                 VARCHAR(255) UNIQUE NOT NULL,
    username              VARCHAR(50) UNIQUE NOT NULL,
    password_hash         VARCHAR(255) NOT NULL,
    token_version         INTEGER NOT NULL DEFAULT 1, -- Added for global session revocation
    status                VARCHAR(20) NOT NULL DEFAULT 'PENDING_VERIFICATION'
                          CHECK (status IN ('PENDING_VERIFICATION', 'VERIFIED', 'SUSPENDED', 'BANNED')),
    failed_login_attempts INTEGER NOT NULL DEFAULT 0,
    locked_until          TIMESTAMPTZ,
    last_login_at         TIMESTAMPTZ,
    email_verified_at     TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_username ON users(username);

-- 2. Create Refresh Tokens Table
CREATE TABLE refresh_tokens (
    id           UUID PRIMARY KEY, -- UUIDv7
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   VARCHAR(255) NOT NULL UNIQUE,
    status       VARCHAR(20) NOT NULL DEFAULT 'ACTIVE'
                 CHECK (status IN ('ACTIVE', 'ROTATED', 'REVOKED')),
    ip_address   INET, -- Native Postgres INET type for IPv4 or IPv6
    user_agent   TEXT,
    device_name  VARCHAR(100),
    last_used_at TIMESTAMPTZ,
    rotated_at   TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_refresh_tokens_user ON refresh_tokens(user_id);

-- 3. Create Durable Blacklisted Access Tokens Table (for single logout recovery)
CREATE TABLE blacklisted_tokens (
    jti        UUID PRIMARY KEY, -- UUIDv7 matching the token JTI
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_blacklisted_tokens_expiry ON blacklisted_tokens(expires_at);
CREATE INDEX idx_blacklisted_tokens_user ON blacklisted_tokens(user_id);

-- 4. Create Outbox Table (for Transactional Outbox Pattern)
CREATE TABLE outbox (
    id             UUID PRIMARY KEY,
    aggregate_type VARCHAR(255) NOT NULL,
    aggregate_id   UUID NOT NULL, -- UUIDv7 matching our aggregate IDs
    event_type     VARCHAR(255) NOT NULL,
    payload        JSONB NOT NULL,
    status         VARCHAR(50) NOT NULL DEFAULT 'PENDING'
                   CHECK (status IN ('PENDING', 'PROCESSED', 'FAILED')),
    failed_reason  TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at   TIMESTAMPTZ,
    UNIQUE (aggregate_type, aggregate_id, event_type) -- Ensures idempotency of published events
);

-- Partial index to speed up scanning for pending events
CREATE INDEX idx_outbox_pending ON outbox(created_at) WHERE status = 'PENDING';

-- +goose Down
-- SQL in this section is executed when the migration is rolled back.
DROP INDEX IF EXISTS idx_outbox_pending;
DROP TABLE IF EXISTS outbox;
DROP INDEX IF EXISTS idx_blacklisted_tokens_user;
DROP INDEX IF EXISTS idx_blacklisted_tokens_expiry;
DROP TABLE IF EXISTS blacklisted_tokens;
DROP INDEX IF EXISTS idx_refresh_tokens_user;
DROP TABLE IF EXISTS refresh_tokens;
DROP INDEX IF EXISTS idx_users_username;
DROP INDEX IF EXISTS idx_users_email;
DROP TABLE IF EXISTS users;
