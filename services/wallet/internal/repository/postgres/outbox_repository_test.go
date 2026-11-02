package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/wallet/internal/repository"
	"tradedrift/services/wallet/internal/repository/postgres"
	"tradedrift/services/wallet/internal/testhelper"
)

func getWalletTestPool(t *testing.T) *pgxpool.Pool {
	return testhelper.GetTestPool(t)
}

func TestOutbox_ClaimIsolation(t *testing.T) {
	pool := getWalletTestPool(t)
	if pool == nil {
		return
	}

	ctx := context.Background()
	repo := postgres.NewOutboxRepository(pool)

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

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM outbox WHERE id = ANY($1)", eventIDs); err != nil {
			t.Errorf("cleanup eventIDs failed: %v", err)
		}
	})

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
	pool := getWalletTestPool(t)
	if pool == nil {
		return
	}

	ctx := context.Background()
	repo := postgres.NewOutboxRepository(pool)

	eventID, _ := platformuuid.New()
	leaseAggregateID, _ := platformuuid.New()

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM outbox WHERE id = $1", eventID); err != nil {
			t.Errorf("cleanup event %s failed: %v", eventID, err)
		}
	})

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
	pool := getWalletTestPool(t)
	if pool == nil {
		return
	}

	ctx := context.Background()
	repo := postgres.NewOutboxRepository(pool)

	// Generate unique, deterministically sortable UUIDs for this test run.
	// Using a unique base prefix guarantees no primary key collisions across runs,
	// while the fixed suffix guarantees deterministic ascending ordering.
	baseUUID, err := platformuuid.New()
	if err != nil {
		t.Fatalf("failed to generate base uuid: %v", err)
	}
	prefix := baseUUID[:32]
	id1 := prefix + "0001"
	id2 := prefix + "0002"
	id3 := prefix + "0003"
	id4 := prefix + "0004"
	id5 := prefix + "0005"

	ids := []string{id5, id2, id4, id1, id3}
	expectedOrder := []string{id1, id2, id3, id4, id5}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM outbox WHERE id = ANY($1)", ids); err != nil {
			t.Errorf("cleanup ids failed: %v", err)
		}
	})

	fixedTime := time.Now().UTC()
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

	// Filter claimed events to those inserted by this test
	var matchedEvents []*repository.OutboxEvent
	for _, ev := range events {
		for _, id := range ids {
			if ev.ID == id {
				matchedEvents = append(matchedEvents, ev)
				break
			}
		}
	}

	if len(matchedEvents) != 5 {
		t.Fatalf("expected 5 test events to be claimed, got %d (total events: %d)", len(matchedEvents), len(events))
	}

	// Must be returned strictly in ascending ID order (id ASC)
	for i, ev := range matchedEvents {
		if ev.ID != expectedOrder[i] {
			t.Fatalf("expected event at index %d to be %s, got %s", i, expectedOrder[i], ev.ID)
		}
	}
}

func TestMarkPublished_OnlyProcessesClaimedRow(t *testing.T) {
	pool := getWalletTestPool(t)
	if pool == nil {
		return
	}

	ctx := context.Background()
	repo := postgres.NewOutboxRepository(pool)

	eventID, _ := platformuuid.New()

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM outbox WHERE id = $1", eventID); err != nil {
			t.Errorf("cleanup event %s failed: %v", eventID, err)
		}
	})

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
	if err != nil {
		t.Fatalf("failed to claim event: %v", err)
	}

	var testEvent *repository.OutboxEvent
	for _, c := range claimed {
		if c.ID == eventID {
			testEvent = c
			break
		}
	}
	if testEvent == nil {
		t.Fatalf("event %s was not claimed in batch: %+v", eventID, claimed)
	}

	// 4. MarkPublished must now SUCCEED
	err = repo.MarkPublished(ctx, eventID, testEvent.ClaimToken)
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
	err = repo.MarkPublished(ctx, eventID, testEvent.ClaimToken)
	if err == nil {
		t.Fatalf("expected duplicate MarkPublished to fail, but succeeded")
	}
}

func TestReleaseClaim_ResetsToPending(t *testing.T) {
	pool := getWalletTestPool(t)
	if pool == nil {
		return
	}

	ctx := context.Background()
	repo := postgres.NewOutboxRepository(pool)

	eventID1, _ := platformuuid.New()
	eventID2, _ := platformuuid.New()

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM outbox WHERE id IN ($1, $2)", eventID1, eventID2); err != nil {
			t.Errorf("cleanup events failed: %v", err)
		}
	})

	for _, id := range []string{eventID1, eventID2} {
		_, err := pool.Exec(ctx, `
			INSERT INTO outbox (id, aggregate_id, event_type, payload, partition_key, status, created_at)
			VALUES ($1, $1, 'TradeSettled', '{}', 'user-release-test', 'PENDING', NOW())
		`, id)
		if err != nil {
			t.Fatalf("failed to insert test event: %v", err)
		}
	}

	// Claim events
	claimed, err := repo.FetchPending(ctx, 10)
	if err != nil {
		t.Fatalf("failed to claim events: %v", err)
	}

	var token string
	for _, c := range claimed {
		if c.ID == eventID1 || c.ID == eventID2 {
			token = c.ClaimToken
			break
		}
	}
	if token == "" {
		t.Fatalf("test events were not claimed in batch: %+v", claimed)
	}

	// Release claims
	if err := repo.ReleaseClaims(ctx, []string{eventID1, eventID2}, token); err != nil {
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
	pool := getWalletTestPool(t)
	if pool == nil {
		return
	}

	ctx := context.Background()
	repo := postgres.NewOutboxRepository(pool)

	eventID, _ := platformuuid.New()

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM outbox WHERE id = $1", eventID); err != nil {
			t.Errorf("cleanup event failed: %v", err)
		}
	})

	_, err := pool.Exec(ctx, `
		INSERT INTO outbox (id, aggregate_id, event_type, payload, partition_key, status, created_at)
		VALUES ($1, $1, 'TradeSettled', '{}', 'user-fenced-fail-test', 'PENDING', NOW())
	`, eventID)
	if err != nil {
		t.Fatalf("failed to insert test event: %v", err)
	}

	claimed, err := repo.FetchPending(ctx, 10)
	if err != nil {
		t.Fatalf("failed to claim event: %v", err)
	}

	var testEvent *repository.OutboxEvent
	for _, c := range claimed {
		if c.ID == eventID {
			testEvent = c
			break
		}
	}
	if testEvent == nil || testEvent.ClaimToken == "" {
		t.Fatalf("expected claim token on claimed event: %+v", testEvent)
	}
	workerToken := testEvent.ClaimToken

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
	pool := getWalletTestPool(t)
	if pool == nil {
		return
	}

	ctx := context.Background()
	repo := postgres.NewOutboxRepository(pool)

	eventID, _ := platformuuid.New()

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM outbox WHERE id = $1", eventID); err != nil {
			t.Errorf("cleanup event failed: %v", err)
		}
	})

	_, err := pool.Exec(ctx, `
		INSERT INTO outbox (id, aggregate_id, event_type, payload, partition_key, status, created_at)
		VALUES ($1, $1, 'TradeSettled', '{}', 'user-fenced-release-test', 'PENDING', NOW())
	`, eventID)
	if err != nil {
		t.Fatalf("failed to insert test event: %v", err)
	}

	claimed, err := repo.FetchPending(ctx, 10)
	if err != nil {
		t.Fatalf("failed to claim event: %v", err)
	}

	var testEvent *repository.OutboxEvent
	for _, c := range claimed {
		if c.ID == eventID {
			testEvent = c
			break
		}
	}
	if testEvent == nil || testEvent.ClaimToken == "" {
		t.Fatalf("expected claim token on claimed event: %+v", testEvent)
	}
	workerToken := testEvent.ClaimToken

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
	pool := getWalletTestPool(t)
	if pool == nil {
		return
	}

	ctx := context.Background()
	repo := postgres.NewOutboxRepository(pool)

	eventID, _ := platformuuid.New()

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM outbox WHERE id = $1", eventID); err != nil {
			t.Errorf("cleanup event failed: %v", err)
		}
	})

	_, err := pool.Exec(ctx, `
		INSERT INTO outbox (id, aggregate_id, event_type, payload, partition_key, status, created_at)
		VALUES ($1, $1, 'TradeSettled', '{}', 'user-fenced-pub-test', 'PENDING', NOW())
	`, eventID)
	if err != nil {
		t.Fatalf("failed to insert test event: %v", err)
	}

	claimed, err := repo.FetchPending(ctx, 10)
	if err != nil {
		t.Fatalf("failed to claim event: %v", err)
	}

	var testEvent *repository.OutboxEvent
	for _, c := range claimed {
		if c.ID == eventID {
			testEvent = c
			break
		}
	}
	if testEvent == nil || testEvent.ClaimToken == "" {
		t.Fatalf("expected claim token on claimed event: %+v", testEvent)
	}
	workerToken := testEvent.ClaimToken
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
	pool := getWalletTestPool(t)
	if pool == nil {
		return
	}

	ctx := context.Background()
	// Short lease: 50ms
	repo := postgres.NewOutboxRepository(pool, 50*time.Millisecond)

	eventID, _ := platformuuid.New()

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM outbox WHERE id = $1", eventID); err != nil {
			t.Errorf("cleanup event failed: %v", err)
		}
	})

	_, err := pool.Exec(ctx, `
		INSERT INTO outbox (id, aggregate_id, event_type, payload, partition_key, status, created_at)
		VALUES ($1, $1, 'TradeSettled', '{}', 'user-lease-test', 'PENDING', NOW())
	`, eventID)
	if err != nil {
		t.Fatalf("failed to insert test event: %v", err)
	}

	// Worker 1 claims
	claimed1, err := repo.FetchPending(ctx, 10)
	if err != nil {
		t.Fatalf("failed to claim event: %v", err)
	}

	var testEvent1 *repository.OutboxEvent
	for _, c := range claimed1 {
		if c.ID == eventID {
			testEvent1 = c
			break
		}
	}
	if testEvent1 == nil {
		t.Fatalf("expected to claim test event %s, got: %+v", eventID, claimed1)
	}
	worker1Token := testEvent1.ClaimToken

	// Immediate fetch should not find eventID (it's within active lease)
	claimedImmediate, err := repo.FetchPending(ctx, 10)
	if err != nil {
		t.Fatalf("unexpected error on immediate fetch: %v", err)
	}
	for _, c := range claimedImmediate {
		if c.ID == eventID {
			t.Fatalf("expected event %s to not be claimed within active lease", eventID)
		}
	}

	// Wait 60ms for lease to expire
	time.Sleep(60 * time.Millisecond)

	// Worker 2 should reclaim the event due to expired lease
	claimed2, err := repo.FetchPending(ctx, 10)
	if err != nil {
		t.Fatalf("reclaim error: %v", err)
	}

	var testEvent2 *repository.OutboxEvent
	for _, c := range claimed2 {
		if c.ID == eventID {
			testEvent2 = c
			break
		}
	}
	if testEvent2 == nil {
		t.Fatalf("expected Worker 2 to reclaim expired event %s, got %+v", eventID, claimed2)
	}
	worker2Token := testEvent2.ClaimToken
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
