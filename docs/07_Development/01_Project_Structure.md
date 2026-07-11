# TradeDrift — Project Structure

> **Status:** ✅ Frozen (V1.0)
> **Document:** 01_Project_Structure.md
> **Directory:** docs/07_Development/
> **Last Updated:** July 2026

---

## 1. Monorepo Organization

TradeDrift is organized as a single Git **Multi-Module Monorepo** written in Go. This allows developers to work on all services and shared code inside a unified repository while maintaining clean module boundaries.

---

## 2. Directory Layout

The repository is structured as follows:

```
tradedrift/
  ├── go.work                         # Go multi-module workspace definition
  ├── proto/                          # Canonical protobuf contracts (Frozen)
  │     └── [service]/v1/
  │           └── [service].proto
  ├── platform/                       # Shared platform SDK library module
  │     ├── go.mod
  │     ├── api/                      # Compiled Go protobuf stubs
  │     ├── uuid/                     # Shared UUIDv7 generator package
  │     ├── outbox/                   # Generic database outbox loop engine
  │     ├── jwt/                      # Shared JWT authorization verification
  │     └── [sdk]/                    # Reserved namespaces (config, logger, etc.)
  ├── services/                       # Standalone microservice modules
  │     ├── auth/
  │     │     ├── go.mod              # Module boundaries per service
  │     │     └── main.go
  │     ├── wallet/
  │     ├── order/
  │     └── ...
  ├── deployments/                    # Infrastructure deployment manifests
  │     ├── docker-compose.yml        # Local development stack compose file
  │     └── k8s/                      # Kubernetes manifests
  └── docs/                           # Central platform documentation
```

---

## 3. Modular Boundaries Guidelines

* **Shared Platform SDK (`/platform`):** Contains library packages that expose generic, reusable capabilities. This module is completely self-contained and **must never import service packages** to prevent circular reference compilation errors.
* **Microservices (`/services/[name]`):** Each microservice is an independent, runnable module with its own `go.mod`. Services import the shared platform library using Go Workspaces:
  ```go
  import "tradedrift/platform/uuid"
  ```
* **Protobuf Schemas (`/proto`):** Standard protobuf contract templates are managed centrally and compiled into the `/platform/api` folder using `make` to maintain schema consistency.
