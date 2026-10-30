package recovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"tradedrift/services/matching-engine/internal/orderbook"

	"github.com/jackc/pgx/v5"
)

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
		ORDER BY "offset" DESC, sequence DESC LIMIT 10`

	rows, err := r.db.Query(ctx, query, marketID, checkpoint)
	if err != nil {
		return nil, nil, fmt.Errorf("query snapshot candidates (market=%s offset<=%d): %w", marketID, checkpoint, err)
	}
	defer rows.Close()

	candidateCount := 0
	var lastErr error

	for rows.Next() {
		candidateCount++
		var dbMarketID string
		var dbSequence int64
		var dbPartition int
		var dbOffset int64
		var dbSchemaVersion int
		var snapJSON []byte
		var checksum []byte

		if err := rows.Scan(&dbMarketID, &dbSequence, &dbPartition, &dbOffset, &dbSchemaVersion, &snapJSON, &checksum); err != nil {
			lastErr = fmt.Errorf("scan snapshot row: %w", err)
			log.Printf("[recovery] WARNING: skipping unreadable snapshot row candidate for market=%s: %v", marketID, err)
			continue
		}

		var snap orderbook.BookSnapshot
		if err := json.Unmarshal(snapJSON, &snap); err != nil {
			lastErr = fmt.Errorf("unmarshal snapshot struct: %w", err)
			log.Printf("[recovery] WARNING: skipping corrupted JSON snapshot candidate for market=%s at offset=%d: %v", marketID, dbOffset, err)
			continue
		}

		// Validate DB columns against snapshot payload properties to guard against row corruption
		if dbMarketID != snap.MarketID ||
			dbSequence != int64(snap.Sequence) ||
			dbPartition != snap.Partition ||
			dbOffset != snap.Offset ||
			dbSchemaVersion != int(snap.SchemaVersion) {
			lastErr = fmt.Errorf("snapshot metadata mismatch: DB columns (market=%s, seq=%d, partition=%d, offset=%d, version=%d) do not match JSON snapshot content (market=%s, seq=%d, partition=%d, offset=%d, version=%d)",
				dbMarketID, dbSequence, dbPartition, dbOffset, dbSchemaVersion,
				snap.MarketID, snap.Sequence, snap.Partition, snap.Offset, snap.SchemaVersion)
			log.Printf("[recovery] WARNING: skipping metadata-corrupted snapshot candidate for market=%s at offset=%d: %v", marketID, dbOffset, lastErr)
			continue
		}

		// Validate SHA-256 checksum against payload
		calcCheck, chkErr := orderbook.Checksum(snap)
		if chkErr != nil || !bytes.Equal(calcCheck, checksum) {
			lastErr = fmt.Errorf("snapshot checksum validation failed (expected %x, got %x)", checksum, calcCheck)
			log.Printf("[recovery] WARNING: skipping checksum-corrupted snapshot candidate for market=%s at offset=%d: %v", marketID, dbOffset, lastErr)
			continue
		}

		// Successfully validated candidate
		log.Printf("[recovery] selected valid snapshot candidate for market=%s sequence=%d offset=%d (evaluated %d candidate(s))",
			marketID, snap.Sequence, snap.Offset, candidateCount)
		return &snap, checksum, nil
	}

	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate snapshot rows for market %s: %w", marketID, err)
	}

	if candidateCount > 0 {
		return nil, nil, fmt.Errorf("FATAL: all %d candidate snapshot(s) for market %s <= checkpoint %d are corrupted or unrecoverable (last error: %v); fail-closed",
			candidateCount, marketID, checkpoint, lastErr)
	}

	return nil, nil, nil
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

func (r *Replayer) saveMarketSequence(ctx context.Context, marketID string, seq uint64) error {
	const query = `
		INSERT INTO market_sequences (market_id, sequence, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (market_id)
		DO UPDATE SET
			sequence   = EXCLUDED.sequence,
			updated_at = NOW()
		WHERE market_sequences.sequence <= EXCLUDED.sequence`
	tag, err := r.db.Exec(ctx, query, marketID, seq)
	if err != nil {
		return fmt.Errorf("upsert market_sequence (market=%s seq=%d): %w", marketID, seq, err)
	}
	if tag.String() != "" && tag.RowsAffected() == 0 {
		return fmt.Errorf("sequence regression rejected: market %s attempted sequence %d is lower than existing sequence", marketID, seq)
	}
	return nil
}
