-- +goose Up

-- 1. Users — core identity table
-- token_version enables global session revocation (invalidate all active JWTs on demand)
-- role enables admin/user access distinction without a separate roles table
CREATE TABLE users (
    id                    UUID PRIMARY KEY,
    email                 VARCHAR(255) UNIQUE NOT NULL,
    username              VARCHAR(50) UNIQUE NOT NULL,
    password_hash         VARCHAR(255) NOT NULL,
    token_version         INTEGER NOT NULL DEFAULT 1,
    role                  VARCHAR(20) NOT NULL DEFAULT 'user',
    status                VARCHAR(20) NOT NULL DEFAULT 'PENDING_VERIFICATION'
                          CHECK (status IN ('PENDING_VERIFICATION', 'VERIFIED', 'SUSPENDED', 'BANNED')),
    failed_login_attempts INTEGER NOT NULL DEFAULT 0,
    locked_until          TIMESTAMPTZ,
    last_login_at         TIMESTAMPTZ,
    email_verified_at     TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_email    ON users(email);
CREATE INDEX idx_users_username ON users(username);

-- Seed the initial admin account
-- +goose StatementBegin
UPDATE users SET role = 'admin' WHERE email = 'akhil2672001@gmail.com';
-- +goose StatementEnd

-- 2. Refresh Tokens — issued on login, rotated on use, revoked on logout
-- Tracks device/IP for session audit; supports per-device revocation
CREATE TABLE refresh_tokens (
    id           UUID PRIMARY KEY,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   VARCHAR(255) NOT NULL UNIQUE,
    status       VARCHAR(20) NOT NULL DEFAULT 'ACTIVE'
                 CHECK (status IN ('ACTIVE', 'ROTATED', 'REVOKED')),
    ip_address   INET,
    user_agent   TEXT,
    device_name  VARCHAR(100),
    last_used_at TIMESTAMPTZ,
    rotated_at   TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_refresh_tokens_user ON refresh_tokens(user_id);

-- 3. Blacklisted Tokens — durable store for single-logout access token invalidation
-- Queried on every authenticated request during the token's remaining TTL window
CREATE TABLE blacklisted_tokens (
    jti        UUID PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_blacklisted_tokens_expiry ON blacklisted_tokens(expires_at);
CREATE INDEX idx_blacklisted_tokens_user   ON blacklisted_tokens(user_id);

-- 4. Outbox — Transactional Outbox Pattern for reliable Kafka event publishing
-- Events are written atomically with the business transaction and consumed by the outbox relay
-- UNIQUE (aggregate_type, aggregate_id, event_type) ensures idempotent publishing
CREATE TABLE outbox (
    id             UUID PRIMARY KEY,
    aggregate_type VARCHAR(255) NOT NULL,
    aggregate_id   UUID NOT NULL,
    event_type     VARCHAR(255) NOT NULL,
    payload        JSONB NOT NULL,
    status         VARCHAR(50) NOT NULL DEFAULT 'PENDING'
                   CHECK (status IN ('PENDING', 'PROCESSED', 'FAILED')),
    failed_reason  TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at   TIMESTAMPTZ,
    UNIQUE (aggregate_type, aggregate_id, event_type)
);

-- Partial index: only scans PENDING rows — stays tiny even at high volume
CREATE INDEX idx_outbox_pending ON outbox(created_at) WHERE status = 'PENDING';

-- +goose Down
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
