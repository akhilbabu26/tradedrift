package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"

	"github.com/jackc/pgx/v5"

	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/orderbook"
)

// tryAuthorizedReset performs a safe, audited, single-use, precondition-guarded empty-book reset
// across ALL markets on a partition in a single atomic database transaction.
// Returns (baselineOffset, handled, error). If handled is false, no authorization was provided.
func (r *Replayer) tryAuthorizedReset(
	ctx context.Context,
	topic string,
	partition int,
	marketsOnPartition []*market.MarketEngine,
	logEndOffset int64,
	checkpointOffset int64,
	marketLastSeenOffset map[string]int64,
) (int64, bool, error) {
	if r.resetAuthorizer == nil {
		return 0, false, nil
	}

	authResults := make(map[string]ResetAuthResult)
	for _, engine := range marketsOnPartition {
		authRes, authErr := r.resetAuthorizer.AuthorizeEmptyReset(ctx, engine.MarketID, partition, logEndOffset)
		if authErr != nil {
			return 0, true, fmt.Errorf("authorize empty reset for market %s partition %d: %w", engine.MarketID, partition, authErr)
		}
		if !authRes.Authorized {
			return 0, false, nil
		}
		authResults[engine.MarketID] = authRes
	}

	// 0. Partition lease check: once reset is authorized, fail closed if lease checker is absent or lease not held
	if r.leaseChecker == nil || !r.leaseChecker.HasLease(partition) {
		return 0, true, fmt.Errorf("fail-closed: cannot execute reset for partition %d without active partition lease", partition)
	}

	// Determine and enforce identical boundary contract across all markets on this partition
	var partitionExpectedLogEnd *int64
	var expectedByMarket string
	for _, engine := range marketsOnPartition {
		authRes := authResults[engine.MarketID]
		if authRes.ExpectedLogEndOffset != nil {
			exp := *authRes.ExpectedLogEndOffset
			if partitionExpectedLogEnd == nil {
				partitionExpectedLogEnd = &exp
				expectedByMarket = engine.MarketID
			} else if *partitionExpectedLogEnd != exp {
				return 0, true, fmt.Errorf("mismatched partition reset boundary for partition %d: market %s expects boundary %d, but market %s expects %d",
					partition, expectedByMarket, *partitionExpectedLogEnd, engine.MarketID, exp)
			}
		}
	}

	targetHWM := logEndOffset
	if partitionExpectedLogEnd != nil {
		exp := *partitionExpectedLogEnd
		if logEndOffset < exp {
			return 0, true, fmt.Errorf("kafka log-end offset %d has regressed below approved boundary %d for partition %d",
				logEndOffset, exp, partition)
		}
		targetHWM = exp
	}

	resetBaseline := targetHWM - 1
	if targetHWM == 0 {
		resetBaseline = -1
	}

	// Sort markets deterministically by MarketID to enforce strict global lock ordering and prevent deadlocks
	sortedMarkets := make([]*market.MarketEngine, len(marketsOnPartition))
	copy(sortedMarkets, marketsOnPartition)
	sort.Slice(sortedMarkets, func(i, j int) bool {
		return sortedMarkets[i].MarketID < sortedMarkets[j].MarketID
	})

	// 1. Begin SINGLE Partition-Wide Database Transaction
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, true, fmt.Errorf("begin partition reset transaction for partition %d: %w", partition, err)
	}
	defer tx.Rollback(ctx)

	// 2. Lock and validate partition checkpoint
	var currentDBCheckpoint int64 = -1
	err = tx.QueryRow(ctx, "SELECT COALESCE(offset, -1) FROM kafka_checkpoints WHERE topic = $1 AND partition = $2 FOR UPDATE", topic, partition).Scan(&currentDBCheckpoint)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, true, fmt.Errorf("query current checkpoint for precondition check: %w", err)
	}

	// Monotonic Checkpoint Assertion: reset baseline cannot regress below existing checkpoint
	if resetBaseline < currentDBCheckpoint && currentDBCheckpoint >= 0 {
		return 0, true, fmt.Errorf("reset baseline offset %d cannot regress below existing checkpoint %d for partition %d",
			resetBaseline, currentDBCheckpoint, partition)
	}

	marketSeqs := make(map[string]uint64, len(sortedMarkets))

	// 3. Process all markets on partition within the same transaction
	for _, engine := range sortedMarkets {
		authRes := authResults[engine.MarketID]

		// Precondition: Checkpoint match
		if authRes.ExpectedCheckpoint != nil && *authRes.ExpectedCheckpoint != currentDBCheckpoint {
			return 0, true, fmt.Errorf("reset authorization precondition failed for market %s: expected checkpoint %d, but database has %d",
				engine.MarketID, *authRes.ExpectedCheckpoint, currentDBCheckpoint)
		}

		// Lock and validate current market sequence
		var currentDBSeq uint64 = 0
		err = tx.QueryRow(ctx, "SELECT COALESCE(sequence, 0) FROM market_sequences WHERE market_id = $1 FOR UPDATE", engine.MarketID).Scan(&currentDBSeq)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return 0, true, fmt.Errorf("query current sequence for precondition check: %w", err)
		}
		if authRes.ExpectedSequence != nil && *authRes.ExpectedSequence != currentDBSeq {
			return 0, true, fmt.Errorf("reset authorization precondition failed for market %s: expected sequence %d, but database has %d",
				engine.MarketID, *authRes.ExpectedSequence, currentDBSeq)
		}

		// Audit Log Verification: Check for prior reset record with the same generation token
		var priorCount int64
		err = tx.QueryRow(ctx, "SELECT COUNT(*) FROM market_reset_audit WHERE market_id = $1 AND generation = $2", engine.MarketID, authRes.Generation).Scan(&priorCount)
		if err != nil {
			return 0, true, fmt.Errorf("query reset audit log for market %s: %w", engine.MarketID, err)
		}
		if priorCount > 0 {
			return 0, true, fmt.Errorf("reset authorization token generation %q was already consumed in database for market %s", authRes.Generation, engine.MarketID)
		}

		// Sequence Reconciler
		seq, err := r.reconcileDownstreamSequence(ctx, engine.MarketID)
		if err != nil {
			return 0, true, fmt.Errorf("reconcile sequence for market %s: %w", engine.MarketID, err)
		}
		if seq < currentDBSeq && currentDBSeq > 0 {
			return 0, true, fmt.Errorf("reconciled sequence %d cannot regress below current DB sequence %d for market %s", seq, currentDBSeq, engine.MarketID)
		}
		marketSeqs[engine.MarketID] = seq

		// Record Audit Trail
		const insertAuditSQL = `
			INSERT INTO market_reset_audit (
				market_id, partition, generation, log_end_offset, sequence, token_hash, authorized_at
			) VALUES ($1, $2, $3, $4, $5, $6, NOW())`
		if _, err := tx.Exec(ctx, insertAuditSQL,
			engine.MarketID, partition, authRes.Generation, targetHWM,
			seq, authRes.TokenHash,
		); err != nil {
			return 0, true, fmt.Errorf("record reset audit trail for market %s: %w", engine.MarketID, err)
		}

		// Record Reconciliation Audit (durable PENDING gate)
		const insertReconSQL = `
			INSERT INTO market_reconciliation_audit (
				market_id, generation, partition, log_end_offset, manifest_checksum, status, created_at
			) VALUES ($1, $2, $3, $4, $5, 'PENDING', NOW())`
		if _, err := tx.Exec(ctx, insertReconSQL,
			engine.MarketID, authRes.Generation, partition, targetHWM, authRes.ManifestChecksum,
		); err != nil {
			return 0, true, fmt.Errorf("record reconciliation audit for market %s: %w", engine.MarketID, err)
		}

		// Upsert market sequence
		const upsertSeqSQL = `
			INSERT INTO market_sequences (market_id, sequence, updated_at)
			VALUES ($1, $2, NOW())
			ON CONFLICT (market_id)
			DO UPDATE SET
				sequence   = EXCLUDED.sequence,
				updated_at = NOW()
			WHERE market_sequences.sequence <= EXCLUDED.sequence`
		tag, err := tx.Exec(ctx, upsertSeqSQL, engine.MarketID, seq)
		if err != nil {
			return 0, true, fmt.Errorf("persist sequence %d for market %s: %w", seq, engine.MarketID, err)
		}
		if tag.String() != "" && tag.RowsAffected() == 0 {
			return 0, true, fmt.Errorf("sequence regression rejected: attempted sequence %d is lower than existing sequence", seq)
		}

		// Upsert baseline empty book snapshot
		emptySnap := orderbook.BookSnapshot{
			SchemaVersion: orderbook.CurrentSchemaVersion,
			MarketID:      engine.MarketID,
			Partition:     partition,
			Offset:        resetBaseline,
			Sequence:      seq,
			Orders:        []orderbook.SnapshotOrder{},
		}
		snapChecksum, cErr := orderbook.Checksum(emptySnap)
		if cErr != nil {
			return 0, true, fmt.Errorf("compute empty book checksum: %w", cErr)
		}
		snapJSON, jErr := json.Marshal(emptySnap)
		if jErr != nil {
			return 0, true, fmt.Errorf("marshal empty book snapshot: %w", jErr)
		}

		const upsertSnapSQL = `
			INSERT INTO market_snapshots (market_id, sequence, partition, "offset", schema_version, snapshot, checksum, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
			ON CONFLICT (market_id, sequence)
			DO UPDATE SET
				partition      = EXCLUDED.partition,
				"offset"       = EXCLUDED."offset",
				schema_version = EXCLUDED.schema_version,
				snapshot       = EXCLUDED.snapshot,
				checksum       = EXCLUDED.checksum,
				created_at     = NOW()
			WHERE market_snapshots.checksum = EXCLUDED.checksum AND market_snapshots."offset" <= EXCLUDED."offset"`
		snapTag, err := tx.Exec(ctx, upsertSnapSQL, engine.MarketID, seq, partition, resetBaseline, orderbook.CurrentSchemaVersion, snapJSON, snapChecksum)
		if err != nil {
			return 0, true, fmt.Errorf("persist baseline snapshot for market %s: %w", engine.MarketID, err)
		}
		if snapTag.String() != "" && snapTag.RowsAffected() == 0 {
			return 0, true, fmt.Errorf("snapshot regression rejected for market %s", engine.MarketID)
		}
	}

	// 4. Upsert shared Kafka checkpoint once for the entire partition
	const upsertCheckpointSQL = `
		INSERT INTO kafka_checkpoints (topic, partition, "offset", updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (topic, partition)
		DO UPDATE SET "offset" = EXCLUDED."offset", updated_at = NOW()
		WHERE kafka_checkpoints."offset" <= EXCLUDED."offset"`
	chkTag, err := tx.Exec(ctx, upsertCheckpointSQL, topic, partition, resetBaseline)
	if err != nil {
		return 0, true, fmt.Errorf("advance kafka checkpoint to baseline for partition %d: %w", partition, err)
	}
	if chkTag.String() != "" && chkTag.RowsAffected() == 0 {
		return 0, true, fmt.Errorf("checkpoint regression rejected for partition %d", partition)
	}

	// Re-verify partition lease immediately before committing reset transaction (fail closed)
	if r.leaseChecker == nil || !r.leaseChecker.HasLease(partition) {
		return 0, true, fmt.Errorf("fail-closed: partition lease lost during reset for partition %d", partition)
	}

	// 5. Commit Partition Reset Transaction Atomically
	if err := tx.Commit(ctx); err != nil {
		return 0, true, fmt.Errorf("commit partition reset transaction for partition %d: %w", partition, err)
	}

	// 6. Mutate in-memory engine state ONLY AFTER durable commit
	for _, engine := range sortedMarkets {
		seq := marketSeqs[engine.MarketID]
		engine.SetSequence(seq)
		engine.SetLastAppliedOffset(resetBaseline)
		marketLastSeenOffset[engine.MarketID] = resetBaseline
		if err := engine.SetReconciling("empty-book reset completed; awaiting reconciliation approval"); err != nil {
			// DB committed but in-memory state transition failed! Trigger fatal halt to prevent split-brain.
			engine.TriggerFatalHalt()
			return 0, true, fmt.Errorf("fatal: partition reset committed to DB but failed to transition engine %s to ModeReconciling: %w",
				engine.MarketID, err)
		}

		authRes := authResults[engine.MarketID]
		log.Printf("[recovery] SUCCESS: authorized empty-book reset baseline offset=%d sequence=%d audit generation=%s fingerprint=%s for market=%s partition=%d",
			resetBaseline, seq, authRes.Generation, authRes.TokenFingerprint, engine.MarketID, partition)
	}

	// 7. Align Kafka consumer group offset on the broker
	if r.groupID != "" {
		if err := r.commitKafkaGroupOffset(ctx, topic, partition, resetBaseline); err != nil {
			return 0, true, fmt.Errorf("align kafka group offset after reset (partition=%d offset=%d): %w", partition, resetBaseline, err)
		}
	}

	return resetBaseline, true, nil
}
