-- +goose Up

-- 1. Make password_hash nullable to support Google OAuth / SSO-only users without a local password.
-- Existing users retain their NOT NULL password_hash.
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;

-- 2. User Authentication Identities — supports multiple login providers (e.g. Google, local) per TradeDrift user
CREATE TABLE user_auth_identities (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider           VARCHAR(32) NOT NULL,
    provider_user_id   VARCHAR(255) NOT NULL,
    email              VARCHAR(255) NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT uq_user_auth_identities_provider_uid UNIQUE (provider, provider_user_id),
    CONSTRAINT uq_user_auth_identities_user_provider UNIQUE (user_id, provider)
);

CREATE INDEX idx_user_auth_identities_user_id ON user_auth_identities(user_id);
CREATE INDEX idx_user_auth_identities_email   ON user_auth_identities(email);

-- +goose Down

DROP INDEX IF EXISTS idx_user_auth_identities_email;
DROP INDEX IF EXISTS idx_user_auth_identities_user_id;
DROP TABLE IF EXISTS user_auth_identities;

-- Rollback limitation: Restoring NOT NULL will fail if any user rows have NULL password_hash.
-- In production rollback scenarios, any OAuth-only users would require a dummy or generated hash before applying.
ALTER TABLE users ALTER COLUMN password_hash SET NOT NULL;
