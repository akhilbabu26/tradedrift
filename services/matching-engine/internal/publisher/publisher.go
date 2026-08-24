package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	kafkago "github.com/segmentio/kafka-go"

	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/orderbook"
)

// TopicTradeExecuted is the Kafka topic the Publisher writes TradeExecuted events to.
// Consumed by: Order Service (update order status), Wallet Service (settle funds).
const TopicTradeExecuted = "trades.executed"

// ─── Dependency interfaces (allow unit test fakes) ────────────────────────────

// kafkaWriter is the interface for writing messages to Kafka.
// *kafkago.Writer implements this.
type kafkaWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
}

// redisWriter is the interface for writing depth snapshots to Redis.
type redisWriter interface {
	Set(ctx context.Context, key string, value []byte, expiration time.Duration) error
}

// checkpointCoordinator ensures contiguous checkpoint progression across markets.
type checkpointCoordinator interface {
	MarkDone(ctx context.Context, pos orderbook.KafkaPosition) error
}

// ─── redisClientAdapter — wraps *redis.Client to implement redisWriter ────────

type redisClientAdapter struct {
	client *redis.Client
}

func (r *redisClientAdapter) Set(ctx context.Context, key string, value []byte, expiration time.Duration) error {
	return r.client.Set(ctx, key, value, expiration).Err()
}

// ─── JSON payloads ────────────────────────────────────────────────────────────

// tradeExecutedMessage is the JSON payload written to trades.executed.
// One message per Fill.
type tradeExecutedMessage struct {
	TradeID      string `json:"trade_id"`
	MarketID     string `json:"market_id"`
	Sequence     uint64 `json:"sequence"`
	MakerOrderID string `json:"maker_order_id"`
	TakerOrderID string `json:"taker_order_id"`
	BuyOrderID   string `json:"buy_order_id"`
	SellOrderID  string `json:"sell_order_id"`
	BuyerUserID  string `json:"buyer_user_id"`
	SellerUserID string `json:"seller_user_id"`
	Price        string `json:"price"`       // decimal as string
	Quantity     string `json:"quantity"`    // decimal as string
	ExecutedAt   string `json:"executed_at"` // RFC3339Nano
}

// depthSnapshotMessage is the JSON payload written to Redis.
type depthSnapshotMessage struct {
	MarketID   string       `json:"market_id"`
	Sequence   uint64       `json:"sequence"`
	Bids       []depthLevel `json:"bids"`
	Asks       []depthLevel `json:"asks"`
	SnapshotAt string       `json:"snapshot_at"` // RFC3339Nano
}

type depthLevel struct {
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
}

// ─── Publisher ────────────────────────────────────────────────────────────────

// Publisher reads MatchResults from a MarketEngine's OutputQueue and:
//  1. Publishes one TradeExecuted Kafka message per Fill (partitioned by MarketID)
//  2. Pushes DepthSnapshot to Redis (key: "depth:{market_id}")
//  3. Advances the contiguous Kafka checkpoint via the CheckpointCoordinator
type Publisher struct {
	writer      kafkaWriter
	redis       redisWriter
	coord       checkpointCoordinator
	retryMu     sync.Mutex
	latestDepth map[string]orderbook.DepthSnapshot // bounded latest snapshot retry buffer per market
}

// NewPublisher creates a Publisher wired to real infrastructure.
func NewPublisher(brokers []string, rdb *redis.Client, coord checkpointCoordinator) *Publisher {
	return &Publisher{
		redis:       &redisClientAdapter{client: rdb},
		coord:       coord,
		latestDepth: make(map[string]orderbook.DepthSnapshot),
		writer: &kafkago.Writer{
			Addr:                   kafkago.TCP(brokers...),
			Topic:                  TopicTradeExecuted,
			Balancer:               &kafkago.LeastBytes{},
			RequiredAcks:           kafkago.RequireOne,          // RequireAll needs replication factor > 1; dev only has 1 broker
			Async:                  false,                        // synchronous — confirm before checkpointing
			AllowAutoTopicCreation: true,                         // auto-create topic if it doesn't exist yet
		},
	}
}

// Run reads from one engine's OutputQueue until ctx is cancelled.
func (p *Publisher) Run(ctx context.Context, engine *market.MarketEngine) {
	retryTicker := time.NewTicker(500 * time.Millisecond)
	defer retryTicker.Stop()

	for {
		select {
		case result, ok := <-engine.OutputQueue:
			if !ok {
				p.flushPendingDepthRetries(context.Background(), engine.MarketID)
				return // channel closed — engine shut down
			}
			if err := p.process(ctx, result); err != nil {
				log.Printf("[publisher] process error (market=%s %s/%d@%d): %v",
					engine.MarketID,
					result.SourcePosition.Topic,
					result.SourcePosition.Partition,
					result.SourcePosition.Offset,
					err,
				)
			}
		case <-retryTicker.C:
			p.flushPendingDepthRetries(ctx, engine.MarketID)
		case <-ctx.Done():
			// Shutdown signal received. Drain remaining in-flight MatchResults in OutputQueue before exit.
			drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for {
				select {
				case result, ok := <-engine.OutputQueue:
					if !ok {
						p.flushPendingDepthRetries(drainCtx, engine.MarketID)
						return
					}
					if err := p.process(drainCtx, result); err != nil {
						log.Printf("[publisher] drain error (market=%s offset=%d): %v",
							engine.MarketID, result.SourcePosition.Offset, err)
					}
				default:
					p.flushPendingDepthRetries(drainCtx, engine.MarketID)
					return
				}
			}
		}
	}
}

func (p *Publisher) flushPendingDepthRetries(ctx context.Context, marketID string) {
	p.retryMu.Lock()
	snap, ok := p.latestDepth[marketID]
	if !ok {
		p.retryMu.Unlock()
		return
	}
	p.retryMu.Unlock()

	if err := p.pushDepth(ctx, snap); err == nil {
		p.retryMu.Lock()
		delete(p.latestDepth, marketID)
		p.retryMu.Unlock()
		log.Printf("[publisher] successfully flushed retried depth snapshot (market=%s seq=%d)", marketID, snap.Sequence)
	}
}

// process handles one MatchResult in strict order.
func (p *Publisher) process(ctx context.Context, result orderbook.MatchResult) error {
	// Step 1: Publish TradeExecuted for every Fill.
	// Kafka IS the durable event boundary — failure here must block the checkpoint.
	if len(result.Fills) > 0 {
		if err := p.publishFills(ctx, result.Fills); err != nil {
			return fmt.Errorf("publish fills: %w", err)
		}
		for _, f := range result.Fills {
			log.Printf("[trade] ✅ MATCH  market=%s  price=%s  qty=%s  buy=%s  sell=%s",
				f.MarketID, f.Price.String(), f.Quantity.String(),
				f.BuyOrderID.String()[:8], f.SellOrderID.String()[:8],
			)
		}
	} else {
		log.Printf("[book]  📋 RESTED market=%s  offset=%d",
			result.DepthSnapshot.MarketID, result.SourcePosition.Offset,
		)
	}

	// Step 2: Push DepthSnapshot to Redis.
	// If it fails, buffer the latest snapshot for background retry.
	if err := p.pushDepth(ctx, result.DepthSnapshot); err != nil {
		log.Printf("[publisher] redis depth push failed (buffered for retry, market=%s offset=%d): %v",
			result.DepthSnapshot.MarketID,
			result.SourcePosition.Offset,
			err,
		)
		p.retryMu.Lock()
		p.latestDepth[result.DepthSnapshot.MarketID] = result.DepthSnapshot
		p.retryMu.Unlock()
	} else {
		p.retryMu.Lock()
		delete(p.latestDepth, result.DepthSnapshot.MarketID)
		p.retryMu.Unlock()
	}

	// Step 3: Advance contiguous Postgres checkpoint via coordinator.
	if p.coord != nil {
		if err := p.coord.MarkDone(ctx, result.SourcePosition); err != nil {
			return fmt.Errorf("advance checkpoint: %w", err)
		}
	}

	return nil
}

// publishFills writes one Kafka message per Fill to trades.executed.
// Partition key: fill.MarketID so all trades for a market land in strict sequence on the same partition.
func (p *Publisher) publishFills(ctx context.Context, fills []orderbook.Fill) error {
	msgs := make([]kafkago.Message, 0, len(fills))
	now := time.Now().UTC().Format(time.RFC3339Nano)

	for _, fill := range fills {
		payload := tradeExecutedMessage{
			TradeID:      fill.TradeID.String(),
			MarketID:     fill.MarketID, // from Fill directly — authoritative
			Sequence:     fill.Sequence,
			MakerOrderID: fill.MakerOrderID.String(),
			TakerOrderID: fill.TakerOrderID.String(),
			BuyOrderID:   fill.BuyOrderID.String(),
			SellOrderID:  fill.SellOrderID.String(),
			BuyerUserID:  fill.BuyerUserID.String(),
			SellerUserID: fill.SellerUserID.String(),
			Price:        fill.Price.String(),
			Quantity:     fill.Quantity.String(),
			ExecutedAt:   now,
		}

		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal fill %s: %w", fill.TradeID, err)
		}

		msgs = append(msgs, kafkago.Message{
			Key:   []byte(fill.MarketID), // partition key by market
			Value: b,
		})
	}

	return p.writer.WriteMessages(ctx, msgs...)
}

// pushDepth serialises the DepthSnapshot and writes it to Redis.
// Key:  depth:{market_id}   e.g. "depth:BTC-USDT"
// TTL:  none — latest snapshot always overwrites the previous one.
func (p *Publisher) pushDepth(ctx context.Context, snap orderbook.DepthSnapshot) error {
	bids := make([]depthLevel, len(snap.Bids))
	asks := make([]depthLevel, len(snap.Asks))

	for i, b := range snap.Bids {
		bids[i] = depthLevel{Price: b.Price.String(), Quantity: b.Quantity.String()}
	}
	for i, a := range snap.Asks {
		asks[i] = depthLevel{Price: a.Price.String(), Quantity: a.Quantity.String()}
	}

	msg := depthSnapshotMessage{
		MarketID:   snap.MarketID,
		Sequence:   snap.Sequence,
		Bids:       bids,
		Asks:       asks,
		SnapshotAt: snap.SnapshotAt.UTC().Format(time.RFC3339Nano),
	}

	b, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal depth snapshot: %w", err)
	}

	return p.redis.Set(ctx, "depth:"+snap.MarketID, b, 0)
}

// Close shuts down the Kafka writer cleanly.
func (p *Publisher) Close() error {
	if wc, ok := p.writer.(interface{ Close() error }); ok {
		return wc.Close()
	}
	return nil
}

// ─── Testable Publisher (for unit tests) ──────────────────────────────────────

// TestablePublisher exposes process() for unit tests with injected fakes.
type TestablePublisher struct {
	p *Publisher
}

// NewTestable creates a Publisher with injected fakes for unit testing.
func NewTestable(w kafkaWriter, r redisWriter, coord checkpointCoordinator) *TestablePublisher {
	return &TestablePublisher{
		p: &Publisher{
			writer:      w,
			redis:       r,
			coord:       coord,
			latestDepth: make(map[string]orderbook.DepthSnapshot),
		},
	}
}

// Process exposes internal process() method for testing.
func (tp *TestablePublisher) Process(ctx context.Context, result orderbook.MatchResult) error {
	return tp.p.process(ctx, result)
}
