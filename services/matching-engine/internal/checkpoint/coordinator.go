package checkpoint

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/jackc/pgx/v5/pgconn"
	kafkago "github.com/segmentio/kafka-go"
	"tradedrift/services/matching-engine/internal/orderbook"
)

// DB abstracts Postgres Exec for production and test fakes.
type DB interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// KafkaCommitter allows committing contiguous offsets back to the Kafka consumer group.
type KafkaCommitter interface {
	CommitMessages(ctx context.Context, msgs ...kafkago.Message) error
}

type partitionTracker struct {
	mu            sync.Mutex
	lastCommitted int64          // highest contiguous offset committed to Postgres
	hasCommitted  bool           // whether lastCommitted is valid
	inFlight      map[int64]bool // offsets dispatched to an engine
	completed     map[int64]bool // offsets processed but waiting for contiguous boundary
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
		completed: make(map[int64]bool),
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
	pt := c.getTracker(pos.Topic, pos.Partition)
	pt.mu.Lock()
	defer pt.mu.Unlock()

	// If no baseline was initialized, use pos.Offset-1 on first message
	if !pt.hasCommitted {
		pt.lastCommitted = pos.Offset - 1
		pt.hasCommitted = true
	}

	// If this offset is already <= lastCommitted, ignore it (idempotent duplicate)
	if pos.Offset <= pt.lastCommitted {
		delete(pt.inFlight, pos.Offset)
		return nil
	}

	pt.completed[pos.Offset] = true

	// Advance contiguous watermark
	var toCommit int64 = -1
	curr := pt.lastCommitted + 1
	for pt.completed[curr] {
		toCommit = curr
		delete(pt.completed, curr)
		delete(pt.inFlight, curr)
		curr++
	}

	if toCommit == -1 {
		// Contiguous chain is not yet complete (waiting for earlier in-flight offset)
		return nil
	}

	pt.lastCommitted = toCommit

	if c.db != nil {
		const query = `
			INSERT INTO kafka_checkpoints (topic, partition, "offset", updated_at)
			VALUES ($1, $2, $3, NOW())
			ON CONFLICT (topic, partition)
			DO UPDATE SET
				"offset"   = EXCLUDED."offset",
				updated_at = NOW()
			WHERE kafka_checkpoints."offset" < EXCLUDED."offset"`

		if _, err := c.db.Exec(ctx, query, pos.Topic, pos.Partition, toCommit); err != nil {
			return err
		}
	}

	// Synchronize consumer group offset with Kafka broker
	c.mu.RLock()
	committer, hasCommitter := c.committers[pos.Topic]
	c.mu.RUnlock()
	if hasCommitter && committer != nil {
		if err := committer.CommitMessages(ctx, kafkago.Message{
			Topic:     pos.Topic,
			Partition: pos.Partition,
			Offset:    toCommit,
		}); err != nil {
			log.Printf("[checkpoint] warning: kafka commit offset %d failed (topic=%s partition=%d): %v",
				toCommit, pos.Topic, pos.Partition, err)
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
