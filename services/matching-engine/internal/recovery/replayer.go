package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/redis/go-redis/v9"

	intkafka "tradedrift/services/matching-engine/internal/kafka"
	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/orderbook"
)

type ReplayerDB interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type ReplayerRedis interface {
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd
}

type KafkaReader interface {
	SetOffset(offset int64) error
	FetchMessage(ctx context.Context) (kafkago.Message, error)
	Close() error
}

// Replayer handles crash recovery for all MarketEngines.
type Replayer struct {
	brokers                []string
	groupID                string
	db                     ReplayerDB
	redis                  ReplayerRedis
	manager                *market.MarketManager
	newReaderFunc          func(brokers []string, topic string, partition int) KafkaReader
	discoverPartitionsFunc func(topic string) ([]int, error)
	queryHWMFunc           func(ctx context.Context, topic string, partition int) (int64, error)
}

// NewReplayer creates a Replayer.
func NewReplayer(brokers []string, groupID string, db ReplayerDB, rdb ReplayerRedis, manager *market.MarketManager) *Replayer {
	r := &Replayer{
		brokers: brokers,
		groupID: groupID,
		db:      db,
		redis:   rdb,
		manager: manager,
	}

	r.newReaderFunc = func(brokers []string, topic string, partition int) KafkaReader {
		return kafkago.NewReader(kafkago.ReaderConfig{
			Brokers:   brokers,
			Topic:     topic,
			Partition: partition,
			MinBytes:  1,
			MaxBytes:  10e6,
			MaxWait:   2 * time.Second,
		})
	}

	r.discoverPartitionsFunc = func(topic string) ([]int, error) {
		conn, err := kafkago.Dial("tcp", brokers[0])
		if err != nil {
			return nil, fmt.Errorf("dial broker %s: %w", brokers[0], err)
		}
		defer conn.Close()

		partitions, err := conn.ReadPartitions(topic)
		if err != nil {
			return nil, fmt.Errorf("read partitions for topic %s: %w", topic, err)
		}

		ids := make([]int, 0, len(partitions))
		for _, p := range partitions {
			ids = append(ids, p.ID)
		}
		return ids, nil
	}

	r.queryHWMFunc = func(ctx context.Context, topic string, partition int) (int64, error) {
		conn, err := kafkago.DialLeader(ctx, "tcp", brokers[0], topic, partition)
		if err != nil {
			return 0, err
		}
		defer conn.Close()
		return conn.ReadLastOffset()
	}

	return r
}

// OverrideDiscoveryAndReader overrides partition discovery, reader creation, and HWM querying for testing.
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

// replayPartition replays a partition from startOffset to checkpointOffset.
func (r *Replayer) replayPartition(ctx context.Context, topic string, partition int, checkpointOffset int64, marketLastSeenOffset map[string]int64) error {
	// 1. Find all registered markets on this partition
	var marketsOnPartition []*market.MarketEngine
	for _, engine := range r.manager.All() {
		if engine.Partition() == partition {
			marketsOnPartition = append(marketsOnPartition, engine)
		}
	}

	if len(marketsOnPartition) == 0 {
		log.Printf("[recovery] partition=%d — no markets registered on this partition", partition)
		return nil
	}

	if checkpointOffset < 0 {
		log.Printf("[recovery] partition=%d — no checkpoint exists, nothing to replay", partition)
		for _, engine := range marketsOnPartition {
			marketLastSeenOffset[engine.MarketID] = -1
		}
		return nil
	}

	// Pre-flight HWM validation (Issue #3)
	logEndOffset, err := r.queryHWMFunc(ctx, topic, partition)
	if err != nil {
		return fmt.Errorf("query partition HWM (partition=%d): %w", partition, err)
	}
	if checkpointOffset >= logEndOffset {
		return fmt.Errorf("checkpoint offset %d is at or beyond Kafka log-end offset %d (partition=%d) — recovery aborted",
			checkpointOffset, logEndOffset, partition)
	}

	// 2. Load latest snapshots for each market
	anyMissingSnapshot := false
	var minSnapshotOffset int64 = -1

	for _, engine := range marketsOnPartition {
		var latestOffset int64
		err := r.db.QueryRow(ctx, "SELECT COALESCE(MAX(\"offset\"), -1) FROM market_snapshots WHERE market_id = $1", engine.MarketID).Scan(&latestOffset)
		if err == nil && latestOffset > checkpointOffset {
			return fmt.Errorf("market %s has snapshot at offset %d beyond partition checkpoint %d: %w",
				engine.MarketID, latestOffset, checkpointOffset, orderbook.ErrSnapshotBeyondCheckpoint)
		}

		snap, checksum, err := r.loadLatestSnapshot(ctx, engine.MarketID, checkpointOffset)
		if err != nil {
			return fmt.Errorf("load latest snapshot for %s: %w", engine.MarketID, err)
		}

		if snap == nil {
			anyMissingSnapshot = true
			log.Printf("[recovery] market=%s has no valid snapshot <= checkpoint=%d", engine.MarketID, checkpointOffset)
		} else {
			if err := engine.RestoreFromSnapshot(*snap, checksum, checkpointOffset); err != nil {
				return fmt.Errorf("restore engine from snapshot (market=%s offset=%d): %w", engine.MarketID, snap.Offset, err)
			}
			log.Printf("[recovery] market=%s restored from snapshot sequence=%d offset=%d", engine.MarketID, snap.Sequence, snap.Offset)

			// Invariant (Issue B): Initialize marketLastSeenOffset to snapshot.offset
			marketLastSeenOffset[engine.MarketID] = snap.Offset

			if minSnapshotOffset == -1 || snap.Offset < minSnapshotOffset {
				minSnapshotOffset = snap.Offset
			}
		}
	}

	// 3. Determine start offset for replay
	var startOffset int64 = 0
	if !anyMissingSnapshot {
		startOffset = minSnapshotOffset + 1
	} else {
		log.Printf("[recovery] partition=%d has market(s) missing snapshots. Replaying from offset 0", partition)
		startOffset = 0
	}

	// Invariant (Issue C): If no snapshot exists, initialize marketLastSeenOffset to startOffset - 1
	for _, engine := range marketsOnPartition {
		if _, ok := marketLastSeenOffset[engine.MarketID]; !ok {
			marketLastSeenOffset[engine.MarketID] = startOffset - 1
		}
	}

	if startOffset > checkpointOffset {
		log.Printf("[recovery] partition=%d — already up to checkpoint (startOffset=%d checkpoint=%d), nothing to replay",
			partition, startOffset, checkpointOffset)
		return nil
	}

	log.Printf("[recovery] partition=%d — replaying offsets %d → %d", partition, startOffset, checkpointOffset)

	reader := r.newReaderFunc(r.brokers, topic, partition)
	defer reader.Close()

	if err := reader.SetOffset(startOffset); err != nil {
		return fmt.Errorf("seek to offset %d on partition %d: %w", startOffset, partition, err)
	}

	// 4. Replay loop with offset continuity check
	expectedOffset := startOffset
	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			return fmt.Errorf("fetch message on partition %d: %w", partition, err)
		}

		// INVARIANT (Issue #5 & #9): Verify partition offset continuity
		if msg.Offset != expectedOffset {
			return fmt.Errorf("partition offset continuity gap detected on partition %d: expected %d, got %d",
				partition, expectedOffset, msg.Offset)
		}
		expectedOffset++

		marketID, routed, err := r.routeMessage(msg)
		if err != nil {
			return fmt.Errorf("corrupt event during recovery (partition=%d offset=%d): %w",
				partition, msg.Offset, err)
		}
		if routed && marketID != "" {
			marketLastSeenOffset[marketID] = msg.Offset
		}

		if msg.Offset >= checkpointOffset {
			break
		}
	}

	return nil
}

func (r *Replayer) routeMessage(msg kafkago.Message) (string, bool, error) {
	var routed bool
	var routedMarketID string

	route := func(marketID string) chan market.InputEvent {
		engine := r.manager.Get(marketID)
		if engine == nil {
			return nil
		}
		if msg.Offset <= engine.GetLastAppliedOffset() {
			routedMarketID = marketID
			return engine.InputQueue
		}
		routed = true
		routedMarketID = marketID
		return engine.InputQueue
	}

	_, err := intkafka.HandleOrderCommand(msg, route)
	if err != nil {
		return "", false, err
	}
	return routedMarketID, routed, nil
}

func (r *Replayer) commitKafkaGroupOffset(ctx context.Context, topic string, partition int, offset int64) error {
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers: r.brokers,
		Topic:   topic,
		GroupID: r.groupID,
	})
	defer reader.Close()

	if err := reader.CommitMessages(ctx, kafkago.Message{
		Topic:     topic,
		Partition: partition,
		Offset:    offset,
	}); err != nil {
		return fmt.Errorf("kafka commit messages: %w", err)
	}

	log.Printf("[recovery] aligned kafka group offset for topic=%s partition=%d to %d", topic, partition, offset)
	return nil
}

func (r *Replayer) loadCheckpoint(ctx context.Context, topic string, partition int) (int64, error) {
	query := `
		SELECT "offset" FROM kafka_checkpoints
		WHERE topic = $1 AND partition = $2`

	var offset int64
	err := r.db.QueryRow(ctx, query, topic, partition).Scan(&offset)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return -1, nil
		}
		return 0, fmt.Errorf("query checkpoint (topic=%s partition=%d): %w", topic, partition, err)
	}
	return offset, nil
}

func (r *Replayer) loadLatestSnapshot(ctx context.Context, marketID string, checkpoint int64) (*orderbook.BookSnapshot, []byte, error) {
	query := `
		SELECT market_id, sequence, partition, "offset", schema_version, snapshot, checksum FROM market_snapshots
		WHERE market_id = $1 AND "offset" <= $2
		ORDER BY "offset" DESC, sequence DESC LIMIT 1`

	var dbMarketID string
	var dbSequence int64
	var dbPartition int
	var dbOffset int64
	var dbSchemaVersion int
	var snapJSON []byte
	var checksum []byte

	err := r.db.QueryRow(ctx, query, marketID, checkpoint).Scan(
		&dbMarketID, &dbSequence, &dbPartition, &dbOffset, &dbSchemaVersion, &snapJSON, &checksum,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("query snapshot (market=%s offset<=%d): %w", marketID, checkpoint, err)
	}

	var snap orderbook.BookSnapshot
	if err := json.Unmarshal(snapJSON, &snap); err != nil {
		return nil, nil, fmt.Errorf("unmarshal snapshot struct: %w", err)
	}

	// Validate DB columns against snapshot payload properties to guard against row corruption (Issue 5)
	if dbMarketID != snap.MarketID ||
		dbSequence != int64(snap.Sequence) ||
		dbPartition != snap.Partition ||
		dbOffset != snap.Offset ||
		dbSchemaVersion != int(snap.SchemaVersion) {
		return nil, nil, fmt.Errorf("snapshot metadata corruption: DB columns (market=%s, seq=%d, partition=%d, offset=%d, version=%d) do not match JSON snapshot content (market=%s, seq=%d, partition=%d, offset=%d, version=%d)",
			dbMarketID, dbSequence, dbPartition, dbOffset, dbSchemaVersion,
			snap.MarketID, snap.Sequence, snap.Partition, snap.Offset, snap.SchemaVersion)
	}

	return &snap, checksum, nil
}

func (r *Replayer) loadMarketSequence(ctx context.Context, marketID string) (uint64, error) {
	const query = `SELECT sequence FROM market_sequences WHERE market_id = $1`
	var seq uint64
	err := r.db.QueryRow(ctx, query, marketID).Scan(&seq)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("query market_sequence (market=%s): %w", marketID, err)
	}
	return seq, nil
}




