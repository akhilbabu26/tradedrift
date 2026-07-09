# TradeDrift Matching Engine — State Diagrams

**Document:** 14_State_Diagrams.md
**Service:** Matching Engine
**Version:** V1.0
**Last Updated:** July 2026

---

## 1. Market Engine State Transitions

Each market (e.g. BTC-USDT) runs its own state machine. This governs recovery, readiness, and panic-halt transitions.

```mermaid
stateDiagram-v2
    [*] --> STARTING : Process Start / Assign Partition
    
    state STARTING {
        [*] --> LoadConfig
        LoadConfig --> ConnectKafka
        ConnectKafka --> ConnectPostgres
    }
    
    STARTING --> LOADING_CHECKPOINT : Connect Success
    
    state LOADING_CHECKPOINT {
        [*] --> ReadRow
        ReadRow --> CheckpointFound
        ReadRow --> NoCheckpoint : Set Offset = 0
    }
    
    LOADING_CHECKPOINT --> RECOVERY : Seek to Offset
    
    state RECOVERY {
        [*] --> FetchReplayEvent
        FetchReplayEvent --> ProcessInMemory : Output Suppressed
        ProcessInMemory --> FetchReplayEvent : offset < highWaterMark
        ProcessInMemory --> CaughtUp : offset == highWaterMark
    }
    
    RECOVERY --> LIVE : Exit Recovery (Send Sentinel)
    
    state LIVE {
        [*] --> FetchLiveEvent
        FetchLiveEvent --> ProcessLive : Output Active (Kafka/Redis)
        ProcessLive --> FetchLiveEvent
    }
    
    LIVE --> HALTED : Panic Caught (processWithRecovery defer)
    RECOVERY --> HALTED : Panic Caught
    
    state HALTED {
        [*] --> StopEventLoop
        StopEventLoop --> PublishHaltEvent
        PublishHaltEvent --> RaiseP1Alert
    }
    
    HALTED --> STARTING : Operator Triggered Restart
```

---

## 2. Order Lifecycle States (Matching Engine View)

The Matching Engine has a simplified view of an order compared to the Order Service, tracking only in-memory presence and quantity.

```mermaid
stateDiagram-v2
    [*] --> INCOMING : Event Dequeued
    
    state INCOMING {
        [*] --> ValidateParameters
    }
    
    INCOMING --> CANCELLED : Validation Fail (invalid_order_parameters)
    
    INCOMING --> MATCHING : Validation Pass
    
    state MATCHING {
        [*] --> CheckOppositeSide
        CheckOppositeSide --> GenerateFills : crosses
        CheckOppositeSide --> CheckRemaining : doesn't cross
    }
    
    MATCHING --> FULLY_FILLED : remainingQty == 0
    
    MATCHING --> INSERT_FLOW : LIMIT and remainingQty > 0
    MATCHING --> IOC_CANCEL_FLOW : MARKET and remainingQty > 0
    
    state INSERT_FLOW {
        [*] --> AddToPriceLevel
        AddToPriceLevel --> IndexInOrderIndex
    }
    
    INSERT_FLOW --> RESTING
    
    state RESTING {
        [*] --> QueuePositionPreserved
    }
    
    RESTING --> PARTIALLY_FILLED : incoming crosses remainingQty
    RESTING --> FULLY_FILLED : incoming fully consumes remainingQty
    RESTING --> CANCELLED : OrderCancelRequested received
    
    state PARTIALLY_FILLED {
        [*] --> ReduceRemainingQty
        ReduceRemainingQty --> KeepQueuePosition
    }
    
    PARTIALLY_FILLED --> FULLY_FILLED : subsequent matches consume remainder
    PARTIALLY_FILLED --> CANCELLED : OrderCancelRequested received
    
    state IOC_CANCEL_FLOW {
        [*] --> DiscardRemainder
        DiscardRemainder --> BuildIocCancelEvent : reason = ioc_expired
    }
    
    IOC_CANCEL_FLOW --> CANCELLED
    
    FULLY_FILLED --> [*] : Removed from Book & Index
    CANCELLED --> [*] : Released / Discarded
```

---

## 3. Price Level States

A price level represents aggregated liquidity at a specific price. It is dynamically created and cleaned up.

```mermaid
stateDiagram-v2
    [*] --> NON_EXISTENT
    
    NON_EXISTENT --> ACTIVE : Insert (First order at price)
    
    state ACTIVE {
        [*] --> CreateLinkedList
        CreateLinkedList --> InsertBinarySortedPrices
        InsertBinarySortedPrices --> AddOrderToQueue
        AddOrderToQueue --> UpdateTotalQty
    }
    
    ACTIVE --> ACTIVE : Add order / Partial fill (totalQty changes)
    ACTIVE --> ACTIVE : Cancel/Fill order (orders queued > 1)
    
    ACTIVE --> CLEANUP : Cancel/Fill order (last queued order removed)
    
    state CLEANUP {
        [*] --> DeleteFromPriceLevelsMap
        DeleteFromPriceLevelsMap --> RemoveFromSortedPricesList
    }
    
    CLEANUP --> NON_EXISTENT
```

---

## 4. References

- `03_Order_Book.md` — Order Book entities
- `04_Data_Structures/03_Order_Node.md` — OrderNode lifecycle
- `04_Data_Structures/04_Price_Level.md` — PriceLevel list properties
- `10_Failure_Handling.md §6` — Panic and halt states
- `13_Flow_Diagrams.md` — Concrete workflow paths
