package ws

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"
)

// SnapshotProvider fetches immediate depth and ticker snapshots upon client subscription.
type SnapshotProvider interface {
	GetImmediateOrderBook(marketID string) (*OrderBookDepthPayload, error)
	GetImmediateTicker(marketID string) (*TickerPayload, error)
}

// Hub manages WebSocket client connections, channel routing, and broadcast distribution.
type Hub struct {
	clients          map[*Client]bool
	subs             map[string]map[*Client]bool // stream -> set of clients
	marketSubs       map[string]int              // marketID -> count of active subscribers
	mu               sync.RWMutex
	logger           *zap.Logger
	snapshotProvider SnapshotProvider

	// Prometheus / Internal Observability Counters
	activeConnections    int64
	messagesSentTotal    int64
	messagesDroppedTotal int64
	slowClientsTotal     int64
}

// NewHub creates a new Hub instance.
func NewHub(logger *zap.Logger, provider SnapshotProvider) *Hub {
	return &Hub{
		clients:          make(map[*Client]bool),
		subs:             make(map[string]map[*Client]bool),
		marketSubs:       make(map[string]int),
		logger:           logger,
		snapshotProvider: provider,
	}
}

// Register adds a new client to the hub.
func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = true
	atomic.AddInt64(&h.activeConnections, 1)
	h.logger.Debug("Client registered", zap.String("user_id", c.UserID()))
}

// Unregister removes a client and cleans up all its active subscriptions.
func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.clients[c] {
		return
	}
	delete(h.clients, c)
	atomic.AddInt64(&h.activeConnections, -1)

	// Clean up all channel subscriptions for this client
	for _, stream := range c.Subscriptions() {
		h.removeSubscriptionLocked(c, stream)
	}

	h.logger.Debug("Client unregistered", zap.String("user_id", c.UserID()))
}

// HandleClientFrame processes an inbound client event.
func (h *Hub) HandleClientFrame(c *Client, frame InboundFrame) {
	switch frame.Event {
	case "ping":
		// Bug Fix #4: Application-level ping also refreshes the 60s read deadline.
		// Previously only RFC 6455 Pong frames (SetPongHandler) refreshed it.
		c.RefreshReadDeadline()
		c.SendPong()

	case "subscribe":
		for _, stream := range frame.Streams {
			h.handleSubscribe(c, stream)
		}

	case "unsubscribe":
		for _, stream := range frame.Streams {
			h.handleUnsubscribe(c, stream)
		}

	default:
		c.SendControlError("UNKNOWN_EVENT", "unrecognized event action")
	}
}

// handleSubscribe executes the subscription validation and registration pipeline.
func (h *Hub) handleSubscribe(c *Client, stream string) {
	stream = strings.TrimSpace(stream)
	if stream == "" {
		c.SendControlError("INVALID_STREAM", "stream name cannot be empty")
		return
	}

	// Bug Fix #6: Use strict validation — rejects malformed patterns like
	// "user:abc:123", "market:foo:BTC-USDT", "market:orderbook:", etc.
	streamType, target, ok := ValidateStream(stream)
	if !ok {
		c.SendControlError("INVALID_STREAM", "unrecognized or malformed stream: "+stream)
		return
	}

	// Authentication & Authorization Guard for Private Streams
	if streamType == StreamTypeNotification {
		if c.UserID() == "" {
			c.SendControlError("UNAUTHORIZED", "authentication required for private notifications")
			return
		}
		if c.UserID() != target {
			c.SendControlError("FORBIDDEN", "cannot subscribe to other user's notification stream")
			h.logger.Warn("Unauthorized private subscription attempt rejected",
				zap.String("client_user_id", c.UserID()),
				zap.String("target_user_id", target),
			)
			return
		}
	}

	// Bug Fix #2: AddSubscription is now idempotent — returns (added, allowed).
	// If the client already subscribed to this stream, skip hub registration to
	// prevent double-incrementing marketSubs[target].
	added, allowed := c.AddSubscription(stream)
	if !allowed {
		c.SendControlError("SUBSCRIPTION_LIMIT_EXCEEDED", "max 50 active subscriptions allowed per connection")
		return
	}
	if !added {
		// Client already subscribed — no state change needed, still send snapshot.
		h.deliverImmediateSnapshot(c, streamType, target)
		return
	}

	// Bug Fix #3: Hub registration (marketSubs increment) BEFORE snapshot delivery.
	// Previously: snapshot was fetched & sent, THEN the client was registered.
	// Race: a Broadcast() happening between snapshot and registration would be missed.
	// Now: client is in the subscriber set before snapshot starts, guaranteeing no gap.
	h.mu.Lock()
	if _, ok := h.subs[stream]; !ok {
		h.subs[stream] = make(map[*Client]bool)
	}
	h.subs[stream][c] = true

	// Increment market subscriber counter for on-demand polling
	if target != "" && (streamType == StreamTypeOrderBook || streamType == StreamTypeTrades || streamType == StreamTypeTicker) {
		h.marketSubs[target]++
	}
	h.mu.Unlock()

	// Deliver immediate snapshot AFTER hub registration
	h.deliverImmediateSnapshot(c, streamType, target)

	h.logger.Debug("Client subscribed to stream",
		zap.String("stream", stream),
		zap.String("user_id", c.UserID()),
	)
}

// deliverImmediateSnapshot dispatches initial depth or ticker snapshot upon subscription.
func (h *Hub) deliverImmediateSnapshot(c *Client, streamType, marketID string) {
	if h.snapshotProvider == nil || marketID == "" {
		return
	}

	switch streamType {
	case StreamTypeOrderBook:
		snapshot, err := h.snapshotProvider.GetImmediateOrderBook(marketID)
		if err != nil {
			// Bug Fix #8: Inform the client when market data is unavailable
			// instead of silently doing nothing.
			c.SendControlError("MARKET_DATA_UNAVAILABLE", "order book data temporarily unavailable for "+marketID)
			return
		}
		if snapshot != nil {
			env := OutboundEnvelope{
				Stream: "market:orderbook:" + marketID,
				Data:   snapshot,
			}
			if bytes, err := json.Marshal(env); err == nil {
				c.Send(StreamTypeOrderBook, bytes)
			}
		}

	case StreamTypeTicker:
		ticker, err := h.snapshotProvider.GetImmediateTicker(marketID)
		if err != nil {
			// Bug Fix #8: Inform the client when ticker data is unavailable.
			c.SendControlError("MARKET_DATA_UNAVAILABLE", "ticker data temporarily unavailable for "+marketID)
			return
		}
		if ticker != nil {
			env := OutboundEnvelope{
				Stream: "market:ticker:" + marketID,
				Data:   ticker,
			}
			if bytes, err := json.Marshal(env); err == nil {
				c.Send(StreamTypeTicker, bytes)
			}
		}
	}
}

// handleUnsubscribe unregisters a stream subscription for a client.
func (h *Hub) handleUnsubscribe(c *Client, stream string) {
	c.RemoveSubscription(stream)

	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeSubscriptionLocked(c, stream)
}

func (h *Hub) removeSubscriptionLocked(c *Client, stream string) {
	clients, ok := h.subs[stream]
	if !ok {
		return
	}
	delete(clients, c)

	// Clean up market subscriber count
	streamType, target, _ := parseStreamStrict(stream)
	if target != "" && (streamType == StreamTypeOrderBook || streamType == StreamTypeTrades || streamType == StreamTypeTicker) {
		if count := h.marketSubs[target]; count > 0 {
			h.marketSubs[target]--
			if h.marketSubs[target] <= 0 {
				delete(h.marketSubs, target)
			}
		}
	}

	if len(clients) == 0 {
		delete(h.subs, stream)
	}
}

// Broadcast dispatches a message to all active subscribers of a channel.
func (h *Hub) Broadcast(channel string, payload []byte, streamType string) {
	h.mu.RLock()
	clients := make([]*Client, 0, len(h.subs[channel]))
	for c := range h.subs[channel] {
		clients = append(clients, c)
	}
	h.mu.RUnlock()

	for _, c := range clients {
		if c.Send(streamType, payload) {
			atomic.AddInt64(&h.messagesSentTotal, 1)
		}
	}
}

// GetActiveMarketIDs returns all markets that currently have at least one active subscriber.
func (h *Hub) GetActiveMarketIDs() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	markets := make([]string, 0, len(h.marketSubs))
	for mkt := range h.marketSubs {
		markets = append(markets, mkt)
	}
	return markets
}

// HasSubscribers checks if a channel currently has at least 1 listener.
func (h *Hub) HasSubscribers(channel string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs[channel]) > 0
}

// Observability Helpers
func (h *Hub) RecordMessageDropped() {
	atomic.AddInt64(&h.messagesDroppedTotal, 1)
}

func (h *Hub) RecordSlowClientDisconnect() {
	atomic.AddInt64(&h.slowClientsTotal, 1)
}

func (h *Hub) ActiveConnections() int64 {
	return atomic.LoadInt64(&h.activeConnections)
}

func (h *Hub) MessagesSentTotal() int64 {
	return atomic.LoadInt64(&h.messagesSentTotal)
}

func (h *Hub) MessagesDroppedTotal() int64 {
	return atomic.LoadInt64(&h.messagesDroppedTotal)
}

func (h *Hub) SlowClientsTotal() int64 {
	return atomic.LoadInt64(&h.slowClientsTotal)
}
