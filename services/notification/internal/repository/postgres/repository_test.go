package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

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

	sourceEventID, _ := platformuuid.New()
	userID, _ := platformuuid.New()
	notifID, _ := platformuuid.New()
	outboxID, _ := platformuuid.New()

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
	err := repo.CreateWithDedupTx(ctx, notif, sourceEventID, outbox)
	if err != nil {
		t.Fatalf("first CreateWithDedupTx failed: %v", err)
	}

	// 2. Duplicate with same sourceEventID must fail with ErrAlreadyProcessed
	notif2ID, _ := platformuuid.New()
	notif2 := &model.Notification{
		ID:        notif2ID,
		UserID:    userID,
		Title:     "Duplicate Alert",
		Message:   "Duplicate message",
		Type:      model.TypeSystem,
		CreatedAt: time.Now().UTC(),
	}
	err = repo.CreateWithDedupTx(ctx, notif2, sourceEventID, outbox)
	if !errors.Is(err, repository.ErrAlreadyProcessed) {
		t.Fatalf("expected ErrAlreadyProcessed on duplicate event_id, got: %v", err)
	}
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

