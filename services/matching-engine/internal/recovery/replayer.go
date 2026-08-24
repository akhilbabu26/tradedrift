package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
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

	return r
}

// OverrideDiscoveryAndReader overrides partition discovery and reader creation for testing.
func (r *Replayer) OverrideDiscoveryAndReader(
	discover func(topic string) ([]int, error),
	newReader func(brokers []string, topic string, partition int) KafkaReader,
) {
	r.discoverPartitionsFunc = discover
	r.newReaderFunc = newReader
}

// ReplayAll recovers all MarketEngines across all Kafka partitions up to their checkpoints.
func (r *Replayer) ReplayAll(ctx context.Context, engineWg *sync.WaitGroup) error {
	topic := intkafka.TopicOrderCommands

	partitions, err := r.discoverPartitionsFunc(topic)
	if err != nil {
		return fmt.Errorf("discover partitions for topic %s: %w", topic, err)
	}
	log.Printf("[recovery] discovered %d partition(s) for topic=%s: %v", len(partitions), topic, partitions)

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

	for _, partition := range partitions {
		checkpointOffset, err := r.loadCheckpoint(ctx, topic, partition)
		if err != nil {
			return fmt.Errorf("load checkpoint (partition=%d): %w", partition, err)
		}

		err = r.replayPartition(ctx, topic, partition, checkpointOffset)
		if err != nil {
			return fmt.Errorf("replay partition %d: %w", partition, err)
		}

		// Push recovery barrier to all engines on this partition (Issue #1)
		for _, engine := range r.manager.All() {
			if engine.Partition() == partition {
				engine.InputQueue <- market.InputEvent{
					Type: market.EventRecoveryBarrier,
				}
			}
		}

		boundaries = append(boundaries, partitionBoundary{
			partition:  partition,
			checkpoint: checkpointOffset,
		})
	}

	// Drain all engines until their respective recovery barriers are reached (Issue #1)
	for _, engine := range r.manager.All() {
		for {
			select {
			case res := <-engine.OutputQueue:
				if res.BarrierReached {
					goto engineDone
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	engineDone:
		log.Printf("[recovery] drained and reached recovery barrier for market=%s", engine.MarketID)
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

		if err := r.pushFreshDepth(ctx, engine); err != nil {
			return fmt.Errorf("push fresh depth for %s: %w", engine.MarketID, err)
		}

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
func (r *Replayer) replayPartition(ctx context.Context, topic string, partition int, checkpointOffset int64) error {
	if checkpointOffset < 0 {
		log.Printf("[recovery] partition=%d — no checkpoint exists, nothing to replay", partition)
		return nil
	}

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

		_, _, err = r.routeMessage(msg)
		if err != nil {
			return fmt.Errorf("corrupt event during recovery (partition=%d offset=%d): %w",
				partition, msg.Offset, err)
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
		SELECT snapshot, checksum FROM market_snapshots
		WHERE market_id = $1 AND "offset" <= $2
		ORDER BY sequence DESC LIMIT 1`

	var snapJSON []byte
	var checksum []byte
	err := r.db.QueryRow(ctx, query, marketID, checkpoint).Scan(&snapJSON, &checksum)
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

func (r *Replayer) highWatermark(topic string, partition int) (int64, error) {
	addr := r.brokers[0]
	conn, err := kafkago.DialLeader(context.Background(), "tcp", addr, topic, partition)
	if err != nil {
		rawConn, dialErr := net.Dial("tcp", addr)
		if dialErr != nil {
			return 0, fmt.Errorf("dial leader (topic=%s partition=%d): %w", topic, partition, err)
		}
		rawConn.Close()
		return 0, fmt.Errorf("dial leader (topic=%s partition=%d): %w", topic, partition, err)
	}
	conn.Close()

	last, err := conn.ReadLastOffset()
	if err != nil {
		return 0, fmt.Errorf("read last offset (topic=%s partition=%d): %w", topic, partition, err)
	}
	return last, nil
}

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

	res := r.redis.Set(ctx, "depth:"+snap.MarketID, b, 0)
	if res == nil {
		return fmt.Errorf("redis set returned nil cmd")
	}
	return res.Err()
}
