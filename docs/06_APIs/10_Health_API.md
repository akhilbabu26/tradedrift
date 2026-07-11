# TradeDrift — Health API

> **Status:** ✅ Frozen (V1.0)
> **Document:** 10_Health_API.md
> **Directory:** docs/06_APIs/
> **Last Updated:** July 2026

---

## 1. Kubernetes Health Probe Specification

Every service in the TradeDrift platform must expose standard HTTP health routes to integrate with container orchestration scheduling and load-balancer pools.

---

## 2. Probe Routes

### 2.1 GET `/live` (Liveness Probe)
Checks if the application service process is running.
* **Response `200 OK`:**
  ```json
  {
    "status": "UP",
    "timestamp": "2026-07-11T13:00:00Z"
  }
  ```
* *Failure Behavior:* If the service fails to return a `200 OK` (e.g. locks or deadlocks), Kubernetes will restart the container.

---

### 2.2 GET `/ready` (Readiness Probe)
Validates that the service has active, functional connections to all of its downstream databases (PostgreSQL, Redis, Kafka brokers).
* **Response `200 OK`:**
  ```json
  {
    "status": "READY",
    "timestamp": "2026-07-11T13:00:00Z"
  }
  ```
* **Failure Response `503 Service Unavailable`:**
  If a downstream connection (like PostgreSQL database) is unreachable or disconnected:
  ```json
  {
    "status": "NOT_READY",
    "details": {
      "postgres": "DISCONNECTED",
      "redis": "CONNECTED",
      "kafka": "CONNECTED"
    },
    "timestamp": "2026-07-11T13:00:05Z"
  }
  ```
* *Failure Behavior:* If a service returns `503 Not Ready`, Kubernetes will remove it from the ingress/service router pool, preventing clients from hitting a broken instance.

---

### 2.3 GET `/health` (Health Assessment Probe)
Returns a verbose diagnostics summary for administrators and monitoring daemons.
* **Response `200 OK`:**
  ```json
  {
    "status": "UP",
    "version": "1.0.4",
    "uptimeSeconds": 86420,
    "systemInfo": {
      "goVersion": "go1.21",
      "goroutines": 45,
      "memoryAllocatedMb": 24.5
    },
    "dependencies": {
      "postgres": "UP",
      "redis": "UP",
      "kafka": "UP"
    }
  }
  ```
