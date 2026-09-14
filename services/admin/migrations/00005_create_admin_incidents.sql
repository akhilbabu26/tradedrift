-- +goose Up
-- Table: admin_incidents
-- Persistent ledger for platform downtime incidents, MTTD/MTTR analytics, and audit correlation.
-- MTTD (Mean Time to Detect): The average time it takes for a team to notice that an incident, outage, or threat has occurred. It tests how good your monitoring tools are.
-- MTTR (Mean Time to Respond / Repair / Resolve / Recover): The average time it takes to fix the problem, restore normal service, or clean up a security threat after it is detected. It tests how efficient your team and processes are.
-- Concurrency Safety: Partial unique index guarantees at most one OPEN/INVESTIGATING incident per service.

CREATE TABLE IF NOT EXISTS admin_incidents (
    id                  UUID             PRIMARY KEY,              -- incident_id (UUIDv7)
    service_name        VARCHAR(64)      NOT NULL,                 -- e.g. 'wallet', 'auth', 'postgres', 'kafka'
    severity            VARCHAR(20)      NOT NULL DEFAULT 'P1_CRITICAL'
                        CHECK (severity IN ('P1_CRITICAL', 'P2_HIGH', 'P3_MODERATE')),
    status              VARCHAR(20)      NOT NULL DEFAULT 'OPEN'
                        CHECK (status IN ('OPEN', 'INVESTIGATING', 'RESOLVED')),
    title               TEXT             NOT NULL,
    root_cause          TEXT,
    triggered_at        TIMESTAMPTZ      NOT NULL,                 -- Estimated or actual start of degradation
    detected_at         TIMESTAMPTZ      NOT NULL,                 -- When HealthWorker first flagged the degradation
    resolved_at         TIMESTAMPTZ,                               -- When service transitioned back to UP
    last_seen_at        TIMESTAMPTZ      NOT NULL DEFAULT NOW(),   -- Heartbeat timestamp from ongoing failed probes
    probe_failure_count INT              NOT NULL DEFAULT 1,       -- Number of successive failed health probes
    mttd_seconds        DOUBLE PRECISION,                          -- Mean Time To Detect (NULL for autonomous HealthWorker incidents when independent failure-start is unavailable)
    mttr_seconds        DOUBLE PRECISION,                          -- Mean Time To Resolve (resolved_at - triggered_at)
    metadata            JSONB,                                     -- Additional diagnostic context (latencies, errors, payload)
    created_at          TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);

-- Partial Unique Index: Concurrency safety rule preventing duplicate open incidents for the same service.
CREATE UNIQUE INDEX IF NOT EXISTS uq_admin_active_incident_per_service
    ON admin_incidents(service_name)
    WHERE status IN ('OPEN', 'INVESTIGATING');

-- Query optimization indexes
CREATE INDEX IF NOT EXISTS idx_admin_incidents_status
    ON admin_incidents(status);

CREATE INDEX IF NOT EXISTS idx_admin_incidents_service
    ON admin_incidents(service_name, triggered_at DESC);

CREATE INDEX IF NOT EXISTS idx_admin_incidents_triggered_at
    ON admin_incidents(triggered_at DESC);

-- +goose Down
DROP TABLE IF EXISTS admin_incidents;
