# TradeDrift — Developer Guidelines Specs

> **Status:** ✅ Designed (V1.0)
> **Document:** README.md
> **Directory:** docs/07_Development/
> **Last Updated:** July 2026

---

## 1. Purpose

This directory contains the developer-facing specifications, project organization structures, code quality requirements, branch strategies, testing guidelines, and review gates for the TradeDrift platform.

---

## 2. Directory Index

This directory is organized into the following modular documents:

* **[`01_Project_Structure.md`](01_Project_Structure.md):** Outlines the multi-module Go monorepo directories mapping for `/platform` code, `/services` backends, `/proto` schemas, and `/deployments`.
* **[`02_Coding_Standards.md`](02_Coding_Standards.md):** Defines our Go formatting checks, `golangci-lint` parameters, context timeouts, and structured log keys.
* **[`03_Branch_Strategy.md`](03_Branch_Strategy.md):** Git branch naming conventions (`feat/`, `fix/`), PR review rules, and linear squash merging policies.
* **[`04_Testing_Strategy.md`](04_Testing_Strategy.md):** Unit test mocking parameters and local docker-compose integration testing guidelines.
* **[`05_Contribution_Guide.md`](05_Contribution_Guide.md):** Pull request checklist, commit message conventions, and architecture review controls.
