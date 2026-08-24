package hub

import (
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"

	"go.uber.org/zap"

	"tradedrift/services/gateway/internal/ws/protocol"
)

const maxSubLimit = 50

// Hub manages WebSocket client connections, channel routing, and broadcast distribution.
// Implements protocol.Broadcaster.
//
// Concurrency Model:
// Hub is the single, authoritative owner of all client connection and subscription states.
// All subscription additions, removals, and client cleanups are protected by Hub.mu,
// eliminating multi-lock coordination issues.
type Hub struct {
	clients          map[*Client]bool
	clientSubs       map[*Client]map[string]bool // client -> set of subscribed streams
	subs             map[string]map[*Client]bool // stream -> set of clients
	marketSubs       map[string]int              // marketID -> count of active subscribers
	mu               sync.RWMutex
	logger           *zap.Logger
	snapshotProvider protocol.SnapshotProvider

	// Prometheus / Internal Observability Counters
	activeConnections    int64
	messagesSentTotal    int64
	messagesDroppedTotal int64
	slowClientsTotal     int64
}

// NewHub creates a new Hub instance.
func NewHub(logger *zap.Logger, provider protocol.SnapshotProvider) *Hub {
	return &Hub{
		clients:          make(map[*Client]bool),
		clientSubs:       make(map[*Client]map[string]bool),
		subs:             make(map[string]map[*Client]bool),
		marketSubs:       make(map[string]int),
		logger:           logger,
		snapshotProvider: provider,
	}
}

// Register adds a new client to the hub under Hub.mu.
func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = true
	h.clientSubs[c] = make(map[string]bool)
	atomic.AddInt64(&h.activeConnections, 1)
	h.logger.Debug("Client registered", zap.String("user_id", c.UserID()))
}

// Unregister atomically removes a client and cleans up all its active subscriptions.
func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.clients[c] {
		return
	}
	delete(h.clients, c)
	atomic.AddInt64(&h.activeConnections, -1)

	// Clean up all stream subscriptions held by this client
	if streams, ok := h.clientSubs[c]; ok {
		for stream := range streams {
			if clients, exists := h.subs[stream]; exists {
				delete(clients, c)
				if len(clients) == 0 {
					delete(h.subs, stream)
				}
			}

			streamType, target, _ := protocol.ValidateStream(stream)
			if target != "" && (streamType == protocol.StreamTypeOrderBook || streamType == protocol.StreamTypeTrades || streamType == protocol.StreamTypeTicker) {
				if h.marketSubs[target] > 0 {
					h.marketSubs[target]--
					if h.marketSubs[target] <= 0 {
						delete(h.marketSubs, target)
					}
				}
			}
		}
		delete(h.clientSubs, c)
	}

	h.logger.Debug("Client unregistered", zap.String("user_id", c.UserID()))
}

// HasClientSubscription checks if a client is subscribed to a specific stream.
func (h *Hub) HasClientSubscription(c *Client, stream string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if subs, ok := h.clientSubs[c]; ok {
		return subs[stream]
	}
	return false
}

// GetClientSubscriptions returns a copy of all active subscriptions for a client.
func (h *Hub) GetClientSubscriptions(c *Client) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	subs, ok := h.clientSubs[c]
	if !ok {
		return nil
	}
	list := make([]string, 0, len(subs))
	for s := range subs {
		list = append(list, s)
	}
	return list
}

// HandleClientFrame processes an inbound client event.
func (h *Hub) HandleClientFrame(c *Client, frame protocol.InboundFrame) {
	switch frame.Event {
	case "ping":
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

// handleSubscribe executes validation, authorization, and atomic registration under Hub.mu.
func (h *Hub) handleSubscribe(c *Client, stream string) {
	stream = strings.TrimSpace(stream)
	if stream == "" {
		c.SendControlError("INVALID_STREAM", "stream name cannot be empty")
		return
	}

	streamType, target, ok := protocol.ValidateStream(stream)
	if !ok {
		c.SendControlError("INVALID_STREAM", "unrecognized or malformed stream: "+stream)
		return
	}

	// Authorization Guard for Private Streams
	if streamType == protocol.StreamTypeNotification {
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

	// ── Atomic Subscription Registration under Hub.mu ────────────────────────
	h.mu.Lock()
	if !h.clients[c] {
		h.mu.Unlock()
		return // Client disconnected before subscription registered
	}

	cSubs := h.clientSubs[c]
	if cSubs[stream] {
		// Idempotent no-op: client is already subscribed
		h.mu.Unlock()
		return
	}

	if len(cSubs) >= maxSubLimit {
		h.mu.Unlock()
		c.SendControlError("SUBSCRIPTION_LIMIT_EXCEEDED", "max 50 active subscriptions allowed per connection")
		return
	}

	// Register subscription
	cSubs[stream] = true

	if _, ok := h.subs[stream]; !ok {
		h.subs[stream] = make(map[*Client]bool)
	}
	h.subs[stream][c] = true

	if target != "" && (streamType == protocol.StreamTypeOrderBook || streamType == protocol.StreamTypeTrades || streamType == protocol.StreamTypeTicker) {
		h.marketSubs[target]++
	}
	h.mu.Unlock()
	// ─────────────────────────────────────────────────────────────────────────

	// Deliver initial snapshot AFTER registration so no live broadcast is missed
	h.deliverImmediateSnapshot(c, streamType, target)

	h.logger.Debug("Client subscribed to stream",
		zap.String("stream", stream),
		zap.String("user_id", c.UserID()),
	)
}

// handleUnsubscribe atomically removes a stream subscription under Hub.mu.
func (h *Hub) handleUnsubscribe(c *Client, stream string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	cSubs, ok := h.clientSubs[c]
	if !ok || !cSubs[stream] {
		return // Client was not subscribed to this stream -> idempotent no-op
	}

	delete(cSubs, stream)

	if clients, exists := h.subs[stream]; exists {
		delete(clients, c)
		if len(clients) == 0 {
			delete(h.subs, stream)
		}
	}

	streamType, target, _ := protocol.ValidateStream(stream)
	if target != "" && (streamType == protocol.StreamTypeOrderBook || streamType == protocol.StreamTypeTrades || streamType == protocol.StreamTypeTicker) {
		if count := h.marketSubs[target]; count > 0 {
			h.marketSubs[target]--
			if h.marketSubs[target] <= 0 {
				delete(h.marketSubs, target)
			}
		}
	}
}

// deliverImmediateSnapshot dispatches initial depth or ticker snapshot upon subscription.
func (h *Hub) deliverImmediateSnapshot(c *Client, streamType, marketID string) {
	if h.snapshotProvider == nil || marketID == "" {
		return
	}

	switch streamType {
	case protocol.StreamTypeOrderBook:
		snapshot, err := h.snapshotProvider.GetImmediateOrderBook(marketID)
		if err != nil {
			c.SendControlError("MARKET_DATA_UNAVAILABLE", "order book data temporarily unavailable for "+marketID)
			return
		}
		if snapshot != nil {
			env := protocol.OutboundEnvelope{
				Stream: "market:orderbook:" + marketID,
				Data:   snapshot,
			}
			if bytes, err := json.Marshal(env); err == nil {
				c.Send(protocol.StreamTypeOrderBook, bytes)
			}
		}

	case protocol.StreamTypeTicker:
		ticker, err := h.snapshotProvider.GetImmediateTicker(marketID)
		if err != nil {
			c.SendControlError("MARKET_DATA_UNAVAILABLE", "ticker data temporarily unavailable for "+marketID)
			return
		}
		if ticker != nil {
			env := protocol.OutboundEnvelope{
				Stream: "market:ticker:" + marketID,
				Data:   ticker,
			}
			if bytes, err := json.Marshal(env); err == nil {
				c.Send(protocol.StreamTypeTicker, bytes)
			}
		}
	}
}

// Broadcast dispatches a message to all active subscribers of a channel.
// Implements protocol.Broadcaster.
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
// Implements protocol.Broadcaster.
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
// Implements protocol.Broadcaster.
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
