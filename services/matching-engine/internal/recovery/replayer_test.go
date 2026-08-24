package recovery_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/redis/go-redis/v9"
	"github.com/shopspring/decimal"

	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/recovery"
	"tradedrift/services/matching-engine/internal/orderbook"
	"tradedrift/services/matching-engine/internal/matcher"
)

// ─── Mocks ──────────────────────────────────────────────────────────────────

type mockDB struct {
	checkpoints     map[string]int64 // key: topic/partition
	marketSequences map[string]uint64
	snapshots       map[string][]byte
	snapshotsOffset map[string]int64
	snapshotsCheck  map[string][]byte
}

func newMockDB() *mockDB {
	return &mockDB{
		checkpoints:     make(map[string]int64),
		marketSequences: make(map[string]uint64),
		snapshots:       make(map[string][]byte),
		snapshotsOffset: make(map[string]int64),
		snapshotsCheck:  make(map[string][]byte),
	}
}

func (m *mockDB) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (m *mockDB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	// A. loadCheckpoint
	if sql == "\n\t\tSELECT \"offset\" FROM kafka_checkpoints\n\t\tWHERE topic = $1 AND partition = $2" {
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

	// D. loadMarketSequence
	if sql == "SELECT sequence FROM market_sequences WHERE market_id = $1" {
		marketID := args[0].(string)
		if seq, ok := m.marketSequences[marketID]; ok {
			return &customRow{vals: []any{seq}}
		}
		return &customRow{vals: []any{uint64(0)}}
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

type mockRedis struct {
	data map[string][]byte
}

func newMockRedis() *mockRedis {
	return &mockRedis{data: make(map[string][]byte)}
}

func (r *mockRedis) Set(ctx context.Context, key string, value any, expiration time.Duration) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(ctx)
	r.data[key] = value.([]byte)
	return cmd
}

type mockKafkaReader struct {
	offset   int64
	messages []kafkago.Message
	msgIndex int
}

func (m *mockKafkaReader) SetOffset(offset int64) error {
	m.offset = offset
	return nil
}

func (m *mockKafkaReader) FetchMessage(ctx context.Context) (kafkago.Message, error) {
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




