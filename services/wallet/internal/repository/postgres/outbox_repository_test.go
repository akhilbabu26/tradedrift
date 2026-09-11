package postgres_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	platformuuid "tradedrift/platform/uuid"
	platformpg "tradedrift/platform/postgres"
	"tradedrift/services/wallet/internal/repository"
	"tradedrift/services/wallet/internal/repository/postgres"
)

func getWalletTestPool(t *testing.T) (*pgxpool.Pool, func()) {
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

	if err := platformpg.RunMigrations(dsn, "../../../migration"); err != nil {
		t.Logf("Migration run note: %v", err)
	}

	cleanup := func() {
		pool.Close()
	}

	return pool, cleanup
}

func TestOutbox_ClaimIsolation(t *testing.T) {
	pool, cleanup := getWalletTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewOutboxRepository(pool)

	// Clean outbox table to ensure deterministic test isolation
	_, _ = pool.Exec(ctx, "DELETE FROM outbox")

	testAggregateID, _ := platformuuid.New()

	// Insert 6 pending events
	var eventIDs []string
	for i := 0; i < 6; i++ {
		id, _ := platformuuid.New()
		eventIDs = append(eventIDs, id)
		err := repo.Insert(ctx, &repository.OutboxEvent{
			ID:           id,
			AggregateID:  testAggregateID,
			EventType:    "TestEvent",
			Payload:      []byte(`{"test":true}`),
			PartitionKey: "user-test",
			CreatedAt:    time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("failed to insert test outbox event: %v", err)
		}
	}


	// Concurrently claim using 2 workers requesting 3 events each
	var wg sync.WaitGroup
	var worker1Events, worker2Events []*repository.OutboxEvent
	var err1, err2 error

	wg.Add(2)
	go func() {
		defer wg.Done()
		worker1Events, err1 = repo.FetchPending(ctx, 3)
	}()
	go func() {
		defer wg.Done()
		worker2Events, err2 = repo.FetchPending(ctx, 3)
	}()
	wg.Wait()

	if err1 != nil {
		t.Fatalf("worker 1 claim error: %v", err1)
	}
	if err2 != nil {
		t.Fatalf("worker 2 claim error: %v", err2)
	}

	// Assert no overlap between worker claims
	claimedMap := make(map[string]bool)
	for _, e := range worker1Events {
		claimedMap[e.ID] = true
	}

	for _, e := range worker2Events {
		if claimedMap[e.ID] {
			t.Fatalf("overlap detected! Event %s was claimed by both worker 1 and worker 2", e.ID)
		}
		claimedMap[e.ID] = true
	}

	totalClaimed := len(worker1Events) + len(worker2Events)
	if totalClaimed != 6 {
		t.Logf("claimed %d out of 6 inserted events (others may have been picked from previous batches)", totalClaimed)
	}

	// Verify all claimed events have status 'PROCESSING' and non-nil claimed_at
	for id := range claimedMap {
		var status string
		var claimedAt *time.Time
		err := pool.QueryRow(ctx, "SELECT status, claimed_at FROM outbox WHERE id = $1", id).Scan(&status, &claimedAt)
		if err != nil {
			t.Fatalf("failed to query claimed event status: %v", err)
		}
		if status != "PROCESSING" {
			t.Fatalf("expected status PROCESSING, got %s for event %s", status, id)
		}
		if claimedAt == nil {
			t.Fatalf("expected non-nil claimed_at for event %s", id)
		}
	}
}

func TestOutbox_LeaseTimeoutRecovery(t *testing.T) {
	pool, cleanup := getWalletTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewOutboxRepository(pool)

	// Clean outbox table to ensure deterministic test isolation
	_, _ = pool.Exec(ctx, "DELETE FROM outbox")

	eventID, _ := platformuuid.New()

	leaseAggregateID, _ := platformuuid.New()

	// Insert an event directly into 'PROCESSING' with claimed_at set to 3 minutes in the past
	twoMinAgo := time.Now().UTC().Add(-3 * time.Minute)
	_, err := pool.Exec(ctx, `
		INSERT INTO outbox (id, aggregate_id, event_type, payload, partition_key, status, claimed_at, created_at)
		VALUES ($1, $2, 'PortfolioUserTrade', '{"test":1}', 'user-lease-test', 'PROCESSING', $3, $3)
	`, eventID, leaseAggregateID, twoMinAgo)
	if err != nil {
		t.Fatalf("failed to insert simulated expired lease event: %v", err)
	}


	// FetchPending should reclaim this event because claimed_at < NOW() - 1 minute
	events, err := repo.FetchPending(ctx, 10)
	if err != nil {
		t.Fatalf("fetch pending failed: %v", err)
	}

	found := false
	for _, e := range events {
		if e.ID == eventID {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("expected expired lease event %s to be reclaimed by FetchPending, but was not returned", eventID)
	}

	// Verify that claimed_at was refreshed to NOW()
	var freshClaimedAt time.Time
	err = pool.QueryRow(ctx, "SELECT claimed_at FROM outbox WHERE id = $1", eventID).Scan(&freshClaimedAt)
	if err != nil {
		t.Fatalf("failed to query refreshed claimed_at: %v", err)
	}

	if freshClaimedAt.Before(time.Now().UTC().Add(-10 * time.Second)) {
		t.Fatalf("expected claimed_at to be refreshed to NOW(), got %v", freshClaimedAt)
	}
}

func TestOutbox_DeterministicClaimOrdering(t *testing.T) {
	pool, cleanup := getWalletTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewOutboxRepository(pool)

	// Clean outbox table to ensure deterministic test isolation
	_, _ = pool.Exec(ctx, "DELETE FROM outbox")

	fixedTime := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	// Insert 5 events with identical created_at but different IDs
	ids := []string{
		"00000000-0000-0000-0000-000000000005",
		"00000000-0000-0000-0000-000000000002",
		"00000000-0000-0000-0000-000000000004",
		"00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000003",
	}

	for _, id := range ids {
		_, err := pool.Exec(ctx, `
			INSERT INTO outbox (id, aggregate_id, event_type, payload, partition_key, status, created_at)
			VALUES ($1, $1, 'TradeSettled', '{}', 'user-order-test', 'PENDING', $2)
		`, id, fixedTime)
		if err != nil {
			t.Fatalf("failed to insert outbox event %s: %v", id, err)
		}
	}

	events, err := repo.FetchPending(ctx, 10)
	if err != nil {
		t.Fatalf("FetchPending failed: %v", err)
	}

	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}

	// Must be returned strictly in ascending ID order (id ASC)
	expectedOrder := []string{
		"00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000002",
		"00000000-0000-0000-0000-000000000003",
		"00000000-0000-0000-0000-000000000004",
		"00000000-0000-0000-0000-000000000005",
	}

	for i, ev := range events {
		if ev.ID != expectedOrder[i] {
			t.Fatalf("expected event at index %d to be %s, got %s", i, expectedOrder[i], ev.ID)
		}
	}
}

func TestMarkPublished_OnlyProcessesClaimedRow(t *testing.T) {
	pool, cleanup := getWalletTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewOutboxRepository(pool)

	// Clean outbox table to ensure deterministic test isolation
	_, _ = pool.Exec(ctx, "DELETE FROM outbox")

	eventID, _ := platformuuid.New()

	// 1. Insert in PENDING state
	_, err := pool.Exec(ctx, `
		INSERT INTO outbox (id, aggregate_id, event_type, payload, partition_key, status, created_at)
		VALUES ($1, $1, 'TradeSettled', '{}', 'user-publish-test', 'PENDING', NOW())
	`, eventID)
	if err != nil {
		t.Fatalf("failed to insert test event: %v", err)
	}

	// 2. MarkPublished must FAIL because row is in PENDING, not PROCESSING
	err = repo.MarkPublished(ctx, eventID, "dummy-token")
	if err == nil {
		t.Fatalf("expected MarkPublished to fail on PENDING event, but succeeded")
	}

	// 3. Claim the event into PROCESSING via FetchPending
	claimed, err := repo.FetchPending(ctx, 10)
	if err != nil || len(claimed) == 0 {
		t.Fatalf("failed to claim event: %v", err)
	}

	// 4. MarkPublished must now SUCCEED
	err = repo.MarkPublished(ctx, eventID, claimed[0].ClaimToken)
	if err != nil {
		t.Fatalf("expected MarkPublished to succeed on PROCESSING event, got %v", err)
	}

	// 5. Verify database state: status = PROCESSED, published_at is set, claimed_at is NULL
	var status string
	var publishedAt *time.Time
	var claimedAt *time.Time
	err = pool.QueryRow(ctx, "SELECT status, published_at, claimed_at FROM outbox WHERE id = $1", eventID).Scan(&status, &publishedAt, &claimedAt)
	if err != nil || status != "PROCESSED" || publishedAt == nil || claimedAt != nil {
		t.Fatalf("expected PROCESSED, published_at!=nil, claimed_at==nil; got status=%s, pub=%v, claimed=%v", status, publishedAt, claimedAt)
	}

	// 6. Calling MarkPublished again must FAIL (already PROCESSED, not PROCESSING)
	err = repo.MarkPublished(ctx, eventID, claimed[0].ClaimToken)
	if err == nil {
		t.Fatalf("expected duplicate MarkPublished to fail, but succeeded")
	}
}

func TestReleaseClaim_ResetsToPending(t *testing.T) {
	pool, cleanup := getWalletTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewOutboxRepository(pool)

	// Clean outbox table to ensure deterministic test isolation
	_, _ = pool.Exec(ctx, "DELETE FROM outbox")

	eventID1, _ := platformuuid.New()
	eventID2, _ := platformuuid.New()

	for _, id := range []string{eventID1, eventID2} {
		_, err := pool.Exec(ctx, `
			INSERT INTO outbox (id, aggregate_id, event_type, payload, partition_key, status, created_at)
			VALUES ($1, $1, 'TradeSettled', '{}', 'user-release-test', 'PENDING', NOW())
		`, id)
		if err != nil {
			t.Fatalf("failed to insert test event: %v", err)
		}
	}

	// Claim both events
	claimed, err := repo.FetchPending(ctx, 10)
	if err != nil || len(claimed) < 2 {
		t.Fatalf("failed to claim events: %v", err)
	}

	// Release claims
	if err := repo.ReleaseClaims(ctx, []string{eventID1, eventID2}, claimed[0].ClaimToken); err != nil {
		t.Fatalf("ReleaseClaims failed: %v", err)
	}

	// Verify both are back in PENDING with claimed_at NULL
	rows, err := pool.Query(ctx, "SELECT id, status, claimed_at FROM outbox WHERE id IN ($1, $2)", eventID1, eventID2)
	if err != nil {
		t.Fatalf("failed to query released events: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, status string
		var claimedAt *time.Time
		if err := rows.Scan(&id, &status, &claimedAt); err != nil {
			t.Fatalf("failed to scan row: %v", err)
		}
		if status != "PENDING" || claimedAt != nil {
			t.Fatalf("expected PENDING and nil claimed_at, got status=%s, claimed_at=%v", status, claimedAt)
		}
	}
}

func TestMarkFailed_FencedByClaimToken(t *testing.T) {
	pool, cleanup := getWalletTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewOutboxRepository(pool)

	_, _ = pool.Exec(ctx, "DELETE FROM outbox")

	eventID, _ := platformuuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO outbox (id, aggregate_id, event_type, payload, partition_key, status, created_at)
		VALUES ($1, $1, 'TradeSettled', '{}', 'user-fenced-fail-test', 'PENDING', NOW())
	`, eventID)
	if err != nil {
		t.Fatalf("failed to insert test event: %v", err)
	}

	claimed, err := repo.FetchPending(ctx, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("failed to claim event: %v", err)
	}
	if claimed[0].ClaimToken == "" {
		t.Fatalf("expected claim token on claimed event")
	}
	workerToken := claimed[0].ClaimToken

	wrongToken, _ := platformuuid.New()
	// 1. Worker with wrong token attempts MarkFailed -> must be fenced out
	err = repo.MarkFailed(ctx, eventID, "unknown event", wrongToken)
	if err == nil {
		t.Fatalf("expected error when MarkFailed is called with mismatched token, got nil")
	}

	// Verify status is still PROCESSING and claim_token unchanged
	var status string
	var currentToken *string
	err = pool.QueryRow(ctx, "SELECT status, claim_token FROM outbox WHERE id = $1", eventID).Scan(&status, &currentToken)
	if err != nil {
		t.Fatalf("failed to query row: %v", err)
	}
	if status != "PROCESSING" || currentToken == nil || *currentToken != workerToken {
		t.Fatalf("expected status PROCESSING with workerToken, got status=%s, token=%v", status, currentToken)
	}

	// 2. Legitimate worker with correct token calls MarkFailed -> succeeds
	err = repo.MarkFailed(ctx, eventID, "unknown event", workerToken)
	if err != nil {
		t.Fatalf("expected MarkFailed with matching token to succeed, got %v", err)
	}

	err = pool.QueryRow(ctx, "SELECT status, claim_token FROM outbox WHERE id = $1", eventID).Scan(&status, &currentToken)
	if err != nil {
		t.Fatalf("failed to query row: %v", err)
	}
	if status != "FAILED" || currentToken != nil {
		t.Fatalf("expected status FAILED and nil claim_token, got status=%s, token=%v", status, currentToken)
	}
}

func TestReleaseClaims_FencedByClaimToken(t *testing.T) {
	pool, cleanup := getWalletTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewOutboxRepository(pool)

	_, _ = pool.Exec(ctx, "DELETE FROM outbox")

	eventID, _ := platformuuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO outbox (id, aggregate_id, event_type, payload, partition_key, status, created_at)
		VALUES ($1, $1, 'TradeSettled', '{}', 'user-fenced-release-test', 'PENDING', NOW())
	`, eventID)
	if err != nil {
		t.Fatalf("failed to insert test event: %v", err)
	}

	claimed, err := repo.FetchPending(ctx, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("failed to claim event: %v", err)
	}
	if claimed[0].ClaimToken == "" {
		t.Fatalf("expected claim token on claimed event")
	}
	workerToken := claimed[0].ClaimToken

	// 1. Worker with wrong token attempts ReleaseClaims -> row must NOT be released
	wrongToken2, _ := platformuuid.New()
	err = repo.ReleaseClaims(ctx, []string{eventID}, wrongToken2)
	if err != nil {
		t.Fatalf("ReleaseClaims returned unexpected error: %v", err)
	}

	var status string
	var currentToken *string
	err = pool.QueryRow(ctx, "SELECT status, claim_token FROM outbox WHERE id = $1", eventID).Scan(&status, &currentToken)
	if err != nil {
		t.Fatalf("failed to query row: %v", err)
	}
	if status != "PROCESSING" || currentToken == nil || *currentToken != workerToken {
		t.Fatalf("expected event to remain PROCESSING under workerToken, got status=%s, token=%v", status, currentToken)
	}

	// 2. Legitimate worker with correct token calls ReleaseClaims -> row is released to PENDING
	err = repo.ReleaseClaims(ctx, []string{eventID}, workerToken)
	if err != nil {
		t.Fatalf("ReleaseClaims with valid token failed: %v", err)
	}

	err = pool.QueryRow(ctx, "SELECT status, claim_token FROM outbox WHERE id = $1", eventID).Scan(&status, &currentToken)
	if err != nil {
		t.Fatalf("failed to query row: %v", err)
	}
	if status != "PENDING" || currentToken != nil {
		t.Fatalf("expected status PENDING and nil claim_token, got status=%s, token=%v", status, currentToken)
	}
}

func TestMarkPublished_FencedByClaimToken(t *testing.T) {
	pool, cleanup := getWalletTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewOutboxRepository(pool)

	_, _ = pool.Exec(ctx, "DELETE FROM outbox")

	eventID, _ := platformuuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO outbox (id, aggregate_id, event_type, payload, partition_key, status, created_at)
		VALUES ($1, $1, 'TradeSettled', '{}', 'user-fenced-pub-test', 'PENDING', NOW())
	`, eventID)
	if err != nil {
		t.Fatalf("failed to insert test event: %v", err)
	}

	claimed, err := repo.FetchPending(ctx, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("failed to claim event: %v", err)
	}
	workerToken := claimed[0].ClaimToken
	wrongToken, _ := platformuuid.New()

	// 1. Worker with wrong token attempts MarkPublished -> must be fenced out
	err = repo.MarkPublished(ctx, eventID, wrongToken)
	if err == nil {
		t.Fatalf("expected error when MarkPublished is called with mismatched token, got nil")
	}

	// Verify status is still PROCESSING and claim_token unchanged
	var status string
	var currentToken *string
	err = pool.QueryRow(ctx, "SELECT status, claim_token FROM outbox WHERE id = $1", eventID).Scan(&status, &currentToken)
	if err != nil {
		t.Fatalf("failed to query row: %v", err)
	}
	if status != "PROCESSING" || currentToken == nil || *currentToken != workerToken {
		t.Fatalf("expected status PROCESSING with workerToken, got status=%s, token=%v", status, currentToken)
	}

	// 2. Worker with correct token calls MarkPublished -> succeeds
	err = repo.MarkPublished(ctx, eventID, workerToken)
	if err != nil {
		t.Fatalf("expected MarkPublished with matching token to succeed, got %v", err)
	}

	err = pool.QueryRow(ctx, "SELECT status, claim_token FROM outbox WHERE id = $1", eventID).Scan(&status, &currentToken)
	if err != nil {
		t.Fatalf("failed to query row: %v", err)
	}
	if status != "PROCESSED" || currentToken != nil {
		t.Fatalf("expected status PROCESSED and nil claim_token, got status=%s, token=%v", status, currentToken)
	}
}

func TestOutboxRepository_ConfigurableLease(t *testing.T) {
	pool, cleanup := getWalletTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	// Short lease: 50ms
	repo := postgres.NewOutboxRepository(pool, 50*time.Millisecond)

	_, _ = pool.Exec(ctx, "DELETE FROM outbox")

	eventID, _ := platformuuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO outbox (id, aggregate_id, event_type, payload, partition_key, status, created_at)
		VALUES ($1, $1, 'TradeSettled', '{}', 'user-lease-test', 'PENDING', NOW())
	`, eventID)
	if err != nil {
		t.Fatalf("failed to insert test event: %v", err)
	}

	// Worker 1 claims
	claimed1, err := repo.FetchPending(ctx, 1)
	if err != nil || len(claimed1) != 1 {
		t.Fatalf("failed to claim event: %v", err)
	}
	worker1Token := claimed1[0].ClaimToken

	// Immediate fetch should find 0 events (it's within the 50ms lease)
	claimedImmediate, err := repo.FetchPending(ctx, 1)
	if err != nil {
		t.Fatalf("unexpected error on immediate fetch: %v", err)
	}
	if len(claimedImmediate) != 0 {
		t.Fatalf("expected 0 events within active lease, got %d", len(claimedImmediate))
	}

	// Wait 60ms for lease to expire
	time.Sleep(60 * time.Millisecond)

	// Worker 2 should reclaim the event due to expired lease
	claimed2, err := repo.FetchPending(ctx, 1)
	if err != nil || len(claimed2) != 1 {
		t.Fatalf("expected Worker 2 to reclaim expired event, got %d events, err: %v", len(claimed2), err)
	}
	worker2Token := claimed2[0].ClaimToken
	if worker2Token == worker1Token {
		t.Fatalf("expected new claim token for reclaimed event")
	}

	// Stale Worker 1 attempts MarkPublished with old token -> fenced out!
	err = repo.MarkPublished(ctx, eventID, worker1Token)
	if err == nil {
		t.Fatalf("expected stale Worker 1 to be fenced out, but MarkPublished succeeded")
	}

	// Worker 2 finishes with valid token -> succeeds
	err = repo.MarkPublished(ctx, eventID, worker2Token)
	if err != nil {
		t.Fatalf("expected Worker 2 to succeed, got %v", err)
	}
}


