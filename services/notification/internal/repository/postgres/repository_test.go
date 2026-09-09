package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/notification/internal/model"
	"tradedrift/services/notification/internal/repository"
	"tradedrift/services/notification/internal/repository/postgres"
)

func getTestPool(t *testing.T) (*pgxpool.Pool, func()) {
	dsn := os.Getenv("NOTIFICATION_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:123@localhost:5432/tradedrift_notification?sslmode=disable"
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

	// Apply schema for test run
	setupDDL := `
		CREATE TABLE IF NOT EXISTS notifications (
			id             UUID PRIMARY KEY,
			user_id        UUID NOT NULL,
			title          VARCHAR(255) NOT NULL,
			message        TEXT NOT NULL,
			type           VARCHAR(30) NOT NULL,
			reference_id   UUID,
			reference_type VARCHAR(30),
			is_read        BOOLEAN NOT NULL DEFAULT FALSE,
			read_at        TIMESTAMPTZ,
			created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_notifications_user_inbox ON notifications(user_id, created_at DESC, id DESC);

		CREATE TABLE IF NOT EXISTS processed_events (
			event_id      UUID PRIMARY KEY,
			user_id       UUID,
			notification_id UUID,
			processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS notification_outbox (
			id             UUID PRIMARY KEY,
			event_type     VARCHAR(50) NOT NULL,
			payload        JSONB NOT NULL,
			target_channel VARCHAR(100) NOT NULL,
			status         VARCHAR(20) NOT NULL DEFAULT 'PENDING'
			               CHECK (status IN ('PENDING', 'PROCESSING', 'PROCESSED', 'FAILED')),
			claim_token    UUID,
			retry_count    INT NOT NULL DEFAULT 0,
			last_error     TEXT,
			claimed_at     TIMESTAMPTZ,
			created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			published_at   TIMESTAMPTZ
		);

		ALTER TABLE notifications DROP CONSTRAINT IF EXISTS chk_notification_type;
		ALTER TABLE notifications ADD CONSTRAINT chk_notification_type CHECK (type IN ('INFO', 'TRADE_FILL', 'SYSTEM', 'ACCOUNT'));
	`
	if _, err := pool.Exec(ctx, setupDDL); err != nil {
		t.Fatalf("failed to setup test tables: %v", err)
	}

	cleanup := func() {
		pool.Close()
	}

	return pool, cleanup
}

func TestRepository_CreateWithDedupTx(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewRepository(pool)

	sourceEventID, err := platformuuid.New()
	require.NoError(t, err)
	userID, err := platformuuid.New()
	require.NoError(t, err)
	notifID, err := platformuuid.New()
	require.NoError(t, err)
	outboxID, err := platformuuid.New()
	require.NoError(t, err)

	notif := &model.Notification{
		ID:        notifID,
		UserID:    userID,
		Title:     "Order Cancelled",
		Message:   "Your limit order was cancelled",
		Type:      model.TypeSystem,
		CreatedAt: time.Now().UTC(),
	}

	outbox := &model.OutboxEvent{
		ID:            outboxID,
		EventType:     "NotificationCreated",
		Payload:       []byte(`{"test":true}`),
		TargetChannel: "user:notifications:" + userID,
		CreatedAt:     time.Now().UTC(),
	}

	// 1. Initial insert succeeds
	err = repo.CreateWithDedupTx(ctx, notif, sourceEventID, outbox)
	require.NoError(t, err, "first CreateWithDedupTx failed")

	// 2. Duplicate with same sourceEventID must fail with ErrAlreadyProcessed
	notif2ID, err := platformuuid.New()
	require.NoError(t, err)
	notif2 := &model.Notification{
		ID:        notif2ID,
		UserID:    userID,
		Title:     "Duplicate Alert",
		Message:   "Duplicate message",
		Type:      model.TypeSystem,
		CreatedAt: time.Now().UTC(),
	}
	outbox2ID, err := platformuuid.New()
	require.NoError(t, err)
	outbox2 := &model.OutboxEvent{
		ID:            outbox2ID,
		EventType:     "NotificationCreated",
		Payload:       []byte(`{"test":true}`),
		TargetChannel: "user:notifications:" + userID,
		CreatedAt:     time.Now().UTC(),
	}
	err = repo.CreateWithDedupTx(ctx, notif2, sourceEventID, outbox2)
	require.ErrorIs(t, err, repository.ErrAlreadyProcessed, "expected ErrAlreadyProcessed on duplicate sourceEventID")
}

func TestRepository_CreateTradeSettledTx(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewRepository(pool)

	sourceTradeID, _ := platformuuid.New()
	buyerID, _ := platformuuid.New()
	sellerID, _ := platformuuid.New()

	buyerNotifID, _ := platformuuid.New()
	sellerNotifID, _ := platformuuid.New()
	buyerOutboxID, _ := platformuuid.New()
	sellerOutboxID, _ := platformuuid.New()

	now := time.Now().UTC()

	buyerNotif := &model.Notification{
		ID:            buyerNotifID,
		UserID:        buyerID,
		Title:         "Trade Executed",
		Message:       "You bought 0.01 BTC",
		Type:          model.TypeTradeFill,
		ReferenceID:   sourceTradeID,
		ReferenceType: model.RefTypeTrade,
		CreatedAt:     now,
	}
	sellerNotif := &model.Notification{
		ID:            sellerNotifID,
		UserID:        sellerID,
		Title:         "Trade Executed",
		Message:       "You sold 0.01 BTC",
		Type:          model.TypeTradeFill,
		ReferenceID:   sourceTradeID,
		ReferenceType: model.RefTypeTrade,
		CreatedAt:     now,
	}

	buyerOutbox := &model.OutboxEvent{
		ID:            buyerOutboxID,
		EventType:     "TradeFill",
		Payload:       []byte(`{"side":"BUY"}`),
		TargetChannel: "user:notifications:" + buyerID,
		CreatedAt:     now,
	}
	sellerOutbox := &model.OutboxEvent{
		ID:            sellerOutboxID,
		EventType:     "TradeFill",
		Payload:       []byte(`{"side":"SELL"}`),
		TargetChannel: "user:notifications:" + sellerID,
		CreatedAt:     now,
	}

	err := repo.CreateTradeSettledTx(ctx, buyerNotif, sellerNotif, sourceTradeID, buyerOutbox, sellerOutbox)
	if err != nil {
		t.Fatalf("CreateTradeSettledTx failed: %v", err)
	}

	// Verify both records exist
	unreadBuyer, _ := repo.GetUnreadCount(ctx, buyerID)
	unreadSeller, _ := repo.GetUnreadCount(ctx, sellerID)
	if unreadBuyer != 1 || unreadSeller != 1 {
		t.Fatalf("expected 1 unread notification each, got buyer=%d, seller=%d", unreadBuyer, unreadSeller)
	}

	// Replay must fail
	err = repo.CreateTradeSettledTx(ctx, buyerNotif, sellerNotif, sourceTradeID, buyerOutbox, sellerOutbox)
	if !errors.Is(err, repository.ErrAlreadyProcessed) {
		t.Fatalf("expected ErrAlreadyProcessed on replayed TradeSettled event, got: %v", err)
	}
}

func TestRepository_OwnershipAndReadState(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewRepository(pool)

	userA, _ := platformuuid.New()
	userB, _ := platformuuid.New()
	notifID, _ := platformuuid.New()

	notif := &model.Notification{
		ID:        notifID,
		UserID:    userA,
		Title:     "Security Alert",
		Message:   "New login",
		Type:      model.TypeAccount,
		CreatedAt: time.Now().UTC(),
	}
	eventID, _ := platformuuid.New()
	_ = repo.CreateWithDedupTx(ctx, notif, eventID, nil)

	// User B tries to mark User A's notification as read
	_, err := repo.MarkAsRead(ctx, userB, notifID)
	if !errors.Is(err, repository.ErrNotificationNotFound) {
		t.Fatalf("expected ErrNotificationNotFound when unauthorized user marks as read, got: %v", err)
	}

	// User A marks their own as read
	updated, err := repo.MarkAsRead(ctx, userA, notifID)
	if err != nil {
		t.Fatalf("User A MarkAsRead failed: %v", err)
	}
	if !updated.IsRead || updated.ReadAt == nil {
		t.Fatalf("expected IsRead=true and valid ReadAt, got: %v", updated)
	}
}

func TestRepository_OutboxClaimAndLeaseRecovery(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewRepository(pool)

	outboxID, _ := platformuuid.New()
	ev := &model.OutboxEvent{
		ID:            outboxID,
		EventType:     "TestEvent",
		Payload:       []byte(`{}`),
		TargetChannel: "test:channel",
		CreatedAt:     time.Now().UTC(),
	}
	err := repo.StageOutboxEvent(ctx, ev)
	if err != nil {
		t.Fatalf("StageOutboxEvent failed: %v", err)
	}

	// Claim outbox
	claimed, err := repo.FetchPendingOutbox(ctx, 10)
	if err != nil {
		t.Fatalf("FetchPendingOutbox failed: %v", err)
	}
	if len(claimed) == 0 {
		t.Fatalf("expected at least 1 claimed outbox event")
	}

	targetEv := claimed[0]
	if targetEv.ClaimToken == "" {
		t.Fatalf("expected non-empty ClaimToken on claimed event")
	}

	// 1. IncrementOutboxRetry with wrong claim token must fail with ErrOutboxClaimLost
	wrongToken, _ := platformuuid.New()
	err = repo.IncrementOutboxRetry(ctx, targetEv.ID, "transient error", wrongToken)
	if !errors.Is(err, repository.ErrOutboxClaimLost) {
		t.Fatalf("expected ErrOutboxClaimLost with wrong claim token, got: %v", err)
	}

	// 2. IncrementOutboxRetry with correct claim token succeeds
	err = repo.IncrementOutboxRetry(ctx, targetEv.ID, "transient error", targetEv.ClaimToken)
	if err != nil {
		t.Fatalf("expected IncrementOutboxRetry with valid token to succeed, got: %v", err)
	}

	// 3. MarkOutboxPublished with wrong claim token must fail with ErrOutboxClaimLost
	err = repo.MarkOutboxPublished(ctx, targetEv.ID, wrongToken)
	if !errors.Is(err, repository.ErrOutboxClaimLost) {
		t.Fatalf("expected ErrOutboxClaimLost with wrong claim token, got: %v", err)
	}

	// 4. Recover stale claims with 0 duration (treats all PROCESSING as stale)
	recovered, err := repo.RecoverStaleOutboxClaims(ctx, 0)
	if err != nil {
		t.Fatalf("RecoverStaleOutboxClaims failed: %v", err)
	}
	if recovered == 0 {
		t.Fatalf("expected recovered rows > 0")
	}
}

func TestRepository_KeysetPagination(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewRepository(pool)

	userID, _ := platformuuid.New()
	baseTime := time.Now().UTC().Truncate(time.Millisecond)

	// Insert 5 notifications with the exact same timestamp to test deterministic id tiebreaker
	var notifIDs []string
	for i := 0; i < 5; i++ {
		nid, _ := platformuuid.New()
		notifIDs = append(notifIDs, nid)
		notif := &model.Notification{
			ID:        nid,
			UserID:    userID,
			Title:     "Pagination Item",
			Message:   "Item body",
			Type:      model.TypeInfo,
			CreatedAt: baseTime,
		}
		eid, _ := platformuuid.New()
		if err := repo.CreateWithDedupTx(ctx, notif, eid, nil); err != nil {
			t.Fatalf("failed to insert notification for pagination test: %v", err)
		}
	}

	// Fetch first page of 3 items.
	// The repo returns limit+1 (4 rows) so the handler can detect has_more exactly.
	page1Raw, err := repo.GetByUserID(ctx, model.PaginationFilter{
		UserID: userID,
		Limit:  3,
	})
	if err != nil {
		t.Fatalf("page 1 fetch failed: %v", err)
	}
	// We have 5 notifications and limit=3, so 4 rows returned and has_more=true.
	if len(page1Raw) != 4 {
		t.Fatalf("expected 4 raw rows on page 1 (limit+1), got %d", len(page1Raw))
	}
	// Simulate handler slicing: trim to limit when len > limit.
	hasMore1 := len(page1Raw) > 3
	page1 := page1Raw[:3]
	if !hasMore1 {
		t.Fatal("expected has_more=true for page 1")
	}

	// Fetch second page of remaining 2 items using page1's last item as cursor.
	// We have 2 items left and limit=3, so repo returns 2+1=3 if sentinel exists, else 2.
	// In this case only 2 remain → len = 2 → has_more=false.
	lastItem := page1[len(page1)-1]
	page2Raw, err := repo.GetByUserID(ctx, model.PaginationFilter{
		UserID:     userID,
		CursorTime: &lastItem.CreatedAt,
		CursorID:   lastItem.ID,
		Limit:      3,
	})
	if err != nil {
		t.Fatalf("page 2 fetch failed: %v", err)
	}
	hasMore2 := len(page2Raw) > 3
	page2 := page2Raw
	if hasMore2 {
		page2 = page2Raw[:3]
	}
	if len(page2) != 2 {
		t.Fatalf("expected 2 items on page 2, got %d", len(page2))
	}
	if hasMore2 {
		t.Fatal("expected has_more=false for page 2")
	}

	// Verify zero overlap between page 1 and page 2
	seen := make(map[string]bool)
	for _, n := range page1 {
		seen[n.ID] = true
	}
	for _, n := range page2 {
		if seen[n.ID] {
			t.Fatalf("duplicate item %s found across paginated pages", n.ID)
		}
	}
}

// TestRepository_OutboxLeaseExpiryAndStolenClaimConcurrency verifies the core distributed systems
// invariant of the claim-token design:
// Worker A claims event
//        ↓
// Worker A's lease expires (recovered to PENDING)
//        ↓
// Worker B claims same event (receives fresh claim_token)
//        ↓
// Worker A tries:
//     MarkOutboxPublished with Token A  → must return ErrOutboxClaimLost
//     IncrementOutboxRetry with Token A → must return ErrOutboxClaimLost
//     ReleaseOutboxClaims with Token A  → must NOT modify Worker B's claim
// Worker B publishes and marks PROCESSED → succeeds
func TestRepository_OutboxLeaseExpiryAndStolenClaimConcurrency(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewRepository(pool)

	// Clean outbox to ensure deterministic isolation
	_, _ = pool.Exec(ctx, "DELETE FROM notification_outbox")

	// Stage an outbox event
	outboxID, _ := platformuuid.New()
	ev := &model.OutboxEvent{
		ID:            outboxID,
		EventType:     "ConcurrencyTest",
		Payload:       []byte(`{"test":"lease"}`),
		TargetChannel: "user:concurrency:" + outboxID,
		CreatedAt:     time.Now().UTC(),
	}
	if err := repo.StageOutboxEvent(ctx, ev); err != nil {
		t.Fatalf("StageOutboxEvent failed: %v", err)
	}

	// 1. Worker A claims the event
	claimedA, err := repo.FetchPendingOutbox(ctx, 1)
	if err != nil || len(claimedA) == 0 {
		t.Fatalf("Worker A failed to claim event: %v", err)
	}
	workerAEvent := claimedA[0]
	tokenA := workerAEvent.ClaimToken
	if tokenA == "" {
		t.Fatalf("expected Worker A to have a non-empty ClaimToken")
	}

	// 2. Simulate Worker A's lease expiring and recovery resetting it to PENDING
	recovered, err := repo.RecoverStaleOutboxClaims(ctx, 0)
	if err != nil || recovered == 0 {
		t.Fatalf("failed to simulate lease expiry recovery: %v", err)
	}

	// 3. Worker B claims the same event and receives a fresh claim_token
	claimedB, err := repo.FetchPendingOutbox(ctx, 1)
	if err != nil || len(claimedB) == 0 {
		t.Fatalf("Worker B failed to claim event: %v", err)
	}
	workerBEvent := claimedB[0]
	tokenB := workerBEvent.ClaimToken
	if tokenB == "" {
		t.Fatalf("expected Worker B to have a non-empty ClaimToken")
	}
	if tokenA == tokenB {
		t.Fatalf("Worker B must receive a distinct ClaimToken from Worker A (got identical %s)", tokenA)
	}

	// 4. Worker A wakes up late and tries MarkOutboxPublished using Token A
	err = repo.MarkOutboxPublished(ctx, outboxID, tokenA)
	if !errors.Is(err, repository.ErrOutboxClaimLost) {
		t.Fatalf("Worker A MarkOutboxPublished: expected ErrOutboxClaimLost, got %v", err)
	}

	// 5. Worker A tries IncrementOutboxRetry using Token A
	err = repo.IncrementOutboxRetry(ctx, outboxID, "transient error from late worker A", tokenA)
	if !errors.Is(err, repository.ErrOutboxClaimLost) {
		t.Fatalf("Worker A IncrementOutboxRetry: expected ErrOutboxClaimLost, got %v", err)
	}

	// 6. Worker A tries ReleaseOutboxClaims using Token A
	// Must execute without error, but MUST NOT modify Worker B's active claim in PostgreSQL!
	err = repo.ReleaseOutboxClaims(ctx, []string{outboxID}, tokenA)
	if err != nil {
		t.Fatalf("Worker A ReleaseOutboxClaims returned error: %v", err)
	}

	// Verify Worker B's claim is completely intact in the database
	var currentStatus, currentClaimToken string
	query := `SELECT status, claim_token::text FROM notification_outbox WHERE id = $1`
	if err := pool.QueryRow(ctx, query, outboxID).Scan(&currentStatus, &currentClaimToken); err != nil {
		t.Fatalf("failed to query outbox state: %v", err)
	}
	if currentStatus != "PROCESSING" {
		t.Fatalf("Worker B's claim was improperly modified by Worker A! expected status 'PROCESSING', got %s", currentStatus)
	}
	if currentClaimToken != tokenB {
		t.Fatalf("Worker B's claim token was corrupted by Worker A! expected %s, got %s", tokenB, currentClaimToken)
	}

	// 7. Worker B completes publishing and marks the row PROCESSED using Token B
	if err := repo.MarkOutboxPublished(ctx, outboxID, tokenB); err != nil {
		t.Fatalf("Worker B MarkOutboxPublished with valid tokenB failed: %v", err)
	}

	// Final verification: status is PROCESSED, claim cleared
	var finalStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM notification_outbox WHERE id = $1`, outboxID).Scan(&finalStatus); err != nil {
		t.Fatalf("failed to query final outbox status: %v", err)
	}
	if finalStatus != "PROCESSED" {
		t.Fatalf("expected final status 'PROCESSED', got %s", finalStatus)
	}
}

func TestRepository_PurgeProcessedOutbox(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewRepository(pool)

	now := time.Now().UTC()
	cutoffPortfolio := now.Add(-24 * time.Hour)
	cutoffNotif := now.Add(-7 * 24 * time.Hour)

	oldPortfolioID, _ := platformuuid.New()
	recentPortfolioID, _ := platformuuid.New()
	oldNotifID, _ := platformuuid.New()
	veryOldNotifID, _ := platformuuid.New()
	oldPendingID, _ := platformuuid.New()
	oldProcessingID, _ := platformuuid.New()

	insertQuery := `
		INSERT INTO notification_outbox (id, event_type, payload, target_channel, status, created_at, published_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`

	// 1. PROCESSED + old + portfolio (published 48h ago -> older than 24h cutoff)
	_, err := pool.Exec(ctx, insertQuery, oldPortfolioID, "PortfolioUpdated", []byte(`{}`), "user:portfolio:u1", "PROCESSED", now.Add(-48*time.Hour), now.Add(-48*time.Hour))
	require.NoError(t, err)

	// 2. PROCESSED + recent + portfolio (published 2h ago -> newer than 24h cutoff)
	_, err = pool.Exec(ctx, insertQuery, recentPortfolioID, "PortfolioUpdated", []byte(`{}`), "user:portfolio:u1", "PROCESSED", now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	require.NoError(t, err)

	// 3. PROCESSED + 48h old + notification (older than 24h, but NEWER than 7d notification cutoff)
	_, err = pool.Exec(ctx, insertQuery, oldNotifID, "NotificationCreated", []byte(`{}`), "user:notifications:u1", "PROCESSED", now.Add(-48*time.Hour), now.Add(-48*time.Hour))
	require.NoError(t, err)

	// 4. PROCESSED + 10d old + notification (published 10d ago -> older than 7d cutoff)
	_, err = pool.Exec(ctx, insertQuery, veryOldNotifID, "NotificationCreated", []byte(`{}`), "user:notifications:u1", "PROCESSED", now.Add(-10*24*time.Hour), now.Add(-10*24*time.Hour))
	require.NoError(t, err)

	// 5. PENDING + old (created 10d ago, published_at is NULL -> MUST NEVER BE PURGED)
	_, err = pool.Exec(ctx, insertQuery, oldPendingID, "NotificationCreated", []byte(`{}`), "user:portfolio:u1", "PENDING", now.Add(-10*24*time.Hour), nil)
	require.NoError(t, err)

	// 6. PROCESSING + old (created 10d ago, published_at is NULL -> MUST NEVER BE PURGED)
	_, err = pool.Exec(ctx, insertQuery, oldProcessingID, "NotificationCreated", []byte(`{}`), "user:portfolio:u1", "PROCESSING", now.Add(-10*24*time.Hour), nil)
	require.NoError(t, err)

	// --- Execution 1: Purge portfolio updates with 24h cutoff ---
	purgedPortfolio, err := repo.PurgeProcessedOutbox(ctx, "user:portfolio:", cutoffPortfolio, 1000)
	require.NoError(t, err)
	assert.Equal(t, int64(1), purgedPortfolio, "should have purged exactly 1 expired portfolio outbox record")

	// Verify old portfolio record is deleted
	var count int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM notification_outbox WHERE id = $1`, oldPortfolioID).Scan(&count)
	assert.Equal(t, 0, count, "old portfolio outbox must be deleted")

	// Verify recent portfolio record is still intact
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM notification_outbox WHERE id = $1`, recentPortfolioID).Scan(&count)
	assert.Equal(t, 1, count, "recent portfolio outbox must be preserved")

	// Verify old notification (48h old) is still intact (different channel prefix)
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM notification_outbox WHERE id = $1`, oldNotifID).Scan(&count)
	assert.Equal(t, 1, count, "old notification outbox must NOT be touched by portfolio purge")

	// Verify active PENDING and PROCESSING rows are still intact
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM notification_outbox WHERE id = $1`, oldPendingID).Scan(&count)
	assert.Equal(t, 1, count, "PENDING outbox must NEVER be deleted by purge")
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM notification_outbox WHERE id = $1`, oldProcessingID).Scan(&count)
	assert.Equal(t, 1, count, "PROCESSING outbox must NEVER be deleted by purge")

	// --- Execution 2: Purge notifications with 7-day cutoff ---
	purgedNotif, err := repo.PurgeProcessedOutbox(ctx, "user:notifications:", cutoffNotif, 1000)
	require.NoError(t, err)
	assert.Equal(t, int64(1), purgedNotif, "should have purged exactly 1 expired (10d old) notification outbox record")

	// Verify 10d old notification is deleted
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM notification_outbox WHERE id = $1`, veryOldNotifID).Scan(&count)
	assert.Equal(t, 0, count, "10d old notification outbox must be deleted")

	// Verify 48h old notification is still intact (younger than 7d cutoff)
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM notification_outbox WHERE id = $1`, oldNotifID).Scan(&count)
	assert.Equal(t, 1, count, "48h old notification outbox must be preserved under 7d retention")
}

func TestRepository_CreateTradeSettledTx_MidTransactionRollback(t *testing.T) {
	pool, cleanup := getTestPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	repo := postgres.NewRepository(pool)

	sourceEventID, _ := platformuuid.New()
	buyerUserID, _ := platformuuid.New()
	sellerUserID, _ := platformuuid.New()
	buyerNotifID, _ := platformuuid.New()
	sellerNotifID, _ := platformuuid.New()
	buyerOutboxID, _ := platformuuid.New()
	sellerOutboxID, _ := platformuuid.New()

	buyerNotif := &model.Notification{
		ID:        buyerNotifID,
		UserID:    buyerUserID,
		Title:     "Buy Filled",
		Message:   "Your buy order filled",
		Type:      model.TypeTradeFill,
		CreatedAt: time.Now().UTC(),
	}

	buyerOutbox := &model.OutboxEvent{
		ID:            buyerOutboxID,
		EventType:     "NotificationCreated",
		Payload:       []byte(`{"side":"BUY"}`),
		TargetChannel: "user:notifications:" + buyerUserID,
		CreatedAt:     time.Now().UTC(),
	}

	sellerOutbox := &model.OutboxEvent{
		ID:            sellerOutboxID,
		EventType:     "NotificationCreated",
		Payload:       []byte(`{"side":"SELL"}`),
		TargetChannel: "user:notifications:" + sellerUserID,
		CreatedAt:     time.Now().UTC(),
	}

	// Deliberately construct an INVALID seller notification type
	// This violates the database CHECK constraint: chk_notification_type
	// Order of execution in CreateTradeSettledTx:
	// 1. processed_events INSERT succeeds
	// 2. buyerNotif INSERT succeeds
	// 3. buyerOutbox INSERT succeeds
	// 4. sellerNotif INSERT FAILS due to CHECK constraint!
	// 5. Transaction must roll back ALL prior writes.
	invalidSellerNotif := &model.Notification{
		ID:        sellerNotifID,
		UserID:    sellerUserID,
		Title:     "Sell Filled",
		Message:   "Your sell order filled",
		Type:      "ILLEGAL_UNCONSTRAINED_TYPE",
		CreatedAt: time.Now().UTC(),
	}

	err := repo.CreateTradeSettledTx(ctx, buyerNotif, invalidSellerNotif, sourceEventID, buyerOutbox, sellerOutbox)
	require.Error(t, err, "expected error due to check constraint violation on seller notification type")

	// Verify atomicity: ZERO rows remain in processed_events, notifications, or notification_outbox
	var dedupCount int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM processed_events WHERE event_id = $1`, sourceEventID).Scan(&dedupCount)
	require.NoError(t, err)
	assert.Equal(t, 0, dedupCount, "processed_events must have rolled back")

	var notifCount int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM notifications WHERE id IN ($1, $2)`, buyerNotifID, sellerNotifID).Scan(&notifCount)
	require.NoError(t, err)
	assert.Equal(t, 0, notifCount, "notifications must have rolled back completely (0 rows)")

	var outboxCount int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM notification_outbox WHERE id IN ($1, $2)`, buyerOutboxID, sellerOutboxID).Scan(&outboxCount)
	require.NoError(t, err)
	assert.Equal(t, 0, outboxCount, "notification_outbox must have rolled back completely (0 rows)")
}


