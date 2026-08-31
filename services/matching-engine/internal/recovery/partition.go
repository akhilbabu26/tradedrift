package recovery

import (
	"context"
	"fmt"
	"log"

	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/orderbook"
	intkafka "tradedrift/services/matching-engine/internal/kafka"

	kafkago "github.com/segmentio/kafka-go"
)

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
	if logEndOffset == 0 && checkpointOffset >= 0 {
		// Topic was wiped (e.g. deleted and recreated). The checkpoint in Postgres is now
		// ahead of Kafka's log-end offset. Treat this as a clean-slate: reset the stale
		// checkpoint and replay from scratch, identical to a fresh first-boot.
		log.Printf("[recovery] partition=%d — Kafka topic was reset (log-end=0, checkpoint=%d). Clearing stale checkpoint and starting fresh.",
			partition, checkpointOffset)
		if _, err := r.db.Exec(ctx,
			`DELETE FROM kafka_checkpoints WHERE topic = $1 AND partition = $2`,
			topic, partition,
		); err != nil {
			return fmt.Errorf("clear stale checkpoint (partition=%d): %w", partition, err)
		}
		if _, err := r.db.Exec(ctx,
			`DELETE FROM market_snapshots WHERE market_id IN (SELECT market_id FROM market_sequences WHERE partition = $1)`,
			partition,
		); err != nil {
			return fmt.Errorf("clear stale snapshots (partition=%d): %w", partition, err)
		}
		if _, err := r.db.Exec(ctx,
			`DELETE FROM market_sequences WHERE partition = $1`,
			partition,
		); err != nil {
			return fmt.Errorf("clear stale sequences (partition=%d): %w", partition, err)
		}
		for _, engine := range marketsOnPartition {
			marketLastSeenOffset[engine.MarketID] = -1
		}
		return nil
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
