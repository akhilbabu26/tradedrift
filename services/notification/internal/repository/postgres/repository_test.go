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
			processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS notification_outbox (
			id             UUID PRIMARY KEY,
			event_type     VARCHAR(50) NOT NULL,
			payload        JSONB NOT NULL,
			target_channel VARCHAR(100) NOT NULL,
			status         VARCHAR(20) NOT NULL DEFAULT 'PENDING',
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

	// Recover stale claims with 0 duration (treats all PROCESSING as stale)
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

	// Fetch first page of 3 items
	page1, err := repo.GetByUserID(ctx, model.PaginationFilter{
		UserID: userID,
		Limit:  3,
	})
	if err != nil {
		t.Fatalf("page 1 fetch failed: %v", err)
	}
	if len(page1) != 3 {
		t.Fatalf("expected 3 items on page 1, got %d", len(page1))
	}

	// Fetch second page of remaining 2 items using page1's last item as cursor
	lastItem := page1[len(page1)-1]
	page2, err := repo.GetByUserID(ctx, model.PaginationFilter{
		UserID:     userID,
		CursorTime: &lastItem.CreatedAt,
		CursorID:   lastItem.ID,
		Limit:      3,
	})
	if err != nil {
		t.Fatalf("page 2 fetch failed: %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("expected 2 items on page 2, got %d", len(page2))
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
