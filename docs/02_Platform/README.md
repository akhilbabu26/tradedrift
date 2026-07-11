# TradeDrift Platform Infrastructure specifications

> **Status:** ✅ Infrastructure Designed (V1.0)
> **Document:** README.md
> **Directory:** docs/02_Platform/
> **Last Updated:** July 2026

---

## 1. Purpose

This directory contains specifications detailing our underlying infrastructure, messaging channels, cache strategies, databases layout, Kubernetes setups, tracing limits, and administration pipelines.

---

## 2. Infrastructure Specifications Catalog

* **[`13_Event_Driven_Architecture.md`](13_Event_Driven_Architecture.md):** Choreography logic sagas, outbox sequence locks, and at-least-once deduplication patterns.
* **[`14_Fund_Reservation_Contract.md`](14_Fund_Reservation_Contract.md):** Core wallet-reservation balance mathematical bounds and background cleanup daemon schedules.
* **[`15_Kafka_Topic_Design.md`](15_Kafka_Topic_Design.md):** Topic names, schemas, partition keys, payload properties, and replication parameters.
* **[`16_gRPC_Contracts.md`](16_gRPC_Contracts.md):** Protocol Buffers schemas, shared types (`common.proto`), and service deadlines.
* **[`17_Redis_Architecture.md`](17_Redis_Architecture.md):** Orderbook snapshot storage structures, session blacklist caches, and cluster clustering.
* **[`18_PostgreSQL_Design.md`](18_PostgreSQL_Design.md):** Tables indexing, partition splits, WAL settings, and execution plans optimization.
* **[`19_WebSocket_Architecture.md`](19_WebSocket_Architecture.md):** Live feeds fanning, socket timeouts, ping/pong frames, and client connection capacities.
* **[`20_Deployment.md`](20_Deployment.md):** Kubernetes StatefulSets manifest variables, HA PodDisruptionBudgets, and rollout bounds.
* **[`21_Observability.md`](21_Observability.md):** Hybrid tracing trace parent propagation standard, W3C standards, and alert catalogs.
* **[`22_Disaster_Recovery.md`](22_Disaster_Recovery.md):** Async cross-region latency limits, promote commands, and restoration drills.
* **[`24_Admin_Workflows.md`](24_Admin_Workflows.md):** Administrative procedures (halts, suspension saga, freeze wallet), mTLS, and rate limits.
