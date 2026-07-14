# TradeDrift — gRPC Service Contracts

> **Status:** ✅ Designed (V1.3)
> **Document:** 16_gRPC_Contracts.md
> **Service:** Platform Architecture
> **Version:** V1.3
> **Last Updated:** July 2026
> Revision notes: V1.3 specifies WalletService gRPC endpoints for funding (DepositFunds, WithdrawFunds) to align with the V10 Wallet Service specification.

---

## 1. Introduction & Global RPC Policies

TradeDrift services communicate internally using high-performance gRPC over HTTP/2. To ensure reliability, low latency, and deterministic failure recovery across our service mesh, all internal gRPC calls must adhere to global policies:

```
[ Client Service ] ──(2000ms Timeout)──► [ Target Service ]
                        ├── Success Response (OK)
                        └── Error Status Code (e.g. FAILED_PRECONDITION)
```

### Global Policies:
* **Protocol Version:** `proto3`
* **Network Encryption:** TLS (Standard encrypted internal cluster networking).
* **Deadline Policy:** All internal gRPC calls must enforce a maximum client-side deadline timeout of **2,000ms**. Consumers must abort the call and handle the timeout error (with fallback or circuit-breaking) if a response is not received.
* **Numeric Representation:** All balance values, order quantities, execution prices, and transaction amounts must be serialized as `string` types in Protobuf. This guarantees that decimal values are parsed with absolute precision (PostgreSQL `DECIMAL(30,10)` scale) and prevents floating-point issues across heterogeneous languages.

---

## 2. Distributed Tracing & Metadata Propagation

All gRPC clients must propagate tracing context as request metadata. Incoming metadata keys must be read and forwarded in downstream RPC calls and outbox messages to preserve tracing context.

| Metadata Header | Format | Purpose |
|---|---|---|
| `x-request-id` | UUIDv7 | Uniquely identifies the client HTTP transaction. |
| `x-correlation-id` | UUIDv7 | Uniquely identifies the transaction saga (e.g., the original order placement ID). |
| `x-causation-id` | UUIDv7 | ID of the immediate action/event that triggered this RPC. |
| `traceparent` | W3C Span | Standard trace parent header for distributed APM engines (Jaeger/OpenTelemetry). |

---

## 3. RPC Resilience & Retry Semantics

We define strict retry strategies based on the safety and idempotency profile of each call:

| RPC Method | Safety Category | Max Retries | Backoff Strategy | Behavior on Permanent Failure |
|---|---|---|---|---|
| `InitializeWallet` | Idempotent | 5 | Exponential (100ms - 2s) | Abort Registration (comp-delete user) |
| `ReserveFunds` | Idempotent | 5 | Exponential (100ms - 2s) | Reject Order placement |
| `ReleaseFunds` | Idempotent | 10 (Critical) | Exponential (100ms - 5s) | Log Alarm / Dead-Letter outbox event |
| `SettleTrade` | Idempotent | 10 (Critical) | Exponential (100ms - 5s) | Hold Match offset / Alert Ops |
| `CreateOrder` | Non-Idempotent (without Key) | 0 | No Retries | Return API Error to client |
| `CreateOrder` | Idempotent (with Key) | 3 | Exponential (100ms - 1s) | Return API Error to client |
| Read queries (e.g. `GetBalance`) | Read-Only | 3 | Linear (200ms) | Fail-closed / Return 503 Service Temp Unavailable |

---

## 4. Protobuf Definitions Catalog

### 4.1 Shared Common Types (`common.proto`)
Shared messages and enums imported by all service-level proto files.

```protobuf
syntax = "proto3";

package tradedrift.common.v1;

option go_package = "github.com/tradedrift/platform/api/common/v1;commonv1";

enum OrderSide {
    ORDER_SIDE_UNSPECIFIED = 0;
    ORDER_SIDE_BUY = 1;
    ORDER_SIDE_SELL = 2;
}

enum OrderType {
    ORDER_TYPE_UNSPECIFIED = 0;
    ORDER_TYPE_LIMIT = 1;
    ORDER_TYPE_MARKET = 2;
}

enum OrderStatus {
    ORDER_STATUS_UNSPECIFIED = 0;
    ORDER_STATUS_OPEN = 1;
    ORDER_STATUS_PARTIALLY_FILLED = 2;
    ORDER_STATUS_FILLED = 3;
    ORDER_STATUS_CANCELLING = 4;
    ORDER_STATUS_CANCELLED = 5;
}

enum ReservationStatus {
    RESERVATION_STATUS_UNSPECIFIED = 0;
    RESERVATION_STATUS_ACTIVE = 1;
    RESERVATION_STATUS_PARTIALLY_CONSUMED = 2;
    RESERVATION_STATUS_CONSUMED = 3;
    RESERVATION_STATUS_RELEASED = 4;
}

enum TradeSide {
    TRADE_SIDE_UNSPECIFIED = 0;
    TRADE_SIDE_BUYER = 1;
    TRADE_SIDE_SELLER = 2;
}
```

---

### 4.2 `WalletService` (`wallet.proto`)
Responsible for user wallets initialization, synchronous fund reservations, releases, and transaction settlements.

```protobuf
syntax = "proto3";

package tradedrift.wallet.v1;

import "common.proto";

option go_package = "github.com/tradedrift/platform/api/wallet/v1;walletv1";

service WalletService {
    rpc InitializeWallet(InitializeWalletRequest) returns (InitializeWalletResponse);
    rpc ReserveFunds(ReserveFundsRequest) returns (ReserveFundsResponse);
    rpc ReleaseFunds(ReleaseFundsRequest) returns (ReleaseFundsResponse);
    rpc SettleTrade(SettleTradeRequest) returns (SettleTradeResponse);
    rpc GetBalance(GetBalanceRequest) returns (GetBalanceResponse);
    rpc GetBalances(GetBalancesRequest) returns (GetBalancesResponse);
    rpc GetSupportedAssets(GetSupportedAssetsRequest) returns (GetSupportedAssetsResponse);
    rpc DepositFunds(DepositFundsRequest) returns (DepositFundsResponse);
    rpc WithdrawFunds(WithdrawFundsRequest) returns (WithdrawFundsResponse);
}

message InitializeWalletRequest {
    string user_id = 1; // User ID (UUIDv7 format)
}

message InitializeWalletResponse {
    string user_id = 1;
    bool success   = 2;
}

message ReserveFundsRequest {
    string user_id  = 1; // Owner of the funds (UUIDv7 format)
    string order_id = 2; // Order requesting reservation (UUIDv7 format)
    string asset    = 3; // Asset code (e.g. "USDT", "BTC")
    string amount   = 4; // Decimal representation of quantity to lock (String)
}

message ReserveFundsResponse {
    string reservation_id = 1; // Generated reservation ID (UUIDv7 format)
    string order_id       = 2; // Associated Order ID
    string asset          = 3; // Reserved asset code
    string locked_amount  = 4; // Total amount locked (String)
    tradedrift.common.v1.ReservationStatus status = 5;
}

message ReleaseFundsRequest {
    string order_id = 1; // Order ID whose remaining funds should be released
}

message ReleaseFundsResponse {
    string order_id        = 1;
    string released_amount = 2; // Amount returned to available balance (String)
    tradedrift.common.v1.ReservationStatus status = 3;
}

message SettleTradeRequest {
    string trade_id      = 1;  // Unique Trade Match ID (UUIDv7 format)
    string buyer_id      = 2;  // Buyer User ID (UUIDv7 format)
    string seller_id     = 3;  // Seller User ID (UUIDv7 format)
    string buy_order_id  = 4;  // Buyer's Order ID (UUIDv7 format)
    string sell_order_id = 5;  // Seller's Order ID (UUIDv7 format)
    string base_asset    = 6;  // e.g. "BTC"
    string quote_asset   = 7;  // e.g. "USDT"
    string price         = 8;  // Match price (String)
    string quantity      = 9;  // Match quantity (String)
    string market_id     = 10; // Market pair ID (e.g. "BTC-USDT")
}

message SettleTradeResponse {
    string trade_id = 1;
    bool success    = 2;
}

message GetBalanceRequest {
    string user_id = 1;
    string asset   = 2;
}

message GetBalanceResponse {
    string user_id           = 1;
    string asset             = 2;
    string available_balance = 3; // (String)
    string reserved_balance  = 4; // (String)
}

message GetBalancesRequest {
    string user_id = 1;
}

message BalanceDetail {
    string asset             = 1;
    string available_balance = 2; // (String)
    string reserved_balance  = 3; // (String)
}

message GetBalancesResponse {
    string user_id                 = 1;
    repeated BalanceDetail balances = 2;
}

message GetSupportedAssetsRequest {}

message AssetDetail {
    string asset_code   = 1; // e.g. "BTC"
    string asset_name   = 2; // e.g. "Bitcoin"
    int32  decimals     = 3; // Decimal scale, e.g. 8 or 18
    bool   is_enabled   = 4;
    string seed_amount  = 5; // Amount credited during onboarding (String)
    int32  display_order = 6;
}

message GetSupportedAssetsResponse {
    repeated AssetDetail assets = 1;
}

message DepositFundsRequest {
    string user_id      = 1; // User receiving credit (UUIDv7 format)
    string asset        = 2; // Asset code, e.g. "USDT"
    string amount       = 3; // Amount to deposit (String)
    string reference_id = 4; // Unique provider transaction identifier (idempotency key)
}

message DepositFundsResponse {
    string transfer_id  = 1; // Ledger transaction ID (UUIDv7 format)
    string reference_id = 2; // Gateway transaction reference ID
    bool success        = 3;
    string new_balance  = 4; // User's updated available balance (String)
}

message WithdrawFundsRequest {
    string user_id      = 1; // User requesting withdrawal (UUIDv7 format)
    string asset        = 2; // Asset code, e.g. "BTC"
    string amount       = 3; // Amount to withdraw (String)
    string reference_id = 4; // Client-supplied idempotency key (UUIDv7 format)
}

message WithdrawFundsResponse {
    string transfer_id  = 1; // Ledger transaction ID (UUIDv7 format)
    string reference_id = 2; // Client-supplied reference ID
    string status       = 3; // Status of transfer: "PENDING" | "COMPLETED" | "FAILED"
    string new_balance  = 4; // User's updated available balance (String)
}
```

---

### 4.3 `OrderService` (`order.proto`)
Exposes methods for placing, cancelling, and listing user orders.

```protobuf
syntax = "proto3";

package tradedrift.order.v1;

import "common.proto";

option go_package = "github.com/tradedrift/platform/api/order/v1;orderv1";

service OrderService {
    rpc CreateOrder(CreateOrderRequest) returns (CreateOrderResponse);
    rpc CancelOrder(CancelOrderRequest) returns (CancelOrderResponse);
    rpc GetOrder(GetOrderRequest) returns (OrderDetailsResponse);
    rpc ListOrders(ListOrdersRequest) returns (ListOrdersResponse);
    rpc CancelAllOrders(CancelAllOrdersRequest) returns (CancelAllOrdersResponse);
}

message CreateOrderRequest {
    string user_id    = 1; // Buyer/Seller ID (UUIDv7 format)
    string market_id  = 2; // Target trade pair (e.g. "BTC-USDT")
    tradedrift.common.v1.OrderSide side = 3;
    tradedrift.common.v1.OrderType order_type = 4;
    string price      = 5; // Price per unit; ignored if MARKET (String)
    string quantity   = 6; // Quantity to purchase (String)
}

message CreateOrderResponse {
    string order_id = 1; // Generated order ID (UUIDv7 format)
    tradedrift.common.v1.OrderStatus status = 2;
}

message CancelOrderRequest {
    string order_id = 1; // Order ID to cancel
    string user_id  = 2; // Owner validation ID
}

message CancelOrderResponse {
    string order_id = 1;
    tradedrift.common.v1.OrderStatus status = 2;
}

message GetOrderRequest {
    string order_id = 1;
}

message OrderDetailsResponse {
    string order_id           = 1;
    string user_id            = 2;
    string market_id          = 3;
    tradedrift.common.v1.OrderSide side = 4;
    tradedrift.common.v1.OrderType order_type = 5;
    string price              = 6;  // (String)
    string quantity           = 7;  // (String)
    string filled_quantity    = 8;  // (String)
    string remaining_quantity = 9;  // (String)
    tradedrift.common.v1.OrderStatus status = 10;
    string created_at         = 11;
    string updated_at         = 12;
}

message ListOrdersRequest {
    string user_id = 1;
    string cursor  = 2; // Pagination keyset cursor
    int32  limit   = 3;
}

message ListOrdersResponse {
    repeated OrderDetailsResponse orders = 1;
    string cursor                        = 2; // Cursor token for next batch
}

message CancelAllOrdersRequest {
    string market_id = 1; // Target market to decommission
}

message CancelAllOrdersResponse {
    int32 cancelled_count = 1; // Estimated count of orders flagged for cancel
}
```

---

### 4.4 `MarketService` (`market.proto`)
Exposes active trade pairs metadata and parameter settings.

```protobuf
syntax = "proto3";

package tradedrift.market.v1;

option go_package = "github.com/tradedrift/platform/api/market/v1;marketv1";

service MarketService {
    rpc GetMarket(GetMarketRequest) returns (MarketResponse);
    rpc ListMarkets(ListMarketsRequest) returns (ListMarketsResponse);
}

message GetMarketRequest {
    string market_id = 1; // e.g. "BTC-USDT"
}

message MarketResponse {
    string id          = 1; // Market identifier
    string base_asset  = 2; // e.g. "BTC"
    string quote_asset = 3; // e.g. "USDT"
    string tick_size   = 4; // Minimum price step increment (String)
    string lot_size    = 5; // Minimum trade quantity increment (String)
    bool   is_enabled  = 6; // Active status flag
}

message ListMarketsRequest {}

message ListMarketsResponse {
    repeated MarketResponse markets = 1;
}
```

---

### 4.5 `TradeService` (`trade.proto`)
Queryable ledger hosting historical matching executions.

```protobuf
syntax = "proto3";

package tradedrift.trade.v1;

option go_package = "github.com/tradedrift/platform/api/trade/v1;tradev1";

service TradeService {
    rpc GetTrade(GetTradeRequest) returns (TradeResponse);
    rpc ListUserTrades(ListUserTradesRequest) returns (ListUserTradesResponse);
    rpc ListMarketTrades(ListMarketTradesRequest) returns (ListMarketTradesResponse);
}

message GetTradeRequest {
    string trade_id = 1;
}

message TradeResponse {
    string trade_id     = 1;
    string market_id    = 2;
    string buyer_id     = 3;
    string seller_id    = 4;
    string buy_order_id = 5;
    string sell_order_id= 6;
    string price        = 7;  // (String)
    string quantity     = 8;  // (String)
    string executed_at  = 9;  // Matching engine execution time
}

message ListUserTradesRequest {
    string user_id = 1;
    string cursor  = 2; // Pagination keyset cursor
    int32  limit   = 3;
}

message ListUserTradesResponse {
    repeated TradeResponse trades = 1;
    string cursor                 = 2;
}

message ListMarketTradesRequest {
    string market_id = 1;
    string cursor    = 2;
    int32  limit     = 3;
}

message ListMarketTradesResponse {
    repeated TradeResponse trades = 1;
    string cursor                 = 2;
}
```

---

### 4.6 `AuthService` (`auth.proto`)
Handles registration, credentials validation, and token blacklisting. Serves REST endpoints via grpc-gateway.

```protobuf
syntax = "proto3";

package tradedrift.auth.v1;

option go_package = "github.com/tradedrift/platform/api/auth/v1;authv1";

service AuthService {
    // ==========================================
    // Public RPCs
    // ==========================================

    // Registers a new user account. Returns the generated user ID.
    rpc Register(RegisterRequest) returns (RegisterResponse);

    // Verifies the user's email using a numeric OTP code.
    rpc VerifyEmail(VerifyEmailRequest) returns (VerifyEmailResponse);

    // Resends a new email verification code (OTP) if expired or lost.
    rpc ResendVerificationCode(ResendVerificationCodeRequest) returns (ResendVerificationCodeResponse);

    // Authenticates credentials (username or email) and issues access + refresh tokens.
    rpc Login(LoginRequest) returns (LoginResponse);

    // Rotates the refresh token to yield a new token pair.
    rpc RefreshToken(RefreshTokenRequest) returns (RefreshTokenResponse);

    // ==========================================
    // Authenticated RPCs (Requires JWT Context)
    // ==========================================

    // Revokes current session tokens based on the authenticated JWT context.
    rpc Logout(LogoutRequest) returns (LogoutResponse);

    // Changes password for the authenticated user and revokes all other sessions.
    rpc ChangePassword(ChangePasswordRequest) returns (ChangePasswordResponse);
}

message RegisterRequest {
    // Required. Must be a valid email format.
    string email    = 1;
    // Required. Must be between 3 and 32 characters, alphanumeric.
    string username = 2;
    // Required. Minimum 8 characters; must contain at least one letter and one number.
    string password = 3;
}

message RegisterResponse {
    string user_id = 1;
}

message VerifyEmailRequest {
    // Required. Must match the registered email address.
    string email = 1;
    // Required. Must be exactly a 6-digit numeric OTP code.
    string code  = 2;
}

message VerifyEmailResponse {
    bool success = 1;
}

message ResendVerificationCodeRequest {
    // Required. Must be a valid email format.
    string email = 1;
}

message ResendVerificationCodeResponse {
    bool success = 1;
}

message LoginRequest {
    // Required. Can be the registered email or username.
    string identifier = 1;
    // Required. User password.
    string password   = 2;
}

message LoginResponse {
    string user_id       = 1;
    string username      = 2;
    string email         = 3;
    string access_token  = 4;
    string refresh_token = 5;
}

message RefreshTokenRequest {
    // Required. Secure, random 64-character opaque refresh token string.
    string refresh_token = 1;
}

message RefreshTokenResponse {
    string access_token  = 1;
    string refresh_token = 2;
}

// Empty body since session is identified by the authenticated access token
message LogoutRequest {}

message LogoutResponse {
    bool success = 1;
}

message ChangePasswordRequest {
    // Required. Currently active password.
    string old_password = 1;
    // Required. New password. Same complexity rules as registration (min 8 chars, letter + number).
    string new_password = 2;
}

message ChangePasswordResponse {
    bool success = 1;
}
```

---

### 4.7 `UserService` (`user.proto`)
Manages user profile records, retrieval, and settings updates. Serves REST endpoints via grpc-gateway.

```protobuf
syntax = "proto3";

package tradedrift.user.v1;

option go_package = "github.com/tradedrift/platform/api/user/v1;userv1";

service UserService {
    // Retrieves user profile metadata details.
    rpc GetProfile(GetProfileRequest) returns (ProfileResponse);

    // Updates user profile parameters.
    rpc UpdateProfile(UpdateProfileRequest) returns (ProfileResponse);
}

message GetProfileRequest {
    string user_id = 1;
}

message ProfileResponse {
    string user_id    = 1;
    string email      = 2;
    string username   = 3;
    string created_at = 4;
}

message UpdateProfileRequest {
    string user_id  = 1;
    string username = 2;
}
```

---

### 4.8 `PortfolioService` (`portfolio.proto`)
Serves read-only portfolio holdings summaries. Serves REST endpoints via grpc-gateway.

```protobuf
syntax = "proto3";

package tradedrift.portfolio.v1;

option go_package = "github.com/tradedrift/platform/api/portfolio/v1;portfoliov1";

service PortfolioService {
    // Computes aggregate PnL and cash valuation on demand.
    rpc GetPortfolioSummary(GetPortfolioSummaryRequest) returns (PortfolioSummaryResponse);

    // Returns user's holding assets list.
    rpc GetPortfolioHoldings(GetPortfolioHoldingsRequest) returns (PortfolioHoldingsResponse);
}

message GetPortfolioSummaryRequest {
    string user_id = 1;
}

message PortfolioSummaryResponse {
    string user_id        = 1;
    string total_value    = 2;  // USDT cash balance + current assets value (String)
    string realized_pnl   = 3;  // Total realized PnL (String)
    string unrealized_pnl = 4;  // Total unrealized PnL based on current market (String)
    string cash_balance   = 5;  // Dynamically pulled from Wallet USDT (String)
    string updated_at     = 6;
}

message GetPortfolioHoldingsRequest {
    string user_id = 1;
}

message HoldingDetail {
    string asset               = 1; // e.g. "BTC"
    string total_quantity      = 2; // (String)
    string average_entry_price = 3; // (String)
    string current_price       = 4; // Fetched from Redis ticker (String)
    string unrealized_pnl      = 5; // (String)
}

message PortfolioHoldingsResponse {
    string user_id                  = 1;
    repeated HoldingDetail holdings = 2;
}
```

---

## 5. API-less Workers and Stream Subscriptions

The following components are designed as asynchronous microservices or streaming layers, exposing no direct gRPC interfaces for other services:

### 5.1 Matching Engine (`matching-engine`)
* **Role:** In-memory order book execution loop.
* **gRPC Ingestion:** Exposes no gRPC endpoints. It consumes `orders.created.v1` and `orders.cancel-requested.v1` from Kafka.
* **gRPC Calls:** Queries `MarketService` via gRPC on startup to sync trading parameters (`tick_size`, `lot_size`).

### 5.2 Settlement Service (`settlement-service`)
* **Role:** Order match transaction broker.
* **gRPC Ingestion:** Exposes no gRPC endpoints. Consumes `trades.executed.v1` events from Kafka.
* **gRPC Calls:** Executes synchronous ledger balance transfers by calling `WalletService.SettleTrade()` via gRPC.

### 5.3 Notification Service (`notification-service`)
* **Role:** High-throughput WebSocket push backplane.
* **gRPC Ingestion:** Exposes no gRPC endpoints. Internal services broadcast notifications by writing directly to shared Redis Pub/Sub channels (e.g., `user:notifications:{user_id}`).
* **WebSocket Feeds:** Feeds public streams (`market:trades:*`, `market:orderbook:*`) and private user streams (`user:portfolio:*`, `user:notifications:*`) directly to guest and authenticated browser clients.

---

## 6. Service Invariants

- **GCO-1 (Numeric Typing Invariant):** All fields representing financial asset quantities, balances, prices, or decimals must be typed as `string` in Protobuf files. SerDe handlers must parse them using arbitrary-precision decimal formats.
- **GCO-2 (Client-Side Deadline Enforcement):** Downstream client stubs must construct context blocks with `context.WithTimeout` limited to 2,000ms for internal RPCs.
- **GCO-3 (Idempotent Method Safety):** Replay execution triggers on `ReserveFunds`, `ReleaseFunds`, `SettleTrade`, and `InitializeWallet` must return success statuses without creating new transactional side-effects or logging errors.
