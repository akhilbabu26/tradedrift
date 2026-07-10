# TradeDrift Service-wide Glossary

This glossary defines core trading, algorithmic, and engineering terms used across all TradeDrift services (API Gateway, Order Service, Matching Engine, Wallet Service, and Settlement Service) to establish a single source of terminology truth.

---

## A

### Asset pair
The combination of a base asset and a quote asset traded on a market (e.g., BTC-USDT).

---

## B

### Base Asset
The asset being bought or sold in a transaction. In the BTC-USDT pair, BTC is the base asset.

### Best Bid / Best Ask
* **Best Bid**: The highest active price that buyers are willing to pay for an asset.
* **Best Ask (Best Offer)**: The lowest active price that sellers are willing to accept for an asset.

---

## C

### Checkpoint
The persisted offset representing the last successfully matched and published event for a Kafka topic partition. In TradeDrift, checkpoints are written to PostgreSQL by the Publisher Layer only after downstream executions are fully written and acknowledged.

---

## E

### Event Loop
The single-threaded execution thread assigned to manage a specific market's order book. It guarantees race-free sequential execution of order creations and cancellations without mutexes.

---

## G

### GTC (Good-Til-Cancelled)
An order execution time-in-force instruction where the order remains resting on the book until it is either fully filled or explicitly cancelled by the user.

---

## H

### High-Water Mark (HWM)
Conventionally, the offset of the next message to be written to a Kafka partition. It marks the boundary of all currently written messages on a topic.

---

## I

### IOC (Immediate-Or-Cancel)
An order execution time-in-force instruction where the order must be matched immediately against resting orders. Any portion of the order that cannot be matched immediately is expired and cancelled (never rests on the book). All TradeDrift market orders are treated as IOC.

---

## M

### Maker (Maker Order)
A resting limit order that sits on the order book and provides liquidity to the market. Makers wait for incoming orders (Takers) to cross their price.

### MatchResult
The bundled, atomic in-memory outcome of a single input event processed by the Matching Engine. It contains a slice of fills, any cancel/reject results, the L2 depth snapshot, and the event's Kafka offset.

### MONETARY_PRECISION
The system-wide PostgreSQL column type used for **all monetary amounts** across every TradeDrift service: `DECIMAL(30,10)`. This provides up to 20 digits left of the decimal point (sufficient for all realistic asset quantities) and 10 digits of fractional precision (sufficient for the highest-precision assets in `supported_assets.decimals`). All services **must** use this type for price, quantity, and balance columns — no service may independently choose a different precision. In V1 this resolves to `DECIMAL(30,10)` for all assets.

---

## P

### PriceLevel
The aggregated liquidity resting in the book at a specific price. It holds a doubly linked list of individual resting orders (`OrderNode` instances) and keeps track of the total remaining quantity.

### Publisher (Publisher Layer)
The asynchronous goroutine companion to the Event Loop. It drains the Output Queue channel, writes execution events to Kafka, pushes depth updates to Redis, and commits offset checkpoints to Postgres.

---

## Q

### Quote Asset
The asset used to price the base asset. In the BTC-USDT pair, USDT is the quote asset.

---

## R

### RECOVERY Mode
The state of a Market Engine immediately after startup where it replays history from offset 0 up to its checkpoint offset. During recovery, all external publications and Redis projections are suppressed to prevent side effects.

---

## S

### Sweep
The process where an incoming aggressive order matches against multiple price levels sequentially, exhausting liquidity at each level until the order's quantity is filled or its price boundary is reached.

---

## T

### Taker (Taker Order)
An incoming aggressive order that matches against resting liquidity (Makers) on the book, removing liquidity from the market.
