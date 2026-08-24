package checkpoint

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"
	"tradedrift/services/matching-engine/internal/orderbook"
)

// DB abstracts Postgres Exec/Begin for production and test fakes.
type DB interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Begin(ctx context.Context) (Tx, error)
}

// Tx abstracts a Postgres transaction.
type Tx interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// KafkaCommitter allows committing contiguous offsets back to the Kafka consumer group.
type KafkaCommitter interface {
	CommitMessages(ctx context.Context, msgs ...kafkago.Message) error
}

type CompletedEvent struct {
	Pos      orderbook.KafkaPosition
	MarketID string
	Sequence uint64
	Snapshot *orderbook.BookSnapshot
	Checksum []byte
}

type partitionTracker struct {
	mu            sync.Mutex
	lastCommitted int64                    // highest contiguous offset committed to Postgres
	hasCommitted  bool                     // whether lastCommitted is valid
	inFlight      map[int64]bool           // offsets dispatched to an engine
	completed     map[int64]*CompletedEvent // offsets processed but waiting for contiguous boundary
}

// Coordinator ensures that Postgres checkpoints for each (topic, partition)
// advance ONLY when every preceding offset has been successfully processed.
// This prevents cross-market interleaving races from skipping unprocessed events on crash.
type Coordinator struct {
	db         DB
	mu         sync.RWMutex
	trackers   map[string]*partitionTracker // key: "topic:partition"
	committers map[string]KafkaCommitter    // key: topic
}

// NewCoordinator creates a Checkpoint Coordinator.
func NewCoordinator(db DB) *Coordinator {
	return &Coordinator{
		db:         db,
		trackers:   make(map[string]*partitionTracker),
		committers: make(map[string]KafkaCommitter),
	}
}

// RegisterCommitter registers a Kafka reader or committer for consumer group offset commits.
func (c *Coordinator) RegisterCommitter(topic string, committer KafkaCommitter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.committers[topic] = committer
}

func (c *Coordinator) getTracker(topic string, partition int) *partitionTracker {
	key := fmt.Sprintf("%s:%d", topic, partition)

	c.mu.RLock()
	pt, ok := c.trackers[key]
	c.mu.RUnlock()
	if ok {
		return pt
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if pt, ok = c.trackers[key]; ok {
		return pt
	}

	pt = &partitionTracker{
		inFlight:  make(map[int64]bool),
		completed: make(map[int64]*CompletedEvent),
	}
	c.trackers[key] = pt
	return pt
}

// InitBaseline sets the initial committed offset for a partition (e.g. loaded at startup from DB).
func (c *Coordinator) InitBaseline(topic string, partition int, baselineOffset int64) {
	pt := c.getTracker(topic, partition)
	pt.mu.Lock()
	defer pt.mu.Unlock()

	pt.lastCommitted = baselineOffset
	pt.hasCommitted = true
}

// Track registers an incoming Kafka offset when consumed from the broker.
func (c *Coordinator) Track(pos orderbook.KafkaPosition) {
	pt := c.getTracker(pos.Topic, pos.Partition)
	pt.mu.Lock()
	defer pt.mu.Unlock()

	pt.inFlight[pos.Offset] = true
}

// MarkDone marks an offset as completed by its MarketEngine/Publisher.
// It computes the highest contiguous processed offset and commits it to Postgres.
func (c *Coordinator) MarkDone(ctx context.Context, pos orderbook.KafkaPosition) error {
	return c.MarkDoneWithSequence(ctx, CompletedEvent{
		Pos: pos,
	})
}

// MarkDoneWithSequence performs atomic updates for checkpoint, sequence, and optional snapshot.
func (c *Coordinator) MarkDoneWithSequence(ctx context.Context, event CompletedEvent) error {
	pt := c.getTracker(event.Pos.Topic, event.Pos.Partition)
	pt.mu.Lock()
	defer pt.mu.Unlock()

	// If no baseline was initialized, use pos.Offset-1 on first message
	if !pt.hasCommitted {
		pt.lastCommitted = event.Pos.Offset - 1
		pt.hasCommitted = true
	}

	// If this offset is already <= lastCommitted, ignore it (idempotent duplicate)
	if event.Pos.Offset <= pt.lastCommitted {
		delete(pt.inFlight, event.Pos.Offset)
		return nil
	}

	pt.completed[event.Pos.Offset] = &event

	// Determine candidate contiguous watermark
	var toCommit int64 = -1
	curr := pt.lastCommitted + 1
	var eventsToCommit []CompletedEvent
	for pt.completed[curr] != nil {
		toCommit = curr
		eventsToCommit = append(eventsToCommit, *pt.completed[curr])
		curr++
	}

	if toCommit == -1 {
		// Contiguous chain is not yet complete (waiting for earlier in-flight offset)
		return nil
	}

	// 1. Write to Postgres in a single transaction
	if c.db != nil {
		tx, err := c.db.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin tx: %w", err)
		}
		defer tx.Rollback(ctx)

		// A. Persist sequence and snapshots for contiguous completed events
		for _, ev := range eventsToCommit {
			if ev.MarketID != "" {
				const seqQuery = `
					INSERT INTO market_sequences (market_id, sequence, updated_at)
					VALUES ($1, $2, NOW())
					ON CONFLICT (market_id)
					DO UPDATE SET
						sequence   = EXCLUDED.sequence,
						updated_at = NOW()`
				if _, err := tx.Exec(ctx, seqQuery, ev.MarketID, ev.Sequence); err != nil {
					return fmt.Errorf("persist market sequence for %s: %w", ev.MarketID, err)
				}
			}

			if ev.Snapshot != nil {
				const snapQuery = `
					INSERT INTO market_snapshots (market_id, sequence, partition, "offset", schema_version, snapshot, checksum, created_at)
					VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
					ON CONFLICT (market_id, sequence)
					DO UPDATE SET
						partition      = EXCLUDED.partition,
						"offset"       = EXCLUDED."offset",
						schema_version = EXCLUDED.schema_version,
						snapshot       = EXCLUDED.snapshot,
						checksum       = EXCLUDED.checksum,
						created_at     = NOW()`
				snapJSON, err := json.Marshal(ev.Snapshot)
				if err != nil {
					return fmt.Errorf("marshal snapshot struct: %w", err)
				}
				if _, err := tx.Exec(ctx, snapQuery, ev.MarketID, ev.Sequence, ev.Pos.Partition, ev.Pos.Offset, ev.Snapshot.SchemaVersion, snapJSON, ev.Checksum); err != nil {
					return fmt.Errorf("persist snapshot for %s: %w", ev.MarketID, err)
				}
			}
		}

		// B. Commit partition checkpoint
		const checkQuery = `
			INSERT INTO kafka_checkpoints (topic, partition, "offset", updated_at)
			VALUES ($1, $2, $3, NOW())
			ON CONFLICT (topic, partition)
			DO UPDATE SET
				"offset"   = EXCLUDED."offset",
				updated_at = NOW()
			WHERE kafka_checkpoints."offset" < EXCLUDED."offset"`
		if _, err := tx.Exec(ctx, checkQuery, event.Pos.Topic, event.Pos.Partition, toCommit); err != nil {
			return fmt.Errorf("persist contiguous checkpoint offset=%d to postgres: %w", toCommit, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit tx: %w", err)
		}
	}

	// 2. Advance in-memory state strictly AFTER Postgres succeeds
	prevCommitted := pt.lastCommitted
	pt.lastCommitted = toCommit
	for i := prevCommitted + 1; i <= toCommit; i++ {
		delete(pt.completed, i)
		delete(pt.inFlight, i)
	}

	// 3. Synchronize consumer group offset with Kafka broker
	c.mu.RLock()
	committer, hasCommitter := c.committers[event.Pos.Topic]
	c.mu.RUnlock()
	if hasCommitter && committer != nil {
		if err := committer.CommitMessages(ctx, kafkago.Message{
			Topic:     event.Pos.Topic,
			Partition: event.Pos.Partition,
			Offset:    toCommit,
		}); err != nil {
			return fmt.Errorf("kafka commit messages (offset=%d): %w", toCommit, err)
		}
	}

	return nil
}

// GetCommittedOffset returns the currently committed contiguous offset for testing/observability.
func (c *Coordinator) GetCommittedOffset(topic string, partition int) (int64, bool) {
	pt := c.getTracker(topic, partition)
	pt.mu.Lock()
	defer pt.mu.Unlock()
	return pt.lastCommitted, pt.hasCommitted
}

type pgxPoolWrapper struct {
	pool *pgxpool.Pool
}

func (w *pgxPoolWrapper) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return w.pool.Exec(ctx, sql, arguments...)
}

func (w *pgxPoolWrapper) Begin(ctx context.Context) (Tx, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &pgxTxWrapper{tx: tx}, nil
}

type pgxTxWrapper struct {
	tx pgx.Tx
}

func (w *pgxTxWrapper) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return w.tx.Exec(ctx, sql, arguments...)
}

func (w *pgxTxWrapper) Commit(ctx context.Context) error {
	return w.tx.Commit(ctx)
}

func (w *pgxTxWrapper) Rollback(ctx context.Context) error {
	return w.tx.Rollback(ctx)
}

// WrapPGXPool wraps a *pgxpool.Pool in the DB interface.
func WrapPGXPool(pool *pgxpool.Pool) DB {
	return &pgxPoolWrapper{pool: pool}
}
