# TradeDrift — Contribution Guide

> **Status:** ✅ Frozen (V1.0)
> **Document:** 05_Contribution_Guide.md
> **Directory:** docs/07_Development/
> **Last Updated:** July 2026

---

## 1. Developer Workflow

To contribute code or documentation changes to TradeDrift, follow these steps:

1. **Check Out Feature Branch:** Create a branch from `main` (e.g. `git checkout -b feat/wallet-transfers`).
2. **Implement Logic & Tests:** Write Go code following [Coding Standards](02_Coding_Standards.md) and include unit test coverage.
3. **Run Checks Locally:** Execute formatting, linter runs, and tests locally:
   ```bash
   gofmt -s -w .
   golangci-lint run
   go test -short ./...
   ```
4. **Push & Create PR:** Push to origin and open a Pull Request against the `main` branch.

---

## 2. Commit Message Convention

TradeDrift uses structured commit messages to generate clean release logs. Commits must follow this prefix pattern:

* **`feat: [description]`:** Merges new features.
* **`fix: [description]`:** Corrects a bug or code logic defect.
* **`docs: [description]`:** Documentation updates.
* **`refactor: [description]`:** General code reworks.
* *Example:* `feat: add deposit funds idempotency checks`

---

## 3. Pull Request Review Checklist

Before merging a PR, the reviewer and author must verify the checklist:

* [ ] **Compilation:** Code compiles cleanly without warning flags.
* [ ] **Testing:** Unit tests cover new paths and pass successfully.
* [ ] **Linting:** All linter checks pass with zero violations.
* [ ] **Database Schema (if changed):** Migration scripts are added to `schema/` and numbered in correct sequence matching `docs/05_Database/11_Migration_Order.md`.
* [ ] **Protobuf (if changed):** Interfaces are updated, and stubs are recompiled in the shared platform API folder.

---

## 4. Architecture Governance & Change Request (ACR)

Once design specs are frozen, developers **cannot bypass or alter specifications** during normal development.
* **ACR Required:** Any change that modifies database schemas, API JSON contracts, shared platform interfaces, or Kafka topic names must go through a formal **Architecture Change Request (ACR)**.
* **Zero Bypass Policy:** Pull requests that bypass the ACR gate or contain unauthorized schema/contract modifications will be automatically rejected.
