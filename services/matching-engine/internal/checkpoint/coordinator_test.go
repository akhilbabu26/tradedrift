package checkpoint_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	kafkago "github.com/segmentio/kafka-go"
	"tradedrift/services/matching-engine/internal/checkpoint"
	"tradedrift/services/matching-engine/internal/orderbook"
)

type fakeDB struct {
	commits []int64
}

func (f *fakeDB) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	if len(args) >= 3 {
		if offset, ok := args[2].(int64); ok {
			f.commits = append(f.commits, offset)
		}
	}
	return pgconn.CommandTag{}, nil
}

func TestCoordinator_ContiguousAdvancement(t *testing.T) {
	db := &fakeDB{}
	coord := checkpoint.NewCoordinator(db)
	ctx := context.Background()
	topic := "orders.submitted"
	const partition = 0

	coord.InitBaseline(topic, partition, 99)

	// Step 1: Track incoming offsets 100, 101, 102
	coord.Track(orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 100})
	coord.Track(orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 101})
	coord.Track(orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 102})

	// Step 2: Offset 101 finishes FIRST (e.g. fast market ETH)
	if err := coord.MarkDone(ctx, orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 101}); err != nil {
		t.Fatalf("MarkDone 101: %v", err)
	}

	// Invariant Check: Offset 100 is still pending, so checkpoint must NOT advance to 101!
	committed, _ := coord.GetCommittedOffset(topic, partition)
	if committed != 99 {
		t.Fatalf("expected committed offset to stay 99 while 100 is in-flight, got %d", committed)
	}
	if len(db.commits) != 0 {
		t.Fatalf("expected 0 DB commits while gap exists, got %d", len(db.commits))
	}

	// Step 3: Offset 100 finishes (e.g. slower market BTC)
	if err := coord.MarkDone(ctx, orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 100}); err != nil {
		t.Fatalf("MarkDone 100: %v", err)
	}

	// Invariant Check: Since 100 and 101 are now BOTH finished, contiguous watermark must jump to 101!
	committed, _ = coord.GetCommittedOffset(topic, partition)
	if committed != 101 {
		t.Fatalf("expected committed offset to advance to 101, got %d", committed)
	}
	if len(db.commits) != 1 || db.commits[0] != 101 {
		t.Fatalf("expected 1 DB commit with offset 101, got %+v", db.commits)
	}

	// Step 4: Offset 102 finishes
	if err := coord.MarkDone(ctx, orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 102}); err != nil {
		t.Fatalf("MarkDone 102: %v", err)
	}
	committed, _ = coord.GetCommittedOffset(topic, partition)
	if committed != 102 {
		t.Fatalf("expected committed offset to advance to 102, got %d", committed)
	}
	if len(db.commits) != 2 || db.commits[1] != 102 {
		t.Fatalf("expected second DB commit with offset 102, got %+v", db.commits)
	}
}

func TestCoordinator_MultiGapResolution(t *testing.T) {
	db := &fakeDB{}
	coord := checkpoint.NewCoordinator(db)
	ctx := context.Background()
	topic := "orders.submitted"
	const partition = 0

	coord.InitBaseline(topic, partition, 99)

	// Offsets 101, 103, 104 complete while 100 and 102 are pending
	_ = coord.MarkDone(ctx, orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 101})
	_ = coord.MarkDone(ctx, orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 103})
	_ = coord.MarkDone(ctx, orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 104})

	committed, _ := coord.GetCommittedOffset(topic, partition)
	if committed != 99 {
		t.Fatalf("expected committed offset to stay 99, got %d", committed)
	}

	// 100 completes -> advances to 101
	_ = coord.MarkDone(ctx, orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 100})
	committed, _ = coord.GetCommittedOffset(topic, partition)
	if committed != 101 {
		t.Fatalf("expected committed offset to advance to 101, got %d", committed)
	}

	// 102 completes -> advances across 103 and 104 to 104!
	_ = coord.MarkDone(ctx, orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 102})
	committed, _ = coord.GetCommittedOffset(topic, partition)
	if committed != 104 {
		t.Fatalf("expected committed offset to advance to 104, got %d", committed)
	}
}

type fakeKafkaCommitter struct {
	committedOffsets []int64
}

func (f *fakeKafkaCommitter) CommitMessages(_ context.Context, msgs ...kafkago.Message) error {
	for _, m := range msgs {
		f.committedOffsets = append(f.committedOffsets, m.Offset)
	}
	return nil
}

func TestCoordinator_KafkaConsumerGroupCommitted(t *testing.T) {
	db := &fakeDB{}
	coord := checkpoint.NewCoordinator(db)
	committer := &fakeKafkaCommitter{}
	topic := "orders.submitted"
	const partition = 0

	coord.RegisterCommitter(topic, committer)
	coord.InitBaseline(topic, partition, 99)
	ctx := context.Background()

	// Offset 100 completes
	coord.Track(orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 100})
	if err := coord.MarkDone(ctx, orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 100}); err != nil {
		t.Fatalf("MarkDone 100: %v", err)
	}

	// Verify both Postgres AND Kafka consumer group received offset 100
	if len(db.commits) != 1 || db.commits[0] != 100 {
		t.Fatalf("expected Postgres commit 100, got %+v", db.commits)
	}
	if len(committer.committedOffsets) != 1 || committer.committedOffsets[0] != 100 {
		t.Fatalf("expected Kafka consumer group commit 100, got %+v", committer.committedOffsets)
	}
}

func TestCoordinator_SkippedUnknownMarketAdvancesCheckpoint(t *testing.T) {
	db := &fakeDB{}
	coord := checkpoint.NewCoordinator(db)
	topic := "orders.submitted"
	const partition = 0

	coord.InitBaseline(topic, partition, 99)
	ctx := context.Background()

	// Offset 100 = unknown market / poison message (auto-acknowledged by Consumer)
	coord.Track(orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 100})
	if err := coord.MarkDone(ctx, orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 100}); err != nil {
		t.Fatalf("MarkDone 100 (skipped): %v", err)
	}

	// Offset 101 = valid BTC order (processed by MarketEngine & Publisher)
	coord.Track(orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 101})
	if err := coord.MarkDone(ctx, orderbook.KafkaPosition{Topic: topic, Partition: partition, Offset: 101}); err != nil {
		t.Fatalf("MarkDone 101: %v", err)
	}

	// Invariant: Checkpoint successfully advanced to 101 without getting stuck on skipped offset 100!
	committed, _ := coord.GetCommittedOffset(topic, partition)
	if committed != 101 {
		t.Fatalf("expected checkpoint to advance to 101 across skipped message, got %d", committed)
	}
}
