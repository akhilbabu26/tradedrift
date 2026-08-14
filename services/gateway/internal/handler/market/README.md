# Gateway Handler — Market (`internal/handler/market`)

> **Package:** `tradedrift/services/gateway/internal/handler/market`  
> **Directory:** `services/gateway/internal/handler/market/`  
> **Role:** Public HTTP endpoints for trading pair specifications, live 24-hour ticker statistics, and OHLC candlestick chart data.

---

## 1. Purpose

The `market` handler provides read-only market data to external clients (trading interfaces, chart widgets, public bots). It communicates with the **Market Microservice** (`services/market`) via gRPC and maps domain/time-series data into standard JSON payloads.

---

## 2. Files in this Directory

| File | Purpose |
| :--- | :--- |
| [`handler.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/market/handler.go) | HTTP request handlers for market catalogs, single-market lookup, 24h ticker, and historical candles. |
| [`dto.go`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/services/gateway/internal/handler/market/dto.go) | JSON Data Transfer Objects (`MarketDTO`, `Ticker24hDTO`, `CandleDTO`) and Protobuf converter functions. |

---

## 3. Endpoints, Functions & Protection Level

| HTTP Route | Handler Function | Auth Level | Why Protected or Public? |
| :--- | :--- | :--- | :--- |
| `GET /api/v1/markets` | `ListMarkets` | **Public** | Public catalog: Anyone can see which trading pairs (e.g. `BTC-USDT`) exist, along with their `tick_size` and `lot_size`. |
| `GET /api/v1/markets/{id}` | `GetMarket` | **Public** | Detailed trading rules for a single currency pair. |
| `GET /api/v1/markets/{id}/ticker` | `GetTicker` | **Public** | Real-time market stats (last price, 24h high/low, volume, 24h percentage change). Displayed on public market overview pages. |
| `GET /api/v1/markets/{id}/candles` | `GetCandles` | **Public** | Historical time-series bars (Open, High, Low, Close, Volume) used by TradingView and chart engines. |

---

## 4. Query Parameters & Validations for `GetCandles`

* **`resolution` (required):** Supported resolutions: `1m`, `5m`, `15m`, `1h`, `1d`.
* **`limit` (optional):** Integer between `1` and `500` (defaults to `100` if omitted or `0`).
* **`from` / `to` (optional):** ISO8601 timestamps (e.g. `2026-08-14T00:00:00Z`).

---

## 5. Middlewares Used & Rationale

1. **`RateLimiter` (Global):**
   * **Why:** High-frequency chart polling and ticker refreshes from hundreds of concurrent visitors could generate substantial traffic. Rate limiting prevents database connection exhaustion.
2. **`CORS` (Global):**
   * **Why:** Allows external web interfaces to render live charting data without browser Cross-Origin blocks.
3. **`RequestID` & `Logger` (Global):**
   * **Why:** Tracks latency metrics across time-series queries.

---

## 6. Tools & Libraries Used

* **`google.golang.org/grpc`**: Downstream RPC communication with Market Service on port `:50054`.
* **`tradedrift/platform/api/gen/market/v1`**: Compiled protobuf interfaces.
* **`google.golang.org/protobuf/types/known/timestamppb`**: Timestamp conversions.
