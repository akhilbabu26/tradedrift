# TradeDrift Services Specifications Directory

> **Status:** ✅ Services Designed (V1.0)
> **Document:** README.md
> **Directory:** docs/01_Services/
> **Last Updated:** July 2026

---

## 1. Purpose

This directory contains the detailed service design specifications for each microservice in the TradeDrift platform. Each service specification defines its database schemas, API contracts (gRPC/REST), state machines, outbox events, and resilience policies.

---

## 2. Services Specification Catalog

* **[`04_API_Gateway`](04_API_Gateway/04_API_Gateway.md):** Ingress gateway middleware pipeline (CORS, rate limit checks, route resolution, JWT validations, gRPC forwarding).
* **[`05_Authentication_Service`](05_Authentication_Service/05_Authentication_Service.md):** Session lifecycles, user profiles management, refresh token rotation, and active blacklist caches.
* **[`07_Wallet_Service`](07_Wallet_Service/07_Wallet_Service.md):** In-ledger balance adjustments, reservations, and V10 deposit/withdrawal funding lifecycles.
* **[`08_Order_Service`](08_Order_Service/08_Order_Service.md):** Order validations, UUIDv7 generation, fund reservation triggers, saga offsets management, and cancels.
* **[`09_Matching_Engine`](09_Matching_Engine/README.md):** High-performance in-memory matching logic, red-black tree books, execution loops, and checkpoints.
* **[`Settlement_Service`](Settlement_Service/Settlement_Service.md):** Asynchronous double-leg balance settlement engine, short-lived transactions, and DLQ retries.
* **[`Trade_Service`](Trade_Service/Trade_Service.md):** Query model tracking executed trades for charts, tickers, and public history endpoints.
* **[`10_Market_Service`](10_Market_Service/10_Market_Service.md):** Handles market status validation, Go singleflight cache request coalescing, and ticker feeds.
* **[`11_Portfolio_Service`](11_Portfolio_Service/11_Portfolio_Service.md):** Computes user historical PnL, holdings allocations, and performance metrics.
* **[`12_Notification_Service`](12_Notification_Service/12_Notification_Service.md):** Push alerts, SMS, and email queues reacting to ledger updates.
