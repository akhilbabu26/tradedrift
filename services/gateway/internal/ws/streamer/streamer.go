package streamer

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"tradedrift/services/gateway/internal/ws/protocol"
)

// availabilityState tracks whether a market's data source is currently reachable.
type availabilityState uint8

const (
	stateAvailable   availabilityState = 0
	stateUnavailable availabilityState = 1
)

// Streamer coordinates Redis depth/ticker polling and Kafka trade event ingestion.
// Implements protocol.SnapshotProvider.
type Streamer struct {
	broadcaster  protocol.Broadcaster
	redisClient  *redis.Client
	kafkaBrokers []string
	kafkaTopic   string
	kafkaGroupID string
	logger       *zap.Logger

	// Snapshot deduplication: xxhash of last-seen Redis value per market.
	depthHashes  map[string]uint64
	tickerHashes map[string]uint64
	hashesMu     sync.Mutex

	// Per-market AVAILABLE↔UNAVAILABLE state for Redis failure notifications.
	depthAvailability  map[string]availabilityState
	tickerAvailability map[string]availabilityState
	availMu            sync.Mutex

	// Per-market trade sequence counters (Gateway assigned for Kafka events).
	tradeSeqs map[string]uint64
	seqMu     sync.Mutex

	// Kafka resilience metrics.
	kafkaErrorsTotal     int64
	kafkaReconnectsTotal int64
}

// NewStreamer creates a new Streamer instance.
func NewStreamer(
	broadcaster protocol.Broadcaster,
	redisClient *redis.Client,
	kafkaBrokers []string,
	kafkaTopic string,
	kafkaGroupID string,
	logger *zap.Logger,
) *Streamer {
	if kafkaGroupID == "" {
		kafkaGroupID = "gateway-websocket-group"
	}
	if kafkaTopic == "" {
		kafkaTopic = "trades.executed"
	}

	return &Streamer{
		broadcaster:        broadcaster,
		redisClient:        redisClient,
		kafkaBrokers:       kafkaBrokers,
		kafkaTopic:         kafkaTopic,
		kafkaGroupID:       kafkaGroupID,
		logger:             logger,
		depthHashes:        make(map[string]uint64),
		tickerHashes:       make(map[string]uint64),
		depthAvailability:  make(map[string]availabilityState),
		tickerAvailability: make(map[string]availabilityState),
		tradeSeqs:          make(map[string]uint64),
	}
}

// SetBroadcaster binds a Broadcaster (such as Hub) to the Streamer after construction.
func (s *Streamer) SetBroadcaster(b protocol.Broadcaster) {
	s.broadcaster = b
}

// SetHub is an alias for SetBroadcaster for backwards compatibility.
func (s *Streamer) SetHub(b protocol.Broadcaster) {
	s.SetBroadcaster(b)
}

// Start launches the three background goroutines. Runs until ctx is cancelled.
func (s *Streamer) Start(ctx context.Context) {
	s.logger.Info("Starting WebSocket Streamer background tasks",
		zap.Strings("kafka_brokers", s.kafkaBrokers),
		zap.String("kafka_group", s.kafkaGroupID),
		zap.String("kafka_topic", s.kafkaTopic),
	)
	go s.runDepthPoller(ctx)
	go s.runTickerPoller(ctx)
	go s.runKafkaTradeStreamer(ctx)
}

// NextTradeSeq returns the next gateway-assigned trade sequence for a market.
func (s *Streamer) NextTradeSeq(marketID string) uint64 {
	s.seqMu.Lock()
	defer s.seqMu.Unlock()
	s.tradeSeqs[marketID]++
	return s.tradeSeqs[marketID]
}

// ─── SnapshotProvider Implementation ─────────────────────────────────────────

// GetImmediateOrderBook retrieves the current Level-2 depth snapshot from Redis.
// The Sequence field strictly reflects the Matching Engine's authoritative sequence.
func (s *Streamer) GetImmediateOrderBook(marketID string) (*protocol.OrderBookDepthPayload, error) {
	if s.redisClient == nil || marketID == "" {
		return nil, errors.New("redis client not configured or empty marketID")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	val, err := s.redisClient.Get(ctx, "depth:"+marketID).Result()
	if err != nil {
		return nil, err
	}

	var raw rawRedisDepth
	if err := json.Unmarshal([]byte(val), &raw); err != nil {
		return nil, err
	}

	payload := convertDepthPayload(marketID, raw)
	payload.Sequence = raw.Sequence
	return payload, nil
}

// GetImmediateTicker retrieves the canonical 24h ticker snapshot from Redis.
func (s *Streamer) GetImmediateTicker(marketID string) (*protocol.TickerPayload, error) {
	if s.redisClient == nil || marketID == "" {
		return nil, errors.New("redis client not configured or empty marketID")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	val, err := s.redisClient.Get(ctx, "ticker:"+marketID).Result()
	if err != nil {
		return nil, err
	}

	var ticker protocol.TickerPayload
	if err := json.Unmarshal([]byte(val), &ticker); err != nil {
		return nil, err
	}
	if ticker.MarketID == "" {
		ticker.MarketID = marketID
	}
	return &ticker, nil
}

// ─── Observability ────────────────────────────────────────────────────────────

// KafkaErrorsTotal returns the total number of Kafka read errors since startup.
func (s *Streamer) KafkaErrorsTotal() int64 {
	return atomic.LoadInt64(&s.kafkaErrorsTotal)
}

// KafkaReconnectsTotal returns the total number of Kafka reconnect attempts since startup.
func (s *Streamer) KafkaReconnectsTotal() int64 {
	return atomic.LoadInt64(&s.kafkaReconnectsTotal)
}

// marshalUnavailable produces a MARKET_DATA_UNAVAILABLE control frame for broadcast.
func (s *Streamer) marshalUnavailable(stream string) []byte {
	evt := protocol.OutboundEvent{
		Event:   "error",
		Code:    "MARKET_DATA_UNAVAILABLE",
		Message: "market data temporarily unavailable for " + stream,
	}
	b, _ := json.Marshal(evt)
	return b
}
