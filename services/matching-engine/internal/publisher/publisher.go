package publisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	kafkago "github.com/segmentio/kafka-go"

	"tradedrift/services/matching-engine/internal/checkpoint"
	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/metrics"
	"tradedrift/services/matching-engine/internal/orderbook"
)

const (
	TopicTradeExecuted   = "trades.executed"
	TopicOrdersCancelled = "orders.cancelled.v1"
)

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

type Publisher struct {
	writer          kafkaWriter
	cancelWriter    kafkaWriter
	redis           redisWriter
	coord           checkpointCoordinator
	db              dbWriter
	retryMu         sync.Mutex
	latestDepth     map[string]orderbook.DepthSnapshot
	HaltCallback    func()
	retentionCancel context.CancelFunc
	drainFailed     int32
}

func (p *Publisher) HasDrainFailed() bool {
	return atomic.LoadInt32(&p.drainFailed) == 1
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
		cancelWriter: &kafkago.Writer{
			Addr:                   kafkago.TCP(brokers...),
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

// SeedDepth seeds the initial order book depth snapshot to Redis and initializes the
// cached depth snapshot for periodic heartbeat publishing. Safe to call during startup
// recovery before live event intake begins.
func (p *Publisher) SeedDepth(ctx context.Context, snap orderbook.DepthSnapshot) error {
	p.retryMu.Lock()
	p.latestDepth[snap.MarketID] = snap
	p.retryMu.Unlock()

	return p.pushDepth(ctx, snap)
}

func (p *Publisher) Run(ctx context.Context, engine *market.MarketEngine) {
	retryTicker := time.NewTicker(500 * time.Millisecond)
	defer retryTicker.Stop()

	heartbeatTicker := time.NewTicker(1 * time.Second)
	defer heartbeatTicker.Stop()

	// Track latest authoritative depth snapshot produced by the event loop or seeded on startup.
	// Confined exclusively to this Publisher.Run goroutine (zero race conditions).
	var currentDepth orderbook.DepthSnapshot
	var hasDepth bool

	p.retryMu.Lock()
	if initial, ok := p.latestDepth[engine.MarketID]; ok {
		currentDepth = initial
		hasDepth = true
	}
	p.retryMu.Unlock()

	for {
		if engine.IsFatalHalt() || atomic.LoadInt32(&p.drainFailed) != 0 {
			log.Printf("[publisher] fatal halt: stopping publisher loop immediately for market=%s", engine.MarketID)
			return
		}

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

			// Update cached depth from authoritative event loop output
			currentDepth = result.DepthSnapshot
			hasDepth = true

		case <-heartbeatTicker.C:
			if !hasDepth {
				p.retryMu.Lock()
				if initial, ok := p.latestDepth[engine.MarketID]; ok && (len(initial.Bids) > 0 || len(initial.Asks) > 0) {
					currentDepth = initial
					hasDepth = true
				}
				p.retryMu.Unlock()
			}

			// Invariant #2: Heartbeat must NOT publish before a valid depth exists
			if !hasDepth || (len(currentDepth.Bids) == 0 && len(currentDepth.Asks) == 0) {
				continue
			}

			// Invariant #1 & #10: SnapshotAt represents publication/refresh freshness, NOT a book mutation.
			// Sequence is NEVER incremented on heartbeat.
			// Shallow copy the struct so cached state is not mutated in-place.
			heartbeatDepth := currentDepth
			heartbeatDepth.SnapshotAt = time.Now().UTC()

			// Invariant #4: Heartbeat failure on idle book is non-fatal (logs warning, increments error metric).
			// Actual trade outcome publish failures remain fail-stop in process().
			if err := p.pushDepth(ctx, heartbeatDepth); err != nil {
				log.Printf("[publisher] warning: depth heartbeat failed for market=%s: %v", engine.MarketID, err)
				metrics.DepthHeartbeatErrorsTotal.WithLabelValues(engine.MarketID).Inc()
			} else {
				metrics.DepthHeartbeatTotal.WithLabelValues(engine.MarketID).Inc()
				metrics.DepthHeartbeatLastTimestamp.WithLabelValues(engine.MarketID).Set(float64(time.Now().Unix()))
			}

		case <-retryTicker.C:
			p.flushPendingDepthRetries(ctx, engine.MarketID)
		case <-ctx.Done():
			if engine.IsFatalHalt() || atomic.LoadInt32(&p.drainFailed) != 0 {
				log.Printf("[publisher] fatal halt: skipping drain for market=%s", engine.MarketID)
				return
			}
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
						atomic.StoreInt32(&p.drainFailed, 1)
						log.Printf("[publisher] FATAL drain error (market=%s offset=%d): %v",
							engine.MarketID, result.SourcePosition.Offset, err)
						if p.HaltCallback != nil {
							p.HaltCallback()
						} else if engine.HaltCallback != nil {
							engine.HaltCallback()
						}
						return
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
	} else if result.CancelResult == nil {
		log.Printf("[book]  📋 RESTED market=%s  offset=%d",
			result.DepthSnapshot.MarketID, result.SourcePosition.Offset,
		)
	}

	// Step 1b: Publish OrderCancelOutcome if CancelResult is present.
	// CancelResult is non-nil for both REMOVED_FROM_BOOK and ALREADY_ABSENT —
	// the Order Service always receives an authoritative outcome.
	if result.CancelResult != nil {
		if err := p.publishCancel(ctx, result.CancelResult); err != nil {
			return fmt.Errorf("publish cancel: %w", err)
		}
		log.Printf("[cancel] market=%s order=%s status=%s reason=%s",
			result.CancelResult.MarketID,
			result.CancelResult.OrderID.String()[:8],
			string(result.CancelResult.CancelStatus),
			result.CancelResult.Reason,
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

func (p *Publisher) publishCancel(ctx context.Context, cancel *orderbook.CancelledOrder) error {
	if cancel.CancelStatus != orderbook.CancelStatusRemovedFromBook &&
		cancel.CancelStatus != orderbook.CancelStatusAlreadyAbsent {
		// Safety guard: non-user-cancel paths (ioc_expired, invalid_order_parameters)
		// do not set CancelStatus. Publish them with the legacy single-event approach
		// using REMOVED_FROM_BOOK semantics so the consumer can release funds.
		// These orders were never CANCELLING in the DB, so MarkOrderCancelled is used.
		cancel.CancelStatus = orderbook.CancelStatusRemovedFromBook
	}

	eventID := uuid.NewSHA1(
		uuid.NameSpaceDNS,
		[]byte(fmt.Sprintf("cancel:%s:%d", cancel.OrderID, cancel.SourceOffset)),
	).String()

	payload := orderCancelOutcomeMessage{
		EventType: "OrderCancelOutcome",
		EventID:   eventID,
		OrderID:   cancel.OrderID.String(),
		UserID:    cancel.UserID.String(),
		MarketID:  cancel.MarketID,
		Status:    string(cancel.CancelStatus),
		Reason:    cancel.Reason,
		Timestamp: cancel.CancelledAt.UTC().Format(time.RFC3339Nano),
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal cancel %s: %w", cancel.OrderID, err)
	}

	w := p.cancelWriter
	if w == nil {
		w = p.writer
	}

	return w.WriteMessages(ctx, kafkago.Message{
		Topic: TopicOrdersCancelled,
		Key:   []byte(cancel.MarketID),
		Value: b,
	})
}

func (p *Publisher) Close() error {
	if p.retentionCancel != nil {
		p.retentionCancel()
	}
	var firstErr error
	if wc, ok := p.writer.(interface{ Close() error }); ok {
		firstErr = wc.Close()
	}
	if p.cancelWriter != nil && p.cancelWriter != p.writer {
		if cc, ok := p.cancelWriter.(interface{ Close() error }); ok {
			if err := cc.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

type TestablePublisher struct {
	p *Publisher
}

func NewTestable(w kafkaWriter, r redisWriter, coord checkpointCoordinator) *TestablePublisher {
	return &TestablePublisher{
		p: &Publisher{
			writer:       w,
			cancelWriter: w,
			redis:        r,
			coord:        coord,
			latestDepth:  make(map[string]orderbook.DepthSnapshot),
		},
	}
}

func (tp *TestablePublisher) Process(ctx context.Context, result orderbook.MatchResult) error {
	return tp.p.process(ctx, result)
}

func (tp *TestablePublisher) Run(ctx context.Context, engine *market.MarketEngine) {
	tp.p.Run(ctx, engine)
}

func (tp *TestablePublisher) SeedDepth(ctx context.Context, snap orderbook.DepthSnapshot) error {
	return tp.p.SeedDepth(ctx, snap)
}

func (tp *TestablePublisher) SetHaltCallback(cb func()) {
	tp.p.HaltCallback = cb
}

