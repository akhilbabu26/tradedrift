package publisher_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet/internal/publisher"
	"tradedrift/services/wallet/internal/repository"
	"tradedrift/services/wallet/internal/repository/postgres"
)

func getPublisherTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	dsn := os.Getenv("WALLET_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:123@localhost:5432/tradedrift_wallet?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("Skipping postgres integration tests: cannot connect to %s: %v", dsn, err)
		return nil, nil
	}

	if err := pool.Ping(ctx); err != nil {
		t.Skipf("Skipping postgres integration tests: ping failed to %s: %v", dsn, err)
		return nil, nil
	}

	cleanup := func() {
		pool.Close()
	}

	return pool, cleanup
}

func TestPublisher_TransientKafkaErrorReleasesClaim(t *testing.T) {
	pool, cleanup := getPublisherTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	outboxRepo := postgres.NewOutboxRepository(pool)

	// Clean outbox table to prevent cross-package test interference
	_, _ = pool.Exec(ctx, "DELETE FROM outbox")

	eventID, _ := platformuuid.New()
	aggregateID, _ := platformuuid.New()

	// Insert test outbox event
	err := outboxRepo.Insert(ctx, &repository.OutboxEvent{
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
	pool, cleanup := getPublisherTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	outboxRepo := postgres.NewOutboxRepository(pool)

	// Clean outbox table to prevent cross-package test interference
	_, _ = pool.Exec(ctx, "DELETE FROM outbox")

	now := time.Now().UTC()
	eventID1, _ := platformuuid.New()
	eventID2, _ := platformuuid.New()
	aggID1, _ := platformuuid.New()
	aggID2, _ := platformuuid.New()

	// Insert Event 1 and Event 2 for the same user partition key
	err := outboxRepo.Insert(ctx, &repository.OutboxEvent{
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
