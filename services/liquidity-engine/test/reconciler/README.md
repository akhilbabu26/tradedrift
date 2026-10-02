# package `reconciler_test` (`test/reconciler/`)

## Purpose

Contains the comprehensive unit, snapshot, generation, timeout, and invariant test suites for the **Liquidity Engine Reconciler**.

Test files are housed in this dedicated `test/reconciler` folder (`package reconciler_test`) to ensure that `internal/reconciler/` remains clean, uncluttered, and focused exclusively on production code.

---

## Test Suites Overview

| Test File | Focus Area | Key Scenarios Tested |
|:---|:---|:---|
| [`mocks_test.go`](./mocks_test.go) | Shared Fixtures & Doubles | `mockProducer`, `mockOrderSvc`, `mockMetrics`, and `newInvTestRec()` helper. |
| [`reconciler_test.go`](./reconciler_test.go) | Timeouts, Expiry & Wiring | Capital exposure caps enforcement (`MaxBidExposureUSDT`, `MaxAskExposureBase`), 30-minute slot order expiry (`CheckExpiredOrders`), live reference price provider integration (`SetRefProvider`), and timeout state machine handling. |
| [`reconciler_snapshot_test.go`](./reconciler_snapshot_test.go) | ME Snapshot Sync & Hysteresis | 2-cycle hysteresis on orders missing from ME snapshots (T11a), RESTING order promotion (T11b), ME recovery pause with zero mutations (T16), CANCELLING state preservation during in-flight snapshot visibility (T19), snapshot restart identity preservation (T22, T23, T27), and fail-closed behavior on nil ME client. |
| [`reconciler_generation_test.go`](./reconciler_generation_test.go) | Generations, Deduplication & Retries | Slot locking preventing Diff create (T12), orphan cancellation in ME (T13, T15), cancel failure retry paths (T14), partial fill quantity sync (T17), generation monotonicity across restarts (T18), transient OS NOT_FOUND hysteresis (T20), old generation locking (T21), and orphan cancel cooldowns & failure handling. |
| [`reconciler_invariants_test.go`](./reconciler_invariants_test.go) | Integration Invariant Properties | Stable ladder convergence across consecutive reconcile cycles, single fill single replacement, partial fill zero-mutation stability, inventory skew count allocation, and restart deduplication. |
| [`refstate_test.go`](./refstate_test.go) | Price Movement Classification | Comprehensive table-driven tests for `ClassifyMovement`: `ActionKeep`, `ActionSelectiveReprice`, `ActionControlledRebase`, `ActionHold` (STALE), `ActionPause` (PAUSED). |
| [`refstate_invariants_test.go`](./refstate_invariants_test.go) | Multi-Cycle Rebase & Lifecycle Invariants (21 Tests) | Persistent multi-cycle rebase, target version tracking, batch price uniqueness, live reference & version stamped on expiry, provider outage decay (`FRESH → STALE → PAUSED`), PAUSED zero-mutation invariant with orphan/missing orders, replacement capital cap enforcement, partial-fill quantity sizing `min(desiredQty, RemainingQty)`, dust validation, rebase awaiting in-flight PENDING/OS_REGISTERED old versions, provider recovery after PAUSED, fully filled cancellation zero-replacement, PAUSED dropped replacements, expiry remaining quantity sizing and zero-remaining protection, rebase remaining quantity sizing and zero-remaining protection, PENDING retry original command preservation (`OriginalQty`, same IDs), rebase generation failure metrics & non-completion, and PAUSED zero mutations across all independent lifecycle paths. |

---

## Running the Tests

To run all reconciler tests:
```powershell
$env:GOTOOLCHAIN="local"; go test -v ./test/reconciler/...
```

To run all tests across the entire Liquidity Engine:
```powershell
$env:GOTOOLCHAIN="local"; go test ./...
```
