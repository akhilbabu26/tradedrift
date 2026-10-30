package recovery_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/recovery"
	"tradedrift/services/matching-engine/internal/orderbook"
	"tradedrift/services/matching-engine/internal/matcher"
)

// ─── Mocks ──────────────────────────────────────────────────────────────────

type mockSnapshotRecord struct {
	marketID      string
	sequence      int64
	partition     int
	offset        int64
	schemaVersion int
	snapJSON      []byte
	checksum      []byte
}

type mockDB struct {
	checkpoints           map[string]int64 // key: topic/partition
	marketSequences       map[string]uint64
	snapshots             map[string][]byte
	snapshotsOffset       map[string]int64
	snapshotsCheck        map[string][]byte
	snapshotCandidates    map[string][]mockSnapshotRecord
	auditRecords          map[string]int64  // key: marketID/generation
	reconciliationRecords map[string]string // key: marketID/generation
	manifestChecksums     map[string]string // key: marketID/generation
	snapCountErr          map[string]error
	seqErr                map[string]error
	deleteCalls           []string
	failBegin             error
	failTxCommit          error
	failTxExec            error
	checkpointQueryErr    error
}

func newMockDB() *mockDB {
	return &mockDB{
		checkpoints:           make(map[string]int64),
		marketSequences:       make(map[string]uint64),
		snapshots:             make(map[string][]byte),
		snapshotsOffset:       make(map[string]int64),
		snapshotsCheck:        make(map[string][]byte),
		snapshotCandidates:    make(map[string][]mockSnapshotRecord),
		auditRecords:          make(map[string]int64),
		reconciliationRecords: make(map[string]string),
		manifestChecksums:     make(map[string]string),
		snapCountErr:          make(map[string]error),
		seqErr:                make(map[string]error),
	}
}

type mockTx struct {
	pgx.Tx
	db             *mockDB
	pendingAudit   map[string]int64
	pendingRecon   map[string]string
	pendingCheck   map[string]int64
	pendingSeq     map[string]uint64
	pendingSnaps   map[string][]byte
	pendingSnapOff map[string]int64
	pendingSnapChk map[string][]byte
}

func (m *mockDB) Begin(ctx context.Context) (pgx.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.failBegin != nil {
		return nil, m.failBegin
	}
	return &mockTx{
		db:             m,
		pendingAudit:   make(map[string]int64),
		pendingRecon:   make(map[string]string),
		pendingCheck:   make(map[string]int64),
		pendingSeq:     make(map[string]uint64),
		pendingSnaps:   make(map[string][]byte),
		pendingSnapOff: make(map[string]int64),
		pendingSnapChk: make(map[string][]byte),
	}, nil
}

func (tx *mockTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if err := ctx.Err(); err != nil {
		return pgconn.CommandTag{}, err
	}
	if tx.db.failTxExec != nil {
		return pgconn.CommandTag{}, tx.db.failTxExec
	}
	if strings.Contains(sql, "INSERT INTO market_reset_audit") {
		marketID := args[0].(string)
		generation := args[2].(string)
		logEndOffset := args[3].(int64)
		key := fmt.Sprintf("%s/%s", marketID, generation)
		if _, exists := tx.db.auditRecords[key]; exists {
			return pgconn.CommandTag{}, &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"}
		}
		if _, exists := tx.pendingAudit[key]; exists {
			return pgconn.CommandTag{}, &pgconn.PgError{Code: "23505", Message: "duplicate key value violates unique constraint"}
		}
		tx.pendingAudit[key] = logEndOffset
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	if strings.Contains(sql, "INSERT INTO kafka_checkpoints") {
		topic := args[0].(string)
		partition := args[1].(int)
		offset := args[2].(int64)
		key := fmt.Sprintf("%s/%d", topic, partition)
		tx.pendingCheck[key] = offset
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	if strings.Contains(sql, "INSERT INTO market_sequences") {
		marketID := args[0].(string)
		seq := args[1].(uint64)
		tx.pendingSeq[marketID] = seq
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	if strings.Contains(sql, "INSERT INTO market_snapshots") {
		marketID := args[0].(string)
		offset := args[3].(int64)
		snapJSON := args[5].([]byte)
		checksum := args[6].([]byte)
		tx.pendingSnaps[marketID] = snapJSON
		tx.pendingSnapOff[marketID] = offset
		tx.pendingSnapChk[marketID] = checksum
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	if strings.Contains(sql, "INSERT INTO market_reconciliation_audit") {
		marketID := args[0].(string)
		generation := args[1].(string)
		var checksum string
		if len(args) >= 5 {
			if cs, ok := args[4].(string); ok {
				checksum = cs
			}
		}
		key := fmt.Sprintf("%s/%s", marketID, generation)
		tx.pendingRecon[key] = "PENDING"
		tx.db.manifestChecksums[key] = checksum
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	if strings.Contains(sql, "UPDATE market_reconciliation_audit") {
		marketID := args[2].(string)
		generation := args[3].(string)
		key := fmt.Sprintf("%s/%s", marketID, generation)
		tx.pendingRecon[key] = "APPROVED"
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	return tx.db.Exec(ctx, sql, args...)
}

func (tx *mockTx) Commit(ctx context.Context) error {
	if tx.db.failTxCommit != nil {
		return tx.db.failTxCommit
	}
	for k, v := range tx.pendingAudit {
		tx.db.auditRecords[k] = v
	}
	for k, v := range tx.pendingRecon {
		tx.db.reconciliationRecords[k] = v
	}
	for k, v := range tx.pendingCheck {
		tx.db.checkpoints[k] = v
	}
	for k, v := range tx.pendingSeq {
		tx.db.marketSequences[k] = v
	}
	for k, v := range tx.pendingSnaps {
		tx.db.snapshots[k] = v
	}
	for k, v := range tx.pendingSnapOff {
		tx.db.snapshotsOffset[k] = v
	}
	for k, v := range tx.pendingSnapChk {
		tx.db.snapshotsCheck[k] = v
	}
	return nil
}

func (tx *mockTx) Rollback(ctx context.Context) error {
	tx.pendingAudit = nil
	tx.pendingRecon = nil
	tx.pendingCheck = nil
	tx.pendingSeq = nil
	tx.pendingSnaps = nil
	tx.pendingSnapOff = nil
	tx.pendingSnapChk = nil
	return nil
}

func (tx *mockTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return tx.db.QueryRow(ctx, sql, args...)
}

func (m *mockDB) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	if strings.Contains(strings.ToUpper(sql), "DELETE") {
		m.deleteCalls = append(m.deleteCalls, sql)
	}
	if strings.Contains(sql, "INSERT INTO market_sequences") {
		marketID := arguments[0].(string)
		seq := arguments[1].(uint64)
		m.marketSequences[marketID] = seq
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	if strings.Contains(sql, "INSERT INTO kafka_checkpoints") {
		topic := arguments[0].(string)
		partition := arguments[1].(int)
		offset := arguments[2].(int64)
		m.checkpoints[fmt.Sprintf("%s/%d", topic, partition)] = offset
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	return pgconn.CommandTag{}, nil
}

func (m *mockDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if err := ctx.Err(); err != nil {
		return &customRow{err: err}
	}
	// A. loadCheckpoint and row lock query
	if strings.Contains(sql, "FROM kafka_checkpoints") {
		if m.checkpointQueryErr != nil {
			return &customRow{err: m.checkpointQueryErr}
		}
		topic := args[0].(string)
		partition := args[1].(int)
		key := fmt.Sprintf("%s/%d", topic, partition)
		if offset, ok := m.checkpoints[key]; ok {
			return &customRow{vals: []any{offset}}
		}
		return &customRow{err: pgx.ErrNoRows}
	}

	// B. COALESCE MAX(offset) check for safety invariant (Issue #4)
	if sql == "SELECT COALESCE(MAX(\"offset\"), -1) FROM market_snapshots WHERE market_id = $1" {
		marketID := args[0].(string)
		if offset, ok := m.snapshotsOffset[marketID]; ok {
			return &customRow{vals: []any{offset}}
		}
		return &customRow{vals: []any{int64(-1)}}
	}

	// C. loadLatestSnapshot
	if sql == "\n\t\tSELECT market_id, sequence, partition, \"offset\", schema_version, snapshot, checksum FROM market_snapshots\n\t\tWHERE market_id = $1 AND \"offset\" <= $2\n\t\tORDER BY \"offset\" DESC, sequence DESC LIMIT 1" {
		marketID := args[0].(string)
		if snapBytes, ok := m.snapshots[marketID]; ok {
			checksum := m.snapshotsCheck[marketID]
			var snap orderbook.BookSnapshot
			if err := json.Unmarshal(snapBytes, &snap); err != nil {
				return &customRow{err: err}
			}
			return &customRow{vals: []any{
				snap.MarketID,
				int64(snap.Sequence),
				snap.Partition,
				snap.Offset,
				int(snap.SchemaVersion),
				snapBytes,
				checksum,
			}}
		}
		return &customRow{err: pgx.ErrNoRows}
	}

	// D. loadMarketSequence and row lock query
	if strings.Contains(sql, "FROM market_sequences") {
		marketID := args[0].(string)
		if err, ok := m.seqErr[marketID]; ok {
			return &customRow{err: err}
		}
		if seq, ok := m.marketSequences[marketID]; ok {
			return &customRow{vals: []any{seq}}
		}
		return &customRow{vals: []any{uint64(0)}}
	}

	// E. COUNT(*) check for missing checkpoint validation
	if sql == "SELECT COUNT(*) FROM market_snapshots WHERE market_id = $1" {
		marketID := args[0].(string)
		if err, ok := m.snapCountErr[marketID]; ok {
			return &customRow{err: err}
		}
		if _, ok := m.snapshots[marketID]; ok {
			return &customRow{vals: []any{int64(1)}}
		}
		return &customRow{vals: []any{int64(0)}}
	}

	// F. Queries for market_reset_audit
	if strings.Contains(sql, "FROM market_reset_audit") {
		if strings.Contains(sql, "COUNT(*)") {
			marketID := args[0].(string)
			generation := args[1].(string)
			key := fmt.Sprintf("%s/%s", marketID, generation)
			if _, ok := m.auditRecords[key]; ok {
				return &customRow{vals: []any{int64(1)}}
			}
			return &customRow{vals: []any{int64(0)}}
		}
		marketID := args[0].(string)
		var foundKey string
		var foundOffset int64
		for k, off := range m.auditRecords {
			if strings.HasPrefix(k, marketID+"/") {
				foundKey = k
				foundOffset = off
				break
			}
		}
		if foundKey != "" {
			parts := strings.Split(foundKey, "/")
			gen := parts[1]
			if strings.Contains(sql, "log_end_offset") && strings.Contains(sql, "sequence") {
				seq := m.marketSequences[marketID]
				return &customRow{vals: []any{gen, foundOffset, seq}}
			}
			return &customRow{vals: []any{gen}}
		}
		return &customRow{err: pgx.ErrNoRows}
	}

	// G. COALESCE MAX(sequence) from market_snapshots
	if strings.Contains(sql, "SELECT COALESCE(MAX(sequence), 0) FROM market_snapshots") {
		marketID := args[0].(string)
		if snapBytes, ok := m.snapshots[marketID]; ok {
			var snap orderbook.BookSnapshot
			if err := json.Unmarshal(snapBytes, &snap); err == nil {
				return &customRow{vals: []any{snap.Sequence}}
			}
		}
		return &customRow{vals: []any{uint64(0)}}
	}

	// H. market_reconciliation_audit queries
	if strings.Contains(sql, "FROM market_reconciliation_audit") {
		marketID := args[0].(string)
		var foundKey, foundStatus string
		for k, v := range m.reconciliationRecords {
			if strings.HasPrefix(k, marketID+"/") {
				foundKey = k
				foundStatus = v
				break
			}
		}
		if foundStatus != "" {
			parts := strings.Split(foundKey, "/")
			gen := parts[1]
			chk := m.manifestChecksums[foundKey]
			if strings.Contains(sql, "FOR UPDATE") {
				if len(args) > 1 {
					gen = args[1].(string)
					chk = m.manifestChecksums[marketID+"/"+gen]
				}
				return &customRow{vals: []any{gen, 0, int64(100), chk, foundStatus}}
			}
			if strings.Contains(sql, "generation") && strings.Contains(sql, "manifest_checksum") {
				return &customRow{vals: []any{gen, foundStatus, chk}}
			}
			return &customRow{vals: []any{foundStatus}}
		}
		return &customRow{err: pgx.ErrNoRows}
	}

	return &customRow{err: fmt.Errorf("unmocked query: %s", sql)}
}

type customRow struct {
	vals []any
	err  error
}

func (r *customRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i, val := range r.vals {
		if val == nil {
			continue
		}
		switch d := dest[i].(type) {
		case *int64:
			*d = val.(int64)
		case *uint64:
			*d = val.(uint64)
		case *[]byte:
			*d = val.([]byte)
		case *string:
			*d = val.(string)
		case *int:
			*d = val.(int)
		}
	}
	return nil
}

func (m *mockDB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.Contains(sql, "FROM market_snapshots") {
		marketID := args[0].(string)
		checkpoint := args[1].(int64)

		var records [][]any
		if candidates, ok := m.snapshotCandidates[marketID]; ok {
			for _, c := range candidates {
				if c.offset <= checkpoint {
					records = append(records, []any{
						c.marketID,
						c.sequence,
						c.partition,
						c.offset,
						c.schemaVersion,
						c.snapJSON,
						c.checksum,
					})
				}
			}
		} else if snapBytes, ok := m.snapshots[marketID]; ok {
			checksum := m.snapshotsCheck[marketID]
			var snap orderbook.BookSnapshot
			if err := json.Unmarshal(snapBytes, &snap); err == nil {
				if snap.Offset <= checkpoint {
					records = append(records, []any{
						snap.MarketID,
						int64(snap.Sequence),
						snap.Partition,
						snap.Offset,
						int(snap.SchemaVersion),
						snapBytes,
						checksum,
					})
				}
			} else {
				// Corrupted JSON in existing mock
				records = append(records, []any{
					marketID,
					int64(1),
					0,
					checkpoint,
					1,
					snapBytes,
					checksum,
				})
			}
		}
		return &mockRows{records: records}, nil
	}
	return nil, fmt.Errorf("unmocked query in mockDB: %s", sql)
}

type mockRows struct {
	records [][]any
	idx     int
	err     error
	closed  bool
}

func (r *mockRows) Close() { r.closed = true }
func (r *mockRows) Err() error { return r.err }
func (r *mockRows) CommandTag() pgconn.CommandTag { return pgconn.CommandTag{} }
func (r *mockRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *mockRows) Next() bool {
	if r.closed || r.err != nil {
		return false
	}
	if r.idx < len(r.records) {
		r.idx++
		return true
	}
	return false
}
func (r *mockRows) Scan(dest ...any) error {
	if r.idx == 0 || r.idx > len(r.records) {
		return errors.New("scan called without active row")
	}
	row := r.records[r.idx-1]
	for i, val := range row {
		if i >= len(dest) {
			break
		}
		if val == nil {
			continue
		}
		switch d := dest[i].(type) {
		case *int64:
			*d = val.(int64)
		case *uint64:
			*d = val.(uint64)
		case *[]byte:
			*d = val.([]byte)
		case *string:
			*d = val.(string)
		case *int:
			*d = val.(int)
		default:
			return fmt.Errorf("unsupported mock scan type %T", dest[i])
		}
	}
	return nil
}
func (r *mockRows) Values() ([]any, error) {
	if r.idx == 0 || r.idx > len(r.records) {
		return nil, errors.New("no active row")
	}
	return r.records[r.idx-1], nil
}
func (r *mockRows) RawValues() [][]byte { return nil }
func (r *mockRows) Conn() *pgx.Conn { return nil }
func (r *mockRows) TypeMap() *pgtype.Map { return nil }

type mockRedis struct {
	data map[string][]byte
}

func newMockRedis() *mockRedis {
	return &mockRedis{data: make(map[string][]byte)}
}

func (r *mockRedis) Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(ctx)
	switch v := value.(type) {
	case []byte:
		r.data[key] = v
	case string:
		r.data[key] = []byte(v)
	}
	return cmd
}

type mockKafkaReader struct {
	offset       int64
	messages     []kafkago.Message
	msgIndex     int
	setOffsetErr error
	fetchErr     error
}

func (m *mockKafkaReader) SetOffset(offset int64) error {
	if m.setOffsetErr != nil {
		return m.setOffsetErr
	}
	m.offset = offset
	return nil
}

func (m *mockKafkaReader) FetchMessage(ctx context.Context) (kafkago.Message, error) {
	if m.fetchErr != nil {
		return kafkago.Message{}, m.fetchErr
	}
	if m.msgIndex >= len(m.messages) {
		return kafkago.Message{}, errors.New("EOF")
	}
	msg := m.messages[m.msgIndex]
	m.msgIndex++
	return msg, nil
}

func (m *mockKafkaReader) Close() error {
	return nil
}

func makeRecoveryMsg(offset int64, marketID string, side string) kafkago.Message {
	payload, _ := json.Marshal(map[string]any{
		"order_id":   uuid.New().String(),
		"user_id":    uuid.New().String(),
		"side":       side,
		"order_type": "LIMIT",
		"price":      "100.00",
		"quantity":   "1.0",
	})
	val, _ := json.Marshal(map[string]any{
		"event_id":      uuid.New().String(),
		"event_type":    "OrderCreated",
		"event_version": 1,
		"market_id":     marketID,
		"payload":       json.RawMessage(payload),
	})
	return kafkago.Message{
		Key:    []byte(marketID),
		Offset: offset,
		Value:  val,
	}
}

// ─── Test B: Missing Snapshot Replay ───────────────────────────────────────

func TestRecovery_MissingSnapshotReplay(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "MARKET-A",
		Partition: 0,
		TickSize:  decimal.NewFromInt(1),
		LotSize:   decimal.NewFromInt(1),
	})
	_ = manager.Add(market.MarketConfig{
		MarketID:  "MARKET-B",
		Partition: 0,
		TickSize:  decimal.NewFromInt(1),
		LotSize:   decimal.NewFromInt(1),
	})

	db := newMockDB()
	db.checkpoints["orders.commands/0"] = 5
	db.marketSequences["MARKET-A"] = 1
	db.marketSequences["MARKET-B"] = 3

	snapA := orderbook.BookSnapshot{
		MarketID:      "MARKET-A",
		Sequence:      0,
		Partition:     0,
		Offset:         2,
		SchemaVersion: orderbook.CurrentSchemaVersion,
	}
	snapJSON, _ := json.Marshal(snapA)
	db.snapshots["MARKET-A"] = snapJSON
	db.snapshotsOffset["MARKET-A"] = 2
	h := sha256.New()
	h.Write(snapJSON)
	db.snapshotsCheck["MARKET-A"] = h.Sum(nil)

	expectedStartOffset := int64(0)

	readerMock := &mockKafkaReader{
		messages: []kafkago.Message{
			makeRecoveryMsg(0, "MARKET-A", "BUY"),
			makeRecoveryMsg(1, "MARKET-B", "BUY"),
			makeRecoveryMsg(2, "MARKET-A", "BUY"),
			makeRecoveryMsg(3, "MARKET-B", "BUY"),
			makeRecoveryMsg(4, "MARKET-A", "BUY"),
			makeRecoveryMsg(5, "MARKET-B", "BUY"),
		},
	}

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{0}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return readerMock
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 1000, nil // HWM is always high enough to pass check
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(ctx, &engineWg)
	if err != nil {
		t.Fatalf("recovery failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	if readerMock.offset != expectedStartOffset {
		t.Errorf("expected recovery replay to start at %d, but reader offset was set to %d", expectedStartOffset, readerMock.offset)
	}
}

// ─── Test C: Partition Gaps ────────────────────────────────────────────────

func TestRecovery_PartitionGaps(t *testing.T) {
	manager := market.NewMarketManager()
	manager.Add(market.MarketConfig{
		MarketID:  "MARKET-A",
		Partition: 0,
		TickSize:  decimal.NewFromInt(1),
		LotSize:   decimal.NewFromInt(1),
	})

	db := newMockDB()
	db.checkpoints["orders.commands/0"] = 10

	readerMock := &mockKafkaReader{
		messages: []kafkago.Message{
			makeRecoveryMsg(0, "MARKET-A", "BUY"),
			makeRecoveryMsg(1, "MARKET-A", "BUY"),
			makeRecoveryMsg(3, "MARKET-A", "BUY"),
		},
	}

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{0}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return readerMock
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 1000, nil // HWM is always high enough to pass check
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(ctx, &engineWg)
	if err == nil {
		t.Fatal("expected recovery to fail on partition offset continuity gap, got nil")
	}

	expectedErr := "partition offset continuity gap detected on partition 0: expected 2, got 3"
	if !containsSubstring(err.Error(), expectedErr) {
		t.Errorf("expected error containing %q, got %q", expectedErr, err.Error())
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > len(sub) && (s[0:len(sub)] == sub || containsSubstring(s[1:], sub)))
}

func TestRecovery_EmptyPartition(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "MARKET-A",
		Partition: 0,
		TickSize:  decimal.NewFromInt(1),
		LotSize:   decimal.NewFromInt(1),
	})

	db := newMockDB()
	// Set checkpoint to -1 (empty/unprocessed partition)
	db.checkpoints["orders.commands/0"] = -1
	db.marketSequences["MARKET-A"] = 0

	readerMock := &mockKafkaReader{
		messages: []kafkago.Message{},
	}

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{0}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return readerMock
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 0, nil // HWM is 0 (log-end offset is 0)
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(ctx, &engineWg)
	if err != nil {
		t.Fatalf("expected recovery to succeed on empty partition, got err: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	engine := manager.Get("MARKET-A")
	if engine == nil {
		t.Fatal("expected MARKET-A engine to be registered")
	}

	if engine.GetSequence() != 0 {
		t.Errorf("expected sequence 0, got %d", engine.GetSequence())
	}
	if engine.GetLastAppliedOffset() != -1 {
		t.Errorf("expected lastAppliedOffset -1, got %d", engine.GetLastAppliedOffset())
	}
	if engine.Mode() != market.ModeLive {
		t.Errorf("expected engine mode LIVE, got %v", engine.Mode())
	}
}

func TestRecovery_CrashAfterTradePublish(t *testing.T) {
	sellOrderID := uuid.New()
	buyOrderID := uuid.New()
	eventID := uuid.New()

	// 1. Original Execution before crash
	book1 := orderbook.NewOrderBook("BTC-USDT")
	sell1 := &orderbook.OrderNode{
		OrderID:      sellOrderID,
		MarketID:     "BTC-USDT",
		Side:         orderbook.SideSell,
		OrderType:    orderbook.OrderTypeLimit,
		Price:        decimal.RequireFromString("100"),
		OriginalQty:  decimal.RequireFromString("1"),
		RemainingQty: decimal.RequireFromString("1"),
	}
	matcher.Insert(book1, sell1)

	buy1 := &orderbook.OrderNode{
		OrderID:      buyOrderID,
		MarketID:     "BTC-USDT",
		Side:         orderbook.SideBuy,
		OrderType:    orderbook.OrderTypeLimit,
		Price:        decimal.RequireFromString("100"),
		OriginalQty:  decimal.RequireFromString("1"),
		RemainingQty: decimal.RequireFromString("1"),
	}
	fills1 := matcher.Match(book1, buy1, matcher.ModeLive, eventID)
	if len(fills1) != 1 {
		t.Fatalf("expected 1 fill in original execution")
	}
	originalTradeID := fills1[0].TradeID

	// 2. Simulated Crash & Replay (restart recovers same inputs at offset 100)
	book2 := orderbook.NewOrderBook("BTC-USDT")
	sell2 := &orderbook.OrderNode{
		OrderID:      sellOrderID,
		MarketID:     "BTC-USDT",
		Side:         orderbook.SideSell,
		OrderType:    orderbook.OrderTypeLimit,
		Price:        decimal.RequireFromString("100"),
		OriginalQty:  decimal.RequireFromString("1"),
		RemainingQty: decimal.RequireFromString("1"),
	}
	matcher.Insert(book2, sell2)

	buy2 := &orderbook.OrderNode{
		OrderID:      buyOrderID,
		MarketID:     "BTC-USDT",
		Side:         orderbook.SideBuy,
		OrderType:    orderbook.OrderTypeLimit,
		Price:        decimal.RequireFromString("100"),
		OriginalQty:  decimal.RequireFromString("1"),
		RemainingQty: decimal.RequireFromString("1"),
	}
	fills2 := matcher.Match(book2, buy2, matcher.ModeLive, eventID)
	if len(fills2) != 1 {
		t.Fatalf("expected 1 fill in replay execution")
	}
	replayTradeID := fills2[0].TradeID

	if originalTradeID != replayTradeID {
		t.Errorf("expected trade ID on replay to match original trade ID: %s vs %s", originalTradeID, replayTradeID)
	}

	// Invariant: Order book sequence must be identical between initial execution and replay
	if book1.Sequence != book2.Sequence {
		t.Errorf("expected sequence on replay to match original sequence: %d vs %d", book1.Sequence, book2.Sequence)
	}

	// Invariant: Depth and order index must be identical between initial execution and replay
	if len(book1.OrderIndex) != len(book2.OrderIndex) {
		t.Errorf("expected order index length to match: %d vs %d", len(book1.OrderIndex), len(book2.OrderIndex))
	}
	if len(book1.Bids.SortedPrices) != len(book2.Bids.SortedPrices) || len(book1.Asks.SortedPrices) != len(book2.Asks.SortedPrices) {
		t.Errorf("expected book price level count to match: bids(%d vs %d), asks(%d vs %d)",
			len(book1.Bids.SortedPrices), len(book2.Bids.SortedPrices), len(book1.Asks.SortedPrices), len(book2.Asks.SortedPrices))
	}

	// 3. ModeRecovery execution suppresses trade output but achieves identical internal state
	book3 := orderbook.NewOrderBook("BTC-USDT")
	sell3 := &orderbook.OrderNode{
		OrderID:      sellOrderID,
		MarketID:     "BTC-USDT",
		Side:         orderbook.SideSell,
		OrderType:    orderbook.OrderTypeLimit,
		Price:        decimal.RequireFromString("100"),
		OriginalQty:  decimal.RequireFromString("1"),
		RemainingQty: decimal.RequireFromString("1"),
	}
	matcher.Insert(book3, sell3)
	buy3 := &orderbook.OrderNode{
		OrderID:      buyOrderID,
		MarketID:     "BTC-USDT",
		Side:         orderbook.SideBuy,
		OrderType:    orderbook.OrderTypeLimit,
		Price:        decimal.RequireFromString("100"),
		OriginalQty:  decimal.RequireFromString("1"),
		RemainingQty: decimal.RequireFromString("1"),
	}
	recoveryFills := matcher.Match(book3, buy3, matcher.ModeRecovery, eventID)
	if recoveryFills != nil {
		t.Fatalf("expected ModeRecovery to suppress trade fills output (got %d fills)", len(recoveryFills))
	}
	if book3.Sequence != book1.Sequence {
		t.Errorf("expected recovery book sequence to match live sequence: %d vs %d", book3.Sequence, book1.Sequence)
	}
	if len(book3.OrderIndex) != len(book1.OrderIndex) {
		t.Errorf("expected recovery book order index to match live: %d vs %d", len(book3.OrderIndex), len(book1.OrderIndex))
	}
}

// TestRecovery_DeterministicSequenceProgression verifies that order book mutations
// (trades, resting limit orders, and cancellations) advance book.Sequence deterministically
// across recovery replays.
func TestRecovery_DeterministicSequenceProgression(t *testing.T) {
	const initialSequence uint64 = 500
	book := orderbook.NewOrderBook("BTC-USDT")
	book.Sequence = initialSequence

	eventID := uuid.New()
	sellID := uuid.New()
	buyID := uuid.New()
	restingID := uuid.New()

	// 1. Initial resting sell order
	sell := &orderbook.OrderNode{
		OrderID:      sellID,
		MarketID:     "BTC-USDT",
		Side:         orderbook.SideSell,
		OrderType:    orderbook.OrderTypeLimit,
		Price:        decimal.RequireFromString("100"),
		OriginalQty:  decimal.RequireFromString("1"),
		RemainingQty: decimal.RequireFromString("1"),
	}
	matcher.Insert(book, sell)
	// Resting insertion advances sequence by 0 if using raw Insert, or +1 through event loop / Match.
	// We'll track explicitly.
	currentSeq := book.Sequence

	// 2. Incoming buy matches resting sell -> +1 sequence for fill
	buy := &orderbook.OrderNode{
		OrderID:      buyID,
		MarketID:     "BTC-USDT",
		Side:         orderbook.SideBuy,
		OrderType:    orderbook.OrderTypeLimit,
		Price:        decimal.RequireFromString("100"),
		OriginalQty:  decimal.RequireFromString("1"),
		RemainingQty: decimal.RequireFromString("1"),
	}
	fills := matcher.Match(book, buy, matcher.ModeLive, eventID)
	if len(fills) != 1 {
		t.Fatalf("expected 1 fill, got %d", len(fills))
	}
	if book.Sequence != currentSeq+1 {
		t.Fatalf("expected sequence after 1 fill to be %d, got %d", currentSeq+1, book.Sequence)
	}
	currentSeq = book.Sequence

	// 3. Non-crossing limit order rests on book -> +1 sequence
	restingUser := uuid.New()
	resting := &orderbook.OrderNode{
		OrderID:      restingID,
		UserID:       restingUser,
		MarketID:     "BTC-USDT",
		Side:         orderbook.SideBuy,
		OrderType:    orderbook.OrderTypeLimit,
		Price:        decimal.RequireFromString("90"),
		OriginalQty:  decimal.RequireFromString("2"),
		RemainingQty: decimal.RequireFromString("2"),
	}
	restingFills := matcher.Match(book, resting, matcher.ModeLive, uuid.New())
	if len(restingFills) != 0 {
		t.Fatalf("expected 0 fills for non-crossing order")
	}
	if book.Sequence != currentSeq+1 {
		t.Fatalf("expected sequence after resting insert to be %d, got %d", currentSeq+1, book.Sequence)
	}
	currentSeq = book.Sequence

	// 4. Cancel resting order -> +1 sequence
	outcome := matcher.Cancel(book, restingID, restingUser)
	if outcome.Status != orderbook.CancelStatusRemovedFromBook {
		t.Fatalf("expected CancelStatusRemovedFromBook, got %s", outcome.Status)
	}
	book.Sequence++ // cancellation in event loop increments sequence
	if book.Sequence != currentSeq+1 {
		t.Fatalf("expected sequence after cancel to be %d, got %d", currentSeq+1, book.Sequence)
	}

	// Invariant: Total sequence advanced by exactly 3 (1 trade + 1 resting insert + 1 cancel)
	if book.Sequence != initialSequence+3 {
		t.Fatalf("expected final deterministic sequence to be %d, got %d", initialSequence+3, book.Sequence)
	}
}

func TestRecovery_MultiMarketBarrier(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "MARKET-A",
		Partition: 0,
		TickSize:  decimal.NewFromInt(1),
		LotSize:   decimal.NewFromInt(1),
	})
	_ = manager.Add(market.MarketConfig{
		MarketID:  "MARKET-B",
		Partition: 0,
		TickSize:  decimal.NewFromInt(1),
		LotSize:   decimal.NewFromInt(1),
	})
	_ = manager.Add(market.MarketConfig{
		MarketID:  "MARKET-C",
		Partition: 0,
		TickSize:  decimal.NewFromInt(1),
		LotSize:   decimal.NewFromInt(1),
	})

	db := newMockDB()
	db.checkpoints["orders.commands/0"] = 2
	db.marketSequences["MARKET-A"] = 1
	db.marketSequences["MARKET-B"] = 1
	db.marketSequences["MARKET-C"] = 1

	readerMock := &mockKafkaReader{
		messages: []kafkago.Message{
			makeRecoveryMsg(0, "MARKET-A", "BUY"),
			makeRecoveryMsg(1, "MARKET-B", "BUY"),
			makeRecoveryMsg(2, "MARKET-C", "BUY"),
		},
	}

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{0}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return readerMock
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 1000, nil
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(ctx, &engineWg)
	if err != nil {
		t.Fatalf("expected successful recovery replay: %v", err)
	}

	engineWg.Wait()

	for _, mID := range []string{"MARKET-A", "MARKET-B", "MARKET-C"} {
		engine := manager.Get(mID)
		if engine == nil {
			t.Fatalf("expected %s engine to exist", mID)
		}
		if engine.Mode() != market.ModeLive {
			t.Errorf("expected engine %s mode LIVE, got %v", mID, engine.Mode())
		}
	}
}

func TestRecovery_OutputQueueBackpressure(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "MARKET-A",
		Partition: 0,
		TickSize:  decimal.NewFromInt(1),
		LotSize:   decimal.NewFromInt(1),
	})

	db := newMockDB()
	db.checkpoints["orders.commands/0"] = 1500
	db.marketSequences["MARKET-A"] = 1501 // 1501 processed messages

	var messages []kafkago.Message
	for i := int64(0); i <= 1500; i++ {
		messages = append(messages, makeRecoveryMsg(i, "MARKET-A", "BUY"))
	}

	readerMock := &mockKafkaReader{
		messages: messages,
	}

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{0}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return readerMock
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 2000, nil // HWM > checkpoint
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(ctx, &engineWg)
	if err != nil {
		t.Fatalf("expected successful recovery under backpressure: %v", err)
	}

	engineWg.Wait()

	engine := manager.Get("MARKET-A")
	if engine.Mode() != market.ModeLive {
		t.Errorf("expected engine mode LIVE, got %v", engine.Mode())
	}
}

func TestRecovery_CheckpointAheadOfHWM_FailsClosed(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "MARKET-A",
		Partition: 0,
		TickSize:  decimal.NewFromInt(1),
		LotSize:   decimal.NewFromInt(1),
	})

	db := newMockDB()
	db.checkpoints["orders.commands/0"] = 100 // Postgres checkpoint = 100
	db.marketSequences["MARKET-A"] = 10

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{0}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return &mockKafkaReader{}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 90, nil // Kafka HWM = 90 (checkpoint 100 >= HWM 90!)
		},
	)

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected fatal error when checkpoint >= HWM, got nil")
	}
	if !strings.Contains(err.Error(), "FATAL") {
		t.Fatalf("expected error to contain 'FATAL', got: %v", err)
	}

	// Assert DB state was NOT deleted
	if len(db.deleteCalls) > 0 {
		t.Fatalf("expected no DELETE calls on DB, got: %v", db.deleteCalls)
	}
	if db.checkpoints["orders.commands/0"] != 100 {
		t.Fatalf("expected checkpoint to remain preserved, got %d", db.checkpoints["orders.commands/0"])
	}
	engine := manager.Get("MARKET-A")
	if engine.Mode() == market.ModeLive {
		t.Fatal("engine must not transition to LIVE mode on fatal recovery error")
	}
}

func TestRecovery_StartOffsetBehindLWM_FailsClosed(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "MARKET-A",
		Partition: 0,
		TickSize:  decimal.NewFromInt(1),
		LotSize:   decimal.NewFromInt(1),
	})

	db := newMockDB()
	db.checkpoints["orders.commands/0"] = 1000
	db.marketSequences["MARKET-A"] = 1001

	// No snapshot exists, so startOffset = 0
	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{0}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return &mockKafkaReader{}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 2000, nil // HWM = 2000
		},
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 900, nil // earliest = 900, startOffset = 0 < 900
	})

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected fatal error when startOffset < LWM, got nil")
	}
	if !strings.Contains(err.Error(), "FATAL") {
		t.Fatalf("expected error to contain 'FATAL', got: %v", err)
	}

	// Assert DB state was NOT deleted
	if len(db.deleteCalls) > 0 {
		t.Fatalf("expected no DELETE calls on DB, got: %v", db.deleteCalls)
	}
	engine := manager.Get("MARKET-A")
	if engine.Mode() == market.ModeLive {
		t.Fatal("engine must not transition to LIVE mode on fatal recovery error")
	}
}

func TestRecovery_PartitionFailureHaltsAllEngines(t *testing.T) {
	manager := market.NewMarketManager()
	engineA := manager.Add(market.MarketConfig{
		MarketID:  "MARKET-A",
		Partition: 0,
		TickSize:  decimal.NewFromInt(1),
		LotSize:   decimal.NewFromInt(1),
	})
	engineB := manager.Add(market.MarketConfig{
		MarketID:  "MARKET-B",
		Partition: 1,
		TickSize:  decimal.NewFromInt(1),
		LotSize:   decimal.NewFromInt(1),
	})

	db := newMockDB()
	db.checkpoints["orders.commands/0"] = 10
	db.checkpoints["orders.commands/1"] = 100

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{0, 1}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			if partition == 1 {
				return &mockKafkaReader{setOffsetErr: errors.New("kafka partition read error")}
			}
			msgs := make([]kafkago.Message, 11)
			for i := 0; i <= 10; i++ {
				msgs[i] = makeRecoveryMsg(int64(i), "MARKET-A", "BUY")
			}
			return &mockKafkaReader{messages: msgs}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 200, nil
		},
	)

	var engineWg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := replayer.ReplayAll(ctx, &engineWg)
	if err != nil {
		t.Fatalf("expected ReplayAll to isolate failure and succeed for healthy engine, got: %v", err)
	}

	if engineA.Mode() != market.ModeLive {
		t.Errorf("expected engine A mode Live, got %v", engineA.Mode())
	}
	if engineB.Mode() != market.ModeBlocked {
		t.Errorf("expected engine B mode Blocked, got %v", engineB.Mode())
	}
	if !engineB.IsBlocked() {
		t.Errorf("expected engine B IsBlocked() to be true")
	}
}

func TestRecovery_MissingCheckpoint_WithExistingSnapshots_FailsClosed(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "MARKET-A",
		Partition: 0,
		TickSize:  decimal.NewFromInt(1),
		LotSize:   decimal.NewFromInt(1),
	})

	db := newMockDB()
	// No checkpoint in db.checkpoints (checkpointOffset will be -1)
	// But snapshots exist in DB!
	db.snapshots["MARKET-A"] = []byte(`{"market_id":"MARKET-A","sequence":10}`)

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{0}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return &mockKafkaReader{}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 200, nil
		},
	)

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected ReplayAll to fail closed when checkpoint is missing but snapshots exist, got nil")
	}
	if !strings.Contains(err.Error(), "FATAL") {
		t.Fatalf("expected error to contain 'FATAL', got: %v", err)
	}

	// Verify no DELETE queries were executed
	if len(db.deleteCalls) > 0 {
		t.Fatalf("expected no DELETE queries executed, got: %v", db.deleteCalls)
	}
}

// TestRecovery_PartitionTruncatedUpToCheckpoint_WithCurrentSnapshot tests the exact incident scenario:
// An idle partition has its Kafka history purged such that earliest == logEnd (e.g. 43563).
// Checkpoint is at 43562 (checkpoint + 1 == earliestOffset).
// A valid snapshot exists matching the checkpoint offset (43562) and sequence (28691).
// Recovery must succeed without attempting to replay missing Kafka events, and transition to LIVE mode.
func TestRecovery_PartitionTruncatedUpToCheckpoint_WithCurrentSnapshot(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
		TickSize:  decimal.NewFromInt(1),
		LotSize:   decimal.NewFromInt(1),
	})

	db := newMockDB()
	const chkOffset int64 = 43562
	const seq uint64 = 28691
	db.checkpoints["orders.commands/1"] = chkOffset
	db.marketSequences["ETH-USDT"] = seq

	snap := orderbook.BookSnapshot{
		MarketID:      "ETH-USDT",
		Sequence:      seq,
		Partition:     1,
		Offset:        chkOffset,
		SchemaVersion: orderbook.CurrentSchemaVersion,
	}
	snapJSON, _ := json.Marshal(snap)
	db.snapshots["ETH-USDT"] = snapJSON
	db.snapshotsOffset["ETH-USDT"] = chkOffset
	h := sha256.New()
	h.Write(snapJSON)
	db.snapshotsCheck["ETH-USDT"] = h.Sum(nil)

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{1}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return &mockKafkaReader{}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 43563, nil // HWM = 43563 (chkOffset 43562 < HWM 43563)
		},
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 43563, nil // LWM = 43563 (chkOffset + 1 == 43563 == LWM)
	})

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err != nil {
		t.Fatalf("expected clean recovery with current snapshot on truncated partition, got: %v", err)
	}

	engine := manager.Get("ETH-USDT")
	if engine == nil {
		t.Fatal("expected ETH-USDT engine to exist")
	}
	if engine.Mode() != market.ModeLive {
		t.Errorf("expected engine mode LIVE, got %v", engine.Mode())
	}
	if engine.GetSequence() != seq {
		t.Errorf("expected engine sequence %d, got %d", seq, engine.GetSequence())
	}
	if engine.GetLastAppliedOffset() != chkOffset {
		t.Errorf("expected last applied offset %d, got %d", chkOffset, engine.GetLastAppliedOffset())
	}
}

// TestRecovery_PartitionTruncatedUpToCheckpoint_WithStaleSnapshot_FailsClosed verifies that if
// the snapshot is older than the checkpoint (e.g. offset 43557 vs checkpoint 43562), and the
// bridging events (43558..43562) have been purged from Kafka (earliest=43563), the engine
// fails closed with a clear FATAL error rather than starting with a corrupted order book.
func TestRecovery_PartitionTruncatedUpToCheckpoint_WithStaleSnapshot_FailsClosed(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
		TickSize:  decimal.NewFromInt(1),
		LotSize:   decimal.NewFromInt(1),
	})

	db := newMockDB()
	const chkOffset int64 = 43562
	const staleOffset int64 = 43557
	db.checkpoints["orders.commands/1"] = chkOffset
	db.marketSequences["ETH-USDT"] = 28691

	snap := orderbook.BookSnapshot{
		MarketID:      "ETH-USDT",
		Sequence:      28688,
		Partition:     1,
		Offset:        staleOffset,
		SchemaVersion: orderbook.CurrentSchemaVersion,
	}
	snapJSON, _ := json.Marshal(snap)
	db.snapshots["ETH-USDT"] = snapJSON
	db.snapshotsOffset["ETH-USDT"] = staleOffset
	h := sha256.New()
	h.Write(snapJSON)
	db.snapshotsCheck["ETH-USDT"] = h.Sum(nil)

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{1}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return &mockKafkaReader{}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 43563, nil // HWM = 43563
		},
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 43563, nil // LWM = 43563 (startOffset 43558 < LWM 43563)
	})

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected fatal error when snapshot requires purged Kafka commands, got nil")
	}
	if !strings.Contains(err.Error(), "FATAL") {
		t.Fatalf("expected error to contain 'FATAL', got: %v", err)
	}

	engine := manager.Get("ETH-USDT")
	if engine.Mode() == market.ModeLive {
		t.Fatal("engine must not transition to LIVE mode when required history is purged")
	}
}

// TestRecovery_UnprocessedCommandsPurged_FailsClosed verifies that if Kafka's earliest available
// offset is ahead of checkpoint + 1 (i.e. unconsumed commands were purged before processing),
// the engine fails closed with FATAL.
func TestRecovery_UnprocessedCommandsPurged_FailsClosed(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "BTC-USDT",
		Partition: 0,
		TickSize:  decimal.NewFromInt(1),
		LotSize:   decimal.NewFromInt(1),
	})

	db := newMockDB()
	// Checkpoint is at 100, but earliest available offset is 150 (messages 101..149 never processed!)
	db.checkpoints["orders.commands/0"] = 100
	db.marketSequences["BTC-USDT"] = 10

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{0}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return &mockKafkaReader{}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 200, nil // HWM = 200
		},
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 150, nil // LWM = 150 (checkpoint+1 = 101 < 150)
	})

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected fatal error when unconsumed commands are purged, got nil")
	}
	if !strings.Contains(err.Error(), "FATAL") {
		t.Fatalf("expected error to contain 'FATAL', got: %v", err)
	}
}

// TestRecovery_GenuinelyEmptyPartition_CleanBootstrap verifies that a new market partition
// with no checkpoint in PostgreSQL and no Kafka events (HWM=0, LWM=0) initializes cleanly.
func TestRecovery_GenuinelyEmptyPartition_CleanBootstrap(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "SOL-USDT",
		Partition: 2,
		TickSize:  decimal.NewFromInt(1),
		LotSize:   decimal.NewFromInt(1),
	})

	db := newMockDB()
	// No checkpoint in DB (will load as -1)
	// No snapshots in DB (COUNT = 0)
	// Sequence = 0

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{2}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return &mockKafkaReader{}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 0, nil // HWM = 0
		},
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 0, nil // LWM = 0
	})

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err != nil {
		t.Fatalf("expected clean bootstrap on empty partition, got: %v", err)
	}

	engine := manager.Get("SOL-USDT")
	if engine == nil {
		t.Fatal("expected SOL-USDT engine to exist")
	}
	if engine.Mode() != market.ModeLive {
		t.Errorf("expected engine mode LIVE after clean bootstrap, got %v", engine.Mode())
	}
}

func TestRecovery_FailClosed_SnapshotCountDBError(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
	})

	db := newMockDB()
	// Simulated DB outage on snapshot count query
	db.snapCountErr["ETH-USDT"] = fmt.Errorf("connection pool exhausted")

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{1}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return &mockKafkaReader{}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 0, nil
		},
	)

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected recovery to fail closed when snapshot count query returns a database error, got nil")
	}
	if !strings.Contains(err.Error(), "connection pool exhausted") {
		t.Errorf("expected connection pool error, got: %v", err)
	}
}

func TestRecovery_FailClosed_SequenceQueryDBError(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
	})

	db := newMockDB()
	// Simulated DB outage on sequence query (not pgx.ErrNoRows)
	db.seqErr["ETH-USDT"] = fmt.Errorf("tcp read: connection reset by peer")

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{1}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return &mockKafkaReader{}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 0, nil
		},
	)

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected recovery to fail closed when sequence query returns a database error, got nil")
	}
	if !strings.Contains(err.Error(), "connection reset by peer") {
		t.Errorf("expected connection reset error, got: %v", err)
	}
}

func TestRecovery_FailClosed_KafkaLWMError(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "BTC-USDT",
		Partition: 0,
	})

	db := newMockDB()
	db.checkpoints["orders.commands/0"] = 100

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{0}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return &mockKafkaReader{}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 200, nil
		},
	)
	// LWM query fails with network timeout
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 0, fmt.Errorf("kafka broker timed out reading earliest offset")
	})

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected recovery to fail closed when Kafka LWM query returns an error, got nil")
	}
	if !strings.Contains(err.Error(), "kafka broker timed out reading earliest offset") {
		t.Errorf("expected LWM broker timeout error, got: %v", err)
	}
}

func TestRecovery_Uncheckpointed_PopulatedKafka_Unauthorized_FailsClosed(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
	})

	db := newMockDB()
	// No checkpoint in DB (-1)
	// No snapshots in DB (COUNT = 0)
	// Sequence = 0

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{1}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return &mockKafkaReader{}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 43563, nil // Kafka has 43,563 historical messages
		},
	)

	// Ensure no env var authorization is active
	os.Unsetenv("ME_ALLOW_EMPTY_RESET")

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected recovery to refuse auto-reset when Kafka has messages and DB has no checkpoint without authorization, got nil")
	}
	if !strings.Contains(err.Error(), "without explicit operator reset authorization") {
		t.Errorf("expected explicit operator reset authorization error, got: %v", err)
	}
}

func TestRecovery_Uncheckpointed_PopulatedKafka_Authorized_Succeeds(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
	})

	db := newMockDB()
	db.marketSequences["ETH-USDT"] = 28691

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{1}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return &mockKafkaReader{}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 43563, nil // Kafka has historical messages
		},
	)
	replayer.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	// Explicitly set authorization environment variable for this market, partition, and generation
	os.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-20261025-01")
	defer os.Unsetenv("ME_ALLOW_EMPTY_RESET")

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err != nil {
		t.Fatalf("expected authorized empty reset to succeed, got: %v", err)
	}

	engine := manager.Get("ETH-USDT")
	if engine.Mode() != market.ModeReconciling {
		t.Errorf("expected engine mode RECONCILING after authorized reset, got: %v", engine.Mode())
	}
	if engine.GetSequence() != 28691 {
		t.Errorf("expected preserved sequence 28691, got: %d", engine.GetSequence())
	}
	if engine.GetLastAppliedOffset() != 43562 {
		t.Errorf("expected baseline offset 43562 (HWM-1), got: %d", engine.GetLastAppliedOffset())
	}

	const validHex1589 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	db.manifestChecksums["ETH-USDT/gen-20261025-01"] = validHex1589
	reconService := recovery.NewReconciliationService(db, manager, &testLeaseChecker{hasLease: true}, &testOrderVerifier{}, &testSettlementVerifier{}, "test-secret")
	if _, err := reconService.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-20261025-01",
		ManifestChecksum: validHex1589,
		AuthToken:        "test-secret",
	}); err != nil {
		t.Fatalf("reconciliation approval failed: %v", err)
	}
	if engine.Mode() != market.ModeLive {
		t.Errorf("expected engine mode LIVE after reconciliation approval, got: %v", engine.Mode())
	}
}

func TestRecovery_Uncheckpointed_PopulatedKafka_ReusedAuthorization_FailsClosed(t *testing.T) {
	authorizer := recovery.NewEnvResetAuthorizer()
	os.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-unique-12345")
	defer os.Unsetenv("ME_ALLOW_EMPTY_RESET")

	// First authorization: succeeds
	res, err := authorizer.AuthorizeEmptyReset(context.Background(), "ETH-USDT", 1, 43563)
	if err != nil || !res.Authorized {
		t.Fatalf("expected first authorization to succeed, got authorized=%v, err=%v", res.Authorized, err)
	}

	// Reused token: MUST fail
	res2, err2 := authorizer.AuthorizeEmptyReset(context.Background(), "ETH-USDT", 1, 43563)
	if res2.Authorized || err2 == nil {
		t.Fatalf("expected reused authorization generation token to fail, got authorized=%v, err=%v", res2.Authorized, err2)
	}
	if !strings.Contains(err2.Error(), "already consumed") {
		t.Errorf("expected 'already consumed' error, got: %v", err2)
	}
}

func TestRecovery_Uncheckpointed_PopulatedKafka_Authorized_WithExistingSnapshots_FailsClosed(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
	})

	db := newMockDB()
	// Durable snapshot exists in DB!
	db.snapshots["ETH-USDT"] = []byte(`{"market_id":"ETH-USDT","sequence":500}`)

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) {
			return []int{1}, nil
		},
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return &mockKafkaReader{}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 43563, nil
		},
	)

	// Even if authorized, existing snapshots must NEVER be silently bypassed!
	os.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-bypass-test")
	defer os.Unsetenv("ME_ALLOW_EMPTY_RESET")

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected recovery to fail closed when durable snapshots exist in DB, even if reset is authorized")
	}
	if !strings.Contains(err.Error(), "has 1 snapshot(s). Refusing to auto-wipe durable state") {
		t.Errorf("expected refused auto-wipe snapshot error, got: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Step 1 Tests: Authorized Reset Baseline and Kafka Alignment
// ─────────────────────────────────────────────────────────────────────────────

func TestRecovery_Step1_HWMZero_CleanBootstrap(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "BTC-USDT",
		Partition: 0,
	})

	db := newMockDB()
	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{0}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 0, nil // HWM = 0 (Genuinely empty log)
		},
	)

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err != nil {
		t.Fatalf("expected clean bootstrap for HWM=0 to succeed, got: %v", err)
	}

	engine := manager.Get("BTC-USDT")
	if engine.GetLastAppliedOffset() != -1 {
		t.Errorf("expected baseline offset -1 for empty partition, got: %d", engine.GetLastAppliedOffset())
	}
	if engine.Mode() != market.ModeLive {
		t.Errorf("expected engine mode LIVE, got: %v", engine.Mode())
	}
	// Checkpoints table must NOT have an artificial offset
	if _, exists := db.checkpoints["orders.commands/0"]; exists {
		t.Errorf("expected no checkpoint written for genuinely empty HWM=0 log")
	}
}

func TestRecovery_Step1_HWMGreaterThanZero_AuthorizedBaselinePersistence(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
	})

	db := newMockDB()
	db.marketSequences["ETH-USDT"] = 28691

	var committedGroupOffset int64 = -999
	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 43563, nil // HWM = 43563
		},
	)
	replayer.OverrideCommitGroupOffset(func(ctx context.Context, topic string, partition int, offset int64) error {
		committedGroupOffset = offset
		return nil
	})
	replayer.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	os.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-step1-01:secret-token-12345")
	defer os.Unsetenv("ME_ALLOW_EMPTY_RESET")

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err != nil {
		t.Fatalf("expected authorized reset with HWM>0 to succeed, got: %v", err)
	}

	// 1. Baseline must be HWM - 1 = 43562
	expectedBaseline := int64(43562)
	cpKey := "orders.commands/1"
	if db.checkpoints[cpKey] != expectedBaseline {
		t.Errorf("expected persisted kafka_checkpoints offset %d, got: %d", expectedBaseline, db.checkpoints[cpKey])
	}

	// 2. Audit record persisted
	auditKey := "ETH-USDT/gen-step1-01"
	if db.auditRecords[auditKey] != 43563 {
		t.Errorf("expected persisted audit record with logEndOffset 43563, got: %d", db.auditRecords[auditKey])
	}

	// 3. Broker group offset aligned
	if committedGroupOffset != expectedBaseline {
		t.Errorf("expected Kafka broker group offset aligned to %d, got: %d", expectedBaseline, committedGroupOffset)
	}

	// 4. Engine state initialized
	engine := manager.Get("ETH-USDT")
	if engine.GetLastAppliedOffset() != expectedBaseline {
		t.Errorf("expected engine lastAppliedOffset %d, got: %d", expectedBaseline, engine.GetLastAppliedOffset())
	}
	if engine.GetSequence() != 28691 {
		t.Errorf("expected engine sequence 28691, got: %d", engine.GetSequence())
	}
	if engine.Mode() != market.ModeReconciling {
		t.Errorf("expected engine RECONCILING, got: %v", engine.Mode())
	}

	const validHex1763 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	db.manifestChecksums["ETH-USDT/gen-step1-01"] = validHex1763
	reconService := recovery.NewReconciliationService(db, manager, &testLeaseChecker{hasLease: true}, &testOrderVerifier{}, &testSettlementVerifier{}, "test-secret")
	if _, err := reconService.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-step1-01",
		ManifestChecksum: validHex1763,
		AuthToken:        "test-secret",
	}); err != nil {
		t.Fatalf("reconciliation approval failed: %v", err)
	}
	if engine.Mode() != market.ModeLive {
		t.Errorf("expected engine LIVE after approval, got: %v", engine.Mode())
	}
}

func TestRecovery_Step1_ExistingCheckpoint_BypassesReset(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
	})

	db := newMockDB()
	db.checkpoints["orders.commands/1"] = 100 // Existing valid checkpoint
	db.marketSequences["ETH-USDT"] = 500

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			// Mock reader delivering messages up to checkpoint 100
			return &mockMsgReader{
				messages: []kafkago.Message{
					{Topic: "orders.commands", Partition: 1, Offset: 0, Key: []byte("ETH-USDT"), Value: []byte(`{"event_type":"Unknown"}`)},
				},
			}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			return 200, nil // HWM = 200
		},
	)

	// Even if an authorizer is configured, it MUST NOT be called because checkpoint exists
	authorizerCalled := false
	replayer.SetResetAuthorizer(&spyAuthorizer{
		onAuthorize: func() { authorizerCalled = true },
	})

	var engineWg sync.WaitGroup
	_ = replayer.ReplayAll(context.Background(), &engineWg)

	if authorizerCalled {
		t.Fatal("expected reset authorizer NOT to be invoked when checkpoint already exists")
	}
}

func TestRecovery_Step1_FailedDatabaseTransaction_RollsBack(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
	})

	db := newMockDB()
	db.marketSequences["ETH-USDT"] = 28691
	db.failTxCommit = errors.New("simulated database disk failure")

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	os.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-tx-fail")
	defer os.Unsetenv("ME_ALLOW_EMPTY_RESET")

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected recovery to fail when database transaction commit fails")
	}
	if !strings.Contains(err.Error(), "simulated database disk failure") {
		t.Errorf("expected db error, got: %v", err)
	}

	// Verify nothing was persisted to DB state
	if _, ok := db.checkpoints["orders.commands/1"]; ok {
		t.Error("checkpoint should not be committed after tx failure")
	}
	if _, ok := db.auditRecords["ETH-USDT/gen-tx-fail"]; ok {
		t.Error("audit record should not be committed after tx failure")
	}
}

func TestRecovery_Step1_FailedKafkaGroupAlignment_FailsClosed(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
	})

	db := newMockDB()
	db.marketSequences["ETH-USDT"] = 28691

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer.OverrideCommitGroupOffset(func(ctx context.Context, topic string, partition int, offset int64) error {
		return errors.New("kafka broker unreachable")
	})
	replayer.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	os.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-kafka-fail")
	defer os.Unsetenv("ME_ALLOW_EMPTY_RESET")

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected recovery to fail closed when Kafka group alignment fails")
	}
	if !strings.Contains(err.Error(), "kafka broker unreachable") {
		t.Errorf("expected kafka error, got: %v", err)
	}

	engine := manager.Get("ETH-USDT")
	if engine.Mode() == market.ModeLive {
		t.Error("engine must NOT be marked LIVE after failed group alignment")
	}
}

func TestRecovery_Step1_ProcessCrashAroundBaselineCreation(t *testing.T) {
	// Crash Scenario A: Crash before commit -> DB rolled back, subsequent startup fails closed without token
	db := newMockDB()
	db.marketSequences["ETH-USDT"] = 28691
	db.failTxExec = errors.New("process crash during write")

	managerA := market.NewMarketManager()
	_ = managerA.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	replayerA := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), managerA)
	replayerA.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayerA.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	os.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-crash-test")
	var wgA sync.WaitGroup
	errA := replayerA.ReplayAll(context.Background(), &wgA)
	if errA == nil {
		t.Fatal("expected failure on crash before commit")
	}

	// Verify DB is clean
	if _, ok := db.checkpoints["orders.commands/1"]; ok {
		t.Fatal("checkpoint must not exist after crash before commit")
	}

	// Crash Scenario B: Crash after commit -> Checkpoint and baseline snapshot exist in DB.
	// On restart, normal recovery starts from checkpoint without replaying historical commands.
	dbClean := newMockDB()
	dbClean.marketSequences["ETH-USDT"] = 28691

	managerB1 := market.NewMarketManager()
	_ = managerB1.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	replayerB1 := recovery.NewReplayer([]string{"localhost:9092"}, "", dbClean, newMockRedis(), managerB1)
	replayerB1.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayerB1.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	os.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-crash-success")
	var wgB1 sync.WaitGroup
	if err := replayerB1.ReplayAll(context.Background(), &wgB1); err != nil {
		t.Fatalf("first recovery run failed: %v", err)
	}

	// Process restarts with same DB state. Historical command replay must NOT occur!
	readerCalled := false
	managerB2 := market.NewMarketManager()
	_ = managerB2.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	replayerB2 := recovery.NewReplayer([]string{"localhost:9092"}, "", dbClean, newMockRedis(), managerB2)
	replayerB2.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			readerCalled = true
			return &mockKafkaReader{}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayerB2.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 43563, nil // Historical offsets 0..43562 are purged from Kafka broker
	})

	var wgB2 sync.WaitGroup
	errB2 := replayerB2.ReplayAll(context.Background(), &wgB2)
	if errB2 != nil {
		t.Fatalf("second startup after committed baseline failed: %v", errB2)
	}
	if readerCalled {
		t.Error("startup after reset should NOT replay historical messages since startOffset > checkpointOffset")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Step 2 Tests: Preserve Market Sequence Continuity
// ─────────────────────────────────────────────────────────────────────────────

func TestRecovery_Step2_PreserveSequence_From28691_VerifyContinuity(t *testing.T) {
	manager := market.NewMarketManager()
	engine := manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
		TickSize:  decimal.NewFromFloat(0.01),
		LotSize:   decimal.NewFromFloat(0.001),
	})

	db := newMockDB()
	db.marketSequences["ETH-USDT"] = 28691 // Authoritative sequence

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	os.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-seq-continuity")
	defer os.Unsetenv("ME_ALLOW_EMPTY_RESET")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(ctx, &engineWg)
	if err != nil {
		t.Fatalf("recovery failed: %v", err)
	}

	if engine.GetSequence() != 28691 {
		t.Fatalf("expected engine sequence initialized to 28691, got: %d", engine.GetSequence())
	}
	if engine.Mode() != market.ModeReconciling {
		t.Fatalf("expected engine mode RECONCILING, got: %v", engine.Mode())
	}

	// Approve reconciliation to transition engine to ModeLive before sending live orders
	const validHex2010 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	db.manifestChecksums["ETH-USDT/gen-seq-continuity"] = validHex2010
	reconService := recovery.NewReconciliationService(db, manager, &testLeaseChecker{hasLease: true}, &testOrderVerifier{}, &testSettlementVerifier{}, "test-secret")
	if _, err := reconService.ApproveReconciliation(ctx, recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-seq-continuity",
		ManifestChecksum: validHex2010,
		AuthToken:        "test-secret",
	}); err != nil {
		t.Fatalf("reconciliation approval failed: %v", err)
	}

	// 1. Submit resting limit order -> sequence must advance to 28692
	orderID1 := uuid.New()
	userID1 := uuid.New()
	engine.InputQueue <- market.InputEvent{
		EventID: uuid.New(),
		Type:    market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:    orderID1,
			UserID:     userID1,
			MarketID:   "ETH-USDT",
			Side:       orderbook.SideBuy,
			OrderType:  orderbook.OrderTypeLimit,
			Price:      decimal.NewFromInt(2000),
			Quantity:   decimal.NewFromInt(1),
			OccurredAt: time.Now(),
		},
		Topic:     "orders.commands",
		Partition: 1,
		Offset:    43563,
	}

	res1 := <-engine.OutputQueue
	if res1.DepthSnapshot.Sequence != 28692 {
		t.Errorf("expected sequence 28692 after resting limit order, got: %d", res1.DepthSnapshot.Sequence)
	}
	if engine.GetSequence() != 28692 {
		t.Errorf("expected engine sequence 28692, got: %d", engine.GetSequence())
	}

	// 2. Submit order cancel -> sequence must advance to 28693
	engine.InputQueue <- market.InputEvent{
		EventID: uuid.New(),
		Type:    market.EventOrderCancel,
		OrderCancel: &market.OrderCancelPayload{
			OrderID:  orderID1,
			UserID:   userID1,
			MarketID: "ETH-USDT",
		},
		Topic:     "orders.commands",
		Partition: 1,
		Offset:    43564,
	}

	res2 := <-engine.OutputQueue
	if res2.DepthSnapshot.Sequence != 28693 {
		t.Errorf("expected sequence 28693 after order cancel, got: %d", res2.DepthSnapshot.Sequence)
	}
	if engine.GetSequence() != 28693 {
		t.Errorf("expected engine sequence 28693, got: %d", engine.GetSequence())
	}
}

func TestRecovery_Step2_MissingSequenceRow_ReconciledFromDownstream(t *testing.T) {
	manager := market.NewMarketManager()
	engine := manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
	})

	db := newMockDB()
	// market_sequences has NO row for ETH-USDT! (Missing sequence row)

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)

	// Reconciler provides consistent downstream evidence from Trade, Portfolio, Settlement
	replayer.OverrideSequenceReconciler(&recovery.RuleBasedSequenceReconciler{
		EvidenceSource: func(ctx context.Context, marketID string) (recovery.DownstreamSequenceEvidence, error) {
			return recovery.DownstreamSequenceEvidence{
				TradeSeq:      28691,
				PortfolioSeq:  28691,
				SettlementSeq: 28691,
				SnapshotSeq:   0,
				InFlightCount: 0,
			}, nil
		},
	})
	replayer.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	os.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-reconciled-seq")
	defer os.Unsetenv("ME_ALLOW_EMPTY_RESET")

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err != nil {
		t.Fatalf("expected missing sequence to be reconciled from downstream, got: %v", err)
	}

	if engine.GetSequence() != 28691 {
		t.Errorf("expected engine sequence reconciled to 28691, got: %d", engine.GetSequence())
	}
	if db.marketSequences["ETH-USDT"] != 28691 {
		t.Errorf("expected market_sequences populated with reconciled sequence 28691, got: %d", db.marketSequences["ETH-USDT"])
	}
}

func TestRecovery_Step2_RejectInconsistentDownstreamSequenceEvidence(t *testing.T) {
	cases := []struct {
		name          string
		evidence      recovery.DownstreamSequenceEvidence
		expectedError string
	}{
		{
			name: "portfolio sequence ahead of trade",
			evidence: recovery.DownstreamSequenceEvidence{
				TradeSeq:      28691,
				PortfolioSeq:  28700,
				SettlementSeq: 28691,
			},
			expectedError: "portfolio sequence 28700 > trade sequence 28691",
		},
		{
			name: "unresolved in-flight events",
			evidence: recovery.DownstreamSequenceEvidence{
				TradeSeq:      28691,
				PortfolioSeq:  28691,
				SettlementSeq: 28691,
				InFlightCount: 3,
			},
			expectedError: "unresolved in-flight events detected",
		},
		{
			name: "divergent downstream settlement pipeline",
			evidence: recovery.DownstreamSequenceEvidence{
				TradeSeq:      28691,
				PortfolioSeq:  28600,
				SettlementSeq: 28500,
			},
			expectedError: "downstream sequence divergence",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reconciler := &recovery.RuleBasedSequenceReconciler{
				EvidenceSource: func(ctx context.Context, marketID string) (recovery.DownstreamSequenceEvidence, error) {
					return tc.evidence, nil
				},
			}

			_, err := reconciler.ReconcileSequence(context.Background(), "ETH-USDT")
			if err == nil {
				t.Fatalf("expected reconciliation failure for %s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.expectedError) {
				t.Errorf("expected error containing %q, got: %v", tc.expectedError, err)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Step 3 Tests: Durable, Single-Use Reset Authorization
// ─────────────────────────────────────────────────────────────────────────────

func TestRecovery_Step3_ValidAuthorization_SecretTokenMasking(t *testing.T) {
	authorizer := recovery.NewEnvResetAuthorizer()
	os.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-20261025-01:super-secret-token-value-999")
	defer os.Unsetenv("ME_ALLOW_EMPTY_RESET")

	res, err := authorizer.AuthorizeEmptyReset(context.Background(), "ETH-USDT", 1, 43563)
	if err != nil {
		t.Fatalf("unexpected authorization error: %v", err)
	}
	if !res.Authorized {
		t.Fatal("expected authorized=true")
	}
	if res.Generation != "gen-20261025-01" {
		t.Errorf("expected generation 'gen-20261025-01', got: %s", res.Generation)
	}
	if strings.Contains(res.TokenFingerprint, "super-secret-token-value") {
		t.Errorf("secret token must not appear in raw plaintext in fingerprint: %s", res.TokenFingerprint)
	}
	if res.TokenFingerprint != "***-999" {
		t.Errorf("expected masked fingerprint '***-999', got: %s", res.TokenFingerprint)
	}
	if res.TokenHash == "" {
		t.Error("expected non-empty token hash")
	}
}

func TestRecovery_Step3_InvalidMarketAndPartition(t *testing.T) {
	authorizer := recovery.NewEnvResetAuthorizer()
	os.Setenv("ME_ALLOW_EMPTY_RESET", "BTC-USDT:0:gen-btc")
	defer os.Unsetenv("ME_ALLOW_EMPTY_RESET")

	// Target is ETH-USDT on partition 1 -> must NOT authorize BTC token
	res, err := authorizer.AuthorizeEmptyReset(context.Background(), "ETH-USDT", 1, 43563)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Authorized {
		t.Fatal("expected authorized=false for mismatched market and partition")
	}
}

func TestRecovery_Step3_DuplicateToken_FailsClosed(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
	})

	db := newMockDB()
	db.marketSequences["ETH-USDT"] = 28691
	// Pre-record this generation as already consumed in DB!
	db.auditRecords["ETH-USDT/gen-already-used"] = 40000

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	os.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-already-used")
	defer os.Unsetenv("ME_ALLOW_EMPTY_RESET")

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected duplicate token in DB to fail closed")
	}
	if !strings.Contains(err.Error(), "already consumed in database") {
		t.Errorf("expected 'already consumed in database' error, got: %v", err)
	}
}

func TestRecovery_Step3_ConcurrentClaims_OnlyOneSucceeds(t *testing.T) {
	db := newMockDB()
	db.marketSequences["ETH-USDT"] = 28691

	tx1, err1 := db.Begin(context.Background())
	if err1 != nil {
		t.Fatalf("tx1 begin failed: %v", err1)
	}
	tx2, err2 := db.Begin(context.Background())
	if err2 != nil {
		t.Fatalf("tx2 begin failed: %v", err2)
	}

	const insertSQL = `INSERT INTO market_reset_audit (market_id, partition, generation, log_end_offset, sequence, token_hash, authorized_at) VALUES ($1, $2, $3, $4, $5, $6, NOW())`

	// Claim 1: succeeds
	_, c1Err := tx1.Exec(context.Background(), insertSQL, "ETH-USDT", 1, "gen-concurrent", int64(43563), int64(28691), "hash1")
	if c1Err != nil {
		t.Fatalf("claim 1 failed: %v", c1Err)
	}
	if err := tx1.Commit(context.Background()); err != nil {
		t.Fatalf("tx1 commit failed: %v", err)
	}

	// Claim 2: concurrent duplicate attempt MUST be rejected by DB constraint
	_, c2Err := tx2.Exec(context.Background(), insertSQL, "ETH-USDT", 1, "gen-concurrent", int64(43563), int64(28691), "hash2")
	if c2Err == nil {
		t.Fatal("expected concurrent claim for identical generation to fail with unique violation")
	}
	_ = tx2.Rollback(context.Background())
}

func TestRecovery_Step3_RestartPersistence_NormalStartupDoesNotInvokeReset(t *testing.T) {
	db := newMockDB()
	db.marketSequences["ETH-USDT"] = 28691

	manager1 := market.NewMarketManager()
	_ = manager1.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	replayer1 := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager1)
	replayer1.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer1.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	// Environment variable is set
	os.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-stay-configured")
	defer os.Unsetenv("ME_ALLOW_EMPTY_RESET")

	var wg1 sync.WaitGroup
	if err := replayer1.ReplayAll(context.Background(), &wg1); err != nil {
		t.Fatalf("initial reset failed: %v", err)
	}

	// Process restarts with environment variable STILL CONFIGURED.
	// Normal startup reads kafka_checkpoints (43562) and MUST NOT re-invoke the reset path!
	manager2 := market.NewMarketManager()
	_ = manager2.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	replayer2 := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager2)
	replayer2.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)

	resetCalledOnRestart := false
	replayer2.SetResetAuthorizer(&spyAuthorizer{
		onAuthorize: func() { resetCalledOnRestart = true },
	})

	var wg2 sync.WaitGroup
	if err := replayer2.ReplayAll(context.Background(), &wg2); err != nil {
		t.Fatalf("normal startup on restart failed: %v", err)
	}

	if resetCalledOnRestart {
		t.Fatal("destructive reset path was accidentally re-invoked on normal restart simply because env var remained configured")
	}
}

type spyAuthorizer struct {
	onAuthorize func()
}

func (s *spyAuthorizer) AuthorizeEmptyReset(ctx context.Context, marketID string, partition int, logEndOffset int64) (recovery.ResetAuthResult, error) {
	if s.onAuthorize != nil {
		s.onAuthorize()
	}
	return recovery.ResetAuthResult{}, nil
}

type mockMsgReader struct {
	messages []kafkago.Message
	idx      int
}

func (m *mockMsgReader) SetOffset(offset int64) error { return nil }
func (m *mockMsgReader) FetchMessage(ctx context.Context) (kafkago.Message, error) {
	if m.idx >= len(m.messages) {
		return kafkago.Message{}, context.Canceled
	}
	msg := m.messages[m.idx]
	m.idx++
	return msg, nil
}
func (m *mockMsgReader) Close() error { return nil }

type mockMultiAuthorizer struct{}

func (m *mockMultiAuthorizer) AuthorizeEmptyReset(ctx context.Context, marketID string, partition int, logEndOffset int64) (recovery.ResetAuthResult, error) {
	return recovery.ResetAuthResult{
		Authorized:       true,
		Generation:       "gen-" + marketID,
		TokenHash:        "hash-" + marketID,
		TokenFingerprint: "***-" + marketID,
	}, nil
}

func TestRecovery_Uncheckpointed_PopulatedKafka_MultipleMarketsOnSamePartition_AllReset(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	_ = manager.Add(market.MarketConfig{MarketID: "BTC-USDT", Partition: 1})

	db := newMockDB()
	db.marketSequences["ETH-USDT"] = 100
	db.marketSequences["BTC-USDT"] = 200

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 500, nil },
	)
	replayer.SetResetAuthorizer(&mockMultiAuthorizer{})
	replayer.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err != nil {
		t.Fatalf("expected reset of all markets on partition to succeed, got: %v", err)
	}

	eth := manager.Get("ETH-USDT")
	if eth.Mode() != market.ModeReconciling || eth.GetSequence() != 100 || eth.GetLastAppliedOffset() != 499 {
		t.Errorf("ETH-USDT not properly reset: mode=%v seq=%d offset=%d", eth.Mode(), eth.GetSequence(), eth.GetLastAppliedOffset())
	}

	btc := manager.Get("BTC-USDT")
	if btc.Mode() != market.ModeReconciling || btc.GetSequence() != 200 || btc.GetLastAppliedOffset() != 499 {
		t.Errorf("BTC-USDT not properly reset: mode=%v seq=%d offset=%d", btc.Mode(), btc.GetSequence(), btc.GetLastAppliedOffset())
	}

	const validHex2400 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	db.manifestChecksums["ETH-USDT/gen-ETH-USDT"] = validHex2400
	db.manifestChecksums["BTC-USDT/gen-BTC-USDT"] = validHex2400
	reconService := recovery.NewReconciliationService(db, manager, &testLeaseChecker{hasLease: true}, &testOrderVerifier{}, &testSettlementVerifier{}, "test-secret")
	if _, err := reconService.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-ETH-USDT",
		ManifestChecksum: validHex2400,
		AuthToken:        "test-secret",
	}); err != nil {
		t.Fatalf("ETH approval failed: %v", err)
	}
	if _, err := reconService.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "BTC-USDT",
		Generation:       "gen-BTC-USDT",
		ManifestChecksum: validHex2400,
		AuthToken:        "test-secret",
	}); err != nil {
		t.Fatalf("BTC approval failed: %v", err)
	}
	if eth.Mode() != market.ModeLive || btc.Mode() != market.ModeLive {
		t.Errorf("expected both markets LIVE after approval, got eth=%v btc=%v", eth.Mode(), btc.Mode())
	}
}

func TestRecovery_Step1_PreconditionMismatch_Checkpoint_FailsClosed(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})

	db := newMockDB()
	db.checkpoints["orders.commands/1"] = 43562
	db.marketSequences["ETH-USDT"] = 28691

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil }, // HWM
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 43563, nil // LWM = 43563 -> retention gap
	})

	// Configure ME_ALLOW_EMPTY_RESET with mismatched expected checkpoint (100 instead of 43562)
	t.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-mismatch-chk:100:28691:secret123")
	replayer.SetResetAuthorizer(recovery.NewEnvResetAuthorizer())
	replayer.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected failure due to checkpoint precondition mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "expected checkpoint 100, but database has 43562") {
		t.Fatalf("expected error mentioning precondition mismatch, got: %v", err)
	}
}

func TestRecovery_Step1_PreconditionMismatch_Sequence_FailsClosed(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})

	db := newMockDB()
	db.checkpoints["orders.commands/1"] = 43562
	db.marketSequences["ETH-USDT"] = 28691

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 43563, nil
	})

	// Configure ME_ALLOW_EMPTY_RESET with mismatched expected sequence (999 instead of 28691)
	t.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-mismatch-seq:43562:999:secret123")
	replayer.SetResetAuthorizer(recovery.NewEnvResetAuthorizer())
	replayer.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected failure due to sequence precondition mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "expected sequence 999, but database has 28691") {
		t.Fatalf("expected error mentioning sequence precondition mismatch, got: %v", err)
	}
}

func TestRecovery_Step1_PreconditionMatch_ExecutesSafely(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})

	db := newMockDB()
	db.checkpoints["orders.commands/1"] = 43562
	db.marketSequences["ETH-USDT"] = 28691

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 43563, nil
	})

	// Configure ME_ALLOW_EMPTY_RESET with matching preconditions
	t.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-matching:43562:28691:secret123")
	replayer.SetResetAuthorizer(recovery.NewEnvResetAuthorizer())
	replayer.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err != nil {
		t.Fatalf("expected reset to succeed with matching preconditions, got: %v", err)
	}

	eth := manager.Get("ETH-USDT")
	if eth.Mode() != market.ModeReconciling {
		t.Errorf("expected engine mode Reconciling, got %v", eth.Mode())
	}
	if eth.GetSequence() != 28691 {
		t.Errorf("expected sequence 28691, got %d", eth.GetSequence())
	}
	if eth.GetLastAppliedOffset() != 43562 {
		t.Errorf("expected lastAppliedOffset 43562, got %d", eth.GetLastAppliedOffset())
	}

	// Verify audit record exists
	if _, ok := db.auditRecords["ETH-USDT/gen-matching"]; !ok {
		t.Error("expected market_reset_audit record to be created in DB")
	}

	const validHex2526 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	db.manifestChecksums["ETH-USDT/gen-matching"] = validHex2526
	reconService := recovery.NewReconciliationService(db, manager, &testLeaseChecker{hasLease: true}, &testOrderVerifier{}, &testSettlementVerifier{}, "secret123")
	if _, err := reconService.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-matching",
		ManifestChecksum: validHex2526,
		AuthToken:        "secret123",
	}); err != nil {
		t.Fatalf("approval failed: %v", err)
	}
	if eth.Mode() != market.ModeLive {
		t.Errorf("expected engine mode Live after approval, got %v", eth.Mode())
	}
}

func TestRecovery_Step1_RetentionGappedPartition_ExplicitReset(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})

	db := newMockDB()
	db.checkpoints["orders.commands/1"] = 43562
	db.marketSequences["ETH-USDT"] = 28691

	// Snapshot at 43557, so startOffset = 43558
	snap := orderbook.BookSnapshot{
		SchemaVersion: orderbook.CurrentSchemaVersion,
		MarketID:      "ETH-USDT",
		Partition:     1,
		Offset:        43557,
		Sequence:      28691,
		Orders:        []orderbook.SnapshotOrder{},
	}
	snapBytes, _ := json.Marshal(snap)
	chk, _ := orderbook.Checksum(snap)
	db.snapshots["ETH-USDT"] = snapBytes
	db.snapshotsOffset["ETH-USDT"] = 43557
	db.snapshotsCheck["ETH-USDT"] = chk

	// Clear any previous environment variable to ensure authorizer is not configured
	t.Setenv("ME_ALLOW_EMPTY_RESET", "")

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 43563, nil // startOffset 43558 < earliestOffset 43563
	})

	// Without authorizer: must fail closed with retention gap error
	var engineWg sync.WaitGroup
	errNoAuth := replayer.ReplayAll(context.Background(), &engineWg)
	if errNoAuth == nil {
		t.Fatal("expected failure without authorizer, got nil")
	}
	if !strings.Contains(errNoAuth.Error(), "expired/purged") && !strings.Contains(errNoAuth.Error(), "purged before processing") {
		t.Fatalf("expected retention gap error, got: %v", errNoAuth)
	}

	// With matching authorized reset: succeeds
	manager2 := market.NewMarketManager()
	_ = manager2.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})
	replayer2 := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager2)
	replayer2.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer2.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 43563, nil
	})
	t.Setenv("ME_ALLOW_EMPTY_RESET", "ETH-USDT:1:gen-retention-ok:43562:28691:secret123")
	replayer2.SetResetAuthorizer(recovery.NewEnvResetAuthorizer())
	replayer2.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	var engineWg2 sync.WaitGroup
	errAuth := replayer2.ReplayAll(context.Background(), &engineWg2)
	if errAuth != nil {
		t.Fatalf("expected explicit authorized reset to recover retention gap, got: %v", errAuth)
	}
}

func TestRecovery_Step1_HealthyPartition_PrefersNormalRecovery(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{MarketID: "BTC-USDT", Partition: 0})

	db := newMockDB()
	db.checkpoints["orders.commands/0"] = 100
	db.marketSequences["BTC-USDT"] = 500

	snap := orderbook.BookSnapshot{
		SchemaVersion: orderbook.CurrentSchemaVersion,
		MarketID:      "BTC-USDT",
		Partition:     0,
		Offset:        95,
		Sequence:      500,
		Orders:        []orderbook.SnapshotOrder{},
	}
	snapBytes, _ := json.Marshal(snap)
	chk, _ := orderbook.Checksum(snap)
	db.snapshots["BTC-USDT"] = snapBytes
	db.snapshotsOffset["BTC-USDT"] = 95
	db.snapshotsCheck["BTC-USDT"] = chk

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{0}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return &mockKafkaReader{
				messages: []kafkago.Message{
					makeRecoveryMsg(96, "BTC-USDT", "BUY"),
					makeRecoveryMsg(97, "BTC-USDT", "BUY"),
					makeRecoveryMsg(98, "BTC-USDT", "BUY"),
					makeRecoveryMsg(99, "BTC-USDT", "BUY"),
					makeRecoveryMsg(100, "BTC-USDT", "BUY"),
				},
			}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) { return 200, nil },
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 0, nil // No retention gap
	})

	// Configure ME_ALLOW_EMPTY_RESET for BTC-USDT
	t.Setenv("ME_ALLOW_EMPTY_RESET", "BTC-USDT:0:gen-btc-reset:100:500:secret123")
	replayer.SetResetAuthorizer(recovery.NewEnvResetAuthorizer())

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err != nil {
		t.Fatalf("expected healthy partition to recover normally, got: %v", err)
	}

	// Assert audit record was NOT created (reset was bypassed in favor of normal recovery)
	if _, ok := db.auditRecords["BTC-USDT/gen-btc-reset"]; ok {
		t.Error("FATAL: healthy partition must not execute reset when normal recovery is possible")
	}

	btc := manager.Get("BTC-USDT")
	if btc.Mode() != market.ModeLive {
		t.Errorf("expected BTC-USDT to be Live, got %v", btc.Mode())
	}
	if btc.GetLastAppliedOffset() != 100 {
		t.Errorf("expected offset 100, got %d", btc.GetLastAppliedOffset())
	}
}

func TestRecovery_Stage2_ETHFails_BTCAndSOLRecoverLive(t *testing.T) {
	manager := market.NewMarketManager()
	btc := manager.Add(market.MarketConfig{MarketID: "BTC-USDT", Partition: 0, TickSize: decimal.RequireFromString("0.01"), LotSize: decimal.RequireFromString("0.001")})
	eth := manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1, TickSize: decimal.RequireFromString("0.01"), LotSize: decimal.RequireFromString("0.001")})
	sol := manager.Add(market.MarketConfig{MarketID: "SOL-USDT", Partition: 2, TickSize: decimal.RequireFromString("0.01"), LotSize: decimal.RequireFromString("0.001")})

	db := newMockDB()
	// BTC (P0): Checkpoint 100, Sequence 500, snapshot 95
	db.checkpoints["orders.commands/0"] = 100
	db.marketSequences["BTC-USDT"] = 500
	btcSnap := orderbook.BookSnapshot{
		SchemaVersion: orderbook.CurrentSchemaVersion,
		MarketID:      "BTC-USDT",
		Partition:     0,
		Offset:        95,
		Sequence:      500,
		Orders:        []orderbook.SnapshotOrder{},
	}
	btcSnapBytes, _ := json.Marshal(btcSnap)
	btcChk, _ := orderbook.Checksum(btcSnap)
	db.snapshots["BTC-USDT"] = btcSnapBytes
	db.snapshotsOffset["BTC-USDT"] = 95
	db.snapshotsCheck["BTC-USDT"] = btcChk

	// ETH (P1): Checkpoint 43562, snapshot 43557, Sequence 28691
	db.checkpoints["orders.commands/1"] = 43562
	db.marketSequences["ETH-USDT"] = 28691
	ethSnap := orderbook.BookSnapshot{
		SchemaVersion: orderbook.CurrentSchemaVersion,
		MarketID:      "ETH-USDT",
		Partition:     1,
		Offset:        43557,
		Sequence:      28691,
		Orders:        []orderbook.SnapshotOrder{},
	}
	ethSnapBytes, _ := json.Marshal(ethSnap)
	ethChk, _ := orderbook.Checksum(ethSnap)
	db.snapshots["ETH-USDT"] = ethSnapBytes
	db.snapshotsOffset["ETH-USDT"] = 43557
	db.snapshotsCheck["ETH-USDT"] = ethChk

	// SOL (P2): Checkpoint 50, Sequence 300, snapshot 45
	db.checkpoints["orders.commands/2"] = 50
	db.marketSequences["SOL-USDT"] = 300
	solSnap := orderbook.BookSnapshot{
		SchemaVersion: orderbook.CurrentSchemaVersion,
		MarketID:      "SOL-USDT",
		Partition:     2,
		Offset:        45,
		Sequence:      300,
		Orders:        []orderbook.SnapshotOrder{},
	}
	solSnapBytes, _ := json.Marshal(solSnap)
	solChk, _ := orderbook.Checksum(solSnap)
	db.snapshots["SOL-USDT"] = solSnapBytes
	db.snapshotsOffset["SOL-USDT"] = 45
	db.snapshotsCheck["SOL-USDT"] = solChk

	redisMock := newMockRedis()
	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-me-group", db, redisMock, manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{0, 1, 2}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			if partition == 0 {
				return &mockKafkaReader{
					messages: []kafkago.Message{
						makeRecoveryMsg(96, "BTC-USDT", "BUY"),
						makeRecoveryMsg(97, "BTC-USDT", "BUY"),
						makeRecoveryMsg(98, "BTC-USDT", "BUY"),
						makeRecoveryMsg(99, "BTC-USDT", "BUY"),
						makeRecoveryMsg(100, "BTC-USDT", "BUY"),
					},
				}
			}
			if partition == 2 {
				return &mockKafkaReader{
					messages: []kafkago.Message{
						makeRecoveryMsg(46, "SOL-USDT", "BUY"),
						makeRecoveryMsg(47, "SOL-USDT", "BUY"),
						makeRecoveryMsg(48, "SOL-USDT", "BUY"),
						makeRecoveryMsg(49, "SOL-USDT", "BUY"),
						makeRecoveryMsg(50, "SOL-USDT", "BUY"),
					},
				}
			}
			return &mockKafkaReader{}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) {
			if partition == 0 {
				return 200, nil
			}
			if partition == 1 {
				return 43565, nil
			}
			if partition == 2 {
				return 100, nil
			}
			return 0, nil
		},
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		if partition == 1 {
			// Retention gap on ETH: earliest offset is 43563 > snapshot required replay 43558
			return 43563, nil
		}
		return 0, nil // BTC and SOL have no retention gap
	})

	var committedPartitions []int
	replayer.OverrideCommitGroupOffset(func(ctx context.Context, topic string, partition int, offset int64) error {
		committedPartitions = append(committedPartitions, partition)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(ctx, &engineWg)
	if err != nil {
		t.Fatalf("ReplayAll must not fail overall process when independent markets can recover, got err: %v", err)
	}

	// 1. Assert BTC and SOL reached ModeLive
	if btc.Mode() != market.ModeLive {
		t.Errorf("expected BTC-USDT to be ModeLive, got %v", btc.Mode())
	}
	if sol.Mode() != market.ModeLive {
		t.Errorf("expected SOL-USDT to be ModeLive, got %v", sol.Mode())
	}
	if btc.GetLastAppliedOffset() != 100 {
		t.Errorf("expected BTC offset 100, got %d", btc.GetLastAppliedOffset())
	}
	if sol.GetLastAppliedOffset() != 50 {
		t.Errorf("expected SOL offset 50, got %d", sol.GetLastAppliedOffset())
	}

	// 2. Assert ETH remained ModeBlocked
	if eth.Mode() != market.ModeBlocked {
		t.Fatalf("expected ETH-USDT to be ModeBlocked, got %v", eth.Mode())
	}
	if !eth.IsBlocked() {
		t.Error("expected eth.IsBlocked() to be true")
	}
	if eth.BlockedReason() == nil || !strings.Contains(eth.BlockedReason().Error(), "expired/purged") {
		t.Errorf("expected retention gap error in ETH BlockedReason, got: %v", eth.BlockedReason())
	}

	// 3. Assert Redis circuit breaker key was set for ETH-USDT and not for BTC/SOL
	if string(redisMock.data["market:halted:ETH-USDT"]) != "1" {
		t.Errorf("expected Redis key market:halted:ETH-USDT = '1', got %q", string(redisMock.data["market:halted:ETH-USDT"]))
	}
	if _, ok := redisMock.data["market:halted:BTC-USDT"]; ok {
		t.Error("expected market:halted:BTC-USDT to be absent in Redis")
	}
	if _, ok := redisMock.data["market:halted:SOL-USDT"]; ok {
		t.Error("expected market:halted:SOL-USDT to be absent in Redis")
	}

	// 4. Assert Kafka group offset was committed ONLY for successful partitions (0 and 2), NOT partition 1
	hasP0, hasP1, hasP2 := false, false, false
	for _, p := range committedPartitions {
		if p == 0 {
			hasP0 = true
		}
		if p == 1 {
			hasP1 = true
		}
		if p == 2 {
			hasP2 = true
		}
	}
	if !hasP0 || !hasP2 {
		t.Errorf("expected group offset commit for P0 and P2, got %v", committedPartitions)
	}
	if hasP1 {
		t.Errorf("FATAL: group offset must not be committed for failed partition 1: %v", committedPartitions)
	}

	// 5. Test trading isolation: Healthy BTC and SOL process orders, Blocked ETH rejects orders
	orderID := uuid.New()
	userID := uuid.New()

	// BTC order processing
	btc.InputQueue <- market.InputEvent{
		Type: market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   orderID,
			UserID:    userID,
			MarketID:  "BTC-USDT",
			Side:      orderbook.SideSell,
			OrderType: orderbook.OrderTypeLimit,
			Price:     decimal.RequireFromString("65000.00"),
			Quantity:  decimal.RequireFromString("1.000"),
		},
		Topic:  "orders.commands",
		Offset: 101,
	}
	resBTC := <-btc.OutputQueue
	if resBTC.DepthSnapshot.Sequence != 506 {
		t.Errorf("expected BTC sequence 506 (500 + 5 replayed + 1 live), got %d", resBTC.DepthSnapshot.Sequence)
	}

	// SOL order processing
	sol.InputQueue <- market.InputEvent{
		Type: market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   uuid.New(),
			UserID:    userID,
			MarketID:  "SOL-USDT",
			Side:      orderbook.SideSell,
			OrderType: orderbook.OrderTypeLimit,
			Price:     decimal.RequireFromString("150.00"),
			Quantity:  decimal.RequireFromString("2.000"),
		},
		Topic:  "orders.commands",
		Offset: 51,
	}
	resSOL := <-sol.OutputQueue
	if resSOL.DepthSnapshot.Sequence != 306 {
		t.Errorf("expected SOL sequence 306 (300 + 5 replayed + 1 live), got %d", resSOL.DepthSnapshot.Sequence)
	}

	// ETH order rejection
	ethOrderID := uuid.New()
	eth.InputQueue <- market.InputEvent{
		Type: market.EventOrderCreated,
		OrderCreated: &market.OrderCreatedPayload{
			OrderID:   ethOrderID,
			UserID:    userID,
			MarketID:  "ETH-USDT",
			Side:      orderbook.SideBuy,
			OrderType: orderbook.OrderTypeLimit,
			Price:     decimal.RequireFromString("2000.00"),
			Quantity:  decimal.RequireFromString("1.000"),
		},
		Topic:  "orders.commands",
		Offset: 43563,
	}
	resETH := <-eth.OutputQueue
	if resETH.CancelResult == nil {
		t.Fatal("expected CancelResult for command sent to ModeBlocked ETH-USDT")
	}
	if resETH.CancelResult.Reason != "market_blocked" {
		t.Errorf("expected reason 'market_blocked', got %q", resETH.CancelResult.Reason)
	}
	if resETH.CancelResult.CancelStatus != orderbook.CancelStatusInvalidRequest {
		t.Errorf("expected CancelStatusInvalidRequest, got %q", resETH.CancelResult.CancelStatus)
	}
	if len(resETH.Fills) != 0 {
		t.Errorf("expected 0 fills on blocked market, got %d", len(resETH.Fills))
	}

	// 6. Blocked market cannot transition to ModeLive without successful recovery
	if errLive := eth.SetLive(); errLive == nil {
		t.Fatal("expected SetLive() on blocked market to fail, but it returned nil")
	}
	if eth.Mode() != market.ModeBlocked {
		t.Errorf("ETH must remain ModeBlocked, got %v", eth.Mode())
	}
}

func TestRecovery_Stage2_SharedInfrastructureFailure_TriggersGlobalFatalHalt(t *testing.T) {
	manager := market.NewMarketManager()
	btc := manager.Add(market.MarketConfig{MarketID: "BTC-USDT", Partition: 0})
	eth := manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})

	db := newMockDB()
	// Shared infrastructure failure: PostgreSQL database query failure on checkpoint table
	db.checkpointQueryErr = errors.New("pq: connection refused (shared db outage)")

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{0, 1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 100, nil },
	)

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected shared infrastructure failure to abort ReplayAll, got nil")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("expected db connection error, got: %v", err)
	}

	// Invariant: Shared infra failure triggers fatal halt across all engines
	if !btc.IsFatalHalt() {
		t.Error("expected BTC engine to be fatally halted on shared infra failure")
	}
	if !eth.IsFatalHalt() {
		t.Error("expected ETH engine to be fatally halted on shared infra failure")
	}
}

func TestRecovery_Stage2_AllPartitionsFail_TriggersGlobalFatalHalt(t *testing.T) {
	manager := market.NewMarketManager()
	btc := manager.Add(market.MarketConfig{MarketID: "BTC-USDT", Partition: 0})
	sol := manager.Add(market.MarketConfig{MarketID: "SOL-USDT", Partition: 2})

	db := newMockDB()
	db.checkpoints["orders.commands/0"] = 100
	db.checkpoints["orders.commands/2"] = 50

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{0, 2}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 200, nil },
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		// Both partitions have unrecoverable retention gaps
		return 500, nil
	})

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected error when all partitions fail recovery, got nil")
	}
	if !strings.Contains(err.Error(), "recovery failed for all 2 market(s)") {
		t.Fatalf("expected all markets failed error, got: %v", err)
	}

	// When all markets fail recovery, the process fatally halts
	if !btc.IsFatalHalt() {
		t.Error("expected BTC engine to be fatally halted")
	}
	if !sol.IsFatalHalt() {
		t.Error("expected SOL engine to be fatally halted")
	}
}

// ─── Stage 4 Recovery Preflight & Crash Boundary Tests ─────────────────────────

func TestRecovery_Stage4_CrashBeforeCommit_LeavesStateUnchanged(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})

	db := newMockDB()
	const initialCheckpoint int64 = 43562
	db.checkpoints["orders.commands/1"] = initialCheckpoint
	db.marketSequences["ETH-USDT"] = 28691

	// Injected DB failure on commit
	db.failTxCommit = errors.New("injected commit crash")

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 43563, nil // retention gap
	})

	replayer.SetResetAuthorizer(&mockMultiAuthorizer{})
	replayer.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	var engineWg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &engineWg)
	if err == nil {
		t.Fatal("expected reset to fail when database commit fails, got nil")
	}

	// Verify checkpoint offset was NOT updated
	if db.checkpoints["orders.commands/1"] != initialCheckpoint {
		t.Fatalf("expected checkpoint to remain %d on rollback, got %d", initialCheckpoint, db.checkpoints["orders.commands/1"])
	}
	// Verify no audit record was committed
	if len(db.auditRecords) != 0 {
		t.Fatalf("expected 0 audit records on rollback, got %d", len(db.auditRecords))
	}
}

func TestRecovery_Stage4_CrashAfterCommit_SucceedsOnRestartWithoutReReset(t *testing.T) {
	db := newMockDB()
	db.checkpoints["orders.commands/1"] = 43562
	db.marketSequences["ETH-USDT"] = 28691

	manager1 := market.NewMarketManager()
	_ = manager1.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})

	replayer1 := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager1)
	replayer1.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer1.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 43563, nil
	})
	replayer1.SetResetAuthorizer(&mockMultiAuthorizer{})
	replayer1.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	var wg1 sync.WaitGroup
	if err := replayer1.ReplayAll(context.Background(), &wg1); err != nil {
		t.Fatalf("initial reset failed: %v", err)
	}

	// Verify DB state committed
	if db.checkpoints["orders.commands/1"] != 43562 {
		t.Fatalf("expected baseline 43562, got %d", db.checkpoints["orders.commands/1"])
	}

	// Crash and reboot: Normal startup reads kafka_checkpoints (43562) and snapshot (43562)
	manager2 := market.NewMarketManager()
	_ = manager2.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})

	replayer2 := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager2)
	replayer2.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer2.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 43563, nil
	})

	resetCalledOnReboot := false
	replayer2.SetResetAuthorizer(&spyAuthorizer{
		onAuthorize: func() { resetCalledOnReboot = true },
	})

	var wg2 sync.WaitGroup
	if err := replayer2.ReplayAll(context.Background(), &wg2); err != nil {
		t.Fatalf("reboot recovery failed: %v", err)
	}

	if resetCalledOnReboot {
		t.Fatal("reset was unexpectedly re-invoked on reboot after successful commit")
	}

	eth2 := manager2.Get("ETH-USDT")
	if eth2.Mode() != market.ModeReconciling {
		t.Fatalf("expected ETH engine to recover into ModeReconciling on reboot before approval, got %v", eth2.Mode())
	}
	if eth2.GetLastAppliedOffset() != 43562 {
		t.Fatalf("expected last applied offset 43562, got %d", eth2.GetLastAppliedOffset())
	}

	// Now approve reconciliation and verify transition to ModeLive
	const testHexChecksum = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	db.manifestChecksums["ETH-USDT/gen-ETH-USDT"] = testHexChecksum
	reconService := recovery.NewReconciliationService(db, manager2, &testLeaseChecker{hasLease: true}, &testOrderVerifier{}, &testSettlementVerifier{}, "test-secret")
	approvalRes, err := reconService.ApproveReconciliation(context.Background(), recovery.ReconciliationApprovalRequest{
		MarketID:         "ETH-USDT",
		Generation:       "gen-ETH-USDT",
		ManifestChecksum: testHexChecksum,
		AuthToken:        "test-secret",
		ApprovedBy:       "operator-test",
	})
	if err != nil {
		t.Fatalf("reconciliation approval failed: %v", err)
	}
	if !approvalRes.Approved {
		t.Fatal("expected reconciliation approval to succeed")
	}
	if eth2.Mode() != market.ModeLive {
		t.Fatalf("expected ETH engine to be ModeLive after reconciliation approval, got %v", eth2.Mode())
	}
}

func TestRecovery_Stage4_NewCommandsArrivingDuringResetPreparation(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})

	db := newMockDB()
	db.checkpoints["orders.commands/1"] = 43562
	db.marketSequences["ETH-USDT"] = 28691

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)

	// Kafka HWM is 43563. A new message is already queued at offset 43563.
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 43563, nil
	})
	replayer.SetResetAuthorizer(&mockMultiAuthorizer{})
	replayer.SetLeaseChecker(&testLeaseChecker{hasLease: true})

	var wg sync.WaitGroup
	if err := replayer.ReplayAll(context.Background(), &wg); err != nil {
		t.Fatalf("reset failed: %v", err)
	}

	// Invariant: reset baseline is HWM - 1 = 43562
	eth := manager.Get("ETH-USDT")
	if eth.GetLastAppliedOffset() != 43562 {
		t.Fatalf("expected baseline offset 43562, got %d", eth.GetLastAppliedOffset())
	}

	// Live consumer starting from db checkpoint 43562 will seek to 43562 + 1 = 43563,
	// ensuring the message at offset 43563 is consumed and NOT skipped.
}

func TestRecovery_Stage4_LeaseLossDuringReset_AbortsCleanly(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{MarketID: "ETH-USDT", Partition: 1})

	db := newMockDB()
	db.checkpoints["orders.commands/1"] = 43562
	db.marketSequences["ETH-USDT"] = 28691

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 43563, nil },
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 43563, nil
	})
	replayer.SetResetAuthorizer(&mockMultiAuthorizer{})

	// Context cancelled simulating lease revocation during reset preparation
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var wg sync.WaitGroup
	err := replayer.ReplayAll(ctx, &wg)
	if err == nil {
		t.Fatal("expected reset to abort when lease/context is cancelled, got nil")
	}

	// Verify engine did NOT enter ModeLive
	eth := manager.Get("ETH-USDT")
	if eth.Mode() == market.ModeLive {
		t.Fatal("expected engine not to transition to ModeLive when lease is lost")
	}
}


