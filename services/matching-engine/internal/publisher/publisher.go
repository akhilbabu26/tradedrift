package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	kafkago "github.com/segmentio/kafka-go"

	"tradedrift/services/matching-engine/internal/checkpoint"
	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/orderbook"
)

const TopicTradeExecuted = "trades.executed"

type dbWriter interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

type kafkaWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
}

type redisWriter interface {
	Set(ctx context.Context, key string, value []byte, expiration time.Duration) error
}

type checkpointCoordinator interface {
	MarkDoneWithSequence(ctx context.Context, event checkpoint.CompletedEvent) error
}

type redisClientAdapter struct {
	client *redis.Client
}

func (r *redisClientAdapter) Set(ctx context.Context, key string, value []byte, expiration time.Duration) error {
	return r.client.Set(ctx, key, value, expiration).Err()
}

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
	Price        string `json:"price"`
	Quantity     string `json:"quantity"`
	ExecutedAt   string `json:"executed_at"`
}

type depthSnapshotMessage struct {
	MarketID   string       `json:"market_id"`
	Sequence   uint64       `json:"sequence"`
	Bids       []depthLevel `json:"bids"`
	Asks       []depthLevel `json:"asks"`
	SnapshotAt string       `json:"snapshot_at"`
}

type depthLevel struct {
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
}

type Publisher struct {
	writer          kafkaWriter
	redis           redisWriter
	coord           checkpointCoordinator
	db              dbWriter
	retryMu         sync.Mutex
	latestDepth     map[string]orderbook.DepthSnapshot
	HaltCallback    func()
	retentionCancel context.CancelFunc
}

func NewPublisher(brokers []string, rdb *redis.Client, coord checkpointCoordinator, db dbWriter) *Publisher {
	retCtx, retCancel := context.WithCancel(context.Background())
	p := &Publisher{
		redis:           &redisClientAdapter{client: rdb},
		coord:           coord,
		db:              db,
		latestDepth:     make(map[string]orderbook.DepthSnapshot),
		retentionCancel: retCancel,
		writer: &kafkago.Writer{
			Addr:                   kafkago.TCP(brokers...),
			Topic:                  TopicTradeExecuted,
			Balancer:               &kafkago.LeastBytes{},
			RequiredAcks:           kafkago.RequireOne,
			Async:                  false,
			AllowAutoTopicCreation: true,
		},
	}
	if db != nil {
		go p.startRetentionJob(retCtx)
	}
	return p
}

func (p *Publisher) startRetentionJob(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := p.runRetention(ctx); err != nil {
				log.Printf("[publisher] snapshot retention job failed: %v", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (p *Publisher) runRetention(ctx context.Context) error {
	const query = `
		WITH ranked AS (
			SELECT market_id, sequence,
			       ROW_NUMBER() OVER (PARTITION BY market_id ORDER BY sequence DESC) as rn
			FROM market_snapshots
		),
		anchors AS (
			SELECT DISTINCT ON (ms.market_id) ms.market_id, ms.sequence
			FROM market_snapshots ms
			JOIN kafka_checkpoints kc ON kc.partition = ms.partition AND kc.topic = 'orders.commands'
			WHERE ms.offset <= kc.offset
			ORDER BY ms.market_id, ms.offset DESC
		)
		DELETE FROM market_snapshots ms
		WHERE NOT EXISTS (
			SELECT 1 FROM ranked r
			WHERE r.market_id = ms.market_id AND r.sequence = ms.sequence AND r.rn <= 3
		)
		AND NOT EXISTS (
			SELECT 1 FROM anchors a
			WHERE a.market_id = ms.market_id AND a.sequence = ms.sequence
		)`
	_, err := p.db.Exec(ctx, query)
	return err
}

func (p *Publisher) Run(ctx context.Context, engine *market.MarketEngine) {
	retryTicker := time.NewTicker(500 * time.Millisecond)
	defer retryTicker.Stop()

	for {
		select {
		case result, ok := <-engine.OutputQueue:
			if !ok {
				p.flushPendingDepthRetries(context.Background(), engine.MarketID)
				return
			}
			if err := p.process(ctx, result); err != nil {
				log.Printf("[publisher] FATAL process error (market=%s %s/%d@%d): %v",
					engine.MarketID,
					result.SourcePosition.Topic,
					result.SourcePosition.Partition,
					result.SourcePosition.Offset,
					err,
				)
				// INVARIANT (Issue #1 & #2): Halt matching engine on process/publish failure to prevent checkpoint advances on failed state.
				if p.HaltCallback != nil {
					p.HaltCallback()
				} else if engine.HaltCallback != nil {
					engine.HaltCallback()
				}
				return
			}
		case <-retryTicker.C:
			p.flushPendingDepthRetries(ctx, engine.MarketID)
		case <-ctx.Done():
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

func (p *Publisher) process(ctx context.Context, result orderbook.MatchResult) error {
	// Step 1: Publish TradeExecuted for every Fill.
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

	// Step 2: Push DepthSnapshot to Redis. (Issue #1: Fail-closed on Redis failure)
	if err := p.pushDepth(ctx, result.DepthSnapshot); err != nil {
		return fmt.Errorf("redis depth push failed (market=%s offset=%d): %w",
			result.DepthSnapshot.MarketID,
			result.SourcePosition.Offset,
			err,
		)
	}

	// Step 3: Commit snapshot, sequence, and checkpoint in a single PostgreSQL transaction
	if p.coord != nil {
		var checksum []byte
		if result.Snapshot != nil {
			var err error
			checksum, err = orderbook.Checksum(*result.Snapshot)
			if err != nil {
				return fmt.Errorf("calculate snapshot checksum: %w", err)
			}
		}

		ev := checkpoint.CompletedEvent{
			Pos:      result.SourcePosition,
			MarketID: result.DepthSnapshot.MarketID,
			Sequence: result.DepthSnapshot.Sequence,
			Snapshot: result.Snapshot,
			Checksum: checksum,
		}

		if err := p.coord.MarkDoneWithSequence(ctx, ev); err != nil {
			return fmt.Errorf("advance checkpoint: %w", err)
		}
	}

	return nil
}

func (p *Publisher) publishFills(ctx context.Context, fills []orderbook.Fill) error {
	msgs := make([]kafkago.Message, 0, len(fills))
	now := time.Now().UTC().Format(time.RFC3339Nano)

	for _, fill := range fills {
		payload := tradeExecutedMessage{
			TradeID:      fill.TradeID.String(),
			MarketID:     fill.MarketID,
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
			Key:   []byte(fill.MarketID),
			Value: b,
		})
	}

	return p.writer.WriteMessages(ctx, msgs...)
}

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

func (p *Publisher) Close() error {
	if p.retentionCancel != nil {
		p.retentionCancel()
	}
	if wc, ok := p.writer.(interface{ Close() error }); ok {
		return wc.Close()
	}
	return nil
}

type TestablePublisher struct {
	p *Publisher
}

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

func (tp *TestablePublisher) Process(ctx context.Context, result orderbook.MatchResult) error {
	return tp.p.process(ctx, result)
}
