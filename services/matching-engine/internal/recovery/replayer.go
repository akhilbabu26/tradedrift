package recovery

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"

	"time"

	"tradedrift/services/matching-engine/internal/market"
	intkafka "tradedrift/services/matching-engine/internal/kafka"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	kafkago "github.com/segmentio/kafka-go"
)

// TopicOrderCommands defines the default Kafka topic for order command inputs.
const TopicOrderCommands = intkafka.TopicOrderCommands

// KafkaReader abstract interface for consumer libraries (e.g. segmentio/kafka-go).
type KafkaReader interface {
	SetOffset(offset int64) error
	FetchMessage(ctx context.Context) (kafkago.Message, error)
	Close() error
}

type pgxConn interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type redisConn interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
}

// Replayer orchestrates the bootstrap recovery process across all assigned partitions.
type Replayer struct {
	brokers                []string
	groupID                string
	db                     pgxConn
	redis                  redisConn
	manager                *market.MarketManager
	discoverPartitionsFunc func(topic string) ([]int, error)
	newReaderFunc          func(brokers []string, topic string, partition int) KafkaReader
	queryHWMFunc           func(ctx context.Context, topic string, partition int) (int64, error)
}

// NewReplayer returns a configured Replayer.
func NewReplayer(brokers []string, groupID string, db pgxConn, redis redisConn, manager *market.MarketManager) *Replayer {
	r := &Replayer{
		brokers: brokers,
		groupID: groupID,
		db:      db,
		redis:   redis,
		manager: manager,
	}

	// Set production defaults
	r.discoverPartitionsFunc = func(topic string) ([]int, error) {
		conn, err := kafkago.Dial("tcp", brokers[0])
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		partitions, err := conn.ReadPartitions(topic)
		if err != nil {
			return nil, err
		}
		var pIDs []int
		for _, p := range partitions {
			pIDs = append(pIDs, p.ID)
		}
		return pIDs, nil
	}

	r.newReaderFunc = func(brokers []string, topic string, partition int) KafkaReader {
		return kafkago.NewReader(kafkago.ReaderConfig{
			Brokers:   brokers,
			Topic:     topic,
			Partition: partition,
			MinBytes:  1,
			MaxBytes:  10e6,
		})
	}

	r.queryHWMFunc = func(ctx context.Context, topic string, partition int) (int64, error) {
		conn, err := kafkago.DialLeader(ctx, "tcp", brokers[0], topic, partition)
		if err != nil {
			return 0, err
		}
		defer conn.Close()
		logEndOffset, err := conn.ReadLastOffset()
		if err != nil {
			return 0, err
		}
		return logEndOffset, nil
	}

	return r
}

// OverrideDiscoveryAndReader allows substituting mocks for external dependencies during tests.
func (r *Replayer) OverrideDiscoveryAndReader(
	discover func(topic string) ([]int, error),
	newReader func(brokers []string, topic string, partition int) KafkaReader,
	queryHWM func(ctx context.Context, topic string, partition int) (int64, error),
) {
	r.discoverPartitionsFunc = discover
	r.newReaderFunc = newReader
	r.queryHWMFunc = queryHWM
}

// ReplayAll recovers all MarketEngines across all Kafka partitions up to their checkpoints.
func (r *Replayer) ReplayAll(ctx context.Context, engineWg *sync.WaitGroup) error {
	topic := intkafka.TopicOrderCommands
	log.Printf("[recovery] starting recovery for topic=%s...", topic)

	// Recover only the partitions assigned to the market engines on this instance (Issue 1)
	partitionsMap := make(map[int]bool)
	for _, engine := range r.manager.All() {
		partitionsMap[engine.Partition()] = true
	}
	partitions := make([]int, 0, len(partitionsMap))
	for p := range partitionsMap {
		partitions = append(partitions, p)
	}
	sort.Ints(partitions)

	log.Printf("[recovery] recovering partition(s) assigned to this instance: %v", partitions)

	for _, engine := range r.manager.All() {
		engineWg.Add(1)
		e := engine
		go func() {
			defer engineWg.Done()
			e.Run(ctx)
		}()
	}

	type partitionBoundary struct {
		partition  int
		checkpoint int64
	}
	boundaries := make([]partitionBoundary, 0, len(partitions))
	marketLastSeenOffset := make(map[string]int64)

	for _, partition := range partitions {
		checkpointOffset, err := r.loadCheckpoint(ctx, topic, partition)
		if err != nil {
			return fmt.Errorf("load checkpoint (partition=%d): %w", partition, err)
		}

		boundaries = append(boundaries, partitionBoundary{
			partition:  partition,
			checkpoint: checkpointOffset,
		})
	}

	partitionToCheckpoint := make(map[int]int64)
	for _, b := range boundaries {
		partitionToCheckpoint[b.partition] = b.checkpoint
	}

	// 1. Start concurrent OutputQueue draining goroutines for all engines (Issue #1 & v9.6 Deadlock Resolution)
	barrierErrChan := make(chan error, len(r.manager.All()))
	var barrierWg sync.WaitGroup

	for _, engine := range r.manager.All() {
		barrierWg.Add(1)
		go func(e *market.MarketEngine) {
			defer barrierWg.Done()
			expectedCheckpoint := partitionToCheckpoint[e.Partition()]
			for {
				select {
				case res, ok := <-e.OutputQueue:
					if !ok {
						barrierErrChan <- fmt.Errorf("OutputQueue closed unexpectedly for market %s", e.MarketID)
						return
					}
					if res.BarrierReached {
						if res.SourcePosition.Topic != topic || res.SourcePosition.Partition != e.Partition() || res.BarrierOffset != expectedCheckpoint {
							barrierErrChan <- fmt.Errorf("recovery barrier partition/offset mismatch for market %s: expected %s/%d@%d, got %s/%d@%d",
								e.MarketID, topic, e.Partition(), expectedCheckpoint,
								res.SourcePosition.Topic, res.SourcePosition.Partition, res.BarrierOffset)
						}
						log.Printf("[recovery] drained and reached recovery barrier for market=%s", e.MarketID)
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}(engine)
	}

	// 2. Replay all partitions
	for _, b := range boundaries {
		err := r.replayPartition(ctx, topic, b.partition, b.checkpoint, marketLastSeenOffset)
		if err != nil {
			return fmt.Errorf("replay partition %d: %w", b.partition, err)
		}

		// Push recovery barrier to all engines on this partition (Issue #1)
		for _, engine := range r.manager.All() {
			if engine.Partition() == b.partition {
				engine.InputQueue <- market.InputEvent{
					Type:      market.EventRecoveryBarrier,
					Topic:     topic,
					Partition: b.partition,
					Offset:    b.checkpoint,
				}
			}
		}
	}

	// 3. Wait for all engines to reach their recovery barriers
	barrierWg.Wait()

	// Check if any goroutine reported an error
	select {
	case err := <-barrierErrChan:
		return err
	default:
	}

	// 5. Verify assertions (Issue #6: Offset alignment assertion after barrier drain)
	for _, engine := range r.manager.All() {
		lastSeen := marketLastSeenOffset[engine.MarketID]
		if engine.GetLastAppliedOffset() != lastSeen {
			return fmt.Errorf("recovery offset mismatch for market %s: engine offset %d != last seen offset %d",
				engine.MarketID, engine.GetLastAppliedOffset(), lastSeen)
		}
	}

	for _, engine := range r.manager.All() {
		dbSeq, err := r.loadMarketSequence(ctx, engine.MarketID)
		if err != nil {
			return fmt.Errorf("load market sequence for %s: %w", engine.MarketID, err)
		}

		if engine.GetSequence() != dbSeq {
			return fmt.Errorf("recovery consistency mismatch for market %s: engine sequence %d != db sequence %d",
				engine.MarketID, engine.GetSequence(), dbSeq)
		}
		log.Printf("[recovery] asserted sequence consistency sequence=%d for market=%s", dbSeq, engine.MarketID)

		engine.SetLive()
		log.Printf("[recovery] market=%s recovered — now LIVE", engine.MarketID)
	}

	for _, b := range boundaries {
		if b.checkpoint < 0 {
			continue
		}
		if r.groupID != "" {
			if err := r.commitKafkaGroupOffset(ctx, topic, b.partition, b.checkpoint); err != nil {
				return fmt.Errorf("align kafka group offset (partition=%d offset=%d): %w", b.partition, b.checkpoint, err)
			}
		}
	}

	return nil
}
