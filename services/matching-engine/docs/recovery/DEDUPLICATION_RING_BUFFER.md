# In-Memory Deduplication Ring Buffer Architecture

**Service:** Matching Engine (`services/matching-engine`)  
**Documentation:** `DEDUPLICATION_RING_BUFFER.md`  
**Topic:** $O(1)$ Fast Duplicate Detection, Fixed-Capacity FIFO Eviction, and Memory Leak Prevention  
**Package References:** 
* [`internal/market/engine.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/engine.go)
* [`internal/market/event_loop.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go)  
**Last Updated:** August 2026  

---

## 1. Executive Summary & Purpose

In financial exchanges, Apache Kafka guarantees **at-least-once delivery**. During network retries, broker rebalances, or crash recovery replays, the Matching Engine may receive the same order command more than once.

If an order is matched twice:
* A buyer would be filled **twice** for the same order ID.
* Trader balances and order books would become **permanently corrupted**.

The **Deduplication Ring Buffer** ([`internal/market/engine.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/engine.go#L68-L92)) is an **in-memory, zero-allocation $O(1)$ deduplication cache** that tracks the last 50,000 processed `EventID`s. It guarantees that duplicate commands are caught in sub-microseconds without causing memory leaks or garbage collection (GC) latency spikes.

---

## 2. Problems Solved, How Solved & Implementing Functions Matrix

| Problem Solved | Danger / Failure Scenario | How It Is Solved | Implementing Function(s) & Code Location |
| :--- | :--- | :--- | :--- |
| **1. Double-Matching on Kafka Redelivery** | Network reconnects cause Kafka to redeliver an `OrderCreated` event that was already matched in RAM. | Evaluates `m.processedEvents[event.EventID]` in $O(1)$ time before applying any mutation. | [`event_loop.go:61-70`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go#L61-L70) |
| **2. Unbounded RAM Growth (Memory Leaks)** | Tracking every event ID in a standard map over months of trading consumes gigabytes of RAM and crashes the node with Out-Of-Memory (OOM). | Maintains a fixed-capacity 50,000-element FIFO ring buffer that evicts the oldest entry in $O(1)$ when full. | [`eventRingBuffer.add`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/engine.go#L78-L92) |
| **3. Garbage Collection (GC) Latency Spikes** | Dynamically resizing slices or allocating millions of map keys causes Go GC stop-the-world pauses ($> 10\text{ms}$). | Pre-allocates a fixed array `[50_000]uuid.UUID` on startup ($\approx 800\text{ KB}$), achieving **0 dynamic heap allocations** per insert. | [`eventRingBuffer`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/engine.go#L70-L76) |
| **4. Redelivery Offset Redundancy** | Replaying from a snapshot sends events with $\text{Offset} \le \text{LastAppliedOffset}$. | Fast pre-filter skips events where `event.Offset <= m.lastAppliedOffset` before even checking the ring buffer. | [`event_loop.go:56-58`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/matching-engine/internal/market/event_loop.go#L56-L58) |

---

## 3. The Dual Data Structure Architecture

To achieve both **$O(1)$ instant lookup** and **$O(1)$ deterministic eviction without slice shifts**, the engine combines two data structures:

```
                  DUAL DATA STRUCTURE DEDUPLICATION
                  ═════════════════════════════════

  1. FAST-LOOKUP HASH SET (map[uuid.UUID]bool)
  ┌─────────────────────────────────────────────────────────────┐
  │ "UUID-100": true, "UUID-101": true, "UUID-102": true ...   │ ──► O(1) Instant Check
  └─────────────────────────────────────────────────────────────┘
                               ▲
                               │ Evicts oldest UUID when full
                               ▼
  2. FIXED-ARRAY CIRCULAR RING BUFFER ([50,000]uuid.UUID)
  ┌──────┬──────┬──────┬──────┬──────┬──────┬───────────────────┐
  │ ID-0 │ ID-1 │ ID-2 │ ID-3 │ ...  │ ...  │ ID-49,999         │ ──► O(1) FIFO Eviction
  └──────┴──────┴──────┴──────┴──────┴──────┴───────────────────┘
           ▲
           │
         head (points to the oldest entry to overwrite)
```

1. **`processedEvents map[uuid.UUID]bool`**: Provides sub-microsecond $O(1)$ membership testing (`if m.processedEvents[id]`).
2. **`eventRing eventRingBuffer`**: Pre-allocated array of `[50_000]uuid.UUID` ($\approx 800\text{ KB}$ fixed memory). Tracks the exact arrival order. When entry 50,001 arrives, it overwrites slot `head`, returns the evicted UUID, and deletes that UUID from the map.

---

## 4. In-Depth Code Walkthrough

### 4.1 Fixed Ring Buffer Eviction (`engine.go`)

```go
// internal/market/engine.go
const ringBufferCapacity = 50_000

type eventRingBuffer struct {
    slots [ringBufferCapacity]uuid.UUID
    head  int // next write position (oldest entry)
    count int // number of live entries
}

func (r *eventRingBuffer) add(id uuid.UUID) (evicted uuid.UUID) {
    if r.count == ringBufferCapacity {
        // Buffer full — evict the oldest entry at head in O(1)
        evicted = r.slots[r.head]
        r.slots[r.head] = id
        r.head = (r.head + 1) % ringBufferCapacity
    } else {
        // Buffer not yet full — write at (head + count) % capacity
        pos := (r.head + r.count) % ringBufferCapacity
        r.slots[pos] = id
        r.count++
    }
    return evicted
}
```

---

### 4.2 Deduplication Lifecycle in Event Loop (`event_loop.go`)

On every incoming Kafka command:

```go
// internal/market/event_loop.go:56-87

// 1. Offset fast-path: Skip redelivered offsets already applied
if event.Offset <= m.lastAppliedOffset {
    continue
}

// 2. Duplicate Detection: Fail-closed if duplicate logical EventID arrives
if event.EventID != uuid.Nil {
    if m.processedEvents[event.EventID] {
        log.Printf("[market] duplicate event_id detected: %s", event.EventID)
        if m.HaltCallback != nil {
            m.HaltCallback()
        }
        return
    }
}

// 3. Apply state mutation to order book
res, err := m.applyEvent(event)
if err != nil {
    // handle error...
}

// 4. Record EventID into ring buffer & evict oldest from hash map
if event.EventID != uuid.Nil {
    if evicted := m.eventRing.add(event.EventID); evicted != uuid.Nil {
        delete(m.processedEvents, evicted) // O(1) eviction
    }
    m.processedEvents[event.EventID] = true
}

// 5. Advance applied offset
m.lastAppliedOffset = event.Offset
```

---

## 5. Why 50,000 Capacity?

* **Memory Footprint:**  
  $50,000 \times 16\text{ bytes} \approx \mathbf{800\text{ KB}}$ per market. For 3 markets (`BTC`, `ETH`, `SOL`), the entire deduplication subsystem consumes less than **2.5 MB** of RAM!
* **Deduplication Window:**  
  At 1,000 orders/second, 50,000 slots provide a **50-second safety window**, which is more than $10\times$ larger than any Kafka consumer rebalance or retry delay.

---

## 6. Summary

* **$O(1)$ Lookup:** Hash map checks duplicates in sub-microseconds before touching the order book.
* **$O(1)$ Eviction:** Circular ring buffer guarantees memory stays strictly bounded at $\approx 800\text{ KB}$ forever.
* **Zero GC Overhead:** Pre-allocated array avoids heap allocation churn during high-frequency trading.
