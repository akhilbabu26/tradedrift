package hub

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"tradedrift/services/gateway/internal/ws/protocol"
)

const (
	writeWait              = 10 * time.Second
	pongWait               = 60 * time.Second
	pingPeriod             = (pongWait * 9) / 10
	maxMessageSize         = 512 * 1024 // 512 KB max inbound message size
	maxOutboundMessageSize = 512 * 1024 // 512 KB max outbound message size
	sendBufferSize         = 256
	inboundRateLimit       = 100 // max frames per second
	inboundBurstLimit      = 150
)

// Client represents a single connected WebSocket client session.
type Client struct {
	hub      *Hub
	conn     *websocket.Conn
	send     chan []byte
	userID   string // authenticated user ID (empty for anonymous)
	logger   *zap.Logger
	closed   bool
	closedMu sync.Mutex

	// Inbound rate limiter tokens
	tokens     float64
	lastToken  time.Time
	tokenMu    sync.Mutex
}

// NewClient creates a new Client instance.
func NewClient(hub *Hub, conn *websocket.Conn, userID string, logger *zap.Logger) *Client {
	return &Client{
		hub:       hub,
		conn:      conn,
		send:      make(chan []byte, sendBufferSize),
		userID:    userID,
		logger:    logger,
		tokens:    float64(inboundBurstLimit),
		lastToken: time.Now(),
	}
}

// NewTestClient creates a Client initialized with an internal buffer for unit testing.
func NewTestClient(hub *Hub, userID string, logger *zap.Logger) *Client {
	return &Client{
		hub:       hub,
		send:      make(chan []byte, sendBufferSize),
		userID:    userID,
		logger:    logger,
		tokens:    float64(inboundBurstLimit),
		lastToken: time.Now(),
	}
}

// SendChan returns the send channel for test inspections.
func (c *Client) SendChan() <-chan []byte {
	return c.send
}

// UserID returns the authenticated user ID of this connection.
func (c *Client) UserID() string {
	return c.userID
}

// HasSubscription checks if the client is subscribed to a stream via Hub authority.
func (c *Client) HasSubscription(stream string) bool {
	return c.hub.HasClientSubscription(c, stream)
}

// Subscriptions returns a copy of all active subscriptions for this client.
func (c *Client) Subscriptions() []string {
	return c.hub.GetClientSubscriptions(c)
}

// AllowInboundFrame checks per-connection token bucket rate limiter.
func (c *Client) AllowInboundFrame() bool {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	now := time.Now()
	elapsed := now.Sub(c.lastToken).Seconds()
	c.lastToken = now

	c.tokens += elapsed * inboundRateLimit
	if c.tokens > inboundBurstLimit {
		c.tokens = inboundBurstLimit
	}

	if c.tokens >= 1.0 {
		c.tokens -= 1.0
		return true
	}
	return false
}

// Send enqueues a message for delivery, strictly enforcing the Stream-Specific Backpressure Matrix
// and Outbound Message Size limit.
func (c *Client) Send(streamType string, msg []byte) bool {
	// Guard against oversized outbound payloads
	if len(msg) > maxOutboundMessageSize {
		c.logger.Warn("Dropping oversized outbound message",
			zap.Int("size", len(msg)),
			zap.Int("max", maxOutboundMessageSize),
		)
		return false
	}

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
		switch streamType {
		case protocol.StreamTypeOrderBook, protocol.StreamTypeTicker:
			c.hub.RecordMessageDropped()
			c.logger.Debug("Dropping stale snapshot due to client backpressure",
				zap.String("stream_type", streamType),
				zap.String("user_id", c.userID),
			)
			return false

		case protocol.StreamTypeTrades, protocol.StreamTypeNotification:
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

		if !c.AllowInboundFrame() {
			c.SendControlError("RATE_LIMIT_EXCEEDED", "too many messages sent")
			continue
		}

		var frame protocol.InboundFrame
		if err := json.Unmarshal(message, &frame); err != nil {
			c.SendControlError("MALFORMED_FRAME", "invalid json payload")
			continue
		}

		c.hub.HandleClientFrame(c, frame)
	}
}

// WritePump writes queued outbound messages to the WebSocket connection.
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
				_ = c.conn.WriteMessage(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
				)
				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
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
	evt := protocol.OutboundEvent{
		Event:   "error",
		Code:    code,
		Message: message,
	}
	bytes, _ := json.Marshal(evt)
	c.Send(protocol.StreamTypeControl, bytes)
}

// SendPong sends an application-level pong response frame.
func (c *Client) SendPong() {
	evt := protocol.OutboundEvent{Event: "pong"}
	bytes, _ := json.Marshal(evt)
	c.Send(protocol.StreamTypeControl, bytes)
}

// RefreshReadDeadline resets the read deadline.
func (c *Client) RefreshReadDeadline() {
	if c.conn == nil {
		return
	}
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
}
