package protocol

// ─── Client Inbound Frames ───────────────────────────────────────────────────

// InboundFrame is the base envelope for all client-to-server messages.
// Authentication is performed exclusively during the HTTP handshake:
//
//	GET /ws?token=JWT          (query param)
//	Authorization: Bearer JWT  (header)
//
// There is no in-band "authenticate" event. A connection is either
// authenticated at upgrade time or it is anonymous.
type InboundFrame struct {
	Event   string   `json:"event"`             // "subscribe", "unsubscribe", "ping"
	Streams []string `json:"streams,omitempty"` // e.g. ["market:orderbook:BTC-USDT"]
}

// ─── Server Outbound Envelopes ──────────────────────────────────────────────

// OutboundEnvelope wraps all stream broadcasts pushed to clients.
type OutboundEnvelope struct {
	Stream string `json:"stream"`
	Data   any    `json:"data"`
}

// OutboundEvent wraps control frames (pong, error).
type OutboundEvent struct {
	Event   string `json:"event"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// ─── Stream Payloads ─────────────────────────────────────────────────────────

// OrderBookDepthPayload represents Level-2 depth data for a market.
// Sequence is the authoritative Matching Engine sequence stored with the Redis snapshot.
// The Gateway does not generate a replacement sequence. Clients MUST use the sequence
// to detect stale snapshots: if a live update arrives with seq=N and the snapshot
// arrives with seq < N, discard the snapshot and use the live state.
type OrderBookDepthPayload struct {
	MarketID  string      `json:"marketId"`
	Bids      [][2]string `json:"bids"` // [[price, qty], ...]
	Asks      [][2]string `json:"asks"` // [[price, qty], ...]
	Sequence  uint64      `json:"sequence"`  // authoritative Matching Engine sequence
	Timestamp int64       `json:"timestamp"` // Unix milliseconds
}

// TradePayload represents a public executed trade fill.
// Sequence is the authoritative Matching Engine trade sequence forwarded from Kafka.
// Privacy contract: BuyerUserID and SellerUserID are intentionally omitted from public feeds.
type TradePayload struct {
	TradeID    string `json:"tradeId"`
	MarketID   string `json:"marketId"`
	Price      string `json:"price"`
	Quantity   string `json:"quantity"`
	Side       string `json:"side,omitempty"` // "BUY" or "SELL"
	Sequence   uint64 `json:"sequence"`       // authoritative Matching Engine trade sequence
	ExecutedAt int64  `json:"executedAt"`     // Unix milliseconds
}

// TickerPayload represents 24h rolling market statistics.
type TickerPayload struct {
	MarketID              string `json:"marketId"`
	LastPrice             string `json:"lastPrice"`
	High24h               string `json:"high24h"`
	Low24h                string `json:"low24h"`
	Volume24h             string `json:"volume24h"`
	QuoteVolume24h        string `json:"quoteVolume24h"`
	PriceChange24hPercent string `json:"priceChange24hPercent"`
	Timestamp             int64  `json:"timestamp"` // Unix milliseconds
}

// NotificationPayload represents a private user alert.
type NotificationPayload struct {
	ID        string `json:"id"`
	UserID    string `json:"userId"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	Type      string `json:"type"`
	CreatedAt int64  `json:"createdAt"` // Unix milliseconds
}

// StreamType constants for classification and backpressure routing
const (
	StreamTypeOrderBook    = "orderbook"
	StreamTypeTicker       = "ticker"
	StreamTypeTrades       = "trades"
	StreamTypeNotification = "notification"
	StreamTypeControl      = "control"
)
