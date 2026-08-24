# TradeDrift — WebSocket Flow Reference

> For the complete, unabridged WebSocket architectural flow and wire protocol specifications, see:
> **[`/WEBSOCKET_FLOW.md`](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/WEBSOCKET_FLOW.md)**

---

## Quick Navigation

1. **[System Topology & Connection Architecture](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/WEBSOCKET_FLOW.md#1-system-architecture--topology)**
2. **[Where & How the WebSocket Connects](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/WEBSOCKET_FLOW.md#2-where--how-the-websocket-connects)**
3. **[Data Ingestion Flow (Depth, Ticker, Trades)](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/WEBSOCKET_FLOW.md#3-how-information-is-ingested--streamed-channel-by-channel)**
4. **[Wire Protocol & Message Schemas](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/WEBSOCKET_FLOW.md#4-protocol-specification--message-schemas)**
5. **[Backpressure & Performance Guarantees](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/WEBSOCKET_FLOW.md#5-architectural-contracts--safety-guarantees)**
6. **[Frontend Reconnect & REST Resync Flow](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/WEBSOCKET_FLOW.md#54-frontend-reconnect--rest-resynchronization-protocol)**
7. **[Observability & Prometheus Metrics](file:///c:/Users/AKHIL%20BABU/OneDrive/Desktop/tradedrift/WEBSOCKET_FLOW.md#6-observability--monitoring-metrics)**
