# Controlled Taker Service — Order Flow & Matching Pipeline

> **Status:** 📐 Designed (V1.1 — Updated with Architectural Feedback)  
> **Service:** Controlled Taker Service (`services/controlled-taker`)  
> **Document:** `05_Order_Flow_And_Engine.md`  
> **Last Updated:** September 2026  

---

## 1. End-to-End Sequence Diagram

The complete lifecycle of a controlled taker order follows the authoritative TradeDrift order pipeline:

```mermaid
sequenceDiagram
    autonumber
    participant CTS as Controlled Taker Service
    participant Redis as Redis (depth:{market})
    participant OS as Order Service (gRPC)
    participant WS as Wallet Service (gRPC)
    participant ODB as Order PostgreSQL & Outbox
    participant OutboxPub as Outbox Publisher
    participant KafkaCmd as Kafka: orders.commands
    participant ME as Matching Engine
    participant KafkaTrd as Kafka: trades.executed
    participant MS as Market Service
    participant SS as Settlement Service
    participant LE as Liquidity Engine (Consumer)

    Note over CTS: Step 1: Pre-Submission Evaluation
    CTS->>Redis: GET depth:BTC-USDT
    Redis-->>CTS: Returns Bids/Asks L2 snapshot
    CTS->>CTS: Check spread & calculate lot-aligned quantity Q and PriceCap

    Note over CTS,OS: Step 2: Order Submission (Aggressive Limit w/ Cap)
    CTS->>OS: CreateOrder(UserId=CT-001, MarketId=BTC-USDT, Side=BUY, OrderType=LIMIT, Qty=Q, Price=PriceCap, IdempotencyKey=CTS-...)
    OS->>OS: Check IdempotencyKey (return existing if duplicate)
    OS->>OS: Validate tick/lot, verify PriceFilter against Redis depth
    OS->>WS: ReserveFunds(UserId=CT-001, Asset=USDT, Amount=PriceCap * Q)
    WS-->>OS: Reservation OK (ReservationID)
    OS->>ODB: BEGIN TX: Insert order + Insert outbox (OrderCreated) COMMIT
    OS-->>CTS: Return Order Created (OrderID)

    Note over ODB,ME: Step 3: Outbox Dispatch to Matching Engine
    OutboxPub->>ODB: Fetch pending outbox records
    OutboxPub->>KafkaCmd: Publish OrderCreated (Key = BTC-USDT)
    KafkaCmd->>ME: Ingest OrderCreated

    Note over ME: Step 4: Deterministic Matching
    ME->>ME: Cross incoming BUY against resting MM ASK in memory
    Note right of ME: Execution price is ALWAYS Maker price (<= PriceCap)
    ME->>KafkaTrd: Publish TradeExecuted (MakerID=MM-001, TakerID=CT-001, Price, Qty)
    ME->>Redis: Update depth:BTC-USDT

    Note over KafkaTrd,MS: Step 5: Downstream Consumption
    KafkaTrd->>MS: Ingest TradeExecuted
    MS->>MS: INSERT INTO market_trades
    MS->>MS: UPSERT INTO ohlc_candles (1m, 5m, 15m, 1h, 4h, 1d)
    MS->>MS: Update 24h High, Low, Volume

    KafkaTrd->>SS: Ingest TradeExecuted
    SS->>WS: SettleTrade (Debit reserved USDT at MakerPrice, release unspent cap, Credit BTC)
    WS->>WS: Post double-entry ledger entries

    Note over KafkaTrd,LE: Step 6: LE Closed-Loop Replenishment
    KafkaTrd->>LE: Ingest TradeExecuted
    LE->>LE: Update MM internal inventory
    LE->>LE: Trigger targeted reconciliation
    LE->>KafkaCmd: Publish replacement resting MM Ask (OrderCreated)
```

---

## 2. Order Contract Specification

CTS reuses the existing Protobuf contract defined in `platform/api/proto/order/v1/order.proto`:

```protobuf
message CreateOrderRequest {
  string user_id = 1;          // "00000000-0000-0000-0000-000000000002" (CT-001)
  string market_id = 2;        // "BTC-USDT", "ETH-USDT", or "SOL-USDT"
  OrderSide side = 3;          // ORDER_SIDE_BUY or ORDER_SIDE_SELL
  OrderType order_type = 4;    // ORDER_TYPE_LIMIT (Aggressive Crossing Limit)
  string price = 5;            // Calculated slippage cap/floor price
  string quantity = 6;         // Lot-rounded quantity string
  string idempotency_key = 7;  // "CTS-{MarketID}-{UUIDv7}"
}
```

---

## 3. Slippage Cap Sizing & Matching Mechanics

### BUY Orders:
- CTS specifies an aggressive limit price:
  $$P_{\text{cap}} = P_{\text{target\_level}} \times (1 + \text{SlippageBuffer})$$
- Order Service `PriceFilter` verifies that $P_{\text{cap}}$ is within the pre-trade percentage band from current mid-price.
- Wallet Service reserves quote funds: $\text{ReservedUSDT} = P_{\text{cap}} \times Q$.
- In the Matching Engine:
  - The incoming BUY order crosses all resting asks with price $P_{\text{ask}} \le P_{\text{cap}}$.
  - Trade execution executes at **$P_{\text{maker}}$** (maker's resting price), strictly guaranteeing price improvement for the taker.
  - Upon settlement, Settlement Service debits $P_{\text{maker}} \times Q$ and immediately **releases the unspent reservation** $(P_{\text{cap}} - P_{\text{maker}}) \times Q$ back to `CT-001`'s available balance.

### SELL Orders:
- CTS specifies a floor limit price:
  $$P_{\text{floor}} = P_{\text{target\_level}} \times (1 - \text{SlippageBuffer})$$
- Wallet Service reserves base asset: $\text{ReservedBase} = Q$.
- Matching Engine crosses resting bids with price $P_{\text{bid}} \ge P_{\text{floor}}$ at maker price.
