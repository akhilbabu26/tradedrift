-- +goose Up
-- Table: daily_topup_limits
-- Enforces per-user daily top-up quota ceiling and non-negative bounds

CREATE TABLE IF NOT EXISTS daily_topup_limits (
    user_id       UUID NOT NULL,
    usage_date    DATE NOT NULL,
    limit_inr     BIGINT NOT NULL DEFAULT 10,
    reserved_inr  BIGINT NOT NULL DEFAULT 0,
    consumed_inr  BIGINT NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT pk_daily_topup_limits PRIMARY KEY (user_id, usage_date),
    CONSTRAINT chk_limit_positive CHECK (limit_inr > 0),
    CONSTRAINT chk_reserved_nonneg CHECK (reserved_inr >= 0),
    CONSTRAINT chk_consumed_nonneg CHECK (consumed_inr >= 0),
    CONSTRAINT chk_quota_ceiling CHECK (reserved_inr + consumed_inr <= limit_inr)
);

-- +goose Down
DROP TABLE IF EXISTS daily_topup_limits;

