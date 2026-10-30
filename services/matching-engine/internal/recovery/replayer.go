
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
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Begin(ctx context.Context) (pgx.Tx, error)
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
	resetAuthorizer        ResetAuthorizer
	sequenceReconciler     DownstreamSequenceReconciler
	leaseChecker           LeaseChecker
	commitGroupOffsetFunc  func(ctx context.Context, topic string, partition int, offset int64) error
	discoverPartitionsFunc func(topic string) ([]int, error)
	newReaderFunc          func(brokers []string, topic string, partition int) KafkaReader
	queryHWMFunc           func(ctx context.Context, topic string, partition int) (int64, error)
	queryLWMFunc           func(ctx context.Context, topic string, partition int) (int64, error)
}

// SetLeaseChecker configures the lease checker used to protect reset operations.
func (r *Replayer) SetLeaseChecker(lc LeaseChecker) {
	r.leaseChecker = lc
}

// NewReplayer returns a configured Replayer.
func NewReplayer(brokers []string, groupID string, db pgxConn, redis redisConn, manager *market.MarketManager) *Replayer {
	r := &Replayer{
		brokers:         brokers,
		groupID:         groupID,
		db:              db,
		redis:           redis,
		manager:         manager,
		resetAuthorizer: NewEnvResetAuthorizer(),
	}

	r.commitGroupOffsetFunc = func(ctx context.Context, topic string, partition int, offset int64) error {
		reader := kafkago.NewReader(kafkago.ReaderConfig{
			Brokers: r.brokers,
			Topic:   topic,
			GroupID: r.groupID,
		})
		defer reader.Close()

		return reader.CommitMessages(ctx, kafkago.Message{
			Topic:     topic,
			Partition: partition,
			Offset:    offset,
		})
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

	r.queryLWMFunc = func(ctx context.Context, topic string, partition int) (int64, error) {
		conn, err := kafkago.DialLeader(ctx, "tcp", brokers[0], topic, partition)
		if err != nil {
			return 0, err
		}
		defer conn.Close()
		firstOffset, err := conn.ReadFirstOffset()
		if err != nil {
			return 0, err
		}
		return firstOffset, nil
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
	r.queryLWMFunc = func(ctx context.Context, topic string, partition int) (int64, error) {
		return 0, nil
	}
	r.commitGroupOffsetFunc = func(ctx context.Context, topic string, partition int, offset int64) error {
		return nil
	}
}

// OverrideLWM allows substituting mock LWM for tests.
func (r *Replayer) OverrideLWM(queryLWM func(ctx context.Context, topic string, partition int) (int64, error)) {
	r.queryLWMFunc = queryLWM
}

// SetResetAuthorizer sets the authorizer for explicit empty-book resets.
func (r *Replayer) SetResetAuthorizer(auth ResetAuthorizer) {
	r.resetAuthorizer = auth
}

// OverrideCommitGroupOffset allows substituting mock commit for unit tests.
func (r *Replayer) OverrideCommitGroupOffset(fn func(ctx context.Context, topic string, partition int, offset int64) error) {
	r.commitGroupOffsetFunc = fn
}

// OverrideSequenceReconciler allows substituting mock sequence reconciler for tests.
func (r *Replayer) OverrideSequenceReconciler(reconciler DownstreamSequenceReconciler) {
	r.sequenceReconciler = reconciler
}

func (r *Replayer) reconcileDownstreamSequence(ctx context.Context, marketID string) (uint64, error) {
	if r.sequenceReconciler != nil {
		return r.sequenceReconciler.ReconcileSequence(ctx, marketID)
	}
	return NewDefaultSequenceReconciler(r.db).ReconcileSequence(ctx, marketID)
}

// ReplayAll recovers all MarketEngines across all Kafka partitions up to their checkpoints.
func (r *Replayer) ReplayAll(ctx context.Context, engineWg *sync.WaitGroup) (retErr error) {
	defer func() {
		if retErr != nil {
			for _, engine := range r.manager.All() {
				engine.TriggerFatalHalt()
			}
		}
	}()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("recovery aborted: context cancelled: %w", err)
	}

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

	// 1. Start concurrent OutputQueue draining goroutines for all engines (Issue #1 & v9.6 Deadlock Resolution)
	barrierErrChan := make(chan error, len(r.manager.All()))
	var barrierWg sync.WaitGroup

	for _, engine := range r.manager.All() {
		barrierWg.Add(1)
		go func(e *market.MarketEngine) {
			defer barrierWg.Done()
			for {
				select {
				case res, ok := <-e.OutputQueue:
					if !ok {
						barrierErrChan <- fmt.Errorf("OutputQueue closed unexpectedly for market %s", e.MarketID)
						return
					}
					if res.BarrierReached {
						if res.SourcePosition.Topic != topic || res.SourcePosition.Partition != e.Partition() {
							barrierErrChan <- fmt.Errorf("recovery barrier partition mismatch for market %s: expected %s/%d, got %s/%d",
								e.MarketID, topic, e.Partition(), res.SourcePosition.Topic, res.SourcePosition.Partition)
							return
						}
						log.Printf("[recovery] drained and reached recovery barrier for market=%s at offset=%d", e.MarketID, res.BarrierOffset)
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}(engine)
	}

	// 2. Replay all partitions independently
	partitionErrors := make(map[int]error)
	for idx, b := range boundaries {
		effectiveCheckpoint, err := r.replayPartition(ctx, topic, b.partition, b.checkpoint, marketLastSeenOffset)
		if err != nil {
			log.Printf("[recovery] ERROR: partition %d recovery failed: %v", b.partition, err)
			partitionErrors[b.partition] = err

			// Mark all markets on this partition as ModeBlocked
			for _, engine := range r.manager.All() {
				if engine.Partition() == b.partition {
					engine.SetBlocked(err)
					log.Printf("[recovery] market %s on partition %d is now ModeBlocked (reason: %v)",
						engine.MarketID, b.partition, err)
					if r.redis != nil {
						_ = r.redis.Set(ctx, "market:halted:"+engine.MarketID, "1", 0).Err()
					}
					// Push recovery barrier to engine InputQueue so barrier drain goroutine completes cleanly
					engine.InputQueue <- market.InputEvent{
						Type:      market.EventRecoveryBarrier,
						Topic:     topic,
						Partition: b.partition,
						Offset:    b.checkpoint,
					}
				}
			}
			continue
		}

		boundaries[idx].checkpoint = effectiveCheckpoint

		// Push recovery barrier to all engines on this partition (Issue #1)
		for _, engine := range r.manager.All() {
			if engine.Partition() == b.partition {
				engine.InputQueue <- market.InputEvent{
					Type:      market.EventRecoveryBarrier,
					Topic:     topic,
					Partition: b.partition,
					Offset:    effectiveCheckpoint,
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
		if engine.IsBlocked() {
			log.Printf("[recovery] skipping sequence/offset assertions for BLOCKED market %s", engine.MarketID)
			continue
		}

		lastSeen := marketLastSeenOffset[engine.MarketID]
		if engine.GetLastAppliedOffset() != lastSeen {
			return fmt.Errorf("recovery offset mismatch for market %s: engine offset %d != last seen offset %d",
				engine.MarketID, engine.GetLastAppliedOffset(), lastSeen)
		}

		dbSeq, err := r.loadMarketSequence(ctx, engine.MarketID)
		if err != nil {
			return fmt.Errorf("load market sequence for %s: %w", engine.MarketID, err)
		}

		if engine.GetSequence() < dbSeq {
			return fmt.Errorf("recovery consistency mismatch for market %s: engine sequence %d < db sequence %d",
				engine.MarketID, engine.GetSequence(), dbSeq)
		}
		if engine.GetSequence() > dbSeq {
			log.Printf("[recovery] engine sequence %d advanced beyond stale DB sequence %d for market %s — updating DB",
				engine.GetSequence(), dbSeq, engine.MarketID)
			if err := r.saveMarketSequence(ctx, engine.MarketID, engine.GetSequence()); err != nil {
				return fmt.Errorf("update market sequence for %s: %w", engine.MarketID, err)
			}
		} else {
			log.Printf("[recovery] asserted sequence consistency sequence=%d for market=%s", dbSeq, engine.MarketID)
		}

		// Durable Reconciliation Gate (P0):
		// An engine must NOT transition to ModeLive if it has undergone a reset that is unapproved,
		// if the approval generation is stale/mismatched, if the checksum is invalid,
		// if the recovered book sequence or checkpoint diverges from the authorized reset boundary,
		// or if it is currently reconciling.
		isPendingRecon := engine.IsReconciling()
		if !isPendingRecon {
			// Check if this market ever had a reset recorded
			var resetGen string
			var resetLogEndOffset int64
			var resetSeq uint64
			resetErr := r.db.QueryRow(ctx, "SELECT generation, log_end_offset, sequence FROM market_reset_audit WHERE market_id = $1 ORDER BY authorized_at DESC LIMIT 1", engine.MarketID).Scan(&resetGen, &resetLogEndOffset, &resetSeq)
			if resetErr == nil && resetGen != "" {
				// A reset was performed for this market. Verify durable reconciliation approval and boundary consistency.
				var reconGen, reconStatus, reconChecksum string
				reconErr := r.db.QueryRow(ctx, "SELECT generation, status, manifest_checksum FROM market_reconciliation_audit WHERE market_id = $1 ORDER BY created_at DESC LIMIT 1", engine.MarketID).Scan(&reconGen, &reconStatus, &reconChecksum)
				if reconErr != nil || reconStatus != "APPROVED" || reconGen != resetGen {
					isPendingRecon = true
					log.Printf("[recovery] market=%s reset generation=%s lacks matching APPROVED reconciliation (reconGen=%s status=%s) — entering ModeReconciling",
						engine.MarketID, resetGen, reconGen, reconStatus)
				} else if len(reconChecksum) != 64 || !isHex(reconChecksum) {
					isPendingRecon = true
					log.Printf("[recovery] market=%s approved reconciliation has invalid manifest checksum %q — entering ModeReconciling",
						engine.MarketID, reconChecksum)
				} else if engine.GetSequence() != resetSeq {
					isPendingRecon = true
					log.Printf("[recovery] market=%s sequence discrepancy: recovered book sequence %d != authorized reset sequence %d — entering ModeReconciling",
						engine.MarketID, engine.GetSequence(), resetSeq)
				} else {
					// Verify checkpoint boundary consistency
					var dbCheckpoint int64 = -1
					_ = r.db.QueryRow(ctx, `SELECT "offset" FROM kafka_checkpoints WHERE topic = $1 AND partition = $2`,
						topic, engine.Partition()).Scan(&dbCheckpoint)
					if dbCheckpoint >= 0 && dbCheckpoint < resetLogEndOffset-1 {
						isPendingRecon = true
						log.Printf("[recovery] market=%s checkpoint boundary discrepancy: checkpoint %d is behind reset boundary %d — entering ModeReconciling",
							engine.MarketID, dbCheckpoint, resetLogEndOffset)
					}
				}
			} else {
				// No reset record found, but check if there's any pending reconciliation audit
				var reconStatus string
				reconErr := r.db.QueryRow(ctx, "SELECT status FROM market_reconciliation_audit WHERE market_id = $1 ORDER BY created_at DESC LIMIT 1", engine.MarketID).Scan(&reconStatus)
				if reconErr == nil && reconStatus == "PENDING" {
					isPendingRecon = true
				}
			}
		}

		if isPendingRecon {
			if !engine.IsReconciling() {
				if err := engine.SetReconciling("unapproved reset; awaiting downstream reconciliation approval"); err != nil {
					return fmt.Errorf("set market %s to reconciling: %w", engine.MarketID, err)
				}
			}
			log.Printf("[recovery] market=%s has pending reconciliation approval — recovered into ModeReconciling (not LIVE)", engine.MarketID)
		} else {
			if err := engine.SetLive(); err != nil {
				return fmt.Errorf("set market %s live: %w", engine.MarketID, err)
			}
			log.Printf("[recovery] market=%s recovered — now LIVE", engine.MarketID)
		}
	}

	for _, b := range boundaries {
		if _, failed := partitionErrors[b.partition]; failed {
			log.Printf("[recovery] skipping kafka group offset commit for failed partition %d", b.partition)
			continue
		}
		if b.checkpoint < 0 {
			continue
		}
		if r.groupID != "" {
			if err := r.commitKafkaGroupOffset(ctx, topic, b.partition, b.checkpoint); err != nil {
				return fmt.Errorf("align kafka group offset (partition=%d offset=%d): %w", b.partition, b.checkpoint, err)
			}
		}
	}

	// Invariant: If ALL markets failed, fail-closed the entire process
	var activeCount int
	for _, engine := range r.manager.All() {
		if engine.Mode() == market.ModeLive || engine.Mode() == market.ModeReconciling {
			activeCount++
		}
	}
	if activeCount == 0 && len(r.manager.All()) > 0 {
		return fmt.Errorf("recovery failed for all %d market(s): partition errors: %v",
			len(r.manager.All()), partitionErrors)
	}

	return nil
}
