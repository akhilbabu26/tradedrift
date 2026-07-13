# TradeDrift — Complete System Flows

> Every flow across all services, in correct order.

---

## Table of Contents

1. [API Gateway — Request Pipeline](#1-api-gateway--request-pipeline)
2. [Auth — Register](#2-auth--register)
3. [Auth — Login](#3-auth--login)
4. [Auth — JWT Validation](#4-auth--jwt-validation)
5. [Auth — Refresh Token](#5-auth--refresh-token)
6. [Auth — Logout](#6-auth--logout)
7. [Auth — Change Password](#7-auth--change-password)
8. [Order — Create Limit Order](#8-order--create-limit-order)
9. [Order — Create Market Order (IOC)](#9-order--create-market-order-ioc)
10. [Order — Cancel Order + Race Condition](#10-order--cancel-order--race-condition)
11. [Wallet — Reserve Funds](#11-wallet--reserve-funds)
12. [Wallet — Trade Settlement](#12-wallet--trade-settlement)
13. [Wallet — Release Funds](#13-wallet--release-funds)
14. [Full End-to-End: Place to Match to Settle](#14-full-end-to-end)
15. [ID Correlation — Where Every ID Is Created and Carried](#15-id-correlation)
16. [Order State Machine](#16-order-state-machine)
17. [Wallet Reservation State Machine](#17-wallet-reservation-state-machine)
18. [Service Communication Map](#18-service-communication-map)

---

## 1. API Gateway — Request Pipeline

Every request from the client passes through this pipeline in this fixed order.

```
Incoming HTTP Request (from web / mobile client)
  |
  v
+----------------------------------------------------------+
| 1. CORS Middleware                                       |
|    Handle OPTIONS preflight                              |
|    Set: Allow-Origin, Allow-Headers, Allow-Methods       |
+-----------------------------+----------------------------+
                              |                |
                            PASS       PREFLIGHT REJECTED
                              |                |
                              |            <-- 403 Forbidden
                              v
+----------------------------------------------------------+
| 2. Logging / Metrics                                     |
|    Record request timing, counters                       |
|    Applied to ALL requests including failures            |
+----------------------------------------------------------+
                              |
                              v
+----------------------------------------------------------+
| 3. Rate Limit Middleware                                 |
|    Redis token bucket -- per-client IP or API key        |
+-----------------------------+----------------------------+
                              |                |
                            PASS          OVER LIMIT
                              |                |
                              |            <-- 429 Too Many Requests
                              v
+----------------------------------------------------------+
| 4. Route Resolution                                      |
|    Match path to target service                          |
|    Set: auth-required flag                               |
+-----------------------------+----------------------------+
                              |                |
                           FOUND          NOT FOUND
                              |                |
                              |            <-- 404 Not Found
                              v
                      [Requires Auth?]
                       /            \
                      NO            YES
                      |              |
                      |              v
                      |    +---------------------------+
                      |    | 5. JWT Middleware          |
                      |    | Shared verification pkg   |
                      |    | NO gRPC call to Auth Svc  |
                      |    |                           |
                      |    | a) Verify signature       |
                      |    | b) Check expiry           |
                      |    | c) Check Redis blacklist  |
                      |    +----------+----------------+
                      |              |         |
                      |           VALID    INVALID / EXPIRED / REVOKED
                      |              |         |
                      |              |     <-- 401 Unauthorized
                      |              |
                      +------+-------+
                             |
                             v
+----------------------------------------------------------+
| 6. gRPC Client                                           |
|    Forward to resolved target service                    |
|    Timeout: 2s  |  Circuit breaker on persistent fails  |
+-----------------------------+----------------------------+
                              |              |
                           SUCCESS         FAIL
                              |          Timeout     --> 504
                              |          Circuit open --> 503
                              v
          +--------------------------------------------+
          | Downstream Services                        |
          | Auth Svc | Order Svc | Wallet Svc | Market |
          +--------------------------------------------+
```

---

## 2. Auth — Register

```
POST /auth/register  { email, username, password }
  |
  v  API Gateway: CORS > Log > Rate limit > Route (public) > gRPC forward
  |
  v  Authentication Service
  |
  +-- Validate input
  |     email format?          NO --> 400 Bad Request
  |     password rules?        NO --> 400 Bad Request
  |     username format?       NO --> 400 Bad Request
  |
  +-- Check duplicate
  |     email exists?          YES --> 409 Conflict
  |     username exists?       YES --> 409 Conflict
  |
  +-- Hash password  (bcrypt -- never stored plaintext)
  |
  +-- [*] Generate user_id  (UUIDv7, in application code)
  |          This ID is passed to Wallet and used by every service forever
  |
  +-- INSERT users(id=user_id, email, username, password_hash, status=ACTIVE)
  |
  +-- gRPC --> Wallet Service: InitializeWallet(user_id)
  |                |
  |                v  Wallet Service
  |                +-- Read supported_assets table
  |                +-- For each asset (USDT, BTC, ETH, SOL):
  |                      CREATE wallet row (available_balance = seed_amount)
  |                      If seed_amount > 0:
  |                        INSERT wallet_transactions(
  |                          reference_id   = user_id  [*]
  |                          reference_type = INITIAL_ALLOCATION
  |                          asset          = asset_code
  |                          amount         = seed_amount
  |                        )
  |                        UNIQUE(reference_id, reference_type, asset)
  |                        prevents duplicate seeds per user per asset
  |
  |     [If InitializeWallet FAILS after user committed]
  |       Retry 2-3x with backoff
  |       Still fails --> DELETE user row (compensating action) --> 500
  |         [If DELETE also fails --> orphan user detected on next startup]
  |
  +-- Generate JWT access token  (15 min)
  +-- Generate refresh token  (7 days)
  +-- INSERT refresh_tokens(user_id, token_hash, status=ACTIVE, expires_at)
  |
  +-- Return 200 { user: {id, email, username}, access_token, refresh_token }
```

---

## 3. Auth — Login

```
POST /auth/login  { email, password }
  |
  v  API Gateway: CORS > Log > Rate limit > Route (public) > gRPC forward
  |
  v  Authentication Service
  |
  +-- SELECT user WHERE email = ?
  |     NOT FOUND --> 401 "invalid credentials"
  |       (same message as wrong password -- intentional, no enumeration)
  |
  +-- Compare password hash  (bcrypt compare)
  |     WRONG --> 401 "invalid credentials"
  |
  +-- Check account status
  |     SUSPENDED --> 403 Forbidden
  |     BANNED    --> 403 Forbidden
  |
  +-- Generate JWT access token  (15 min)
  +-- Generate refresh token  (7 days)
  +-- INSERT refresh_tokens(user_id, token_hash, ACTIVE, expires_at)
  |
  +-- UPDATE users SET
  |     last_login_at = now()
  |     last_login_ip = request.ip
  |     last_login_ua = request.user_agent
  |
  +-- Return 200 { user: {id, email, username}, access_token, refresh_token }
```

---

## 4. Auth — JWT Validation

> Runs inside API Gateway's JWT Middleware on every authenticated request.
> Uses the shared verification package -- no gRPC call to Auth Service.

```
Authorization: Bearer <access_token>
  |
  v  JWT Middleware (inside API Gateway -- local execution, shared package)
  |
  +-- Extract token from Authorization header
  |     Missing header --> 401
  |
  +-- Parse JWT structure (header.payload.signature)
  |     Malformed --> 401
  |
  +-- Verify signature using signing key
  |     Invalid signature --> 401
  |
  +-- Check exp claim (expiry timestamp)
  |     Token expired --> 401
  |
  +-- Check Redis blacklist
  |     Key: "blacklist:{jti}"  (jti = JWT unique ID)
  |     Found in blacklist --> 401  (token was revoked on logout)
  |
  +-- VALID --> Attach to request context:
        user_id  (from sub claim)
        roles    (from roles claim)
        jti      (for downstream checks)
        |
        v  Request forwarded to target service with user context
```

---

## 5. Auth — Refresh Token

```
POST /auth/refresh  { refresh_token }
  |
  v  API Gateway: public route, no JWT check
  |
  v  Authentication Service
  |
  +-- Hash incoming refresh_token
  +-- SELECT FROM refresh_tokens WHERE token_hash = ?
  |     NOT FOUND --> 401 "invalid or expired refresh token"
  |
  +-- Check token status
  |     status = ROTATED --> 403 "token reuse detected"
  |       Security: UPDATE refresh_tokens SET status = REVOKED
  |                 WHERE user_id = ?   (revoke ALL sessions)
  |
  +-- Check expiry (expires_at < now)
  |     EXPIRED --> 401
  |
  +-- Validate signature
  |     INVALID --> 401
  |
  +-- UPDATE old token: SET status = ROTATED
  +-- INSERT new refresh_tokens(user_id, new_hash, ACTIVE, new_expires_at)
  +-- Generate new access token  (15 min)
  |
  +-- Return 200 { access_token, refresh_token }
```

---

## 6. Auth — Logout

```
POST /auth/logout  { refresh_token }
Authorization: Bearer <access_token>
  |
  v  API Gateway --> JWT Middleware validates access token
  |
  v  Authentication Service
  |
  +-- Hash incoming refresh_token
  +-- UPDATE refresh_tokens SET status = REVOKED WHERE token_hash = ?
  |
  +-- Extract jti from access token claims
  +-- SET Redis key: "blacklist:{jti}"
  |     Value: "revoked"
  |     TTL  : remaining lifetime of access token
  |     (blacklist only needed until natural expiry)
  |
  +-- [Optional] logout all sessions:
  |     UPDATE refresh_tokens SET status = REVOKED WHERE user_id = ?
  |
  +-- Return 204 No Content
```

---

## 7. Auth — Change Password

```
POST /auth/change-password  { current_password, new_password }
Authorization: Bearer <access_token>
  |
  v  API Gateway --> JWT Middleware --> gRPC forward
  |
  v  Authentication Service
  |
  +-- SELECT user WHERE id = user_id (from JWT sub claim)
  |
  +-- Compare current_password with stored hash
  |     WRONG --> 401 "incorrect current password"
  |
  +-- Validate new_password strength
  |     WEAK --> 400
  |
  +-- Hash new_password  (bcrypt)
  +-- UPDATE users SET password_hash = new_hash
  |
  +-- Revoke all sessions:
  |     UPDATE refresh_tokens SET status = REVOKED WHERE user_id = ?
  |     Reason: if password was compromised, kill attacker sessions too
  |
  +-- Return 200 "password changed, all sessions revoked"
```

---

## 8. Order — Create Limit Order

```
POST /orders  { market: BTC_USDT, side: BUY, type: LIMIT, price: 60000, qty: 0.5 }
Authorization: Bearer <access_token>
  |
  v  API Gateway --> JWT Middleware --> gRPC --> Order Service
  |
  v  Order Service
  |
  +-- [*] Generate order_id  (UUIDv7, in application code)
  |          Generated BEFORE any DB write or Wallet call
  |          Reason: Wallet.ReserveFunds needs order_id as parameter
  |
  +-- Validate request
  |     Trading pair exists (BTC_USDT)?    NO --> 400
  |     Market is open?                    NO --> 400
  |     qty > 0?                           NO --> 400
  |     LIMIT requires price field?        NO --> 400
  |     FAIL: no order row created, no funds touched
  |
  +-- Calculate reservation amount
  |     BUY  LIMIT: price x qty = 60,000 x 0.5 = 30,000 USDT
  |     SELL LIMIT: qty of base asset = 0.5 BTC
  |
  +-- gRPC --> Wallet Service: ReserveFunds(user_id, order_id[*], asset, amount)
  |                |
  |                v  Wallet Service  [see Flow 11]
  |                +-- Check available_balance >= 30,000 USDT
  |                |     NO --> gRPC error --> Order rejects request (no row created)
  |                +-- available_balance  -= 30,000
  |                +-- reserved_balance   += 30,000
  |                +-- INSERT wallet_reservations(
  |                      order_id        = order_id  [*]
  |                      user_id         = user_id
  |                      asset           = USDT
  |                      reserved_amount = 30,000
  |                      status          = ACTIVE
  |                    )
  |                    UNIQUE(order_id) -- one reservation per order
  |
  +-- DB Transaction  (atomic -- both succeed or both roll back)
  |     INSERT orders(
  |       id             = order_id  [*]
  |       user_id, market_id = BTC_USDT,
  |       side = BUY, order_type = LIMIT,
  |       price = 60000, qty = 0.5,
  |       filled_qty = 0, remaining_qty = 0.5,
  |       status = OPEN
  |     )
  |     INSERT outbox(
  |       aggregate_id  = order_id  [*]
  |       event_type    = OrderCreated
  |       partition_key = BTC_USDT
  |       payload       = { order_id, user_id, side, price, qty, ... }
  |     )
  |
  +-- Commit --> Return 201 { order_id, status: OPEN }
  |
  +-- [Background: Outbox Publisher -- polls every 100-250ms]
        SELECT * FROM outbox WHERE published_at IS NULL
        Publish to Kafka (partition key = BTC_USDT)
        UPDATE outbox SET published_at = now()  (only after Kafka ack)
          |
          v  Kafka: OrderCreated  (partition: BTC_USDT)
              |
              v  Matching Engine
                   Add to in-memory order book  (price-time priority)
```

---

## 9. Order — Create Market Order (IOC)

```
POST /orders  { market: BTC_USDT, side: BUY, type: MARKET, qty: 0.5 }
Authorization: Bearer <access_token>
  |
  v  API Gateway --> JWT --> Order Service
  |
  +-- [*] Generate order_id  (UUIDv7)
  |
  +-- Validate
  |     MARKET must NOT have price field    --> price present --> 400
  |     qty > 0                             --> NO --> 400
  |
  +-- Reservation for MARKET BUY:
  |     Price unknown at order time
  |     Decision (V1): Reserve user's ENTIRE available USDT balance
  |     Reason: guarantees funds at any fill price; remainder released after fill
  |
  +-- gRPC --> Wallet: ReserveFunds(user_id, order_id, USDT, full_available_balance)
  |
  +-- DB Txn { INSERT order(price=NULL, status=OPEN) + outbox(OrderCreated) }
  |
  +-- Commit --> Return 201
  |
  +-- Kafka: OrderCreated --> Matching Engine

  Matching Engine  (IOC: fill immediately, NEVER rest on book):

  CASE A -- Fully filled
    Kafka: TradeExecuted  --> Settlement flow
    Unused USDT reservation released by Wallet after SettleTrade

  CASE B -- Partially filled  (ran out of sell orders)
    Kafka: TradeExecuted   (for filled portion)
    Kafka: OrderCancelled  (remaining qty)
    Order Service calls Wallet: ReleaseFunds(order_id) for unfilled portion

  CASE C -- No liquidity
    Kafka: OrderCancelled  (full qty)
    Order Service calls Wallet: ReleaseFunds(order_id) for full reservation
```

---

## 10. Order — Cancel Order + Race Condition

```
DELETE /orders/{order_id}
Authorization: Bearer <access_token>
  |
  v  API Gateway --> JWT --> Order Service
  |
  +-- SELECT order WHERE id = order_id
  |     NOT FOUND --> 404
  |
  +-- Check ownership: order.user_id == JWT user_id
  |     MISMATCH --> 403 Forbidden
  |
  +-- Check status: only OPEN or PARTIALLY_FILLED can cancel
  |     WRONG STATUS --> 409 Conflict
  |
  +-- DB Transaction  (atomic)
  |     UPDATE orders SET status = CANCELLING
  |     INSERT outbox(
  |       event         = OrderCancelRequested
  |       partition_key = market_id   (same as OrderCreated -- same partition)
  |     )
  |
  +-- Commit --> Return 202 Accepted  (cancel requested, not yet confirmed)
  |
  +-- [Background: Outbox Publisher]
        Kafka: OrderCancelRequested  (partition: BTC_USDT)
          |
          v  Matching Engine

---------------------------------------------------------------------
CASE A: Not yet filled when cancel arrives
---------------------------------------------------------------------
  ME:
    Remove order from in-memory order book
    Publish Kafka: OrderCancelled { order_id, remaining_qty = full qty }
      |
      v  Order Service (consumes OrderCancelled)
           UPDATE orders SET status = CANCELLED
           gRPC --> Wallet: ReleaseFunds(order_id)
             |
             v  Wallet Service
                  remaining = reserved - consumed
                  available_balance += remaining
                  UPDATE reservation SET status = RELEASED

---------------------------------------------------------------------
CASE B: Partial fill occurred before cancel arrived
---------------------------------------------------------------------
  ME:
    Publish Kafka: TradeExecuted       (for the filled portion)
    Publish Kafka: OrderCancelled      { remaining_qty = unfilled amount }
      |
      v  Order Service
           On TradeExecuted:   update filled_qty, status stays CANCELLING
           On OrderCancelled:  status --> CANCELLED
           gRPC --> Wallet: ReleaseFunds(order_id)  (releases unfilled portion)

---------------------------------------------------------------------
CASE C: Fully filled before cancel arrived
---------------------------------------------------------------------
  ME:
    Publish Kafka: TradeExecuted  (full fill)
    Cancel ignored -- does NOT publish OrderCancelled
      |
      v  Order Service
           On TradeExecuted: status --> FILLED  (cancel has no effect)
```

---

## 11. Wallet — Reserve Funds

> Called synchronously by Order Service before saving the order.

```
gRPC: ReserveFunds(user_id, order_id, asset, amount)
  |
  v  Wallet Service
  |
  +-- SELECT wallets WHERE user_id = ? AND asset = ?  FOR UPDATE
  |     NOT FOUND --> error (wallet doesn't exist for this asset)
  |
  +-- Check: available_balance >= amount
  |     NO --> gRPC error "insufficient balance"
  |            Order Service receives error
  |            Rejects create-order request
  |            No order row created, no reservation row created
  |
  +-- UPDATE wallets SET
  |     available_balance -= amount
  |     reserved_balance  += amount
  |
  +-- INSERT wallet_reservations(
  |     id               = UUIDv7  (generated by Wallet Service)
  |     order_id         = order_id   [*] correlation key
  |     user_id          = user_id
  |     asset            = asset
  |     reserved_amount  = amount
  |     consumed_amount  = 0
  |     remaining_amount = amount
  |     status           = ACTIVE
  |   )
  |   UNIQUE(order_id) -- exactly one reservation per order
  |
  +-- Return success
```

---

## 12. Wallet — Trade Settlement

> Called synchronously by Settlement Service after consuming TradeExecuted from Kafka.

```
gRPC: SettleTrade(trade_id, buyer_id, seller_id,
                  buy_order_id, sell_order_id,
                  base_asset, quote_asset, price, quantity)
  |
  v  Wallet Service
  |
  +-- Idempotency check:
  |     SELECT FROM wallet_transactions
  |     WHERE reference_id = trade_id AND reference_type = SETTLEMENT
  |     FOUND --> already settled, return success (safe replay)
  |
  +-- Lock both reservation rows  (prevent concurrent modification)
  |     SELECT FROM wallet_reservations
  |     WHERE order_id IN (buy_order_id, sell_order_id)  FOR UPDATE
  |
  +-- BUYER LEG  (spent USDT, receives BTC)
  |     quote_spent = price x quantity = 60,000 x 0.5 = 30,000 USDT
  |     UPDATE wallet_reservations (buyer)
  |       consumed_amount  += 30,000
  |       remaining_amount  = reserved - consumed
  |     UPDATE wallets (buyer's BTC wallet)
  |       available_balance += 0.5 BTC
  |     INSERT wallet_transactions(
  |       id               = UUIDv7
  |       wallet_id        = buyer_btc_wallet_id
  |       reference_id     = trade_id  [*]
  |       reference_type   = SETTLEMENT
  |       transaction_type = CREDIT
  |       asset = BTC, amount = 0.5
  |     )
  |
  +-- SELLER LEG  (spent BTC, receives USDT)
  |     UPDATE wallet_reservations (seller)
  |       consumed_amount  += 0.5 BTC
  |       remaining_amount  = reserved - consumed
  |     UPDATE wallets (seller's USDT wallet)
  |       available_balance += 30,000 USDT
  |     INSERT wallet_transactions(
  |       id               = UUIDv7
  |       wallet_id        = seller_usdt_wallet_id
  |       reference_id     = trade_id  [*]
  |       reference_type   = SETTLEMENT
  |       transaction_type = CREDIT
  |       asset = USDT, amount = 30,000
  |     )
  |
  +-- INSERT outbox(event = TradeSettled, payload includes trade_id, order_ids)
  |
  +-- COMMIT  (all-or-nothing -- if any step fails, full rollback)
  |
  |   [If commit fails]
  |   Settlement Service retries with backoff
  |   Exhausted retries --> dead-letter topic --> manual reconciliation
  |   Kafka offset committed ONLY after success (at-least-once, idempotent)
  |
  +-- [Background: Outbox Publisher]
        Kafka: TradeSettled
          +-- Portfolio Service  --> update holdings, avg entry, PnL
          +-- Notification Service --> push via WebSocket --> Client
```

---

## 13. Wallet — Release Funds

> Called by Order Service when an order is cancelled.

```
gRPC: ReleaseFunds(order_id)
  |
  v  Wallet Service
  |
  +-- SELECT FROM wallet_reservations WHERE order_id = ?  FOR UPDATE
  |     NOT FOUND or status = RELEASED --> return success (idempotent)
  |
  +-- Calculate release:
  |     release_amount = reserved_amount - consumed_amount  (= remaining_amount)
  |     consumed_amount accounts for any partial fills that already settled
  |
  +-- UPDATE wallets SET
  |     available_balance += release_amount
  |     reserved_balance  -= release_amount
  |
  +-- UPDATE wallet_reservations SET
  |     status           = RELEASED
  |     remaining_amount = 0
  |
  +-- Return success
```

---

## 14. Full End-to-End

```
CLIENT
  |
  |  POST /orders  { BTC_USDT, BUY, LIMIT, price=60000, qty=0.5 }
  v
API GATEWAY
  CORS > Log > Rate limit > Route (auth required) > JWT validate > gRPC forward
  |
  v
ORDER SERVICE
  +-- [*] Generate order_id (UUIDv7)
  +-- Validate request
  +-- gRPC --> WALLET: ReserveFunds(user_id, order_id[*], USDT, 30000)
  |                |
  |                v  WALLET SERVICE
  |                     Lock wallet FOR UPDATE
  |                     available -= 30000, reserved += 30000
  |                     INSERT reservation(order_id=[*], status=ACTIVE)
  |
  +-- DB Txn { INSERT order(id=[*], status=OPEN) + INSERT outbox(OrderCreated) }
  +-- Commit --> Return 201 to CLIENT  <-- client sees success here
  |
  |  [Background: Outbox Publisher]
  v
KAFKA: OrderCreated  (partition: BTC_USDT)
  |
  v
MATCHING ENGINE
  +-- Add to in-memory order book
  +-- Finds matching sell order (sell_order_B at 60000, qty=0.5)
  +-- [*] Generate trade_id (UUIDv7)
  +-- Publish KAFKA: TradeExecuted {
        trade_id=[*], buy_order_id=[*], sell_order_id,
        buyer_id, seller_id, price=60000, qty=0.5
      }
  |
  v
SETTLEMENT SERVICE  (consumes TradeExecuted)
  +-- gRPC --> WALLET: SettleTrade(trade_id[*], buy_order_id[*], ...)
  |                |
  |                v  WALLET SERVICE
  |                     Idempotency: check trade_id not already settled
  |                     Lock reservations FOR UPDATE
  |                     BUYER:  consumed += 30000 USDT, credit 0.5 BTC
  |                     SELLER: consumed += 0.5 BTC,   credit 30000 USDT
  |                     INSERT wallet_transactions(ref=trade_id[*])
  |                     INSERT outbox(TradeSettled)
  |                     COMMIT
  |
  |  [Background: Outbox Publisher]
  v
KAFKA: TradeSettled
  +-- PORTFOLIO SERVICE  --> update holdings, avg entry, PnL
  +-- NOTIFICATION SERVICE --> WebSocket --> CLIENT
```

---

## 15. ID Correlation

> Every ID is a UUIDv7 generated by the owning service in application code before any DB write.

```
+------------------+-----------------------+--------------------------+
|  ID              |  Generated By         |  When                    |
+------------------+-----------------------+--------------------------+
|  user_id    [*]  |  Auth Service         |  Before INSERT users     |
|  order_id   [*]  |  Order Service        |  Before ReserveFunds     |
|  reservation_id  |  Wallet Service       |  During ReserveFunds     |
|  wallet_id       |  Wallet Service       |  During InitializeWallet |
|  transaction_id  |  Wallet Service       |  Each balance move       |
|  trade_id   [*]  |  Matching Engine      |  At match time           |
|  outbox row id   |  Each owning service  |  With each event write   |
+------------------+-----------------------+--------------------------+

[*] = Correlation IDs -- carried across ALL downstream events and services

How user_id flows:
  Auth Service generates user_id
    --> Wallet Service (InitializeWallet parameter)
    --> Order Service  (from JWT sub claim on every request)
    --> Wallet Service (ReserveFunds parameter)
    --> TradeExecuted event (as buyer_id or seller_id)
    --> SettleTrade parameter
    --> wallet_transactions rows
    --> Portfolio Service
    --> Notification Service

How order_id flows:
  Order Service generates order_id (BEFORE ReserveFunds call)
    --> Wallet Service (ReserveFunds parameter)
    --> wallet_reservations.order_id
    --> outbox.aggregate_id
    --> Kafka: OrderCreated payload
    --> Matching Engine in-memory order book entry
    --> Kafka: TradeExecuted  (as buy_order_id or sell_order_id)
    --> Settlement Service
    --> Wallet SettleTrade parameter (buy_order_id / sell_order_id)
    --> ReleaseFunds parameter (on cancel)
    --> wallet_transactions.reference_id with type=SETTLEMENT

How trade_id flows:
  Matching Engine generates trade_id (at match time, no DB)
    --> Kafka: TradeExecuted.trade_id
    --> Settlement Service
    --> Wallet SettleTrade parameter
    --> wallet_transactions.reference_id (SETTLEMENT type)
    --> Idempotency key for replay safety
    --> Kafka: TradeSettled.trade_id
    --> Portfolio Service
    --> Notification Service

Why UUIDv7 and not auto-increment?
  order_id must exist BEFORE the order row is inserted
    (Wallet.ReserveFunds needs it before the DB write)
  trade_id is generated in Matching Engine memory, no DB
    (Matching Engine has no persistent DB for the order book)
  UUIDv7 is time-ordered --> index-friendly, no hotspot
  Service-generated --> no database round-trip to get an ID
```

---

## 16. Order State Machine

```
          [Create Order Request]
                  |
       Validate + ReserveFunds
                  |
       +----------+-----------+
     FAILS                 SUCCESS
       |                       |
  Reject request         INSERT order
  (no row created,       status = OPEN
   no funds touched)         |
               +-------------+-------------+
               |             |             |
          Partial fill   Full fill   Cancel requested
               |             |             |
      PARTIALLY_FILLED    FILLED      CANCELLING
               |                      |        |
         +-----+------+         ME         Fill before
     Remaining    Cancel      confirms      cancel
     matched     requested    cancel       arrives
         |           |           |             |
      FILLED    CANCELLING   CANCELLED   PARTIALLY_FILLED
                    |                      or FILLED
               ME confirms
               cancel
                    |
                CANCELLED

Valid transitions:
  OPEN             --> PARTIALLY_FILLED   (partial fill)
  OPEN             --> FILLED             (full fill)
  OPEN             --> CANCELLING         (cancel requested)
  PARTIALLY_FILLED --> FILLED             (remaining filled)
  PARTIALLY_FILLED --> CANCELLING         (cancel requested)
  CANCELLING       --> CANCELLED          (ME confirmed removal)
  CANCELLING       --> PARTIALLY_FILLED   (fill before cancel processed)
  CANCELLING       --> FILLED             (fully filled before cancel processed)
```

---

## 17. Wallet Reservation State Machine

```
        ReserveFunds(order_id) called
                   |
                ACTIVE
                   |
       +-----------+------------------+
       |           |                  |
   Full fill   Partial fill    ReleaseFunds
       |           |           (cancel order)
  CONSUMED  PARTIALLY_CONSUMED       |
                   |              RELEASED
             +-----+---------+
         Further fill    ReleaseFunds
             |            (cancel)
          CONSUMED        RELEASED

remaining_amount = reserved_amount - consumed_amount  (always)
ReleaseFunds returns exactly remaining_amount to available_balance
```

---

## 18. Service Communication Map

```
===================== SYNCHRONOUS (gRPC -- blocking) =====================

  API Gateway        --> Any downstream service    (gRPC forward, 2s timeout)
  Auth Service       --> Wallet Service            InitializeWallet(user_id)
  Order Service      --> Wallet Service            ReserveFunds(user_id, order_id, asset, amount)
  Order Service      --> Wallet Service            ReleaseFunds(order_id)
  Settlement Service --> Wallet Service            SettleTrade(trade_id, ...)

===================== ASYNCHRONOUS (Kafka -- event-driven) ===============

  Publisher             Event                   Consumer(s)
  ------------------------------------------------------------------
  Order Service         OrderCreated            Matching Engine
  Order Service         OrderCancelRequested    Matching Engine
  Matching Engine       TradeExecuted           Settlement Service
  Matching Engine       OrderCancelled          Order Service
  Wallet Service        TradeSettled            Portfolio Service
                                                Notification Service
  Portfolio Service     PortfolioUpdated        Notification Service
  Notification Service  push via WebSocket      Client

===================== PARTITION KEYS (Kafka ordering) ====================

  Event                  Partition Key     Reason
  ------------------------------------------------------------------
  OrderCreated           market_id         Per-market ordering in ME
  OrderCancelRequested   market_id         Same partition as OrderCreated
                                           guarantees cancel processed after create
  TradeExecuted          trade_id
  TradeSettled           trade_id
  OrderCancelled         order_id
```
