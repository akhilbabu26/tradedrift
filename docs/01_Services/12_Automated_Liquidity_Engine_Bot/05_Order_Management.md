# Liquidity Engine — Order Management & Reconciliation

> **Service:** `services/liquidity-engine`
> **Version:** V1.1

---

## Order Identity

Every MM order is fully traceable with a structured `client_order_id`:

```
order_id         = ORD-uuid
account_id       = MM-001
market_id        = BTC-USDT
side             = SELL
price            = 96469.29
quantity         = 0.25
client_order_id  = MM-BTC-USDT-ASK-01
```

### Naming Convention

```
MM-{MarketID}-{SIDE}-{Level:02d}

Examples:
  MM-BTC-USDT-BID-01    (Closest bid to mid)
  MM-BTC-USDT-BID-02
  ...
  MM-BTC-USDT-BID-12    (Furthest bid from mid)

  MM-BTC-USDT-ASK-01    (Closest ask to mid)
  MM-BTC-USDT-ASK-02
  ...
  MM-BTC-USDT-ASK-12    (Furthest ask from mid)
```

The engine uses `client_order_id` to map every live order back to its logical price level.

---

## Order Manager

Maintains the **desired state vs. actual state** comparison:

```
Desired State:
  12 Bids
  12 Asks

Actual State (from order tracker):
  10 Bids
  12 Asks

Delta:
  2 Bids missing → Create them
```

**Operations:**

| Operation | Trigger |
| :--- | :--- |
| `Create` | Level missing in actual state |
| `Cancel` | Stale level (price drift, risk breach) |
| `Replace` | Cancel + Create on price recalculation (single event) |
| `Track` | Receive `OrderAccepted` event from ME |

---

## Reconciliation Engine

The **heart of reliable operation**. Runs on every engine cycle (event-driven, not timer-driven):

```
GET desired state (from strategy)
        ↓
GET actual MM orders (from tracker / order API)
        ↓
COMPARE
        ↓
CALCULATE differences
        ↓
CREATE missing orders
        ↓
CANCEL obsolete orders
        ↓
REPLACE stale orders
```

### Example

```
Desired Bids:
  BID 96,430  (MM-BTC-USDT-BID-01)
  BID 96,410  (MM-BTC-USDT-BID-02)
  BID 96,390  (MM-BTC-USDT-BID-03)

Actual Bids:
  BID 96,430  ✅ present
  BID 96,410  ✅ present
  (missing)   ❌ MM-BTC-USDT-BID-03

Action:
  Create MM-BTC-USDT-BID-03 @ 96,390
```

> **When reconciliation triggers:**
> - Price drift exceeds threshold → reconcile affected levels only
> - Fill event received → reconcile that specific level
> - Periodic health check (every 30s) → full pass
>
> **Not:** blindly re-send all orders on a fixed timer.

---

## Fill Handling — Partial Fill

```
MM-001 ASK: 1.00 BTC @ $96,469.29
User BUY:   0.40 BTC @ $96,469.29

Matching Engine → PARTIAL FILL

MM Order becomes:
  original  = 1.00 BTC
  filled    = 0.40 BTC
  remaining = 0.60 BTC

Liquidity Engine receives:
  OrderPartiallyFilled (via Kafka)

Decision:
  remaining >= MinOrderSize → keep resting
  remaining <  MinOrderSize → replace with fresh full order
```

---

## Fill Handling — Full Fill

```
MM-001 ASK: 1.00 BTC @ $96,469.29
User BUY:   1.00 BTC @ $96,469.29

Matching Engine → FULL FILL

Event: OrderFilled

Liquidity Engine:
  Detects ASK-01 missing
  Recalculates ASK-01 price (may shift if reference moved)
  Submits new ASK-01 order

Result:
  12 asks → 11 asks (briefly) → 12 asks (after reconcile)
```

---

## Matching Mechanics Reference

### The Matching Condition

```
BUY executes against a resting SELL if:  buy_price >= sell_price
SELL executes against a resting BUY if:  sell_price <= buy_price

Execution price = the MAKER's price (the resting order)
```

No exact price or exact quantity match is required. A limit order rests in the book
until an executable opposite-side order arrives.

### Limit BUY Decision Tree

```
LIMIT BUY arrives
      │
      ├── buy_price < best_ask
      │        │
      │        v
      │      REST in book (wait for a seller)
      │
      └── buy_price >= best_ask
               │
               v
             MATCH
               │
               v
        price-time priority
               │
               v
        consume ask liquidity
```

### Four Cases for a User BUY Order

**Case 1 — Exact price**
```
User BUY 0.5 BTC @ $96,469
  best ask = $96,469
  buy_price >= sell_price → MATCH
  Executes at $96,469 (maker price)
  MM ASK-01: 0.81 BTC → 0.31 BTC remaining (partial fill)
```

**Case 2 — Price improvement (user limit above best ask)**
```
User BUY 0.5 BTC @ $96,550
  best ask = $96,469
  buy_price ($96,550) >= sell_price ($96,469) → MATCH
  Executes at $96,469 (maker price — user receives price improvement)
  User pays $96,469, not $96,550
```

**Case 3 — Market order sweeping multiple levels (corrected)**
```
User MARKET BUY 2.00 BTC

Ask ladder available:
  ASK-01  $96,469  0.81 BTC
  ASK-02  $96,488  0.63 BTC
  ASK-03  $96,507  0.55 BTC
  ASK-04  $96,526  ...

ME matches in price-time order:

  ASK-01  0.81 BTC consumed  →  remaining = 2.00 - 0.81 = 1.19 BTC
  ASK-02  0.63 BTC consumed  →  remaining = 1.19 - 0.63 = 0.56 BTC
  ASK-03  0.55 BTC consumed  →  remaining = 0.56 - 0.55 = 0.01 BTC
  ASK-04  0.01 BTC consumed  →  remaining = 0.00 BTC  ← done

Result:
  ASK-01  → FULLY consumed    (OrderFilled)
  ASK-02  → FULLY consumed    (OrderFilled)
  ASK-03  → FULLY consumed    (OrderFilled)
  ASK-04  → PARTIALLY consumed 0.01 BTC  (OrderPartiallyFilled)

LE receives 3x OrderFilled + 1x OrderPartiallyFilled events
→ Reconciler rebuilds ASK-01, ASK-02, ASK-03
→ ASK-04 kept resting with updated quantity
```

**Case 4 — Resting bid, no immediate match**
```
User BUY 0.5 BTC @ $96,400 (below best ask of $96,469)
  buy_price < best_ask → NO MATCH
  Order rests as a new bid level in the book

Book state:
  Bids: 12 MM orders + 1 User order  (user rests alongside MM bids)
  Asks: 12 MM orders

MM reconciler:
  Desired MM bids:  12
  Actual MM bids:   12  (user's bid is a different account_id, not counted)
  Diff:             NOTHING  → no commands
```

### Liquidity Caveat

The MM's 12-level ask ladder provides **immediate executable liquidity** for BUY orders
**while sufficient ask-side liquidity is available**. Larger orders sweep progressively deeper levels.

```
MM total ask-side liquidity ≈ sum of all 12 ask quantities

Example: 5.00 BTC total across 12 levels

User MARKET BUY 20 BTC
  → 5 BTC filled against MM
  → 15 BTC remaining, no more MM ask liquidity

Whether the remaining 15 BTC is:
  - Rejected (if FOK/IOC order type)
  - Partially filled and cancelled (IOC)
  - Partially filled and rested (limit order that swept available)

...depends on the order type and any other resting sells in the book.
```

### LE & ME Separation of Concerns

```
User Order
    │
    v
Matching Engine
    │
    v
Match against MM/user resting orders
(price-time priority, ME decides everything)
    │
    v
MM order gets consumed
    │
    v
TradeExecuted / OrderFilled events (Kafka)
    │
    v
Liquidity Engine
    │
    v
"Do I now have a missing MM level?"
    │
    .--------+--------.
    │                 │
   YES               NO
    │                 │
    v                 v
Create replacement  Nothing
order
```

**ME decides matching. LE maintains MM liquidity. They never interfere with each other.**

