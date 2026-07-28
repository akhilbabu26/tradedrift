# `platform/postgres` Package

This package provides the foundational PostgreSQL infrastructure for the TradeDrift platform.
It exposes two responsibilities: **running database migrations** and **managing a connection pool**.

---

## Files

### [`migrate.go`](./migrate.go) — Database Migrations via Goose

This file exposes a single function, `RunMigrations`, which applies SQL migrations to the database before the application starts.

#### What it does
1. Opens a standard `database/sql` connection using the `pgx` driver.
2. Pings the database to confirm it is reachable.
3. Calls **Goose** to run all pending `Up` migrations from a given migrations directory.
4. Closes the connection when done — the caller doesn't manage any DB handle.

#### What is Goose?

[Goose](https://github.com/pressly/goose) is a database migration tool for Go. It works by reading versioned SQL (or Go) migration files from a directory and tracking which ones have already been applied using a `goose_db_version` table inside the database itself.

**Key concepts:**
| Term | Meaning |
|---|---|
| `goose.Up` | Applies all pending migrations in version order |
| `goose.Down` | Rolls back the last applied migration |
| `goose.SetDialect` | Tells Goose which SQL dialect to use (here: `"postgres"`) |
| Migration file | A `.sql` file prefixed with a version number, e.g. `00001_create_users.sql` |

**Why Goose?**  
It keeps database schema changes version-controlled and reproducible across environments (dev, staging, prod) without manual SQL scripts.

---

### [`pool.go`](./pool.go) — Connection Pool via pgxpool

This file exposes `NewPool`, which creates and returns a live, pre-verified `*pgxpool.Pool` that services use to talk to PostgreSQL.

#### What it does
1. Parses the DSN (Data Source Name / connection string) into a `pgxpool` config.
2. Applies service-defined settings from `PoolConfig` (e.g. max connections, idle timeouts).
3. Creates the pool and pings the database.
4. Returns the pool, or closes it and returns an error if the ping fails.

#### What is a Connection Pool?

A **connection pool** is a cache of reusable database connections. Instead of opening a new TCP connection to PostgreSQL for every query (which is expensive), a pool keeps a set of connections alive and hands them out on demand.

**`pgxpool`** is the built-in pool from the [pgx](https://github.com/jackc/pgx) driver — the fastest and most feature-complete PostgreSQL driver for Go.

**`PoolConfig` fields:**
| Field | Purpose |
|---|---|
| `MaxConns` | Maximum number of open connections in the pool |
| `MinConns` | Minimum connections kept alive at all times |
| `MaxConnLifetime` | How long a connection can live before being replaced |
| `MaxConnIdleTime` | How long an idle connection is kept before being closed |
| `HealthCheckPeriod` | How often pgxpool checks that connections are still alive |

**Why a pool?**  
High-throughput services like TradeDrift would exhaust PostgreSQL's connection limit instantly if they opened a new connection per request. The pool keeps connection count bounded and reuse efficient.

---

## How they work together

```
Application Start
      │
      ▼
RunMigrations(dsn, dir)   ← migrate.go
      │  applies all pending SQL schema changes
      ▼
NewPool(ctx, dsn, cfg)    ← pool.go
      │  creates a live connection pool
      ▼
Service receives *pgxpool.Pool and starts handling requests
```

Migrations run **once at startup** using a short-lived connection.  
The pool is created **after** and lives for the entire lifetime of the service.

---

## Why Does PostgreSQL Need a Connection?

PostgreSQL is a **separate process** — it runs on its own server or container, completely outside your Go application. Your code cannot directly read or write to it. It must first establish a **connection** — a dedicated communication pipe — before it can send any SQL queries.

### What is TCP?

**TCP (Transmission Control Protocol)** is the standard way two programs communicate over a network. It guarantees that data sent from one side arrives at the other side in order and without corruption.

When your Go app connects to PostgreSQL:
1. Your app and PostgreSQL perform a **TCP handshake** — a 3-step exchange to open the pipe.
2. If TLS (encryption) is enabled, a **TLS handshake** follows.
3. PostgreSQL **authenticates** you (checks username + password).
4. PostgreSQL spawns a **dedicated backend process** just for your connection.
5. The TCP socket is now open — SQL can flow through it.

```
Your Go App                    PostgreSQL Server
     │                                │
     │──── TCP SYN ──────────────────►│
     │◄─── TCP SYN-ACK ───────────────│   (1) TCP handshake
     │──── TCP ACK ──────────────────►│
     │                                │
     │──── TLS ClientHello ──────────►│   (2) TLS handshake (if enabled)
     │◄─── TLS ServerHello ───────────│
     │                                │
     │──── Auth (user/password) ─────►│   (3) Authentication
     │◄─── Auth OK ───────────────────│
     │                                │   (4) PostgreSQL spawns a backend process
     │══════ Connection Ready ════════│
     │                                │
     │──── SELECT * FROM orders ─────►│   (5) SQL queries flow
     │◄─── rows ──────────────────────│
```

### Why is Opening a Connection Expensive?

Each new connection requires all the steps above, which involves:

| Step | Typical Cost |
|---|---|
| TCP handshake | ~1ms (local) / ~10–100ms (remote) |
| TLS handshake | ~5–10ms |
| Authentication round-trips | ~1–5ms |
| PostgreSQL spawning backend process | ~5–20ms |
| **Total per connection** | **~10–50ms+** |

If your app opened a **new connection for every request**, a service handling 1,000 requests/sec would spend most of its time just shaking hands — not doing real work.

### Why the Pool Solves This

The pool opens connections **once at startup** and keeps them alive. When a request comes in:
- It **borrows** an existing connection from the pool (near zero cost).
- Runs the query.
- **Returns** the connection for the next request to reuse.

```
Without pool:  [request] → open connection (50ms) → query (2ms) → close → total: ~52ms
With pool:     [request] → borrow connection (0ms) → query (2ms) → return → total: ~2ms
```

This is why `NewPool` is called **once at startup** and the `*pgxpool.Pool` is passed around to every service — it's a long-lived, shared resource.
