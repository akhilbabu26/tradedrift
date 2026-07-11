# TradeDrift — API Design specifications

> **Status:** ✅ Designed (V1.0)
> **Document:** README.md
> **Directory:** docs/06_APIs/
> **Last Updated:** July 2026

---

## 1. Purpose

This directory contains the API design specifications, request-response payloads, streaming message formats, and health probe definitions for all TradeDrift services.

---

## 2. Directory Index

This directory is organized into the following modular design documents:

* **[`01_API_Standards.md`](01_API_Standards.md):** Defines our REST versioning, request casing mapping, idempotency headers, cursor paginations, query filters, rate limits, correlation logs, and error catalogs.
* **[`02_Authentication_API.md`](02_Authentication_API.md):** User register, login, credentials validation, refresh, logout, and change-password endpoints.
* **[`03_Wallet_API.md`](03_Wallet_API.md):** User balance checking, supported assets retrieval, and simulated deposit/withdrawal endpoints.
* **[`04_Order_API.md`](04_Order_API.md):** Limit and market order placements, cancellation endpoints, and order state queries.
* **[`05_Market_API.md`](05_Market_API.md):** Active trading pairs listing, L2 orderbook depth snapshots, and execution transaction logs.
* **[`06_Notification_API.md`](06_Notification_API.md):** User notification alerts inbox and read-state updates.
* **[`07_Portfolio_API.md`](07_Portfolio_API.md):** Holdings revaluation and PnL summaries.
* **[`08_Admin_API.md`](08_Admin_API.md):** User suspension, wallet freeze, trading halts, and resuming controls.
* **[`09_WebSocket_API.md`](09_WebSocket_API.md):** Real-time subscription controls, socket handshakes, and public/private message frames.
* **[`10_Health_API.md`](10_Health_API.md):** Probes for liveness, readiness, and components checkups.

---

## 3. API Diagrams Catalog

The following diagrams are saved inside the `diagrams/` folder and render natively in any standard markdown viewer or browser:

1. **[`API_Gateway_Routing.svg`](diagrams/API_Gateway_Routing.svg):** Maps client requests through authorization and rate-limiting gates to backend gRPC services.
2. **[`WebSocket_Subscription_Flow.svg`](diagrams/WebSocket_Subscription_Flow.svg):** Shows connection lifecycle, authentication, subscription parsing, and client push distributions.
