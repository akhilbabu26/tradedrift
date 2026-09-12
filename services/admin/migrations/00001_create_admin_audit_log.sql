-- +goose Up
-- Table: admin_audit_log
-- Append-only record of every administrative action.
-- Immutability is enforced by database triggers (not application logic).

CREATE TABLE IF NOT EXISTS admin_audit_log (
    id              UUID        PRIMARY KEY,
    admin_id        UUID        NOT NULL,
    operation_id    UUID,                           -- FK to admin_operations (nullable for pre-op audit)
    request_id      VARCHAR(128) NOT NULL,
    action          VARCHAR(50)  NOT NULL,          -- 'SUSPEND_USER', 'UNSUSPEND_USER', 'FREEZE_WALLET', 'UNFREEZE_WALLET', 'HALT_MARKET', 'RESUME_MARKET'
    target_type     VARCHAR(50)  NOT NULL,          -- 'USER', 'WALLET', 'MARKET'
    target_id       VARCHAR(128) NOT NULL,
    reason          TEXT         NOT NULL,
    metadata        JSONB        NOT NULL DEFAULT '{}',
    ip_address      VARCHAR(45),
    user_agent      TEXT,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_admin_audit_admin_id
    ON admin_audit_log(admin_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_admin_audit_operation_id
    ON admin_audit_log(operation_id)
    WHERE operation_id IS NOT NULL;

-- ─── Immutability Triggers ────────────────────────────────────────────────────
-- Any UPDATE or DELETE on admin_audit_log raises PostgreSQL error P0001.
-- This prevents tampering from both application code and direct operator queries.

CREATE OR REPLACE FUNCTION trg_enforce_audit_immutability()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'admin_audit_log entries are strictly immutable and append-only'
        USING ERRCODE = 'P0001';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_no_update_admin_audit_log
    BEFORE UPDATE ON admin_audit_log
    FOR EACH ROW EXECUTE FUNCTION trg_enforce_audit_immutability();

CREATE TRIGGER trg_no_delete_admin_audit_log
    BEFORE DELETE ON admin_audit_log
    FOR EACH ROW EXECUTE FUNCTION trg_enforce_audit_immutability();

-- +goose Down
DROP TRIGGER IF EXISTS trg_no_delete_admin_audit_log ON admin_audit_log;
DROP TRIGGER IF EXISTS trg_no_update_admin_audit_log ON admin_audit_log;
DROP FUNCTION IF EXISTS trg_enforce_audit_immutability();
DROP TABLE IF EXISTS admin_audit_log;
