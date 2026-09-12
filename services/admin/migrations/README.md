# Admin Service Database Migrations Manual

This document provides a comprehensive architectural and operational guide to the database migrations powering the **TradeDrift Admin Service**. It explains the core purpose of migrations, the specific problems each migration file and table solves, why these architectural decisions were made, and the relational data flows executing across the system.

---

## Table of Contents

1. [Overview & Migration Engine](#1-overview--migration-engine)
2. [Database Schema Relational Architecture](#2-database-schema-relational-architecture)
3. [Deep Dive: Migration Files & Tables](#3-deep-dive-migration-files--tables)
   - [Migration 00001: admin_audit_log](#migration-00001-admin_audit_log)
   - [Migration 00002: admin_operations](#migration-00002-admin_operations)
   - [Migration 00003: admin_outbox](#migration-00003-admin_outbox)
   - [Migration 00004: admin_saga_tasks](#migration-00004-admin_saga_tasks)
4. [Critical Architectural & Schema Decisions](#4-critical-architectural--schema-decisions)
   - [Monotonic UUIDv7 Primary Keys](#monotonic-uuidv7-primary-keys)
   - [PL/pgSQL Immutability Triggers (SQL State P0001)](#plpgsql-immutability-triggers-sql-state-p0001)
   - [High-Performance Partial Indexes](#high-performance-partial-indexes)
   - [Durable Worker Leases with SKIP LOCKED](#durable-worker-leases-with-skip-locked)
5. [End-to-End Operational Flows](#5-end-to-end-operational-flows)
   - [Flow 1: The Atomic Mutation Transaction Flow](#flow-1-the-atomic-mutation-transaction-flow)
   - [Flow 2: Non-Blocking Outbox Worker Event Publishing](#flow-2-non-blocking-outbox-worker-event-publishing)
   - [Flow 3: Asynchronous Saga Worker Execution & Backoff](#flow-3-asynchronous-saga-worker-execution--backoff)
   - [Flow 4: Database-Level Tamper-Proof Audit Enforcement](#flow-4-database-level-tamper-proof-audit-enforcement)
6. [Migration Execution & CLI Guide](#6-migration-execution--cli-guide)

---

## 1. Overview & Migration Engine

In a financial exchange and trading platform, database schema integrity is mission-critical. The Admin Service operates as the authoritative command center controlling user suspensions, wallet freezing, and market circuit breakers.

### Purpose of the `migrations/` Directory
The `migrations/` directory contains version-controlled, declarative, forward-and-backward SQL migration scripts applied via [Goose](https://github.com/pressly/goose).

### What Problem It Solves
1. **Schema Drift**: Eliminates discrepancies between local development, CI/CD testing, staging, and production databases.
2. **Uncontrolled DDL Execution**: Prevents manual, ad-hoc `ALTER TABLE` operations that bypass code review.
3. **Rollback Ambiguity**: Every migration defines an explicit `-- +goose Up` and `-- +goose Down` block, guaranteeing clean disaster recovery and safe schema rollbacks.
4. **Zero-Downtime Deployment**: Migration scripts are written to be non-blocking and additive, allowing new application binaries to deploy alongside active database nodes.

---

## 2. Database Schema Relational Architecture

The Admin Service schema is designed around the **Unit of Work** pattern. A single incoming administrative request connects an operation, an audit record, an outbox publishing event, and optionally an external saga compensation task through foreign key relationships.

```
                      ┌─────────────────────────────────┐
                      │        admin_operations         │
                      ├─────────────────────────────────┤
                      │ id (UUIDv7, PK)                 │
                      │ admin_id (UUID)                 │
                      │ idempotency_key (VARCHAR, UQ)   │
                      │ operation_type (VARCHAR)        │
                      │ target_id (VARCHAR)             │
                      │ status (PENDING|COMPL|FAIL)     │
                      │ response_body (JSONB)           │
                      └────────────────┬────────────────┘
                                       │
         ┌─────────────────────────────┼─────────────────────────────┐
         ▼ (1:N)                       ▼ (1:N)                       ▼ (1:N)
┌──────────────────────┐      ┌──────────────────────┐      ┌──────────────────────┐
│   admin_audit_log    │      │     admin_outbox     │      │   admin_saga_tasks   │
├──────────────────────┤      ├──────────────────────┤      ├──────────────────────┤
│ id (UUIDv7, PK)      │      │ id (UUIDv7, PK)      │      │ id (UUIDv7, PK)      │
│ operation_id (FK)    │      │ operation_id (FK)    │      │ operation_id (FK)    │
│ admin_id (UUID)      │      │ topic (VARCHAR)      │      │ task_type (VARCHAR)  │
│ action (VARCHAR)     │      │ payload (JSONB)      │      │ payload (JSONB)      │
│ ip_address (VARCHAR) │      │ published (BOOLEAN)  │      │ status (PEND|RETRY)  │
│ immutable (Trigger)  │      │ locked_by / at       │      │ locked_by / at       │
└──────────────────────┘      └──────────────────────┘      └──────────────────────┘
```

---

## 3. Deep Dive: Migration Files & Tables

### Migration 00001: `admin_audit_log`
- **File**: [`00001_create_admin_audit_log.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00001_create_admin_audit_log.sql)
- **Table Created**: `admin_audit_log`

#### 1. Purpose
Provides an append-only, tamper-proof, regulatory-compliant forensic record of every action performed by an administrator (suspending accounts, freezing capital, halting order books).

#### 2. What Problem This Solves
- **Forensic Tampering & Non-Repudiation**: In financial platforms, rogue administrators or compromised services might attempt to modify or delete logs to cover unauthorized actions.
- **Legal Compliance**: Financial authorities (SEC, CFTC, MiFID II) require strict, immutable audit trails of who took an action, when, why, and from what IP address.

#### 3. How It Solves It
- Defines the `admin_audit_log` table storing caller details (`admin_id`, `ip_address`, `user_agent`), action details (`action`, `target_type`, `target_id`, `reason`), and structured context (`metadata` JSONB).
- Attaches a **PostgreSQL PL/pgSQL Trigger** (`trg_enforce_audit_immutability`) on `BEFORE UPDATE` and `BEFORE DELETE` that immediately halts the operation and throws SQL state `P0001`.
- Creates a descending index on `(admin_id, created_at DESC)` for fast admin history inspection.

```sql
CREATE TABLE IF NOT EXISTS admin_audit_log (
    id              UUID        PRIMARY KEY,
    admin_id        UUID        NOT NULL,
    operation_id    UUID,
    request_id      VARCHAR(128) NOT NULL,
    action          VARCHAR(50)  NOT NULL,
    target_type     VARCHAR(50)  NOT NULL,
    target_id       VARCHAR(128) NOT NULL,
    reason          TEXT         NOT NULL,
    metadata        JSONB        NOT NULL DEFAULT '{}',
    ip_address      VARCHAR(45),
    user_agent      TEXT,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
```

---

### Migration 00002: `admin_operations`
- **File**: [`00002_create_admin_operations.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00002_create_admin_operations.sql)
- **Table Created**: `admin_operations`

#### 1. Purpose
Serves as the central state machine and idempotency registry for all administrative mutation requests.

#### 2. What Problem This Solves
- **Double Execution / Network Retries**: If a browser, script, or proxy retries an HTTP request due to a transient timeout, a user could be suspended twice, or conflicting status updates could occur.
- **Concurrent In-Flight Collisions**: Multiple admins or simultaneous requests must not mutate the same target simultaneously with different intents without coordination.
- **Historical Payload Replay**: Allows returning the exact cached response body if a client replays an identical idempotency key.

#### 3. How It Solves It
- Enforces a composite unique constraint: `CONSTRAINT uq_admin_operation_idempotency UNIQUE (admin_id, idempotency_key)`.
- Defines an explicit state machine via SQL check constraint: `CHECK (status IN ('PENDING', 'PROCESSING', 'COMPLETED', 'FAILED'))`.
- Restricts actions to valid business operations: `CHECK (operation_type IN ('SUSPEND_USER', 'UNSUSPEND_USER', 'FREEZE_WALLET', 'UNFREEZE_WALLET', 'HALT_MARKET', 'RESUME_MARKET'))`.
- Stores cached response data in `response_body JSONB` so duplicate requests immediately return the completed result without re-executing business logic.

```sql
CREATE TABLE IF NOT EXISTS admin_operations (
    id              UUID         PRIMARY KEY,
    admin_id        UUID         NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    request_id      VARCHAR(128) NOT NULL,
    operation_type  VARCHAR(50)  NOT NULL,
    target_id       VARCHAR(128) NOT NULL,
    reason          TEXT         NOT NULL,
    status          VARCHAR(20)  NOT NULL DEFAULT 'PENDING',
    response_body   JSONB,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_admin_operation_idempotency UNIQUE (admin_id, idempotency_key)
);
```

---

### Migration 00003: `admin_outbox`
- **File**: [`00003_create_admin_outbox.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00003_create_admin_outbox.sql)
- **Table Created**: `admin_outbox`

#### 1. Purpose
Implements the **Transactional Outbox Pattern** to guarantee **At-Least-Once event publishing** to Apache Kafka without distributed 2-Phase Commit (2PC) transactions.

#### 2. What Problem This Solves
- **The Dual-Write Problem**: If the service updates PostgreSQL and then calls Kafka directly, a network crash or Kafka broker failure leaves PostgreSQL committed while Kafka never receives the event (or vice versa).
- **Silent Event Loss**: Prevents administrative events (e.g. `market.halted`, `user.suspended`) from vanishing into the void when external message brokers experience latency or brief downtime.

#### 3. How It Solves It
- The Kafka event payload is inserted into `admin_outbox` in the **exact same ACID database transaction** as `admin_operations` and `admin_audit_log`. If the transaction commits, the event is guaranteed to exist on disk.
- Equipped with worker lease fields (`locked_at`, `locked_by`) for safe horizontal scaling of outbox publisher worker nodes.
- Equipped with retry backoff columns (`attempt_count`, `max_attempts`, `next_attempt_at`, `last_error`).
- Features a **Partial Index** on `(next_attempt_at ASC) WHERE published = FALSE` so workers only poll rows that are actually ready to be delivered.

```sql
CREATE TABLE IF NOT EXISTS admin_outbox (
    id              UUID         PRIMARY KEY,
    operation_id    UUID         NOT NULL REFERENCES admin_operations(id),
    topic           VARCHAR(128) NOT NULL,
    payload         JSONB        NOT NULL,
    published       BOOLEAN      NOT NULL DEFAULT FALSE,
    published_at    TIMESTAMPTZ,
    status          VARCHAR(20)  NOT NULL DEFAULT 'PENDING'
                    CHECK (status IN ('PENDING', 'PROCESSING', 'PUBLISHED', 'FAILED')),
    attempt_count   INT          NOT NULL DEFAULT 0,
    max_attempts    INT          NOT NULL DEFAULT 10,
    next_attempt_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_error      TEXT,
    locked_at       TIMESTAMPTZ,
    locked_by       UUID,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_admin_outbox_due
    ON admin_outbox(next_attempt_at ASC)
    WHERE published = FALSE;
```

---

### Migration 00004: `admin_saga_tasks`
- **File**: [`00004_create_admin_saga_tasks.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00004_create_admin_saga_tasks.sql)
- **Table Created**: `admin_saga_tasks`

#### 1. Purpose
Implements a persistent, durable retry queue for distributed cross-service side-effects and compensations (specifically coordinating session invalidations with the Auth Service during user suspensions).

#### 2. What Problem This Solves
- **Distributed Partial Failure**: When an admin suspends a user, the Admin Service updates its own state, but it must also instruct the Auth Service to revoke all active JWT refresh tokens and kill WebSocket sessions. If the Auth Service is restarting, an inline HTTP call would fail and force the admin request to error out.
- **Cascading Latency**: Calling external services synchronously inside the primary HTTP request loop degrades response times and exhausts connection pools.

#### 3. How It Solves It
- When suspending a user, an `AUTH_INVALIDATE_SESSIONS` task is written to `admin_saga_tasks` within the primary database transaction.
- The background `SagaWorker` asynchronously polls pending tasks, executes the remote call with timeouts, and implements an exponential backoff retry schedule (up to 10 attempts).
- State transitions follow: `PENDING → RETRYING → COMPLETED | EXHAUSTED`.
- Uses a **Partial Index** `ON admin_saga_tasks(next_attempt_at ASC) WHERE status IN ('PENDING', 'RETRYING')` so completed or exhausted tasks are never scanned.

```sql
CREATE TABLE IF NOT EXISTS admin_saga_tasks (
    id              UUID         PRIMARY KEY,
    operation_id    UUID         NOT NULL REFERENCES admin_operations(id),
    task_type       VARCHAR(50)  NOT NULL,
    payload         JSONB        NOT NULL,
    status          VARCHAR(20)  NOT NULL DEFAULT 'PENDING'
                    CHECK (status IN ('PENDING', 'RETRYING', 'COMPLETED', 'EXHAUSTED')),
    attempt_count   INT          NOT NULL DEFAULT 0,
    max_attempts    INT          NOT NULL DEFAULT 10,
    next_attempt_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    last_error      TEXT,
    locked_at       TIMESTAMPTZ,
    locked_by       UUID,
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_saga_tasks_pending
    ON admin_saga_tasks(next_attempt_at ASC)
    WHERE status IN ('PENDING', 'RETRYING');
```

---

## 4. Critical Architectural & Schema Decisions

| Decision | Why We Chose It | What Happens Without It |
| :--- | :--- | :--- |
| **Monotonic UUIDv7** | Combines UNIX millisecond timestamp prefix with random entropy. Sequential inserts prevent B-Tree index fragmentation and enable natural chronological ordering. | UUIDv4 random inserts cause severe page splits and high random I/O write amplification as tables grow. |
| **Database-Level Trigger Immutability** | PL/pgSQL triggers (`trg_no_update_admin_audit_log`, `trg_no_delete_admin_audit_log`) enforce `RAISE EXCEPTION` on any UPDATE or DELETE. | Application-only validation can be bypassed by rogue queries, compromised microservices, or direct DBA mistakes. |
| **Partial Indexes (`WHERE ...`)** | Indexes only unhandled rows (`WHERE published = FALSE` and `WHERE status IN ('PENDING', 'RETRYING')`). Index size remains negligible regardless of historical row count. | Full-table indexes grow to millions of rows, causing high memory usage and slow worker polling queries. |
| **Lease Locking (`locked_at`, `locked_by`)** | Enables multiple parallel instances of `OutboxWorker` and `SagaWorker` to safely claim batches using `SELECT ... FOR UPDATE SKIP LOCKED`. | Single-worker bottleneck, or duplicate processing where two worker pods publish the same event simultaneously. |

---

## 5. End-to-End Operational Flows

### Flow 1: The Atomic Mutation Transaction Flow
This diagram illustrates how an incoming admin operation (e.g. `POST /api/v1/admin/users/suspend`) writes across all 4 tables in a single atomic ACID transaction.

```
                          Administrator Request
                    (POST /api/v1/admin/users/suspend)
                                   │
                                   ▼
                          BEGIN TRANSACTION
                                   │
         ┌─────────────────────────┼─────────────────────────┐
         ▼                         ▼                         ▼
INSERT admin_operations   INSERT admin_audit_log    INSERT admin_outbox
 (id, idempotency_key)     (id, action, reason)      (id, topic, payload)
         │                         │                         │
  Check Idempotency                │                         │
  Conflict (409)                   │                         │
         │                         │                         ▼
         │                         │               INSERT admin_saga_tasks
         │                         │               (AUTH_INVALIDATE_SESS)
         │                         │                         │
         └─────────────────────────┼─────────────────────────┘
                                   │
                                   ▼
                           COMMIT TRANSACTION
                                   │
                         HTTP 200 OK Response
```

---

### Flow 2: Non-Blocking Outbox Worker Event Publishing
The Outbox Worker processes pending events asynchronously and streams them to Apache Kafka.

```
                         OutboxWorker
                      (Every 100ms Loop)
                              │
                              ▼
            SELECT id, topic, payload FROM admin_outbox
            WHERE published = FALSE AND next_attempt_at <= NOW()
            ORDER BY next_attempt_at ASC LIMIT 50
                   FOR UPDATE SKIP LOCKED
                              │
                              ▼
                    Claimed Event Batch
                              │
                              ▼
                   Produce to Kafka Broker
                              │
               ┌──────────────┴──────────────┐
       (Broker ACK)                  (Network Timeout)
               ▼                             ▼
   UPDATE admin_outbox            UPDATE admin_outbox
   SET published = TRUE,          SET attempt_count = count + 1,
   published_at = NOW(),          next_attempt_at = NOW() + backoff,
   status = 'PUBLISHED',          last_error = err,
   locked_at = NULL,              locked_at = NULL,
   locked_by = NULL               locked_by = NULL
```

---

### Flow 3: Asynchronous Saga Worker Execution & Backoff
The Saga Worker handles side-effects to external microservices with exponential backoff and jitter.

```
                          SagaWorker
                      (Every 500ms Loop)
                              │
                              ▼
           SELECT id, payload FROM admin_saga_tasks
           WHERE status IN ('PENDING', 'RETRYING')
           AND next_attempt_at <= NOW()
                   FOR UPDATE SKIP LOCKED
                              │
                              ▼
             Execute Call: InvalidateSessions()
                              │
               ┌──────────────┴──────────────┐
       (Call Succeeded)              (Remote Service 5xx)
               ▼                             ▼
   UPDATE admin_saga_tasks        attempt < max_attempts (10)?
   SET status = 'COMPLETED',       /                        \
   completed_at = NOW(),     (Yes)/                          \(No)
   locked_at = NULL              ▼                            ▼
                        UPDATE admin_saga_tasks      UPDATE admin_saga_tasks
                        SET status = 'RETRYING',     SET status = 'EXHAUSTED',
                        attempt_count = count + 1,   last_error = 'exhausted',
                        next_attempt_at = backoff    locked_at = NULL
```

---

### Flow 4: Database-Level Tamper-Proof Audit Enforcement
Demonstrates the trigger intercepting and rejecting modification queries.

```
                    Rogue Process / SQL Console
                                 │
           UPDATE admin_audit_log SET reason = 'Modified'
                                 │
                                 ▼
                     PostgreSQL Query Engine
                                 │
                                 ▼
                 trg_enforce_audit_immutability()
                    (BEFORE UPDATE / DELETE)
                                 │
                                 ▼
              RAISE EXCEPTION (SQLSTATE 'P0001')
      "admin_audit_log entries are strictly immutable and append-only"
                                 │
                                 ▼
                     Transaction Aborted
                   Row Unchanged on Disk
```

---

## 6. Migration Execution & CLI Guide

The Admin Service uses Goose to manage migration versions stored in the `goose_db_version` table.

### Applying Migrations
To run all pending migrations up to the latest version:
```bash
goose -dir services/admin/migrations postgres "postgres://admin_user:admin_pass@localhost:5432/tradedrift_admin?sslmode=disable" up
```

### Checking Current Status
To inspect applied and pending migration scripts:
```bash
goose -dir services/admin/migrations postgres "postgres://admin_user:admin_pass@localhost:5432/tradedrift_admin?sslmode=disable" status
```

### Rolling Back
To roll back the most recent single migration:
```bash
goose -dir services/admin/migrations postgres "postgres://admin_user:admin_pass@localhost:5432/tradedrift_admin?sslmode=disable" down
```

### Creating a New Migration
To generate a new timestamped migration pair:
```bash
goose -dir services/admin/migrations create add_new_feature_table sql
```
