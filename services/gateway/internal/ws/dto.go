package ws

import (
	"encoding/json"
	"strings"
)

// ─── Client Inbound Frames ───────────────────────────────────────────────────

// InboundFrame is the base envelope for all client requests.
type InboundFrame struct {
	Event   string   `json:"event"`             // "subscribe", "unsubscribe", "ping", "authenticate"
	Streams []string `json:"streams,omitempty"` // e.g. ["market:orderbook:BTC-USDT"]
	Token   string   `json:"token,omitempty"`   // for inline auth if provided
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
type OrderBookDepthPayload struct {
	MarketID  string      `json:"marketId"`
	Bids      [][2]string `json:"bids"` // [[price, qty], [price, qty]...]
	Asks      [][2]string `json:"asks"` // [[price, qty], [price, qty]...]
	Sequence  uint64      `json:"sequence"`  // monotonically increasing; frontend detects gaps
	Timestamp int64       `json:"timestamp"` // Unix milliseconds
}

// TradePayload represents a public executed trade fill.
type TradePayload struct {
	TradeID      string `json:"tradeId"`
	MarketID     string `json:"marketId"`
	Price        string `json:"price"`
	Quantity     string `json:"quantity"`
	Side         string `json:"side,omitempty"` // "BUY" or "SELL"
	BuyerUserID  string `json:"buyerUserId,omitempty"`
	SellerUserID string `json:"sellerUserId,omitempty"`
	Sequence     uint64 `json:"sequence"`  // monotonically increasing
	ExecutedAt   int64  `json:"executedAt"` // Unix milliseconds
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

// validStream is the strict allowlist of accepted stream patterns.
// Returns (streamType, marketOrUserID, ok).
// Bug Fix #6: Strict stream validation — rejects any pattern not exactly matching
// "market:orderbook:{id}", "market:ticker:{id}", "market:trades:{id}",
// or "user:notifications:{user_id}".
func ParseStreamType(stream string) (streamType, target string) {
	streamType, target, _ = parseStreamStrict(stream)
	return
}

// ValidateStream returns (streamType, target, ok=true) only for fully valid stream names.
func ValidateStream(stream string) (streamType, target string, ok bool) {
	return parseStreamStrict(stream)
}

func parseStreamStrict(stream string) (streamType, target string, ok bool) {
	parts := strings.SplitN(stream, ":", 3)
	if len(parts) != 3 {
		return StreamTypeControl, "", false
	}

	prefix, sub, id := parts[0], parts[1], parts[2]

	// Reject empty target IDs
	if id == "" {
		return StreamTypeControl, "", false
	}

	switch prefix {
	case "market":
		switch sub {
		case "orderbook":
			return StreamTypeOrderBook, id, true
		case "ticker":
			return StreamTypeTicker, id, true
		case "trades":
			return StreamTypeTrades, id, true
		}
	case "user":
		if sub == "notifications" {
			return StreamTypeNotification, id, true
		}
	}

	return StreamTypeControl, "", false
}

// MarshalEnvelope marshals an OutboundEnvelope into JSON bytes.
// Returns nil if marshaling fails (should never happen with valid payloads).
func MarshalEnvelope(env OutboundEnvelope) []byte {
	b, err := json.Marshal(env)
	if err != nil {
		return nil
	}
	return b
}
