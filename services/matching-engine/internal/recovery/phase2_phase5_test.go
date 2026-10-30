package recovery_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/shopspring/decimal"

	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/orderbook"
	"tradedrift/services/matching-engine/internal/recovery"
)

// -----------------------------------------------------------------------------
// Phase 2 Tests: Prove Kafka Offset Semantics & Boundaries
// -----------------------------------------------------------------------------

func TestOffsetSemantics_EmptyPartition(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "BTC-USDT",
		Partition: 0,
		TickSize:  decimal.RequireFromString("0.01"),
		LotSize:   decimal.RequireFromString("0.001"),
	})

	db := newMockDB()
	db.checkpoints["orders.commands/0"] = -1
	db.marketSequences["BTC-USDT"] = 0

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{0}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader {
			return &mockKafkaReader{messages: []kafkago.Message{}}
		},
		func(ctx context.Context, topic string, partition int) (int64, error) { return 0, nil }, // HWM = 0
	)

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := replayer.ReplayAll(ctx, &wg); err != nil {
		t.Fatalf("expected empty partition recovery to succeed, got %v", err)
	}

	btc := manager.Get("BTC-USDT")
	if btc.Mode() != market.ModeLive {
		t.Fatalf("expected BTC-USDT to be LIVE, got %v", btc.Mode())
	}
	if btc.GetLastAppliedOffset() != -1 {
		t.Fatalf("expected last applied offset -1, got %d", btc.GetLastAppliedOffset())
	}
	if btc.GetSequence() != 0 {
		t.Fatalf("expected sequence 0, got %d", btc.GetSequence())
	}
}

func TestOffsetSemantics_OneMessagePartition(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "BTC-USDT",
		Partition: 0,
		TickSize:  decimal.RequireFromString("0.01"),
		LotSize:   decimal.RequireFromString("0.001"),
	})

	db := newMockDB()
	// Durable checkpoint at 0 (1 message processed in history at offset 0)
	db.checkpoints["orders.commands/0"] = 0
	db.marketSequences["BTC-USDT"] = 1

	reader := &mockKafkaReader{
		messages: []kafkago.Message{
			makeRecoveryMsg(0, "BTC-USDT", "BUY"),
		},
	}

	var committedOffset int64 = -1
	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{0}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return reader },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 1, nil }, // HWM = 1 (next write is 1)
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 0, nil
	})
	replayer.OverrideCommitGroupOffset(func(ctx context.Context, topic string, partition int, offset int64) error {
		committedOffset = offset
		return nil
	})

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := replayer.ReplayAll(ctx, &wg); err != nil {
		t.Fatalf("one message partition recovery failed: %v", err)
	}

	btc := manager.Get("BTC-USDT")
	if btc.Mode() != market.ModeLive {
		t.Fatalf("expected BTC to be LIVE, got %v", btc.Mode())
	}
	if btc.GetLastAppliedOffset() != 0 {
		t.Fatalf("expected last applied offset 0, got %d", btc.GetLastAppliedOffset())
	}
	if committedOffset != 0 {
		t.Fatalf("expected aligned Kafka group offset 0, got %d", committedOffset)
	}
}

func TestOffsetSemantics_SnapshotEqualCheckpoint(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "BTC-USDT",
		Partition: 0,
		TickSize:  decimal.RequireFromString("0.01"),
		LotSize:   decimal.RequireFromString("0.001"),
	})

	db := newMockDB()
	const snapOffset = int64(100)
	const snapSeq = uint64(500)

	db.checkpoints["orders.commands/0"] = snapOffset
	db.marketSequences["BTC-USDT"] = snapSeq

	snap := orderbook.BookSnapshot{
		MarketID:      "BTC-USDT",
		Sequence:      snapSeq,
		Partition:     0,
		Offset:        snapOffset,
		SchemaVersion: 1,
		Orders:        []orderbook.SnapshotOrder{},
	}
	snapBytes, _ := json.Marshal(snap)
	chk, _ := orderbook.Checksum(snap)

	db.snapshots["BTC-USDT"] = snapBytes
	db.snapshotsCheck["BTC-USDT"] = chk
	db.snapshotsOffset["BTC-USDT"] = snapOffset

	reader := &mockKafkaReader{
		messages: []kafkago.Message{},
	}

	var committedGroupOffset int64 = -1
	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{0}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return reader },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 200, nil }, // HWM = 200
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 0, nil
	})
	replayer.OverrideCommitGroupOffset(func(ctx context.Context, topic string, partition int, offset int64) error {
		committedGroupOffset = offset
		return nil
	})

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := replayer.ReplayAll(ctx, &wg); err != nil {
		t.Fatalf("recovery failed: %v", err)
	}

	btc := manager.Get("BTC-USDT")
	if btc.Mode() != market.ModeLive {
		t.Fatalf("expected LIVE, got %v", btc.Mode())
	}
	if btc.GetLastAppliedOffset() != snapOffset {
		t.Fatalf("expected lastAppliedOffset %d, got %d", snapOffset, btc.GetLastAppliedOffset())
	}
	if committedGroupOffset != snapOffset {
		t.Fatalf("expected committed group offset %d, got %d", snapOffset, committedGroupOffset)
	}
	// Verify reader was NOT asked to seek because startOffset (101) > checkpoint (100)
	if reader.offset != 0 {
		t.Fatalf("expected reader seek bypassed, but reader offset was %d", reader.offset)
	}
}

func TestOffsetSemantics_SnapshotBelowCheckpoint(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "BTC-USDT",
		Partition: 0,
		TickSize:  decimal.RequireFromString("0.01"),
		LotSize:   decimal.RequireFromString("0.001"),
	})

	db := newMockDB()
	const snapOffset = int64(95)
	const checkpointOffset = int64(100)
	const snapSeq = uint64(500)

	db.checkpoints["orders.commands/0"] = checkpointOffset
	db.marketSequences["BTC-USDT"] = snapSeq + 5 // 5 additional commands up to offset 100

	snap := orderbook.BookSnapshot{
		MarketID:      "BTC-USDT",
		Sequence:      snapSeq,
		Partition:     0,
		Offset:        snapOffset,
		SchemaVersion: 1,
		Orders:        []orderbook.SnapshotOrder{},
	}
	snapBytes, _ := json.Marshal(snap)
	chk, _ := orderbook.Checksum(snap)

	db.snapshots["BTC-USDT"] = snapBytes
	db.snapshotsCheck["BTC-USDT"] = chk
	db.snapshotsOffset["BTC-USDT"] = snapOffset

	// Messages from offset 96 through 100
	messages := make([]kafkago.Message, 0, 5)
	for off := int64(96); off <= 100; off++ {
		messages = append(messages, makeRecoveryMsg(off, "BTC-USDT", "BUY"))
	}
	reader := &mockKafkaReader{messages: messages}

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{0}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return reader },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 200, nil },
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 0, nil
	})

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := replayer.ReplayAll(ctx, &wg); err != nil {
		t.Fatalf("recovery failed: %v", err)
	}

	btc := manager.Get("BTC-USDT")
	if btc.Mode() != market.ModeLive {
		t.Fatalf("expected LIVE mode, got %v", btc.Mode())
	}
	if btc.GetLastAppliedOffset() != checkpointOffset {
		t.Fatalf("expected engine offset %d, got %d", checkpointOffset, btc.GetLastAppliedOffset())
	}
	if reader.offset != 96 {
		t.Fatalf("expected replay startOffset 96 (snapOffset+1), got %d", reader.offset)
	}
}

func TestOffsetSemantics_CheckpointAtOrBeyondHWM_FatalHalt(t *testing.T) {
	testCases := []struct {
		name       string
		checkpoint int64
		hwm        int64
	}{
		{"CheckpointEqualHWM", 100, 100},
		{"CheckpointAheadOfHWM", 150, 100},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			manager := market.NewMarketManager()
			_ = manager.Add(market.MarketConfig{
				MarketID:  "BTC-USDT",
				Partition: 0,
			})

			db := newMockDB()
			db.checkpoints["orders.commands/0"] = tc.checkpoint

			replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
			replayer.OverrideDiscoveryAndReader(
				func(topic string) ([]int, error) { return []int{0}, nil },
				func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
				func(ctx context.Context, topic string, partition int) (int64, error) { return tc.hwm, nil },
			)

			var wg sync.WaitGroup
			err := replayer.ReplayAll(context.Background(), &wg)
			if err == nil {
				t.Fatalf("expected fatal error when checkpoint (%d) >= HWM (%d), got nil", tc.checkpoint, tc.hwm)
			}
			if !strings.Contains(err.Error(), "checkpoint >= logEndOffset") {
				t.Fatalf("expected error mentioning 'checkpoint >= logEndOffset', got %v", err)
			}
		})
	}
}

func TestOffsetSemantics_MultipleMarketsSharingPartition(t *testing.T) {
	manager := market.NewMarketManager()
	btc := manager.Add(market.MarketConfig{
		MarketID:  "BTC-USDT",
		Partition: 0,
		TickSize:  decimal.RequireFromString("0.01"),
		LotSize:   decimal.RequireFromString("0.001"),
	})
	sol := manager.Add(market.MarketConfig{
		MarketID:  "SOL-USDT",
		Partition: 0, // Shares partition 0 with BTC
		TickSize:  decimal.RequireFromString("0.01"),
		LotSize:   decimal.RequireFromString("0.001"),
	})

	db := newMockDB()
	const checkpointOffset = int64(60)

	// BTC snapshot at offset 50, sequence 100
	btcSnap := orderbook.BookSnapshot{
		MarketID: "BTC-USDT", Sequence: 100, Partition: 0, Offset: 50, SchemaVersion: 1,
	}
	btcBytes, _ := json.Marshal(btcSnap)
	btcChk, _ := orderbook.Checksum(btcSnap)

	// SOL snapshot at offset 30, sequence 50 (older snapshot)
	solSnap := orderbook.BookSnapshot{
		MarketID: "SOL-USDT", Sequence: 50, Partition: 0, Offset: 30, SchemaVersion: 1,
	}
	solBytes, _ := json.Marshal(solSnap)
	solChk, _ := orderbook.Checksum(solSnap)

	db.checkpoints["orders.commands/0"] = checkpointOffset
	db.marketSequences["BTC-USDT"] = 105
	db.marketSequences["SOL-USDT"] = 65

	db.snapshotCandidates["BTC-USDT"] = []mockSnapshotRecord{{
		marketID: "BTC-USDT", sequence: 100, partition: 0, offset: 50, schemaVersion: 1, snapJSON: btcBytes, checksum: btcChk,
	}}
	db.snapshotCandidates["SOL-USDT"] = []mockSnapshotRecord{{
		marketID: "SOL-USDT", sequence: 50, partition: 0, offset: 30, schemaVersion: 1, snapJSON: solBytes, checksum: solChk,
	}}

	// Messages from min(50, 30)+1 = 31 up to 60
	messages := make([]kafkago.Message, 0, 30)
	for off := int64(31); off <= 60; off++ {
		// Alternate markets
		if off%2 == 0 {
			messages = append(messages, makeRecoveryMsg(off, "BTC-USDT", "BUY"))
		} else {
			messages = append(messages, makeRecoveryMsg(off, "SOL-USDT", "BUY"))
		}
	}
	reader := &mockKafkaReader{messages: messages}

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{0}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return reader },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 100, nil },
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 0, nil
	})

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := replayer.ReplayAll(ctx, &wg); err != nil {
		t.Fatalf("recovery failed for shared partition: %v", err)
	}

	if btc.Mode() != market.ModeLive || sol.Mode() != market.ModeLive {
		t.Fatalf("expected both markets LIVE, got BTC=%v SOL=%v", btc.Mode(), sol.Mode())
	}
	if btc.GetLastAppliedOffset() != 60 || sol.GetLastAppliedOffset() != 59 {
		t.Fatalf("expected BTC at offset 60 and SOL at offset 59, got BTC=%d SOL=%d", btc.GetLastAppliedOffset(), sol.GetLastAppliedOffset())
	}
	// Invariant: Replay started from min(snapshot.Offset) + 1 = 30 + 1 = 31
	if reader.offset != 31 {
		t.Fatalf("expected replay startOffset 31, got %d", reader.offset)
	}
}

// -----------------------------------------------------------------------------
// Phase 5 Tests: Snapshot Corruption, Multi-Candidate Validation & Fallback
// -----------------------------------------------------------------------------

func TestSnapshotFallback_CorruptLatest_ValidOlderCandidate(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
		TickSize:  decimal.RequireFromString("0.01"),
		LotSize:   decimal.RequireFromString("0.001"),
	})

	db := newMockDB()
	const checkpointOffset = int64(100)

	// Valid older snapshot at offset 80, seq 200
	olderSnap := orderbook.BookSnapshot{
		MarketID: "ETH-USDT", Sequence: 200, Partition: 1, Offset: 80, SchemaVersion: 1,
	}
	olderBytes, _ := json.Marshal(olderSnap)
	olderChk, _ := orderbook.Checksum(olderSnap)

	// Corrupt latest snapshot at offset 98: invalid checksum
	corruptLatestSnap := orderbook.BookSnapshot{
		MarketID: "ETH-USDT", Sequence: 218, Partition: 1, Offset: 98, SchemaVersion: 1,
	}
	corruptBytes, _ := json.Marshal(corruptLatestSnap)
	corruptChk := []byte{0xDE, 0xAD, 0xBE, 0xEF} // Tampered / corrupt checksum

	db.checkpoints["orders.commands/1"] = checkpointOffset
	db.marketSequences["ETH-USDT"] = 220

	// Ordered DESC: newest candidate first
	db.snapshotCandidates["ETH-USDT"] = []mockSnapshotRecord{
		{marketID: "ETH-USDT", sequence: 218, partition: 1, offset: 98, schemaVersion: 1, snapJSON: corruptBytes, checksum: corruptChk},
		{marketID: "ETH-USDT", sequence: 200, partition: 1, offset: 80, schemaVersion: 1, snapJSON: olderBytes, checksum: olderChk},
	}

	// Replay messages from offset 81 (olderSnap + 1) through 100
	messages := make([]kafkago.Message, 0, 20)
	for off := int64(81); off <= 100; off++ {
		messages = append(messages, makeRecoveryMsg(off, "ETH-USDT", "BUY"))
	}
	reader := &mockKafkaReader{messages: messages}

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return reader },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 200, nil },
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return 0, nil
	})

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := replayer.ReplayAll(ctx, &wg); err != nil {
		t.Fatalf("expected fallback to older snapshot to succeed, got %v", err)
	}

	eth := manager.Get("ETH-USDT")
	if eth.Mode() != market.ModeLive {
		t.Fatalf("expected ETH to be LIVE, got %v", eth.Mode())
	}
	if eth.GetLastAppliedOffset() != checkpointOffset {
		t.Fatalf("expected lastAppliedOffset %d, got %d", checkpointOffset, eth.GetLastAppliedOffset())
	}
	// Verify replay started from older snapshot offset + 1 = 81
	if reader.offset != 81 {
		t.Fatalf("expected replay startOffset 81, got %d", reader.offset)
	}
}

func TestSnapshotFallback_InvalidJSONPayload_FallsBack(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
	})

	db := newMockDB()
	const checkpointOffset = int64(50)

	// Valid older snapshot at offset 40
	olderSnap := orderbook.BookSnapshot{
		MarketID: "ETH-USDT", Sequence: 100, Partition: 1, Offset: 40, SchemaVersion: 1,
	}
	olderBytes, _ := json.Marshal(olderSnap)
	olderChk, _ := orderbook.Checksum(olderSnap)

	db.checkpoints["orders.commands/1"] = checkpointOffset
	db.marketSequences["ETH-USDT"] = 110

	db.snapshotCandidates["ETH-USDT"] = []mockSnapshotRecord{
		{marketID: "ETH-USDT", sequence: 109, partition: 1, offset: 49, schemaVersion: 1, snapJSON: []byte(`{not-json-corrupt`), checksum: olderChk},
		{marketID: "ETH-USDT", sequence: 100, partition: 1, offset: 40, schemaVersion: 1, snapJSON: olderBytes, checksum: olderChk},
	}

	messages := make([]kafkago.Message, 0, 10)
	for off := int64(41); off <= 50; off++ {
		messages = append(messages, makeRecoveryMsg(off, "ETH-USDT", "BUY"))
	}
	reader := &mockKafkaReader{messages: messages}

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return reader },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 100, nil },
	)

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := replayer.ReplayAll(ctx, &wg); err != nil {
		t.Fatalf("expected fallback after corrupt JSON to succeed, got %v", err)
	}

	eth := manager.Get("ETH-USDT")
	if eth.Mode() != market.ModeLive {
		t.Fatalf("expected LIVE, got %v", eth.Mode())
	}
	if reader.offset != 41 {
		t.Fatalf("expected start offset 41, got %d", reader.offset)
	}
}

func TestSnapshotFallback_MetadataPayloadMismatch_FallsBack(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
	})

	db := newMockDB()
	const checkpointOffset = int64(50)

	olderSnap := orderbook.BookSnapshot{
		MarketID: "ETH-USDT", Sequence: 100, Partition: 1, Offset: 40, SchemaVersion: 1,
	}
	olderBytes, _ := json.Marshal(olderSnap)
	olderChk, _ := orderbook.Checksum(olderSnap)

	// Latest snapshot has DB columns saying offset 49, but payload JSON says offset 30
	mismatchedSnap := orderbook.BookSnapshot{
		MarketID: "ETH-USDT", Sequence: 100, Partition: 1, Offset: 30, SchemaVersion: 1,
	}
	mismatchedBytes, _ := json.Marshal(mismatchedSnap)
	mismatchedChk, _ := orderbook.Checksum(mismatchedSnap)

	db.checkpoints["orders.commands/1"] = checkpointOffset
	db.marketSequences["ETH-USDT"] = 110

	db.snapshotCandidates["ETH-USDT"] = []mockSnapshotRecord{
		{marketID: "ETH-USDT", sequence: 109, partition: 1, offset: 49, schemaVersion: 1, snapJSON: mismatchedBytes, checksum: mismatchedChk},
		{marketID: "ETH-USDT", sequence: 100, partition: 1, offset: 40, schemaVersion: 1, snapJSON: olderBytes, checksum: olderChk},
	}

	messages := make([]kafkago.Message, 0, 10)
	for off := int64(41); off <= 50; off++ {
		messages = append(messages, makeRecoveryMsg(off, "ETH-USDT", "BUY"))
	}
	reader := &mockKafkaReader{messages: messages}

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return reader },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 100, nil },
	)

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := replayer.ReplayAll(ctx, &wg); err != nil {
		t.Fatalf("expected fallback after metadata mismatch to succeed, got %v", err)
	}

	eth := manager.Get("ETH-USDT")
	if eth.Mode() != market.ModeLive {
		t.Fatalf("expected LIVE, got %v", eth.Mode())
	}
	if reader.offset != 41 {
		t.Fatalf("expected start offset 41, got %d", reader.offset)
	}
}

func TestSnapshotFallback_AllCandidatesCorrupt_FailsClosed(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
	})

	db := newMockDB()
	const checkpointOffset = int64(50)

	db.checkpoints["orders.commands/1"] = checkpointOffset
	db.marketSequences["ETH-USDT"] = 110

	db.snapshotCandidates["ETH-USDT"] = []mockSnapshotRecord{
		{marketID: "ETH-USDT", sequence: 109, partition: 1, offset: 49, schemaVersion: 1, snapJSON: []byte(`corrupt-1`), checksum: []byte{0x01}},
		{marketID: "ETH-USDT", sequence: 100, partition: 1, offset: 40, schemaVersion: 1, snapJSON: []byte(`corrupt-2`), checksum: []byte{0x02}},
	}

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 100, nil },
	)

	var wg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &wg)
	if err == nil {
		t.Fatal("expected fail-closed error when all snapshots are corrupt, got nil")
	}

	if !strings.Contains(err.Error(), "corrupted or unrecoverable") {
		t.Fatalf("expected error mentioning 'corrupted or unrecoverable', got %v", err)
	}

	eth := manager.Get("ETH-USDT")
	if eth.Mode() == market.ModeLive {
		t.Fatal("expected ETH not to be LIVE when all snapshots corrupt")
	}
}

func TestSnapshotFallback_SnapshotAheadOfCheckpoint_FailsClosed(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "BTC-USDT",
		Partition: 0,
	})

	db := newMockDB()
	db.checkpoints["orders.commands/0"] = 100
	db.snapshotsOffset["BTC-USDT"] = 120 // Snapshot offset ahead of checkpoint

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{0}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 200, nil },
	)

	var wg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &wg)
	if err == nil {
		t.Fatal("expected snapshot beyond checkpoint to fail closed, got nil")
	}
	if !strings.Contains(err.Error(), "beyond partition checkpoint") {
		t.Fatalf("expected error mentioning beyond partition checkpoint, got %v", err)
	}
	btc := manager.Get("BTC-USDT")
	if btc.BlockedReason() == nil || !errors.Is(btc.BlockedReason(), orderbook.ErrSnapshotBeyondCheckpoint) {
		t.Fatalf("expected BTC BlockedReason to wrap ErrSnapshotBeyondCheckpoint, got %v", btc.BlockedReason())
	}
}

func TestSnapshotFallback_SnapshotOlderThanKafkaRetention_FailsClosed(t *testing.T) {
	manager := market.NewMarketManager()
	_ = manager.Add(market.MarketConfig{
		MarketID:  "ETH-USDT",
		Partition: 1,
	})

	db := newMockDB()
	const checkpointOffset = int64(100)
	const snapOffset = int64(30)
	const kafkaLWM = int64(50) // Kafka has purged everything before 50

	snap := orderbook.BookSnapshot{
		MarketID: "ETH-USDT", Sequence: 100, Partition: 1, Offset: snapOffset, SchemaVersion: 1,
	}
	snapBytes, _ := json.Marshal(snap)
	chk, _ := orderbook.Checksum(snap)

	db.checkpoints["orders.commands/1"] = checkpointOffset
	db.marketSequences["ETH-USDT"] = 150
	db.snapshotCandidates["ETH-USDT"] = []mockSnapshotRecord{
		{marketID: "ETH-USDT", sequence: 100, partition: 1, offset: snapOffset, schemaVersion: 1, snapJSON: snapBytes, checksum: chk},
	}

	replayer := recovery.NewReplayer([]string{"localhost:9092"}, "test-group", db, newMockRedis(), manager)
	replayer.OverrideDiscoveryAndReader(
		func(topic string) ([]int, error) { return []int{1}, nil },
		func(brokers []string, topic string, partition int) recovery.KafkaReader { return &mockKafkaReader{} },
		func(ctx context.Context, topic string, partition int) (int64, error) { return 200, nil },
	)
	replayer.OverrideLWM(func(ctx context.Context, topic string, partition int) (int64, error) {
		return kafkaLWM, nil
	})

	var wg sync.WaitGroup
	err := replayer.ReplayAll(context.Background(), &wg)
	if err == nil {
		t.Fatal("expected recovery to fail when snapshot replay start offset is behind LWM, got nil")
	}
	if !strings.Contains(err.Error(), "snapshot is too old for available Kafka history") {
		t.Fatalf("expected error mentioning snapshot too old for Kafka history, got %v", err)
	}
}
