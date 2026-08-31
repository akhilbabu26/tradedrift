# Automated Liquidity Engine — Documentation Index

> **Service:** `services/liquidity-engine`
> **Version:** V1.1
> **Status:** 🚀 Designed

---

## Documents

| # | File | Contents |
| :--- | :--- | :--- |
| 01 | [01_Overview.md](./01_Overview.md) | High-level summary, V1.1 goals |
| 02 | [02_Architecture.md](./02_Architecture.md) | System diagram, responsibility boundaries, ME recovery interaction (4-layer mitigation, LE state machine) |
| 03 | [03_Identity_And_Wallet.md](./03_Identity_And_Wallet.md) | MM-001 account model, permissions, wallet immutability rule, system treasury |
| 04 | [04_Market_Config_And_Pricing.md](./04_Market_Config_And_Pricing.md) | MarketConfig struct, V1 default values, reference price, ladder generation |
| 05 | [05_Order_Management.md](./05_Order_Management.md) | Order identity, naming convention, order manager, reconciliation engine, fill handling |
| 06 | [06_Inventory_Management.md](./06_Inventory_Management.md) | Thresholds, inventory-aware skew, automatic replenishment |
| 07 | [07_Kafka_And_Redis.md](./07_Kafka_And_Redis.md) | Kafka event contracts (publish/consume), Redis read-only usage |
| 08 | [08_Lifecycle.md](./08_Lifecycle.md) | Startup flow, runtime event loop, graceful shutdown, directory structure |
| 09 | [09_Implementation_Phases.md](./09_Implementation_Phases.md) | 10-phase incremental build plan with Phase 1/3/10 detail |
| 10 | [10_Mental_Model.md](./10_Mental_Model.md) | STP rules, no micro-trader rationale, final system diagram, design validation checklist |

---

## The Three Fundamental Rules

> *The Liquidity Engine provides the counterparty.*
> *The Matching Engine performs the match.*
> *Settlement moves the assets.*

> *When ME is down, LE stops being a producer.*
> *When ME becomes live, LE becomes a reconciler first and a liquidity provider second.*
