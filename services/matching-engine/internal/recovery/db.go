package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
