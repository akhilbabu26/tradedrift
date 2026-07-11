# TradeDrift — Git Branching Strategy

> **Status:** ✅ Frozen (V1.0)
> **Document:** 03_Branch_Strategy.md
> **Directory:** docs/07_Development/
> **Last Updated:** July 2026

---

## 1. Branch Naming Conventions

To keep development history organized, all code updates must be pushed inside dedicated feature branches following standard naming namespaces:

* **`feat/`:** New feature implementations (e.g. `feat/auth-login-limiter`).
* **`fix/`:** Bug fixes and corrections (e.g. `fix/wallet-deadlock-sorting`).
* **`refactor/`:** Structural reworks with no behavioral changes (e.g. `refactor/outbox-serializer`).
* **`docs/`:** Documentation updates (e.g. `docs/add-api-endpoints`).

---

## 2. Pull Request Gateways

Code is merged into the stable `main` branch through the Pull Request (PR) process:

* **Automated CI Gates:** Every PR must run automated workflows that execute:
  - Code compilation check.
  - Linter check (`golangci-lint run`).
  - Unit test suite (`go test ./...`).
* **Review Criteria:** A PR requires at least **1 peer review approval** before it can be merged.
* **Pre-Commit Checks:** Developers are encouraged to install pre-commit git hooks that automatically run formatting and short tests locally before pushing to remote branches.

---

## 3. Merge Policy

* **Squash and Merge:** All PRs must use the "Squash and Merge" merge pattern. This combines the PR commits into a single, clean commit on the `main` branch, keeping the repository git history linear and easy to trace.
