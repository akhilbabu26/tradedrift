-- +goose Up
-- Table: admin_saga_tasks
-- Persistent retry queue for the Auth session invalidation saga.
-- State machine: PENDING → RETRYING → COMPLETED │ EXHAUSTED (10 attempts consumed)

CREATE TABLE IF NOT EXISTS admin_saga_tasks (
    id              UUID         PRIMARY KEY,              -- task_id (UUIDv7)
    operation_id    UUID         NOT NULL REFERENCES admin_operations(id),
    task_type       VARCHAR(50)  NOT NULL,                 -- 'AUTH_INVALIDATE_SESSIONS'
    payload         JSONB        NOT NULL,                 -- {"user_id": "...", "reason": "..."}
    status          VARCHAR(20)  NOT NULL DEFAULT 'PENDING'
                    CHECK (status IN ('PENDING', 'RETRYING', 'COMPLETED', 'EXHAUSTED')),
    attempt_count   INT          NOT NULL DEFAULT 0,
    max_attempts    INT          NOT NULL DEFAULT 10,
    next_attempt_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_error      TEXT,
    locked_at       TIMESTAMPTZ,                          -- Optimistic lease locking for multi-worker safety
    locked_by       UUID,                                  -- Worker lease token (UUIDv4/UUIDv7 per SagaWorker poll cycle)
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Partial index: SagaWorker only polls actionable (non-terminal) tasks due for retry.
CREATE INDEX IF NOT EXISTS idx_saga_tasks_pending
    ON admin_saga_tasks(next_attempt_at ASC)
    WHERE status IN ('PENDING', 'RETRYING');

-- +goose Down
DROP TABLE IF EXISTS admin_saga_tasks;
