package publisher_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet/internal/publisher"
	"tradedrift/services/wallet/internal/repository"
	"tradedrift/services/wallet/internal/repository/postgres"
	"tradedrift/services/wallet/internal/testhelper"
)

func TestMain(m *testing.M) {
	unlock, err := testhelper.AcquireTestLock()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: wallet publisher test lock setup failed: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	unlock()
	os.Exit(code)
}

func getPublisherTestPool(t *testing.T) *pgxpool.Pool {
	return testhelper.GetTestPool(t)
}

func TestPublisher_TransientKafkaErrorReleasesClaim(t *testing.T) {
	pool := getPublisherTestPool(t)
	if pool == nil {
		return
	}

	ctx := context.Background()
	outboxRepo := postgres.NewOutboxRepository(pool)

	eventID, err := platformuuid.New()
	if err != nil {
		t.Fatalf("failed to generate event id: %v", err)
	}
	aggregateID, err := platformuuid.New()
	if err != nil {
		t.Fatalf("failed to generate aggregate id: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM outbox WHERE id = $1", eventID); err != nil {
			t.Errorf("cleanup event %s failed: %v", eventID, err)
		}
	})

	// Insert test outbox event
	err = outboxRepo.Insert(ctx, &repository.OutboxEvent{
		ID:           eventID,
		AggregateID:  aggregateID,
		EventType:    "TradeSettled",
		Payload:      []byte(`{"trade_id":"` + aggregateID + `"}`),
		PartitionKey: "user-kafka-fail-test",
		CreatedAt:    time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("failed to insert test event: %v", err)
	}

	// Create publisher with unreachable broker
	pub := publisher.NewOutboxPublisher(
		outboxRepo,
		[]string{"127.0.0.1:59999"}, // unreachable port
		"trades.settled.v1",
		"portfolio.user.trades.v1",
		zap.NewNop(),
	)
	defer pub.Close()

	// Run publisher with timeout
	pubCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		pub.Run(pubCtx)
		close(done)
	}()
	<-done

	// Verify that the event was NOT marked FAILED!
	// It must be released back to PENDING (with claimed_at = NULL) for immediate retry on next cycle
	var status string
	var claimedAt *time.Time
	err = pool.QueryRow(ctx, "SELECT status, claimed_at FROM outbox WHERE id = $1", eventID).Scan(&status, &claimedAt)
	if err != nil {
		t.Fatalf("failed to query outbox event status: %v", err)
	}

	if status == "FAILED" {
		t.Fatalf("🚨 CRITICAL BUG: outbox event %s was marked FAILED on transient Kafka outage! Must be released to PENDING", eventID)
	}

	if status != "PENDING" || claimedAt != nil {
		t.Fatalf("expected status PENDING and nil claimed_at after transient failure release, got status=%s, claimed_at=%v", status, claimedAt)
	}
}

func TestPublisher_StopsAfterTransientFailure(t *testing.T) {
	pool := getPublisherTestPool(t)
	if pool == nil {
		return
	}

	ctx := context.Background()
	outboxRepo := postgres.NewOutboxRepository(pool)

	now := time.Now().UTC()
	eventID1, err := platformuuid.New()
	if err != nil {
		t.Fatalf("failed to generate event id 1: %v", err)
	}
	eventID2, err := platformuuid.New()
	if err != nil {
		t.Fatalf("failed to generate event id 2: %v", err)
	}
	aggID1, err := platformuuid.New()
	if err != nil {
		t.Fatalf("failed to generate aggregate id 1: %v", err)
	}
	aggID2, err := platformuuid.New()
	if err != nil {
		t.Fatalf("failed to generate aggregate id 2: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM outbox WHERE id IN ($1, $2)", eventID1, eventID2); err != nil {
			t.Errorf("cleanup events %s, %s failed: %v", eventID1, eventID2, err)
		}
	})

	// Insert Event 1 and Event 2 for the same user partition key
	err = outboxRepo.Insert(ctx, &repository.OutboxEvent{
		ID:           eventID1,
		AggregateID:  aggID1,
		EventType:    "TradeSettled",
		Payload:      []byte(`{"trade_id":"` + aggID1 + `"}`),
		PartitionKey: "user-ordering-test",
		CreatedAt:    now,
	})
	if err != nil {
		t.Fatalf("failed to insert event 1: %v", err)
	}

	err = outboxRepo.Insert(ctx, &repository.OutboxEvent{
		ID:           eventID2,
		AggregateID:  aggID2,
		EventType:    "TradeSettled",
		Payload:      []byte(`{"trade_id":"` + aggID2 + `"}`),
		PartitionKey: "user-ordering-test",
		CreatedAt:    now.Add(100 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("failed to insert event 2: %v", err)
	}

	// Create publisher with unreachable broker
	pub := publisher.NewOutboxPublisher(
		outboxRepo,
		[]string{"127.0.0.1:59999"},
		"trades.settled.v1",
		"portfolio.user.trades.v1",
		zap.NewNop(),
	)
	defer pub.Close()

	pubCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		pub.Run(pubCtx)
		close(done)
	}()
	<-done

	// Verify Event 2 was NEVER published to Kafka and was released to PENDING!
	var status1, status2 string
	err = pool.QueryRow(ctx, "SELECT status FROM outbox WHERE id = $1", eventID1).Scan(&status1)
	if err != nil {
		t.Fatalf("failed to query event 1 status: %v", err)
	}
	err = pool.QueryRow(ctx, "SELECT status FROM outbox WHERE id = $1", eventID2).Scan(&status2)
	if err != nil {
		t.Fatalf("failed to query event 2 status: %v", err)
	}

	if status2 == "PROCESSED" {
		t.Fatalf("🚨 CRITICAL BUG: Event 2 was marked PROCESSED when Event 1 failed! Out-of-order delivery detected!")
	}

	if status1 != "PENDING" || status2 != "PENDING" {
		t.Fatalf("expected both events to be returned to PENDING after failure; got event1=%s, event2=%s", status1, status2)
	}
}
