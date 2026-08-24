package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	kafkago "github.com/segmentio/kafka-go"

	intkafka "tradedrift/services/matching-engine/internal/kafka"
	"tradedrift/services/matching-engine/internal/market"
)

// Replayer handles crash recovery for all MarketEngines.
//
// For each MarketEngine it:
//  1. Loads the last saved Kafka position from Postgres (kafka_checkpoints)
//  2. Finds the current high water mark (end offset) of each Kafka partition
//  3. Replays all events from savedOffset+1 to highWatermark through the Engine
//  4. Drains OutputQueue (results discarded — engine is in ModeRecovery, no external writes)
//  5. Pushes a fresh depth snapshot to Redis (in-memory book is now correct)
//  6. Calls engine.SetLive() — engine is ready for live Kafka events
//
// ORDERING CONTRACT (cross-topic replay):
//   Replay processes orders.submitted before orders.cancel-requested.
//   This is safe because:
//   (a) In live mode the Consumer also runs two independent goroutines with no
//       global ordering guarantee across topics — so replay is no worse than live.
//   (b) A cancel event can only exist in Kafka AFTER the corresponding order was
//       submitted. Causal ordering (submitted → cancel) is always preserved.
//   (c) Cancel of an already-filled order returns nil (no-op) — correct behavior.
//
// CHECKPOINT CONTRACT:
//   The Postgres checkpoint is only written by the Publisher (not by the Replayer).
//   During recovery we READ the checkpoint to find where to resume.
//   We do NOT write new checkpoints during recovery — the checkpoint represents
//   the last externally-acknowledged live processing boundary.
//
// CALL ORDER (in cmd/server/main.go):
//
//	replayer.ReplayAll(ctx)          // blocks until all markets are in ModeLive
//	consumer.Start(ctx)              // AFTER all markets live
//	for _, e := range manager.All() { go publisher.Run(ctx, e) }
type Replayer struct {
	brokers []string
	db      *pgxpool.Pool
	redis   *redis.Client
	manager *market.MarketManager
}

// NewReplayer creates a Replayer.
func NewReplayer(brokers []string, db *pgxpool.Pool, rdb *redis.Client, manager *market.MarketManager) *Replayer {
	return &Replayer{
		brokers: brokers,
		db:      db,
		redis:   rdb,
		manager: manager,
	}
}

// ReplayAll replays historical events for every registered MarketEngine sequentially.
// High Water Marks are captured ONCE before replay begins to freeze the recovery boundary.
// After all markets finish replay and draining, the frozen boundaries are saved to PostgreSQL
// so the live consumer starts strictly after the replayed events without skipping anything.
//
// Consumer.Start() MUST NOT be called until ReplayAll returns.
func (r *Replayer) ReplayAll(ctx context.Context) error {
	const partition = 0
	boundaries := make(map[string]int64)

	// Step 1: Capture HWM for all topics BEFORE starting any replay
	for _, topic := range []string{intkafka.TopicOrderCreated, intkafka.TopicOrderCancel} {
		hwm, err := r.highWatermark(topic, partition)
		if err != nil {
			return fmt.Errorf("high watermark for topic %s: %w", topic, err)
		}
		boundaries[topic] = hwm
		log.Printf("[recovery] frozen recovery boundary: topic=%s partition=%d HWM=%d", topic, partition, hwm)
	}

	// Step 2: Replay each engine up to the frozen HWM
	for _, engine := range r.manager.All() {
		if err := r.replayEngine(ctx, engine, boundaries); err != nil {
			return fmt.Errorf("replay market %s: %w", engine.MarketID, err)
		}
	}

	// Step 3: Persist the frozen recovery boundaries to PostgreSQL
	for _, topic := range []string{intkafka.TopicOrderCreated, intkafka.TopicOrderCancel} {
		hwm := boundaries[topic]
		if hwm > 0 {
			lastReplayed := hwm - 1
			if err := r.saveCheckpoint(ctx, topic, partition, lastReplayed); err != nil {
				return fmt.Errorf("save recovery checkpoint (topic=%s offset=%d): %w", topic, lastReplayed, err)
			}
			log.Printf("[recovery] saved recovery checkpoint topic=%s partition=%d offset=%d", topic, partition, lastReplayed)
		}
	}

	return nil
}

// saveCheckpoint advances the Postgres checkpoint after recovery finishes replaying up to HWM.
func (r *Replayer) saveCheckpoint(ctx context.Context, topic string, partition int, offset int64) error {
	const query = `
		INSERT INTO kafka_checkpoints (topic, partition, "offset", updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (topic, partition)
		DO UPDATE SET
			"offset"   = EXCLUDED."offset",
			updated_at = NOW()
		WHERE kafka_checkpoints."offset" < EXCLUDED."offset"`

	_, err := r.db.Exec(ctx, query, topic, partition, offset)
	return err
}

// replayEngine recovers one MarketEngine up to the frozen topic boundaries.
func (r *Replayer) replayEngine(ctx context.Context, engine *market.MarketEngine, boundaries map[string]int64) error {
	log.Printf("[recovery] starting replay for market=%s", engine.MarketID)

	// Step 1: Start the engine's event loop goroutine (runs in ModeRecovery).
	// processEvent() always sends to OutputQueue even in ModeRecovery, so we
	// drain OutputQueue below using an exact count to avoid blocking forever.
	go engine.Run(ctx)

	totalEvents := 0

	// Step 2: Replay both consumed topics for this engine up to the frozen HWM.
	for _, topic := range []string{intkafka.TopicOrderCreated, intkafka.TopicOrderCancel} {
		n, err := r.replayTopic(ctx, engine, topic, boundaries[topic])
		if err != nil {
			return fmt.Errorf("topic %s: %w", topic, err)
		}
		totalEvents += n
	}

	// Step 3: Drain exactly totalEvents from OutputQueue.
	for i := 0; i < totalEvents; i++ {
		select {
		case <-engine.OutputQueue:
			// discard — Publisher is not running, checkpoint is not written
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// Step 4: Push a fresh Top-20 depth snapshot to Redis.
	if err := r.pushFreshDepth(ctx, engine); err != nil {
		return fmt.Errorf("push fresh depth: %w", err)
	}

	// Step 5: Transition to LIVE mode.
	engine.SetLive()

	log.Printf("[recovery] market=%s recovered %d events — now LIVE", engine.MarketID, totalEvents)
	return nil
}

// replayTopic replays one Kafka topic for one engine from savedOffset+1 to the frozen high water mark.
func (r *Replayer) replayTopic(ctx context.Context, engine *market.MarketEngine, topic string, hwm int64) (int, error) {
	const partition = 0

	// Load the last successfully committed offset for this topic+partition.
	savedOffset, err := r.loadCheckpoint(ctx, topic, partition)
	if err != nil {
		return 0, fmt.Errorf("load checkpoint: %w", err)
	}

	// startOffset is the first offset we need to replay.
	startOffset := savedOffset + 1

	// If nothing to replay (already at or beyond HWM), skip.
	if startOffset >= hwm {
		log.Printf("[recovery] topic=%s partition=%d — at HWM (checkpoint=%d hwm=%d), nothing to replay",
			topic, partition, savedOffset, hwm)
		return 0, nil
	}

	log.Printf("[recovery] topic=%s partition=%d — replaying offsets %d → %d",
		topic, partition, startOffset, hwm-1)

	// Create a dedicated replay reader for this partition.
	// Uses no GroupID so it doesn't interfere with the live consumer group offsets.
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:   r.brokers,
		Topic:     topic,
		Partition: partition,
		MinBytes:  1,
		MaxBytes:  10e6,
		MaxWait:   2 * time.Second,
	})
	defer reader.Close()

	// Seek directly to savedOffset+1. Does not replay already-processed events.
	if err := reader.SetOffset(startOffset); err != nil {
		return 0, fmt.Errorf("seek to offset %d: %w", startOffset, err)
	}

	count := 0
	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			return count, fmt.Errorf("fetch message: %w", err)
		}

		sent, err := r.routeMessage(msg, engine, topic)
		if err != nil {
			// Corrupt message — log and skip. One bad message must not block recovery.
			log.Printf("[recovery] skip bad message (topic=%s partition=%d offset=%d): %v",
				topic, partition, msg.Offset, err)
		} else if sent {
			count++ // only count events that were actually sent to InputQueue
		}

		// Stop when we have replayed up to the HWM captured at startup.
		// Events at or after hwm are live events — the Consumer will handle them.
		if msg.Offset >= hwm-1 {
			break
		}
	}

	return count, nil
}

// routeMessage deserialises one Kafka message and sends it to the engine's
// InputQueue if it belongs to this engine's market.
//
// Returns (true, nil)  — message sent to InputQueue
// Returns (false, nil) — wrong market, nothing sent (must NOT be counted in drain)
// Returns (false, err) — parse/validation error, logged by caller
func (r *Replayer) routeMessage(msg kafkago.Message, engine *market.MarketEngine, topic string) (bool, error) {
	// The sent flag tracks whether the message was actually queued.
	// Without it, wrong-market messages (route returns nil) would be silently
	// counted, causing the drain loop to wait for OutputQueue items that will
	// never arrive — deadlock.
	sent := false

	route := func(marketID string) chan market.InputEvent {
		if marketID == engine.MarketID {
			sent = true
			return engine.InputQueue
		}
		return nil // wrong market — skip silently
	}

	var err error
	switch topic {
	case intkafka.TopicOrderCreated:
		_, err = intkafka.HandleOrderCreated(msg, route)
	case intkafka.TopicOrderCancel:
		_, err = intkafka.HandleOrderCancel(msg, route)
	default:
		return false, fmt.Errorf("unknown topic %q", topic)
	}

	if err != nil {
		return false, err
	}
	return sent, nil
}

// loadCheckpoint reads the last saved Kafka offset for a topic+partition from Postgres.
// Returns -1 if no checkpoint exists (meaning: replay from the very beginning, offset 0).
func (r *Replayer) loadCheckpoint(ctx context.Context, topic string, partition int) (int64, error) {
	query := `
		SELECT "offset" FROM kafka_checkpoints
		WHERE topic = $1 AND partition = $2`

	var offset int64
	err := r.db.QueryRow(ctx, query, topic, partition).Scan(&offset)
	if err != nil {
		// Fix: use pgx.ErrNoRows sentinel — not a string comparison.
		if errors.Is(err, pgx.ErrNoRows) {
			return -1, nil // -1 → startOffset = 0 (replay from beginning)
		}
		return 0, fmt.Errorf("query checkpoint (topic=%s partition=%d): %w", topic, partition, err)
	}
	return offset, nil
}

// highWatermark connects to the Kafka partition leader and returns the latest
// available offset. The last committed message is at offset highWatermark-1.
func (r *Replayer) highWatermark(topic string, partition int) (int64, error) {
	conn, err := kafkago.DialLeader(context.Background(), "tcp", r.brokers[0], topic, partition)
	if err != nil {
		return 0, fmt.Errorf("dial leader (topic=%s partition=%d): %w", topic, partition, err)
	}
	defer conn.Close()

	last, err := conn.ReadLastOffset()
	if err != nil {
		return 0, fmt.Errorf("read last offset (topic=%s partition=%d): %w", topic, partition, err)
	}
	return last, nil
}

// pushFreshDepth serialises the current in-memory Top-20 depth and writes it to Redis.
// Called at the end of recovery so Redis reflects the fully rebuilt book state.
// Uses the same key format as the Publisher: depth:{market_id}
func (r *Replayer) pushFreshDepth(ctx context.Context, engine *market.MarketEngine) error {
	snap := engine.GetDepth(20)

	type depthLevel struct {
		Price    string `json:"price"`
		Quantity string `json:"quantity"`
	}
	type depthMsg struct {
		MarketID   string       `json:"market_id"`
		Sequence   uint64       `json:"sequence"`
		Bids       []depthLevel `json:"bids"`
		Asks       []depthLevel `json:"asks"`
		SnapshotAt string       `json:"snapshot_at"`
	}

	bids := make([]depthLevel, len(snap.Bids))
	asks := make([]depthLevel, len(snap.Asks))
	for i, b := range snap.Bids {
		bids[i] = depthLevel{Price: b.Price.String(), Quantity: b.Quantity.String()}
	}
	for i, a := range snap.Asks {
		asks[i] = depthLevel{Price: a.Price.String(), Quantity: a.Quantity.String()}
	}

	msg := depthMsg{
		MarketID:   snap.MarketID,
		Sequence:   snap.Sequence,
		Bids:       bids,
		Asks:       asks,
		SnapshotAt: snap.SnapshotAt.UTC().Format(time.RFC3339Nano),
	}

	b, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal depth: %w", err)
	}

	return r.redis.Set(ctx, "depth:"+snap.MarketID, b, 0).Err()
}
