package ws

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

// Streamer coordinates Redis depth/ticker polling and Kafka trade event ingestion.
type Streamer struct {
	hub           *Hub
	redisClient   *redis.Client
	kafkaBrokers  []string
	kafkaTopic    string
	kafkaGroupID  string
	logger        *zap.Logger
	depthHashes   map[string]uint64
	tickerHashes  map[string]uint64
	hashesMu      sync.Mutex

	// Kafka Resilience Metrics
	kafkaErrorsTotal     int64
	kafkaReconnectsTotal int64
	kafkaConsumerLag     int64
}

// NewStreamer creates a new Streamer instance.
func NewStreamer(
	hub *Hub,
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
		hub:          hub,
		redisClient:  redisClient,
		kafkaBrokers: kafkaBrokers,
		kafkaTopic:   kafkaTopic,
		kafkaGroupID: kafkaGroupID,
		logger:       logger,
		depthHashes:  make(map[string]uint64),
		tickerHashes: make(map[string]uint64),
	}
}

// SetHub binds a Hub to the Streamer after construction.
// This resolves the circular dependency between Hub and Streamer without
// constructing two Streamer instances.
//
// Bug Fix #7: main.go previously created two Streamers:
//
//	s1 := NewStreamer(nil, ...)      // used as Hub's SnapshotProvider
//	hub := NewHub(logger, s1)
//	s2 := NewStreamer(hub, ...)      // used for background streaming (s1 discarded)
//
// s1 was never started and s2 did not share s1's snapshot state. Now:
//
//	streamer := NewStreamer(nil, ...) // no hub yet
//	hub := NewHub(logger, streamer)
//	streamer.SetHub(hub)              // bind the hub
//	streamer.Start(ctx)
func (s *Streamer) SetHub(h *Hub) {
	s.hub = h
}

// ─── SnapshotProvider Implementation ─────────────────────────────────────────

// GetImmediateOrderBook retrieves the current Level-2 depth snapshot from Redis for immediate dispatch.
func (s *Streamer) GetImmediateOrderBook(marketID string) (*OrderBookDepthPayload, error) {
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

	return s.convertDepthPayload(marketID, raw), nil
}

// GetImmediateTicker retrieves the canonical 24h ticker snapshot from Redis for immediate dispatch.
func (s *Streamer) GetImmediateTicker(marketID string) (*TickerPayload, error) {
	if s.redisClient == nil || marketID == "" {
		return nil, errors.New("redis client not configured or empty marketID")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	val, err := s.redisClient.Get(ctx, "ticker:"+marketID).Result()
	if err != nil {
		return nil, err
	}

	var ticker TickerPayload
	if err := json.Unmarshal([]byte(val), &ticker); err != nil {
		return nil, err
	}
	if ticker.MarketID == "" {
		ticker.MarketID = marketID
	}
	return &ticker, nil
}

// ─── Background Poller Loops ────────────────────────────────────────────────

// Start begins background polling and Kafka ingestion. Runs until ctx is cancelled.
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

// runDepthPoller polls Redis depth for active markets every 250ms with fingerprint deduplication.
func (s *Streamer) runDepthPoller(ctx context.Context) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pollActiveMarketDepth(ctx)
		}
	}
}

func (s *Streamer) pollActiveMarketDepth(ctx context.Context) {
	if s.redisClient == nil {
		return
	}

	activeMarkets := s.hub.GetActiveMarketIDs()
	if len(activeMarkets) == 0 {
		return // On-Demand: Zero subscribers -> Zero Redis load
	}

	for _, mkt := range activeMarkets {
		channel := "market:orderbook:" + mkt
		if !s.hub.HasSubscribers(channel) {
			continue
		}

		reqCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		val, err := s.redisClient.Get(reqCtx, "depth:"+mkt).Result()
		cancel()

		if err != nil {
			if !errors.Is(err, redis.Nil) {
				s.logger.Debug("Redis depth read failed for active market",
					zap.String("market", mkt),
					zap.Error(err),
				)
			}
			continue
		}

		// Snapshot Deduplication: compute xxhash of raw JSON
		hash := xxhash.Sum64String(val)
		s.hashesMu.Lock()
		prevHash := s.depthHashes[mkt]
		s.depthHashes[mkt] = hash
		s.hashesMu.Unlock()

		if hash == prevHash {
			continue // Unchanged orderbook state -> skip broadcast
		}

		var raw rawRedisDepth
		if err := json.Unmarshal([]byte(val), &raw); err != nil {
			continue
		}

		payload := s.convertDepthPayload(mkt, raw)
		env := OutboundEnvelope{
			Stream: channel,
			Data:   payload,
		}
		bytes, err := json.Marshal(env)
		if err != nil {
			continue
		}

		s.hub.Broadcast(channel, bytes, StreamTypeOrderBook)
	}
}

// runTickerPoller polls canonical 24h ticker from Redis for active markets every 1000ms.
func (s *Streamer) runTickerPoller(ctx context.Context) {
	ticker := time.NewTicker(1000 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.pollActiveMarketTickers(ctx)
		}
	}
}

func (s *Streamer) pollActiveMarketTickers(ctx context.Context) {
	if s.redisClient == nil {
		return
	}

	activeMarkets := s.hub.GetActiveMarketIDs()
	if len(activeMarkets) == 0 {
		return
	}

	for _, mkt := range activeMarkets {
		channel := "market:ticker:" + mkt
		if !s.hub.HasSubscribers(channel) {
			continue
		}

		reqCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		val, err := s.redisClient.Get(reqCtx, "ticker:"+mkt).Result()
		cancel()

		if err != nil {
			continue
		}

		hash := xxhash.Sum64String(val)
		s.hashesMu.Lock()
		prevHash := s.tickerHashes[mkt]
		s.tickerHashes[mkt] = hash
		s.hashesMu.Unlock()

		if hash == prevHash {
			continue
		}

		var payload TickerPayload
		if err := json.Unmarshal([]byte(val), &payload); err != nil {
			continue
		}
		if payload.MarketID == "" {
			payload.MarketID = mkt
		}

		env := OutboundEnvelope{
			Stream: channel,
			Data:   payload,
		}
		bytes, err := json.Marshal(env)
		if err != nil {
			continue
		}

		s.hub.Broadcast(channel, bytes, StreamTypeTicker)
	}
}

// ─── Kafka Trade Streamer (Best-Effort Fanout & Auto-Reconnect) ──────────────

func (s *Streamer) runKafkaTradeStreamer(ctx context.Context) {
	if len(s.kafkaBrokers) == 0 {
		s.logger.Warn("No Kafka brokers configured for WebSocket Trade Streamer")
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		reader := kafka.NewReader(kafka.ReaderConfig{
			Brokers:        s.kafkaBrokers,
			GroupID:        s.kafkaGroupID,
			Topic:          s.kafkaTopic,
			MinBytes:       1,
			MaxBytes:       10e6, // 10MB
			CommitInterval: 0,    // synchronous manual commits
			MaxWait:        500 * time.Millisecond,
		})

		s.logger.Info("Connected to Kafka trades topic for WebSocket fanout",
			zap.String("topic", s.kafkaTopic),
			zap.String("group_id", s.kafkaGroupID),
		)

		err := s.consumeKafkaTrades(ctx, reader)
		_ = reader.Close()

		if err != nil && !errors.Is(err, context.Canceled) {
			atomic.AddInt64(&s.kafkaErrorsTotal, 1)
			atomic.AddInt64(&s.kafkaReconnectsTotal, 1)
			s.logger.Error("Kafka trade consumer disconnected, retrying in 3s...", zap.Error(err))
			time.Sleep(3 * time.Second)
		}
	}
}

func (s *Streamer) consumeKafkaTrades(ctx context.Context, reader *kafka.Reader) error {
	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			return err
		}

		// Parse TradeExecuted message
		var event rawTradeEvent
		if err := json.Unmarshal(msg.Value, &event); err != nil {
			s.logger.Warn("Malformed trade event from Kafka", zap.Error(err))
			_ = reader.CommitMessages(ctx, msg)
			continue
		}

		// Best-effort fan-out to active channel subscribers
		channel := "market:trades:" + event.MarketID
		if s.hub.HasSubscribers(channel) {
			execMs := event.ExecutedAt.UnixMilli()
			if execMs <= 0 {
				execMs = time.Now().UnixMilli()
			}
			tradePayload := TradePayload{
				TradeID:      event.TradeID,
				MarketID:     event.MarketID,
				Price:        event.Price,
				Quantity:     event.Quantity,
				BuyerUserID:  event.BuyerUserID,
				SellerUserID: event.SellerUserID,
				ExecutedAt:   execMs,
			}
			env := OutboundEnvelope{
				Stream: channel,
				Data:   tradePayload,
			}
			if bytes, err := json.Marshal(env); err == nil {
				s.hub.Broadcast(channel, bytes, StreamTypeTrades)
			}
		}

		// Always commit offset immediately (Best-Effort Fanout Contract)
		if err := reader.CommitMessages(ctx, msg); err != nil {
			s.logger.Debug("Kafka commit error", zap.Error(err))
		}
	}
}

// ─── Helpers & Internal Schemas ──────────────────────────────────────────────

type rawRedisDepth struct {
	MarketID   string `json:"market_id"`
	Bids       []struct {
		Price    string `json:"price"`
		Quantity string `json:"quantity"`
	} `json:"bids"`
	Asks []struct {
		Price    string `json:"price"`
		Quantity string `json:"quantity"`
	} `json:"asks"`
	SnapshotAt string `json:"snapshot_at"`
}

func (s *Streamer) convertDepthPayload(marketID string, raw rawRedisDepth) *OrderBookDepthPayload {
	bids := make([][2]string, 0, len(raw.Bids))
	for _, b := range raw.Bids {
		bids = append(bids, [2]string{b.Price, b.Quantity})
	}
	asks := make([][2]string, 0, len(raw.Asks))
	for _, a := range raw.Asks {
		asks = append(asks, [2]string{a.Price, a.Quantity})
	}

	t, _ := time.Parse(time.RFC3339Nano, raw.SnapshotAt)
	if t.IsZero() {
		t = time.Now().UTC()
	}

	return &OrderBookDepthPayload{
		MarketID:  marketID,
		Bids:      bids,
		Asks:      asks,
		Timestamp: t.UnixMilli(),
	}
}

type rawTradeEvent struct {
	TradeID      string    `json:"trade_id"`
	MarketID     string    `json:"market_id"`
	Price        string    `json:"price"`
	Quantity     string    `json:"quantity"`
	BuyerUserID  string    `json:"buyer_user_id"`
	SellerUserID string    `json:"seller_user_id"`
	ExecutedAt   time.Time `json:"executed_at"`
}

// Observability Helpers
func (s *Streamer) KafkaErrorsTotal() int64 {
	return atomic.LoadInt64(&s.kafkaErrorsTotal)
}

func (s *Streamer) KafkaReconnectsTotal() int64 {
	return atomic.LoadInt64(&s.kafkaReconnectsTotal)
}
