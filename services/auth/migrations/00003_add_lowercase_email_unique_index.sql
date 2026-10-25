-- +goose Up

-- Create unique index on normalized email to enforce case-insensitive uniqueness at the DB level.
-- Audited against live database: 0 duplicate records found.
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_lower ON users (LOWER(TRIM(email)));

-- +goose Down

DROP INDEX IF EXISTS idx_users_email_lower;
