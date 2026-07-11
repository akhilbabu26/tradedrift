# TradeDrift — WebSocket API

> **Status:** ✅ Frozen (V1.0)
> **Document:** 09_WebSocket_API.md
> **Directory:** docs/06_APIs/
> **Last Updated:** July 2026

---

## 1. Connection & Authentication

* **URL Path:** `/ws`
* **Protocol:** HTTP/1.1 WebSocket Upgrade.
* **Authentication Handshake:**
  - Authenticated connections must pass the JWT token in query parameters: `/ws?token=eyJhbGciOi...`
  - Unauthenticated connections are allowed but only receive access to public channels.
* **Ping / Pong Heartbeat:**
  - Client must send `{"event": "ping"}` frame every 30 seconds.
  - Server responds with `{"event": "pong"}` frame to keep connection alive.

---

## 2. Subscription Commands

Clients join and leave channels by writing text frames.

### 2.1 Subscribe Command
```json
{
  "event": "subscribe",
  "streams": [
    "market:ticker:BTC-USDT",
    "market:orderbook:BTC-USDT",
    "user:portfolio:018f673a"
  ]
}
```

### 2.2 Unsubscribe Command
```json
{
  "event": "unsubscribe",
  "streams": [
    "market:orderbook:BTC-USDT"
  ]
}
```

---

## 3. Streaming Event Formats

### 3.1 Market Ticker Stream (`market:ticker:{market_id}`)
Pushed whenever statistical OHLC daily boundaries alter.
```json
{
  "stream": "market:ticker:BTC-USDT",
  "data": {
    "marketId": "BTC-USDT",
    "open": "64850.0000000000",
    "high": "66200.0000000000",
    "low": "64200.0000000000",
    "close": "65120.0000000000",
    "volume": "120.4500000000",
    "timestamp": "2026-07-11T13:00:00Z"
  }
}
```

### 3.2 L2 Orderbook Stream (`market:orderbook:{market_id}`)
Pushed at a 250ms polling interval.
```json
{
  "stream": "market:orderbook:BTC-USDT",
  "data": {
    "marketId": "BTC-USDT",
    "bids": [
      ["65100.0000000000", "0.0540000000"],
      ["65090.0000000000", "0.1200000000"]
    ],
    "asks": [
      ["65120.0000000000", "0.0120000000"],
      ["65130.0000000000", "0.2450000000"]
    ],
    "timestamp": "2026-07-11T13:00:05Z"
  }
}
```

### 3.3 Public Executed Trades Stream (`market:trades:{market_id}`)
Pushed instantly when matching execution executes.
```json
{
  "stream": "market:trades:BTC-USDT",
  "data": {
    "tradeId": "018f6745-77b1-7f33-8a1a-c3fde0aa8a22",
    "price": "65110.0000000000",
    "quantity": "0.0040000000",
    "executedAt": "2026-07-11T12:59:50Z"
  }
}
```

### 3.4 Private Inbox Notifications Stream (`user:notifications:{user_id}`)
Pushed when trade fills or deposits credit.
```json
{
  "stream": "user:notifications:018f673a-4e2b-7f11-80a2-c3bfde34aa5a",
  "data": {
    "id": "018f6749-11ba-7f44-8a1a-fdecba088220",
    "title": "Trade Executed",
    "message": "Your BUY order of 0.0050 BTC on BTC-USDT filled at 65120.00",
    "type": "TRADE_FILL",
    "createdAt": "2026-07-11T13:00:10Z"
  }
}
```
