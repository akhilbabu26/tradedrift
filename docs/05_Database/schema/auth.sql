-- TradeDrift Authentication Database Schema

CREATE TABLE users (
    id                     UUID PRIMARY KEY,
    email                  VARCHAR(255) NOT NULL UNIQUE,
    username               VARCHAR(64) NOT NULL UNIQUE,
    password_hash          VARCHAR(255) NOT NULL,
    status                 VARCHAR(20) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'SUSPENDED', 'BANNED')),
    failed_login_attempts  INT NOT NULL DEFAULT 0 CHECK (failed_login_attempts >= 0),
    locked_until           TIMESTAMPTZ,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_login_at          TIMESTAMPTZ,
    last_login_ip          VARCHAR(45),
    last_login_ua          TEXT
);

CREATE TABLE refresh_tokens (
    id          UUID PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  VARCHAR(255) NOT NULL,
    status      VARCHAR(20) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'ROTATED', 'REVOKED')),
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE blacklisted_tokens (
    jti         UUID PRIMARY KEY,
    user_id     UUID NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL
);
