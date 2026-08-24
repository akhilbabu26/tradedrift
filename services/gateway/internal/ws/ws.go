package ws

import (
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"tradedrift/services/gateway/internal/ws/hub"
	"tradedrift/services/gateway/internal/ws/protocol"
	"tradedrift/services/gateway/internal/ws/streamer"
)

// ─── Re-exported Types & Interfaces ──────────────────────────────────────────

type (
	// Hub manages WebSocket client connections and channel routing.
	Hub = hub.Hub

	// Client represents a single connected WebSocket client session.
	Client = hub.Client

	// Handler handles HTTP requests to upgrade to WebSocket sessions.
	Handler = hub.Handler

	// Streamer coordinates Redis depth/ticker polling and Kafka trade event ingestion.
	Streamer = streamer.Streamer

	// SnapshotProvider fetches immediate depth and ticker snapshots.
	SnapshotProvider = protocol.SnapshotProvider

	// Broadcaster sends real-time market updates to connected WebSocket subscribers.
	Broadcaster = protocol.Broadcaster

	// Inbound and Outbound Frames
	InboundFrame          = protocol.InboundFrame
	OutboundEnvelope      = protocol.OutboundEnvelope
	OutboundEvent         = protocol.OutboundEvent
	OrderBookDepthPayload = protocol.OrderBookDepthPayload
	TradePayload          = protocol.TradePayload
	TickerPayload         = protocol.TickerPayload
	NotificationPayload   = protocol.NotificationPayload
)

// ─── Stream Type Constants ───────────────────────────────────────────────────

const (
	StreamTypeOrderBook    = protocol.StreamTypeOrderBook
	StreamTypeTicker       = protocol.StreamTypeTicker
	StreamTypeTrades       = protocol.StreamTypeTrades
	StreamTypeNotification = protocol.StreamTypeNotification
	StreamTypeControl      = protocol.StreamTypeControl
)

// ─── Constructors ────────────────────────────────────────────────────────────

// NewHub creates a new Hub instance.
func NewHub(logger *zap.Logger, provider SnapshotProvider) *Hub {
	return hub.NewHub(logger, provider)
}

// NewClient creates a new Client instance.
func NewClient(h *Hub, conn *websocket.Conn, userID string, logger *zap.Logger) *Client {
	return hub.NewClient(h, conn, userID, logger)
}

// NewTestClient creates a new Client for unit tests.
func NewTestClient(h *Hub, userID string, logger *zap.Logger) *Client {
	return hub.NewTestClient(h, userID, logger)
}

// NewHandler creates a new WebSocket Handler.
func NewHandler(h *Hub, jwtSecret string, corsOrigin string, logger *zap.Logger) *Handler {
	return hub.NewHandler(h, jwtSecret, corsOrigin, logger)
}

// NewStreamer creates a new Streamer instance.
func NewStreamer(
	broadcaster Broadcaster,
	redisClient *redis.Client,
	kafkaBrokers []string,
	kafkaTopic string,
	kafkaGroupID string,
	logger *zap.Logger,
) *Streamer {
	return streamer.NewStreamer(broadcaster, redisClient, kafkaBrokers, kafkaTopic, kafkaGroupID, logger)
}

// ─── Stream Validation Helpers ───────────────────────────────────────────────

// ValidateStream returns (streamType, target, ok=true) for valid stream names.
func ValidateStream(stream string) (streamType, target string, ok bool) {
	return protocol.ValidateStream(stream)
}

// ParseStreamType extracts the stream type and target from a stream path.
func ParseStreamType(stream string) (streamType, target string) {
	return protocol.ParseStreamType(stream)
}

// MarshalEnvelope marshals an OutboundEnvelope into JSON bytes.
func MarshalEnvelope(env OutboundEnvelope) []byte {
	return protocol.MarshalEnvelope(env)
}
