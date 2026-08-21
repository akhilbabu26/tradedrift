package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
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

// dbWriter is the interface for writing checkpoints to Postgres.
type dbWriter interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
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
//  1. Publishes one TradeExecuted Kafka message per Fill (fill.MarketID is set)
//  2. Pushes DepthSnapshot to Redis (key: "depth:{market_id}")
//  3. Writes topic+partition+offset checkpoint to Postgres (ONLY after 1+2 succeed)
//
// ORDERING GUARANTEE:
//   Kafka publish → Redis push → Postgres checkpoint
//
// If any step fails, the checkpoint is NOT written.
// On restart, recovery replays from the last checkpoint offset.
// Match() runs in ModeRecovery — fills are re-derived but NOT re-published.
type Publisher struct {
	writer kafkaWriter
	redis  redisWriter
	db     dbWriter
}

// NewPublisher creates a Publisher wired to real infrastructure.
func NewPublisher(brokers []string, rdb *redis.Client, db dbWriter) *Publisher {
	return &Publisher{
		redis: &redisClientAdapter{client: rdb},
		db:    db,
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
// Call as: go publisher.Run(ctx, engine)
// One goroutine per MarketEngine — markets are fully isolated.
//
// IMPORTANT: Results are processed sequentially per market.
// Do NOT process multiple results concurrently for the same market —
// that could cause the checkpoint to move backwards.
func (p *Publisher) Run(ctx context.Context, engine *market.MarketEngine) {
	for {
		select {
		case result, ok := <-engine.OutputQueue:
			if !ok {
				return // channel closed — engine shut down
			}
			if err := p.process(ctx, result); err != nil {
				// Checkpoint NOT written — recovery will re-process this event.
				log.Printf("[publisher] process error (market=%s %s/%d@%d): %v",
					engine.MarketID,
					result.SourcePosition.Topic,
					result.SourcePosition.Partition,
					result.SourcePosition.Offset,
					err,
				)
			}
		case <-ctx.Done():
			return
		}
	}
}

// process handles one MatchResult in strict order.
//
// Failure semantics:
//   - Kafka publish failure  → return error → checkpoint NOT written → event replayed on restart.
//   - Redis push failure     → log and continue → checkpoint IS written → Redis self-heals on next event.
//   - Checkpoint failure     → return error → Kafka already written → at-least-once delivery on restart.
//
// Redis is a read-only projection/cache, NOT a durable event boundary.
// Blocking the checkpoint on a Redis failure would cause duplicate Kafka trade events
// for the entire Redis outage window upon restart — a worse outcome than a stale snapshot.
// The next successful event always overwrites the missed Redis snapshot.
func (p *Publisher) process(ctx context.Context, result orderbook.MatchResult) error {
	// Step 1: Publish TradeExecuted for every Fill.
	// Kafka IS the durable event boundary — failure here must block the checkpoint.
	if len(result.Fills) > 0 {
		if err := p.publishFills(ctx, result.Fills); err != nil {
			return fmt.Errorf("publish fills: %w", err)
		}
		// Log each fill so trades are visible in console output.
		for _, f := range result.Fills {
			log.Printf("[trade] ✅ MATCH  market=%s  price=%s  qty=%s  buy=%s  sell=%s",
				f.MarketID, f.Price.String(), f.Quantity.String(),
				f.BuyOrderID.String()[:8], f.SellOrderID.String()[:8],
			)
		}
	} else {
		// No fills — order was rested in the book.
		log.Printf("[book]  📋 RESTED market=%s  offset=%d",
			result.DepthSnapshot.MarketID, result.SourcePosition.Offset,
		)
	}

	// Step 2: Push DepthSnapshot to Redis (non-critical projection — log and continue).
	// Redis is a cache, not a source of truth. A failed write is self-healing:
	// the next successful event overwrites with a fresh snapshot.
	// Returning an error here would prevent the Postgres checkpoint from advancing,
	// causing duplicate Kafka trade events for the full Redis outage window on restart.
	if err := p.pushDepth(ctx, result.DepthSnapshot); err != nil {
		log.Printf("[publisher] redis depth push failed (non-critical, market=%s offset=%d): %v",
			result.DepthSnapshot.MarketID,
			result.SourcePosition.Offset,
			err,
		)
		// intentionally continue — do not return
	}

	// Step 3: Write Postgres checkpoint — ONLY after Kafka succeeds.
	// Redis failure above does NOT prevent checkpoint advancement.
	if err := p.writeCheckpoint(ctx, result.SourcePosition); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}

	return nil
}

// publishFills writes one Kafka message per Fill to trades.executed.
// Partition key: BuyOrderID — fills for the same buy order land on the same partition.
//
// NOTE: Change to fill.BuyerUserID.String() for user-centric partitioning.
func (p *Publisher) publishFills(ctx context.Context, fills []orderbook.Fill) error {
	msgs := make([]kafkago.Message, 0, len(fills))
	now := time.Now().UTC().Format(time.RFC3339Nano)

	for _, fill := range fills {
		payload := tradeExecutedMessage{
			TradeID:      fill.TradeID.String(),
			MarketID:     fill.MarketID, // from Fill directly — authoritative
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
			Key:   []byte(fill.BuyOrderID.String()), // partition key
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

// writeCheckpoint writes the Kafka position to Postgres using a MONOTONIC UPSERT.
// Each (topic, partition) pair has exactly one row — the latest processed offset.
//
// MONOTONIC GUARANTEE: The WHERE guard ensures the checkpoint can only advance
// forward, never backwards. Multiple MarketEngine publishers write to the same
// (topic, partition) row. Without this guard, a slower publisher processing an
// earlier offset could move the checkpoint backwards, causing duplicate replays on
// restart.
//
// Example race without the guard:
//
//	ETH writes checkpoint=101  (fast publisher, higher offset)
//	BTC writes checkpoint=100  (slow publisher, lower offset) ← regression
//
// With the WHERE guard, BTC's write is silently ignored — correct behavior.
func (p *Publisher) writeCheckpoint(ctx context.Context, pos orderbook.KafkaPosition) error {
		const query = `
		INSERT INTO kafka_checkpoints (topic, partition, "offset", updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (topic, partition)
		DO UPDATE SET
			"offset"     = EXCLUDED."offset",
			updated_at = NOW()
		WHERE kafka_checkpoints."offset" < EXCLUDED."offset"`


	_, err := p.db.Exec(ctx, query, pos.Topic, pos.Partition, pos.Offset)
	return err
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
func NewTestable(w kafkaWriter, r redisWriter, db dbWriter) *TestablePublisher {
	return &TestablePublisher{
		p: &Publisher{writer: w, redis: r, db: db},
	}
}

// Process exposes internal process() method for testing.
func (tp *TestablePublisher) Process(ctx context.Context, result orderbook.MatchResult) error {
	return tp.p.process(ctx, result)
}
