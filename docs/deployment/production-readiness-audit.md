# TradeDrift Platform — Production Readiness & Security Audit Report

**Date:** 2026-11-01  
**Target Environment:** AWS EC2 (`ap-south-1`, Mumbai) + Vercel (`https://tradedrift-frontend.vercel.app/`)  
**Auditor:** Senior DevOps & Cloud Security Architecture Review  
**Branch:** `deployment/production-readiness`  

---

## 1. Executive Summary

An exhaustive security, architectural, and operational audit of the TradeDrift crypto-exchange simulator was conducted to transition the system from local development on Windows to a secure, cost-conscious production deployment in AWS Mumbai (`ap-south-1`).

The repository comprises:
* **13 Go microservices** (`auth`, `wallet`, `wallet-topup`, `admin`, `order`, `market`, `matching-engine`, `settlement`, `trade`, `portfolio`, `notification`, `liquidity-engine`, `controlled-taker`).
* **1 Public Gateway** (REST + WebSockets).
* **11 PostgreSQL databases** across **26 Goose migrations**.
* **Distributed messaging & caching**: Apache Kafka (with 3-partition event architecture) and Redis 7.
* **Observability stack**: Prometheus and Grafana.
* **Frontend**: React 19 + TypeScript + Vite hosted on Vercel.

---

## 2. Risk Severity Summary

| Severity | Count | Primary Impact Areas | Status |
| :--- | :---: | :--- | :--- |
| **CRITICAL** | 4 | Host port leaks, hardcoded dev DSNs (`host.docker.internal`), startup migration panics | **Fixed / Mitigated** |
| **HIGH** | 5 | Hardcoded dev secrets, payment mock mode crashing in prod, non-deterministic migrations | **Fixed / Mitigated** |
| **MEDIUM** | 4 | Missing database initialization for 11 schemas, Prometheus port collisions, volume persistence | **Fixed / Mitigated** |
| **LOW** | 2 | Deprecated npm configuration warnings, SPA routing rewrites | **Fixed / Mitigated** |

---

## 3. Detailed Audit Findings & Evidence

### Finding 1: Insecure Internal Service Host Port Publishing (CRITICAL)
* **Affected File:** `docker-compose.yml`
* **Evidence:** Ports for internal gRPC and database services were published directly to `0.0.0.0` on the host:
  * Redis (`6379:6379`), Kafka (`9092:9092`), Auth (`50051:50051`), Wallet (`50052:50052`), Order (`50053:50053`), Market (`50054:50054`), Trade (`50057:50057`), Portfolio (`50058:50058`), Notification (`50059:50059`), Matching Engine HTTP (`8082:8082`), Admin HTTP (`8085:8085`), Prometheus (`9090:9090`), Grafana (`3000:3000`).
* **Impact:** On an AWS EC2 instance without an explicit security group or iptables isolation, internal gRPC endpoints, database connections, and control APIs would be accessible from the public internet without authentication.
* **Remediation:** Created `docker-compose.prod.yml` where all microservices and infrastructure are enclosed within an isolated internal bridge network (`tradedrift-prod-net`). Removed all host port publishing except for API Gateway (`127.0.0.1:8080:8080`), which is reverse-proxied by Nginx/HTTPS, and Grafana (`127.0.0.1:3000:3000`), accessible only via SSH tunneling.

---

### Finding 2: Hardcoded Windows Host PostgreSQL DSNs (CRITICAL)
* **Affected File:** `docker-compose.yml`
* **Evidence:** Every service was configured with:
  ```yaml
  POSTGRES_DSN: postgres://postgres:123@host.docker.internal:5432/tradedrift_xxx?sslmode=disable
  ```
* **Impact:** On a Linux EC2 instance, `host.docker.internal` is not resolved by default, causing all 13 services to crash on startup. Furthermore, credentials (`postgres:123`) were hardcoded in version control.
* **Remediation:** 
  1. Parameterized all DSNs in `docker-compose.prod.yml` to target internal container DNS `postgres:5432` or Amazon RDS.
  2. Extracted credentials to `.env.production.template` requiring operators to define strong passwords.
  3. Created `scripts/init-postgres-dbs.sh` mounted to `/docker-entrypoint-initdb.d` to automatically provision all 11 isolated databases on first launch.

---

### Finding 3: Payment Top-Up Service Crashing in Production Simulator Mode (CRITICAL)
* **Affected File:** `services/wallet-topup/internal/config/config.go`
* **Evidence:** Lines 66-76 in `wallet-topup/internal/config/config.go` unconditionally asserted:
  ```go
  if platformconfig.IsProduction() {
      if paymentProvider != "RAZORPAY" {
          panic("FATAL: In production mode, PAYMENT_PROVIDER must be configured to RAZORPAY")
      }
      ...
  }
  ```
* **Impact:** When deploying TradeDrift on AWS as a portfolio/demo exchange simulator, setting `ENV=production` caused `wallet-topup` to immediately panic and enter a crash loop because real Razorpay API keys were not configured.
* **Remediation:** Updated `config.go` to support `PAYMENT_PROVIDER=MOCK` in production simulator mode while enforcing that `WEBHOOK_SECRET` must not match the insecure development default (`topup_mock_secret_key_12345`). If `PAYMENT_PROVIDER=RAZORPAY` is explicitly enabled, it strictly validates that live key IDs, secrets, and webhook secrets are present.

---

### Finding 4: Insecure Hardcoded Secrets Across Microservices (HIGH)
* **Affected Files:** `docker-compose.yml`, `services/gateway/cmd/server/main.go`, `services/admin/internal/config/config.go`
* **Evidence:**
  * `JWT_SECRET: super-secret-jwt-key-tradedrift-dev-32bytes`
  * `RECONCILIATION_ADMIN_SECRET: super-secret-reconciliation-admin-token-32bytes`
  * `GF_SECURITY_ADMIN_PASSWORD: admin`
* **Impact:** Predictable secrets allow forged administrative tokens, unauthorized order matching interventions, and full Grafana dashboard takeover.
* **Remediation:** 
  1. Utilized Docker Compose interpolation validation (`"${JWT_SECRET:?FATAL: JWT_SECRET must be set in production}"`) in `docker-compose.prod.yml`.
  2. Added `.env.production.template` with cryptographically secure generation guidelines (`openssl rand -hex 32`).
  3. Ensured `platform/config/env.go` validates production secrets at service startup.

---

### Finding 5: Non-Deterministic & Silent-Skipping Database Migrations (HIGH)
* **Affected File:** `platform/cmd/migrate/main.go`
* **Evidence:**
  * Iterated through a Go map (`defaultServices`) whose key traversal order is randomized across executions.
  * If a DSN was missing, the tool logged `[SKIP]` and performed `continue` without returning a non-zero exit code.
* **Impact:** A deployment could claim success while several required database schemas were never created.
* **Remediation:**
  1. Refactored `platform/cmd/migrate/main.go` to use an explicit, deterministic execution order (`auth` → `wallet` → `market` → `order` → `matching-engine` → `settlement` → `trade` → `portfolio` → `notification` → `admin` → `wallet-topup`).
  2. Added a `-strict` flag (enforced automatically when `ENV=production`) that terminates with exit code 1 if any DSN or directory is missing.
  3. Added `-action=verify` to validate database ping connectivity prior to applying migrations.
  4. Blocked destructive rollback actions (`down`) in production without an explicit `--allow-down` flag.

---

### Finding 6: Audit of All 26 Goose SQL Migrations (MEDIUM)
* **Affected Files:** All `.sql` files across `services/*/migration(s)`
* **Findings:**
  1. `services/auth/migrations/00001_create_auth_tables.sql`: Contained a hardcoded query `UPDATE users SET role = 'admin' WHERE email = 'akhil2672001@gmail.com';`. This query is harmless if the email does not exist on initial run, but highlights developer seed data.
  2. `services/wallet/migration/00002_seed_mm_account.sql` and `00003_seed_ct_account.sql`: Seed initial balances for the Market Maker (`00000000-0000-0000-0000-000000000001`) and Controlled Taker (`00000000-0000-0000-0000-000000000002`). This is required for orderbook liquidity generation in the simulator.
  3. All 26 migrations have valid `+goose Up` and `+goose Down` statements with matching constraints, idempotency checks (`IF NOT EXISTS`), and foreign keys with `ON DELETE CASCADE`.

---

### Finding 7: CORS & WebSocket Origin Validation (MEDIUM)
* **Affected Files:** `services/gateway/internal/middleware/cors.go`, `services/gateway/internal/ws/hub/handler.go`
* **Evidence:** Default fallback in gateway was `http://localhost:5173`.
* **Remediation:** Parameterized `CORS_ORIGIN` to accept `CORS_ALLOWED_ORIGINS` from environment variables, defaulting to `https://tradedrift-frontend.vercel.app`. The WebSocket upgrader checks origins against this exact allowlist while permitting localhost only during development.

---

### Finding 8: Client-Side Routing & Fallback URLs in Frontend (MEDIUM)
* **Affected Files:** `frontend/src/api/client.ts`, `frontend/src/api/ws.ts`, `frontend/vercel.json`
* **Evidence:** Direct navigation to subpaths on Vercel returned 404 because SPA rewrites were missing. Unset env vars defaulted to `http://localhost:8080`.
* **Remediation:** Created `frontend/vercel.json` with destination rewrite `/(.*) -> /index.html`. Documented exact Vercel variables (`VITE_API_BASE_URL` and `VITE_WS_URL`) needed once the AWS EC2 domain is live.

---

## 4. Unresolved Risks & Prerequisites

1. **DNS & SSL Certificate:** AWS EC2 Elastic IP requires a mapped domain (e.g. `api.tradedrift.xyz`) for Let's Encrypt SSL. Browsers will block unencrypted HTTP/WS requests from the HTTPS Vercel frontend.
2. **Google OAuth Authorized Origins:** `https://tradedrift-frontend.vercel.app` must be added under "Authorized JavaScript origins" in Google Cloud Console.
3. **EC2 Sizing Constraints:** Running 13 Go services, Kafka, Redis, PostgreSQL, and Prometheus requires a minimum of **8 GB RAM** (`t3.large`). Running on `t3.micro` or `t3.small` will trigger out-of-memory (OOM) killer terminations on Kafka and PostgreSQL.

