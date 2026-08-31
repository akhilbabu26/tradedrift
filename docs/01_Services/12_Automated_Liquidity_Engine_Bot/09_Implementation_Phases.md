# Liquidity Engine — V1 Implementation Phases

> **Service:** `services/liquidity-engine`
> **Version:** V1.1

Build incrementally. **Do not implement everything at once.**

---

## Phase Breakdown

| Phase | Deliverable | Key Components |
| :--- | :--- | :--- |
| **1** | MM Identity | `MM-001` account + wallet provisioned via DB seeds/migrations |
| **2** | Market Config | `MarketConfig` structs for BTC/ETH/SOL + reference prices in YAML |
| **3** | Ladder Generator | `ladder.go` with deterministic unit tests for price levels |
| **4** | Order Manager | `manager.go`: Create, Cancel, Track, Replace |
| **5** | Kafka Integration | `producer.go` + `consumer.go` ↔ Matching Engine round-trip |
| **6** | Reconciliation | `reconciler.go`: detect missing, stale, filled, cancelled levels |
| **7** | Inventory Manager | `manager.go` + `limits.go`: Normal / Low / Critical thresholds |
| **8** | Auto-Replenishment | `replenishment.go` → System Treasury → Wallet Service |
| **9** | Price Movement | Re-center ladder on reference price change (full cancel + rebuild) |
| **10** | Production Hardening | Metrics, tracing, health checks, retries, idempotency, ME epoch tracking, graceful shutdown |

---

## Phase 1 Detail — MM Identity

The `MM-001` account and wallet must exist **before** the service starts. The Liquidity Engine does not create its own identity at boot — it validates that identity exists and fails fast if not.

```go
// On startup
account, err := accountService.Get("MM-001")
if err != nil || account.Role != "MARKET_MAKER" {
    log.Fatal("MM-001 account not found or misconfigured")
}
```

Provisioned via a seed script or migration:

```sql
INSERT INTO accounts (id, type, role, status) VALUES ('MM-001', 'SYSTEM', 'MARKET_MAKER', 'ACTIVE')
  ON CONFLICT DO NOTHING;

INSERT INTO wallets (account_id, asset, balance) VALUES
  ('MM-001', 'BTC',  100),
  ('MM-001', 'ETH',  500),
  ('MM-001', 'SOL',  5000),
  ('MM-001', 'USDT', 10000000)
  ON CONFLICT DO NOTHING;
```

---

## Phase 3 Detail — Ladder Generator Tests

The ladder generator must be **purely deterministic** and unit tested without any external dependencies:

```go
func TestBTCLadder(t *testing.T) {
    cfg := MarketConfig{
        ReferencePrice: decimal.NewFromFloat(96450.00),
        SpreadBps:      decimal.NewFromInt(4),
        LevelCount:     12,
    }
    ladder := GenerateLadder(cfg)

    assert.Equal(t, "96469.29", ladder.Asks[0].Price.String())
    assert.Equal(t, "96430.71", ladder.Bids[0].Price.String())
    assert.True(t, ladder.Bids[0].Price.LessThan(ladder.Asks[0].Price))
}
```

---

## Phase 10 Detail — ME Epoch Tracking

Phase 10 must include the `ME_LIVE` epoch integration defined in `02_Architecture.md`:

- Subscribe to `system.events` Kafka topic
- On `ME_RECOVERING`: transition to `PAUSED`, stop all command generation
- On `ME_LIVE`: compare received epoch to last known epoch
  - If epoch changed: invalidate `order.Tracker`, fetch actual state from Order Service API
  - Transition to `RECONCILING`, then `RUNNING`
