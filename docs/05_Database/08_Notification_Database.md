# TradeDrift — Notification Database Design

> **Status:** ✅ Frozen (V1.0)
> **Document:** 08_Notification_Database.md
> **Directory:** docs/07_Database/
> **Last Updated:** July 2026

---

## 1. Purpose

The Notification Database manages historical alert events, client notification inboxes, templates, and processed transaction offsets to keep communications clean and idempotent.

---

## 2. Table Schemas

### 2.1 Table: `notifications`
```sql
CREATE TABLE notifications (
    id          UUID PRIMARY KEY,                      -- UUIDv7
    user_id     UUID NOT NULL,                         -- Target user reference
    title       VARCHAR(255) NOT NULL,
    message     TEXT NOT NULL,
    type        VARCHAR(30) NOT NULL,                  -- 'TRADE_FILL', 'DEPOSIT_CONFIRMED', 'SYSTEM'
    is_read     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    read_at     TIMESTAMPTZ
);
```

### 2.2 Table: `processed_events`
```sql
CREATE TABLE processed_events (
    event_id      UUID PRIMARY KEY,                      -- Outbox Event ID
    user_id       UUID NOT NULL,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

---

## 3. Query Design & Expected Patterns

### 3.1 Fetch User Inbox (Query)
```sql
SELECT id, title, message, type, is_read, created_at 
FROM notifications 
WHERE user_id = $1 
ORDER BY created_at DESC 
LIMIT $2;
```
*Index support:* Require a multi-column index on `(user_id, created_at DESC)`.

### 3.2 Write Notification on Event Consumption (Transaction)
```sql
BEGIN;

-- 1. Deduplicate event
INSERT INTO processed_events (event_id, user_id) 
VALUES ($1, $2); -- Will fail with unique violation if already processed

-- 2. Insert alert entry
INSERT INTO notifications (id, user_id, title, message, type) 
VALUES ($3, $2, $4, $5, $6);

COMMIT;
```
*Index support:* Covered by the primary key index on `event_id`.
