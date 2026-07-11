# TradeDrift — Coding Standards

> **Status:** ✅ Frozen (V1.0)
> **Document:** 02_Coding_Standards.md
> **Directory:** docs/07_Development/
> **Last Updated:** July 2026

---

## 1. Code Quality & Formatting

All Go source code must adhere to standard community rules to guarantee readability:
* **Formatting:** All files must run through `gofmt` and `goimports` to maintain unified spacing and import groupings.
* **Linter Tooling:** Pull requests must pass lint checks using the standard **`golangci-lint`** suite. The linter configuration checks for:
  - Deadcode/unused variables.
  - Error checking omissions (all returned errors must be checked).
  - Shadowed variable logic issues.

---

## 2. Context Lifecycle & Deadline Enforcement

All asynchronous network calls, REST routing, and database queries must support Go's `context.Context` library:
* **Context Propagation:** `ctx context.Context` must be passed as the first parameter to all boundary functions (gRPC calls, SQL mutations, and outbox handlers).
* **Deadline Policy:** Client-side context calls must define explicit timeouts:
  - Internal gRPC calls: Enforce a maximum context deadline of **2,000ms**.
  - Database SQL queries: Enforce a maximum context query deadline of **5,000ms**.
* **Trace Context:** Context blocks must carry W3C distributed trace values (`traceparent`) to propagate telemetry across network hops.

---

## 3. Structured Logging Conventions

Services must output logs to `stdout`/`stderr` as single-line structured **JSON** streams:
* **Log Level Guidelines:**
  - `DEBUG`: Local developer logging (suppressed in production environments).
  - `INFO`: Standard lifecycle events (such as server start, connection completed).
  - `WARN`: Recoverable errors (such as transient database timeouts or validation failures).
  - `ERROR`: Severe failures (such as panic recovery, database connection drops, or settlement failures).
* **Standard Key Attributes:**
  - `level`: The severity string (`info`, `error`, etc.).
  - `message`: Text message explaining the event.
  - `time`: RFC3339 timezone-aware timestamp.
  - `userId` / `orderId` / `tradeId`: Domain context identifiers where available.
  - `traceId`: The propagated tracing context ID.
