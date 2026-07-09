# TradeDrift Matching Engine — Design Invariants

**Document:** 15_Design_Invariants.md
**Service:** Matching Engine
**Version:** V1.0
**Last Updated:** July 2026

---

## 1. Concurrency & State Ownership Invariants

These invariants govern how goroutines interact with the memory structures of the matching engine, preventing data races and memory corruption.

### CI-1: Exclusive Goroutine Ownership
> **Invariant:** Only the Event Loop goroutine assigned to market $M$ is allowed to read from or write to the `OrderBook` struct, `OrderIndex` map, `Side` structs, `PriceLevel` nodes, or `OrderNode` instances belonging to market $M$.
* **Implication:** The Publisher goroutine and the Market Engine Manager are strictly prohibited from holding or dereferencing pointers to the order book state. Any data required by the Publisher must be copied and transferred via the Output Queue channel.
* **Benefit:** Eliminates the need for Mutexes, RW-Mutexes, or Atomic pointers in the matching hot path. Matches run at raw single-threaded CPU speed.

### CI-2: Happens-Before via Channel Serialization
> **Invariant:** All changes to order book state $S_t \rightarrow S_{t+1}$ are triggered exclusively by deserialized events drained sequentially from the market's `inputQueue` channel.
* **Implication:** No concurrent mutation of the book can occur. The execution of event $E_{n}$ must fully complete (including matching, book modification, and pushing the outcome to `outputQueue`) before event $E_{n+1}$ can be dequeued.

### CI-3: I/O Isolation
> **Invariant:** The Matching Core and Event Loop are strictly side-effect-free with respect to external systems. They must never perform database queries, Redis pushes, Kafka publications, or network calls.
* **Implication:** If a downstream service (like Redis or Kafka) is slow or offline, it cannot block or add latency to the core match execution loop. Latency is isolated to the asynchronous Publisher Layer.

---

## 2. Order Book & Algorithmic Invariants

These invariants define the logical rules of matching and the mathematical constraints on the order book data structure.

### BI-1: Strict Order Book Separation (Non-Crossing)
> **Invariant:** For any active bid price $P_b$ and active ask price $P_a$ resting in the same book, the inequality $P_b < P_a$ must always hold.
* **Implication:** The bid-ask spread $S = P_a - P_b$ is always strictly positive ($S > 0$). An incoming order that would violate this inequality ($P_{incoming\_bid} \geq P_a$ or $P_{incoming\_ask} \leq P_b$) is matched immediately and swept until the inequality is restored, or the remaining portion rests.

### BI-2: Price-Time Priority
> **Invariant:** For any incoming order, matches are executed against resting orders ordered by:
> 1. Price priority: highest bid first, lowest ask first.
> 2. Time priority: oldest resting order first (FIFO queue per price level).
* **Implication:**
  * No order at price level $P_x$ can be filled if there is resting quantity at a better price level $P_{better}$.
  * No order $O_2$ at price level $P_x$ can be filled if order $O_1$ (arrived earlier at price $P_x$) still has remaining quantity.
  * A partial fill on order $O_1$ must reduce its `remainingQty` but **must not** change its timestamp or linked-list queue position.

### BI-3: Index Completeness & Referential Integrity
> **Invariant:** The index map `book.orderIndex` contains an order ID key $ID$ if and only if that order is currently resting on the book.
* **Implication:**
  * $\forall O \in book.orderIndex \iff O \in priceLevels[O.price].orders$
  * The element pointer `OrderNode.element` must point to the exact node in the `PriceLevel`'s doubly linked list containing that same order.
  * When an order is fully filled or cancelled, it must be removed from both the linked list and deleted from `orderIndex` atomically within the same Event Loop step.

---

## 3. Crash Recovery & Checkpoint Invariants

These invariants guarantee that the system can recover from crashes without losing state or duplicating trades.

### RI-1: Replay Idempotency & Replay-from-Zero
> **Invariant:** Replaying the complete ordered sequence of `OrderCreated` and `OrderCancelRequested` events from offset 0 up to the checkpoint offset $O_{committed}$ starting from a clean, empty book state $S_0$ will always reconstruct the identical memory state $S_n$ at the time the checkpoint was written.
* **Implication:** Because V1 has no snapshotting mechanism, starting replay from the checkpoint offset would drop all active orders placed prior to the checkpoint. Therefore, recovery **must always consume from offset 0** of the Kafka topic, re-building the book in memory.
* **Action Suppression:** During the replay of offsets $0 \leq O \leq O_{committed}$, the engine runs in `RECOVERY` mode: all Kafka publications, Redis projection updates, and metrics are fully suppressed to prevent side effects.

### RI-2: Checkpoint Monotonicity
> **Invariant:** A checkpoint offset $O_{committed}$ for partition $P$ is persisted in PostgreSQL if and only if the input event at $O_{committed}$ and all prior events have been fully matched, and all resulting execution events have been successfully published and acknowledged by Kafka.
* **Implication:** Checkpoint offsets are strictly monotonic ($O_{committed\_new} > O_{committed\_old}$). Recovery never needs to rollback or reconcile partial checkpoint writes.
* **Exception Note:** The recovery-exit sentinel pushes a Redis depth snapshot but does not write a checkpoint to PostgreSQL, since transitioning to LIVE at exactly the checkpoint boundary introduces no new offset to persist. Re-seeking to this checkpoint on a subsequent restart is a no-op that correctly processes the next live event.


## 3.1 Performance & Resource Invariants

These invariants define constraints on execution complexity and I/O boundaries inside the Matching Engine, ensuring low-latency execution paths.

### PI-1: O(1) Cancel Complexity
> **Invariant:** Order cancellation by order ID must always execute in $O(1)$ time complexity, independent of the total depth of the book or the position of the order in its price queue.
* **Implication:** Enforced by the `book.orderIndex` map lookup. The map yields a pointer to the linked-list element (`OrderNode`), allowing direct, immediate removal from the doubly-linked list without traversal.

### PI-2: O(1) Best Price Lookup
> **Invariant:** Resolving the best bid or best ask price must always execute in $O(1)$ time complexity.
* **Implication:** The engine must maintain direct references to the highest price level in `Side.bids` and the lowest price level in `Side.asks` (e.g., direct head/tail pointer of a sorted tree or active index of a sorted array/heap).

### PI-3: I/O-Free Event Loop
> **Invariant:** The Event Loop goroutine must execute completely free of blocking system calls, file operations, disk reads, network operations, or heap-locking operations.
* **Implication:** Any logging within the hot path must be asynchronous or buffered. Memory allocations must be minimal (recycling nodes via a `sync.Pool` where possible) to keep the Event Loop CPU-bound.

### PI-4: Bounded Matching Scans
> **Invariant:** The matching algorithm must never scan or iterate through the entire order book. The scan depth is strictly bounded by the size of the incoming order, traversing only the active opposite-side price level queues until the incoming quantity is filled or the price crossing boundary is breached.

---

## 4. Summary Matrix of Invariants

| Code | Type | Description | Enforced by |
|---|---|---|---|
| **CI-1** | Concurrency | Exclusive loop ownership of book state | Goroutine construction; no pointers shared with Publisher |
| **CI-2** | Concurrency | Serialized happens-before ordering | Channel read loop (`m.inputQueue` FIFO) |
| **CI-3** | Concurrency | Complete I/O isolation | Publisher goroutine handles all Kafka/Redis/Postgres I/O |
| **BI-1** | Algorithmic | No crossed book state ($P_b < P_a$) | Match sweep loop execution |
| **BI-2** | Algorithmic | Price-Time priority FIFO queue | Binary sorted list + doubly-linked list (`container/list`) |
| **BI-3** | Algorithmic | Index referential integrity | Atomic list removal + index key delete inside matching core |
| **RI-1** | Recovery | Idempotent replay without side effects | `RECOVERY` mode flag; suppression of Output Queue |
| **RI-2** | Recovery | Monotonic Kafka checkpointing | Publisher writing checkpoint only after Kafka ACK |
| **PI-1** | Performance | O(1) Order cancellation complexity | `book.orderIndex` map-pointer removals |
| **PI-2** | Performance | O(1) Best bid/ask lookup complexity | Direct head pointer reference on bids/asks |
| **PI-3** | Performance | Complete avoidance of blocking operations | Asynchronous I/O delegation to Publisher |
| **PI-4** | Performance | Bounded matching book scans | Sweep termination upon remainingQty == 0 or non-crossing price |

