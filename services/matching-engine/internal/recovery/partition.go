package recovery

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	kafkago "github.com/segmentio/kafka-go"

	intkafka "tradedrift/services/matching-engine/internal/kafka"
	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/orderbook"
)

// replayPartition replays a partition from startOffset to checkpointOffset.
// Returns the effective partition checkpoint offset and any error encountered.
func (r *Replayer) replayPartition(ctx context.Context, topic string, partition int, checkpointOffset int64, marketLastSeenOffset map[string]int64) (int64, error) {
	// 1. Find all registered markets on this partition
	var marketsOnPartition []*market.MarketEngine
	for _, engine := range r.manager.All() {
		if engine.Partition() == partition {
			marketsOnPartition = append(marketsOnPartition, engine)
		}
	}

	if len(marketsOnPartition) == 0 {
		log.Printf("[recovery] partition=%d — no markets registered on this partition", partition)
		return checkpointOffset, nil
	}

	if checkpointOffset < 0 {
		log.Printf("[recovery] partition=%d — no checkpoint exists, verifying clean state and durability records", partition)
		for _, engine := range marketsOnPartition {
			var snapCount int64
			err := r.db.QueryRow(ctx, "SELECT COUNT(*) FROM market_snapshots WHERE market_id = $1", engine.MarketID).Scan(&snapCount)
			if err != nil {
				return -1, fmt.Errorf("verify clean state: query snapshot count for market %s: %w", engine.MarketID, err)
			}
			if snapCount > 0 {
				return -1, fmt.Errorf("FATAL: partition=%d has no checkpoint in kafka_checkpoints, but market %s has %d snapshot(s). Refusing to auto-wipe durable state. Operator must investigate.",
					partition, engine.MarketID, snapCount)
			}
		}

		// Phase 4: Empty-book bootstrap safety check against Kafka topic watermarks
		logEndOffset, err := r.queryHWMFunc(ctx, topic, partition)
		if err != nil {
			return -1, fmt.Errorf("query partition HWM for uncheckpointed partition %d: %w", partition, err)
		}

		// Day-1 clean bootstrap: Kafka log is genuinely empty (logEndOffset == 0)
		if logEndOffset == 0 {
			for _, engine := range marketsOnPartition {
				var seq uint64
				err = r.db.QueryRow(ctx, "SELECT sequence FROM market_sequences WHERE market_id = $1", engine.MarketID).Scan(&seq)
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return -1, fmt.Errorf("verify clean state: query sequence for market %s: %w", engine.MarketID, err)
				}
				if err == nil && seq > 0 {
					return -1, fmt.Errorf("FATAL: partition=%d has no checkpoint in kafka_checkpoints and Kafka is empty (HWM=0), but market %s has sequence %d in market_sequences. Refusing to auto-wipe durable state. Operator must investigate.",
						partition, engine.MarketID, seq)
				}
				marketLastSeenOffset[engine.MarketID] = -1
			}
			log.Printf("[recovery] partition=%d is genuinely empty (logEndOffset=0), clean Day-1 bootstrap baseline=-1", partition)
			return -1, nil
		}

		// Non-empty log (logEndOffset > 0): REQUIRES explicit authorized reset!
		baseline, handled, err := r.tryAuthorizedReset(ctx, topic, partition, marketsOnPartition, logEndOffset, checkpointOffset, marketLastSeenOffset)
		if !handled {
			return -1, fmt.Errorf("FATAL: partition=%d has no checkpoint in PostgreSQL, but Kafka topic %s has %d messages (logEndOffset=%d). "+
				"Refusing to auto-bootstrap market with an empty book without explicit operator reset authorization.", partition, topic, logEndOffset, logEndOffset)
		}
		if err != nil {
			return -1, err
		}
		return baseline, nil
	}

	// Pre-flight HWM and LWM validation (Issue #3)
	logEndOffset, err := r.queryHWMFunc(ctx, topic, partition)
	if err != nil {
		return -1, fmt.Errorf("query partition HWM (partition=%d): %w", partition, err)
	}

	var earliestOffset int64 = 0
	if r.queryLWMFunc != nil {
		lwm, err := r.queryLWMFunc(ctx, topic, partition)
		if err != nil {
			return -1, fmt.Errorf("query partition LWM (partition=%d): %w", partition, err)
		}
		earliestOffset = lwm
	}

	// Invariant 1: Checkpoint must NEVER exceed or equal Kafka's logEndOffset (HWM).
	// Since checkpointOffset represents an inclusive offset of a processed message and logEndOffset
	// is the exclusive next offset to be written, C >= L means PostgreSQL recorded an offset Kafka has never produced.
	if checkpointOffset >= logEndOffset && checkpointOffset >= 0 {
		return -1, fmt.Errorf(
			"FATAL: partition=%d checkpoint offset %d is inconsistent with Kafka log-end offset %d "+
				"(checkpoint >= logEndOffset). This indicates Kafka data loss, an uninitialized topic/cluster, "+
				"or an accidental database restore from another environment. Refusing to auto-reset durable state. Operator must investigate.",
			partition, checkpointOffset, logEndOffset,
		)
	}

	// Invariant 2: Next required live intake offset (checkpointOffset + 1) must not have been purged.
	// If C + 1 < earliestOffset, Kafka retention has purged unconsumed commands that the engine never processed.
	if (checkpointOffset+1 < earliestOffset && earliestOffset > 0) && checkpointOffset >= 0 {
		if baseline, handled, err := r.tryAuthorizedReset(ctx, topic, partition, marketsOnPartition, logEndOffset, checkpointOffset, marketLastSeenOffset); handled {
			if err != nil {
				return -1, err
			}
			return baseline, nil
		}
		return -1, fmt.Errorf(
			"FATAL: partition=%d checkpoint offset %d is behind Kafka earliest available offset %d "+
				"(next expected intake offset %d < earliest %d). Unconsumed commands were purged before processing. "+
				"Refusing to skip missing events. Operator must restore missing Kafka segments or reconcile state.",
			partition, checkpointOffset, earliestOffset, checkpointOffset+1, earliestOffset,
		)
	}

	// 2. Load latest snapshots for each market
	anyMissingSnapshot := false
	var minSnapshotOffset int64 = -1

	for _, engine := range marketsOnPartition {
		var latestOffset int64
		err := r.db.QueryRow(ctx, "SELECT COALESCE(MAX(\"offset\"), -1) FROM market_snapshots WHERE market_id = $1", engine.MarketID).Scan(&latestOffset)
		if err == nil && latestOffset > checkpointOffset {
			return -1, fmt.Errorf("market %s has snapshot at offset %d beyond partition checkpoint %d: %w",
				engine.MarketID, latestOffset, checkpointOffset, orderbook.ErrSnapshotBeyondCheckpoint)
		}

		snap, checksum, err := r.loadLatestSnapshot(ctx, engine.MarketID, checkpointOffset)
		if err != nil {
			return -1, fmt.Errorf("load latest snapshot for %s: %w", engine.MarketID, err)
		}

		if snap == nil {
			anyMissingSnapshot = true
			log.Printf("[recovery] market=%s has no valid snapshot <= checkpoint=%d", engine.MarketID, checkpointOffset)
		} else {
			if err := engine.RestoreFromSnapshot(*snap, checksum, checkpointOffset); err != nil {
				return -1, fmt.Errorf("restore engine from snapshot (market=%s offset=%d): %w", engine.MarketID, snap.Offset, err)
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
		return checkpointOffset, nil
	}

	if startOffset < earliestOffset && earliestOffset > 0 {
		if baseline, handled, err := r.tryAuthorizedReset(ctx, topic, partition, marketsOnPartition, logEndOffset, checkpointOffset, marketLastSeenOffset); handled {
			if err != nil {
				return -1, err
			}
			return baseline, nil
		}
		return -1, fmt.Errorf(
			"FATAL: partition=%d snapshot requires replay from offset %d up to checkpoint %d, "+
				"but Kafka earliest available offset is %d (messages %d..%d expired/purged). "+
				"Cannot recover — snapshot is too old for available Kafka history. "+
				"Operator must either restore a more recent snapshot or perform "+
				"a controlled state reset via admin tooling.",
			partition, startOffset, checkpointOffset, earliestOffset, startOffset, earliestOffset-1,
		)
	}

	log.Printf("[recovery] partition=%d — replaying offsets %d → %d", partition, startOffset, checkpointOffset)

	reader := r.newReaderFunc(r.brokers, topic, partition)
	defer reader.Close()

	if err := reader.SetOffset(startOffset); err != nil {
		return -1, fmt.Errorf("seek to offset %d on partition %d: %w", startOffset, partition, err)
	}

	// 4. Replay loop with offset continuity check
	expectedOffset := startOffset
	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			return -1, fmt.Errorf("fetch message on partition %d: %w", partition, err)
		}

		// INVARIANT (Issue #5 & #9): Verify partition offset continuity
		if msg.Offset != expectedOffset {
			return -1, fmt.Errorf("partition offset continuity gap detected on partition %d: expected %d, got %d",
				partition, expectedOffset, msg.Offset)
		}
		expectedOffset++

		marketID, routed, err := r.routeMessage(msg)
		if err != nil {
			return -1, fmt.Errorf("corrupt event during recovery (partition=%d offset=%d): %w",
				partition, msg.Offset, err)
		}
		if routed && marketID != "" {
			marketLastSeenOffset[marketID] = msg.Offset
		}

		if msg.Offset >= checkpointOffset {
			break
		}
	}

	return checkpointOffset, nil
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
	if r.commitGroupOffsetFunc != nil {
		if err := r.commitGroupOffsetFunc(ctx, topic, partition, offset); err != nil {
			return fmt.Errorf("kafka commit messages: %w", err)
		}
		log.Printf("[recovery] aligned kafka group offset for topic=%s partition=%d to %d", topic, partition, offset)
		return nil
	}

	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers: r.brokers,
		Topic:   topic,
		GroupID: r.groupID,
	})
	defer reader.Close()

	commitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := reader.CommitMessages(commitCtx, kafkago.Message{
		Topic:     topic,
		Partition: partition,
		Offset:    offset,
	}); err != nil {
		return fmt.Errorf("kafka commit messages: %w", err)
	}

	log.Printf("[recovery] aligned kafka group offset for topic=%s partition=%d to %d", topic, partition, offset)
	return nil
}
