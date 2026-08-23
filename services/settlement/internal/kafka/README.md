# Settlement Service — Kafka Consumer (`internal/kafka`)

> **Package:** `tradedrift/services/settlement/internal/kafka`  
> **File:** `consumer.go`  
> **Topic Consumed:** `trades.executed`  
> **Consumer Group:** `settlement-service-group`  
> **Commit Strategy:** Manual — offset committed only after Phase 3 (MarkSettled) succeeds

---

## 1. Purpose

The `kafka` package is the **event ingestion layer** of the Settlement Service. It is the single entry point through which matched trade events enter the system. It handles three failure modes that are fundamental to at-least-once Kafka consumers:

| Failure Mode | Risk | How `consumer.go` Handles It |
|---|---|---|
| **Poison pill** | Malformed JSON blocks the partition forever on retry | Logged + offset committed immediately to unblock |
| **Premature ACK** | Offset committed before settlement completes → data loss | `CommitMessages` called only after Phase 3 success |
| **Duplicate delivery** | Network retry redelivers an already-settled event | Phase 1 `ON CONFLICT DO NOTHING` + `FindByTradeID` idempotency check |

---

## 2. Files

```
services/settlement/internal/kafka/
├── consumer.go   ← Kafka consumer group reader with manual offset management
└── README.md     ← This file
```

---

## 3. Struct: `Consumer`

```go
type Consumer struct {
    reader  *kafkago.Reader   // kafka-go consumer group reader
    service *service.Service  // 3-phase settlement pipeline
    logger  *zap.Logger
}
```

**Why `*service.Service` not an interface?**  
The consumer has exactly one collaborator — the settlement service. A concrete dependency keeps the code straightforward. If the service needs to be mocked for consumer-level tests, the field can be promoted to an interface at that time.

---

## 4. Function: `NewConsumer`

```go
func NewConsumer(brokers []string, groupID, topic string, svc *service.Service, log *zap.Logger) *Consumer
```

**Purpose:** Creates a `kafka-go` consumer group reader and wraps it with settlement-specific commit logic.  
**Why `CommitInterval: 0`:** Setting `CommitInterval` to zero completely disables auto-commit. The reader will never commit an offset on its own — every offset advancement is an explicit call to `reader.CommitMessages`. This is the only way to guarantee that a Kafka offset is not committed until Phase 3 (MarkSettled) has succeeded.  
**Why `StartOffset: FirstOffset`:** On first startup (no committed offset in the consumer group), begin from the earliest available message. Ensures no trade events are missed during initial deployment.  
**Why `MaxBytes: 10e6` (10 MB):** Sets an upper bound on the fetch response size. Each `TradeExecuted` payload is well under 1 KB, so this is effectively unlimited in practice — it just prevents pathological responses from crashing the reader.

---

## 5. Function: `Start`

```go
func (c *Consumer) Start(ctx context.Context)
```

**Purpose:** Runs the sequential consume loop. Blocks the calling goroutine until `ctx` is cancelled. Called as `go consumer.Start(ctx)` from `main.go`.  
**Why sequential (not concurrent per message)?**  
Settlement satisfies `SI-3` — each trade is independent. Multiple consumer instances (each in their own pod) already provide parallelism via Kafka partition assignment. A single goroutine per consumer is simpler, has no synchronization overhead, and is much easier to reason about for correctness.

**Message processing logic:**

```
FetchMessage (blocks until message or ctx cancelled)
       │
       ├── ctx.Err() != nil  →  log "shutting down" + return
       │
       ├── fetch error       →  log error + continue (retry next fetch)
       │
       ├── json.Unmarshal fails
       │         → log POISON PILL (with full raw payload for investigation)
       │         → commitMsg (unblock partition)
       │         → continue
       │
       └── service.Settle(ctx, event)
                 │
                 ├── error  →  log + continue (DO NOT commit → Kafka redelivers)
                 │
                 └── nil    →  commitMsg (ACK) + continue
```

**Why log the full raw payload on poison pill?**  
A poison pill is the rarest and most difficult failure to diagnose. Logging `msg.Value` as a string gives the on-call engineer the exact bytes to inspect — they can check if the Matching Engine published a schema-breaking change.

---

## 6. Function: `commitMsg` (private)

```go
func (c *Consumer) commitMsg(ctx context.Context, msg kafkago.Message) error
```

**Purpose:** Thin wrapper around `reader.CommitMessages`. Formats an error with the specific offset to make log correlation easier.  
**Why private?** Only `Start` should ever commit offsets — no external caller should be able to ACK a Kafka offset directly. Encapsulating this as a private method enforces the invariant.  
**Why not `defer`?** Offset commit must happen at a specific point in the control flow (after Phase 3), not when the enclosing function returns. `defer` would be the wrong tool here.

---

## 7. Function: `Close`

```go
func (c *Consumer) Close() error
```

**Purpose:** Gracefully shuts down the `kafka-go` reader, flushing any pending offset commits and releasing the TCP connection to the Kafka broker.  
**When called:** From `main.go` after `wg.Wait()` — i.e., after the `Start` goroutine has already exited. Calling `Close` while `Start` is still running would cause a data race on the reader.  
**Why `wg.Wait()` before `Close`?**  
`kafka-go`'s `Reader.FetchMessage` unblocks immediately when `ctx` is cancelled. The `Start` goroutine detects `ctx.Err() != nil` and returns. Only after that goroutine exits is it safe to call `Close`.

---

## 8. External Packages

| Package | Why Used |
|---|---|
| `github.com/segmentio/kafka-go` | Consumer group reader with manual commit control (`CommitInterval: 0`). Chosen for its simple API and explicit offset management |
| `encoding/json` | Unmarshal `TradeExecuted` JSON payload from Kafka message value |
| `go.uber.org/zap` | Structured logging — every fetch, settle, commit, and poison-pill event is logged with partition and offset for traceability |
| `tradedrift/services/settlement/internal/service` | The 3-phase settlement pipeline driven for each valid event |
