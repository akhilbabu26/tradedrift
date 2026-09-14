# Feature 18: Immutable Enterprise Audit Trail

## 1. What This Feature Does
The **Immutable Enterprise Audit Trail** provides tamper-evident, regulatory-grade accountability for every single mutation executed within the Admin Service:
* **The `admin_audit_log` Table**: Every administrative action automatically inserts an immutable row recording *who* performed the action, *what* was altered, *when*, from *which IP address*, and under *what justification*.
* **Append-Only Guarantee**: There are zero API endpoints, repository methods, or database triggers that permit `UPDATE` or `DELETE` operations on the audit log.

---

## 2. Why We Need It
In regulated financial systems, non-repudiation and forensic auditability are legal mandates:
1. **Regulatory Compliance (SOC2, ISO 27001, AML/CFT)**: Regulators require proof that accounts were suspended or wallets were frozen for valid, documented reasons.
2. **Forensic Accountability**: If an unauthorized market halt occurs, the audit log identifies the exact cryptographic JWT identity, remote IP address, and browser User-Agent that submitted the command.
3. **End-to-End Correlation**: Propagates a single `request_id` from the HTTP header through PostgreSQL, Kafka outbox events, and downstream gRPC calls.

---

## 3. Where We Used It in Code

| Layer | File Path | Key Functions / Responsibilities |
| :--- | :--- | :--- |
| **Audit Repository** | [`services/admin/internal/repository/postgres/audit_repo.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/audit_repo.go) | `InsertAuditLog`, `GetAuditLogsInWindow`, `ListAuditLogs` |
| **Transaction Manager**| [`services/admin/internal/repository/postgres/tx_manager.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/repository/postgres/tx_manager.go) | Inserts audit entry in atomic transaction alongside operation |
| **Context Middleware** | [`services/admin/internal/handler/middleware.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/internal/handler/middleware.go#L40-L75) | Extracts `admin_id`, `remote_ip`, `user_agent`, `request_id` |
| **Database Schema** | [`services/admin/migrations/00001_create_admin_audit_log.sql`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/admin/migrations/00001_create_admin_audit_log.sql) | DDL with indexes on `(admin_id)`, `(target_id)`, and `(created_at)` |

---

## 4. How We Achieve This Feature

### Structured Audit Schema
Each audit record captures comprehensive operational context:
```sql
CREATE TABLE admin_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id VARCHAR(64) NOT NULL,
    action_type VARCHAR(64) NOT NULL,      -- e.g. HALT_MARKET, SUSPEND_USER
    target_id VARCHAR(128) NOT NULL,       -- e.g. BTC-USDT, user_uuid
    reason TEXT NOT NULL,                  -- Minimum 5 characters
    ip_address VARCHAR(45) NOT NULL,       -- IPv4 or IPv6
    user_agent TEXT NOT NULL,
    request_id VARCHAR(64) NOT NULL,       -- UUIDv7 tracing correlation
    metadata JSONB,                        -- Extended contextual data
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### Atomic Commit Guarantee
The audit record is inserted **inside the same PostgreSQL transaction** that updates the operational state. If the state change rolls back, no phantom audit entry is left behind; if the state change succeeds, the audit entry is guaranteed to exist.

---

## 5. Execution Flow

```
                               ADMIN SUBMITS OPERATIONAL MUTATION
                                               │
                                               ▼
                             ┌───────────────────────────────────┐
                             │    StructuredLoggingMiddleware    │
                             │  - Extracts Client IP & User-Agent│
                             │  - Injects/Generates Request-ID   │
                             └─────────────────┬─────────────────┘
                                               │
                                               ▼
                             ┌───────────────────────────────────┐
                             │       RequireAdmin Middleware     │
                             │  - Validates Cryptographic JWT    │
                             │  - Injects Verified admin_id      │
                             └─────────────────┬─────────────────┘
                                               │
                                               ▼
                             ┌───────────────────────────────────┐
                             │   POSTGRESQL ATOMIC TRANSACTION   │
                             │  ├── INSERT INTO admin_operations │
                             │  ├── INSERT INTO admin_outbox     │
                             │  └── INSERT INTO admin_audit_log: │
                             │      - actor: admin_id            │
                             │      - target: target_id          │
                             │      - reason: reason             │
                             │      - ip: client_ip              │
                             │      - trace: request_id          │
                             └─────────────────┬─────────────────┘
                                               │
                                               ▼
                             ┌───────────────────────────────────┐
                             │ TRANSACTION ATOMICALLY COMMITTED  │
                             │ Immutable Forensic Record Stored  │
                             └───────────────────────────────────┘
```
