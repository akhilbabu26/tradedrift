package ws

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512 * 1024 // 512 KB
	sendBufferSize = 256
	maxSubLimit    = 50
)

// Client represents a single connected WebSocket client session.
type Client struct {
	hub      *Hub
	conn     *websocket.Conn
	send     chan []byte
	userID   string // authenticated user ID (empty for anonymous)
	logger   *zap.Logger
	subsMu   sync.RWMutex
	subs     map[string]bool
	closed   bool
	closedMu sync.Mutex
}

// NewClient creates a new Client instance.
func NewClient(hub *Hub, conn *websocket.Conn, userID string, logger *zap.Logger) *Client {
	return &Client{
		hub:    hub,
		conn:   conn,
		send:   make(chan []byte, sendBufferSize),
		userID: userID,
		logger: logger,
		subs:   make(map[string]bool),
	}
}

// UserID returns the authenticated user ID of this connection.
func (c *Client) UserID() string {
	return c.userID
}

// HasSubscription checks if the client is subscribed to a stream.
func (c *Client) HasSubscription(stream string) bool {
	c.subsMu.RLock()
	defer c.subsMu.RUnlock()
	return c.subs[stream]
}

// AddSubscription registers a stream subscription idempotently.
// Returns (added bool, allowed bool):
//   - added=true means the subscription was newly registered (not a duplicate).
//   - added=false, allowed=true means the client was already subscribed (no-op, still OK).
//   - added=false, allowed=false means the subscription limit was exceeded.
//
// Bug Fix #2: AddSubscription is now idempotent.
// Previously, subscribing to the same stream twice would return true both times,
// causing Hub.marketSubs[target] to be incremented twice and then only
// decremented once on unsubscribe, leaving a ghost subscription entry.
func (c *Client) AddSubscription(stream string) (added bool, allowed bool) {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()

	// Already subscribed — idempotent, no change
	if c.subs[stream] {
		return false, true
	}

	// Cap at max 50 streams per socket
	if len(c.subs) >= maxSubLimit {
		return false, false
	}

	c.subs[stream] = true
	return true, true
}

// RemoveSubscription unregisters a stream subscription.
func (c *Client) RemoveSubscription(stream string) {
	c.subsMu.Lock()
	defer c.subsMu.Unlock()
	delete(c.subs, stream)
}

// Subscriptions returns a copy of all active subscriptions for this client.
func (c *Client) Subscriptions() []string {
	c.subsMu.RLock()
	defer c.subsMu.RUnlock()
	list := make([]string, 0, len(c.subs))
	for s := range c.subs {
		list = append(list, s)
	}
	return list
}

// Send enqueues a message for delivery, strictly enforcing the Stream-Specific Backpressure Matrix.
func (c *Client) Send(streamType string, msg []byte) bool {
	c.closedMu.Lock()
	if c.closed {
		c.closedMu.Unlock()
		return false
	}
	c.closedMu.Unlock()

	select {
	case c.send <- msg:
		return true
	default:
		// Channel buffer (256) is full: apply backpressure policy based on stream type
		switch streamType {
		case StreamTypeOrderBook, StreamTypeTicker:
			// Non-critical high-frequency updates: drop stale snapshot
			c.hub.RecordMessageDropped()
			c.logger.Debug("Dropping stale snapshot due to client backpressure",
				zap.String("stream_type", streamType),
				zap.String("user_id", c.userID),
			)
			return false

		case StreamTypeTrades, StreamTypeNotification:
			// Critical execution feed / private notification: DO NOT silently drop.
			// Disconnect the slow client to maintain stream integrity.
			c.hub.RecordSlowClientDisconnect()
			c.logger.Warn("Disconnecting slow client to prevent message loss on execution stream",
				zap.String("stream_type", streamType),
				zap.String("user_id", c.userID),
			)
			c.Close()
			return false

		default:
			c.hub.RecordMessageDropped()
			return false
		}
	}
}

// Close gracefully closes the client connection.
func (c *Client) Close() {
	c.closedMu.Lock()
	if c.closed {
		c.closedMu.Unlock()
		return
	}
	c.closed = true
	c.closedMu.Unlock()

	if c.conn != nil {
		_ = c.conn.Close()
	}
	c.hub.Unregister(c)
}

// ReadPump listens for incoming frames from the WebSocket connection.
func (c *Client) ReadPump() {
	defer c.Close()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))

	// RFC 6455 Pong frames (sent by browsers in response to server Pings) refresh the deadline.
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure, websocket.CloseNormalClosure) {
				c.logger.Debug("WebSocket read error", zap.Error(err))
			}
			break
		}

		var frame InboundFrame
		if err := json.Unmarshal(message, &frame); err != nil {
			c.SendControlError("MALFORMED_FRAME", "invalid json payload")
			continue
		}

		c.hub.HandleClientFrame(c, frame)
	}
}

// WritePump writes queued outbound messages to the WebSocket connection.
//
// Bug Fix #1: Each message in the c.send queue is written as a SEPARATE
// WebSocket frame using WriteMessage(TextMessage, ...).
//
// Previously, the code used NextWriter + batched multiple JSON objects
// separated by '\n' into ONE frame:
//
//	w, _ := conn.NextWriter(TextMessage)
//	w.Write(msg1)
//	w.Write('\n')
//	w.Write(msg2)   ← ALL inside one WebSocket frame
//	w.Close()
//
// This meant the browser received one WebSocket frame with:
//
//	{"stream":"market:ticker:BTC-USDT",...}\n{"stream":"market:orderbook:BTC-USDT",...}
//
// and JSON.parse(event.data) would throw because that is NOT valid JSON.
//
// Now each item from c.send becomes exactly one WriteMessage call → one WebSocket frame.
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				return
			}
			if !ok {
				// Channel was closed: send RFC 6455 Close frame
				_ = c.conn.WriteMessage(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
				)
				return
			}

			// Write one JSON object = one WebSocket text frame. Never batch.
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			// Send RFC 6455 Ping frame; browser replies with a Pong frame which
			// refreshes the read deadline in the PongHandler above.
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				return
			}
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// SendControlError sends a structured error frame to the client.
func (c *Client) SendControlError(code, message string) {
	evt := OutboundEvent{
		Event:   "error",
		Code:    code,
		Message: message,
	}
	bytes, _ := json.Marshal(evt)
	c.Send(StreamTypeControl, bytes)
}

// SendPong sends an application-level pong response frame.
func (c *Client) SendPong() {
	evt := OutboundEvent{Event: "pong"}
	bytes, _ := json.Marshal(evt)
	c.Send(StreamTypeControl, bytes)
}

// RefreshReadDeadline resets the read deadline.
// Called on application-level "ping" so a client that only sends JSON pings
// (not RFC 6455 Pong frames) does not time out after 60s.
//
// Bug Fix #4: Application-level {"event":"ping"} now also refreshes the deadline.
// Previously only RFC 6455 Pong frames (SetPongHandler) refreshed it, meaning
// a client sending JSON pings every 30s could still be disconnected at 60s.
func (c *Client) RefreshReadDeadline() {
	if c.conn == nil {
		return
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
}
