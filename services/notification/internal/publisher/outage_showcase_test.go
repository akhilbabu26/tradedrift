package publisher_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	platformuuid "tradedrift/platform/uuid"
	"tradedrift/services/notification/internal/model"
	"tradedrift/services/notification/internal/publisher"
	"tradedrift/services/notification/internal/repository/postgres"
	"tradedrift/services/notification/internal/service"
)

func getIntegrationPool(t *testing.T) (*pgxpool.Pool, func()) {
	dsn := os.Getenv("NOTIFICATION_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://postgres:123@localhost:5432/tradedrift_notification?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Skipf("Skipping integration test: cannot connect to %s: %v", dsn, err)
		return nil, nil
	}

	if err := pool.Ping(ctx); err != nil {
		t.Skipf("Skipping integration test: ping failed to %s: %v", dsn, err)
		return nil, nil
	}

	cleanup := func() {
		pool.Close()
	}

	return pool, cleanup
}

func TestShowcase_RedisOutageAndRecoveryPipeline(t *testing.T) {
	pool, cleanup := getIntegrationPool(t)
	if pool == nil {
		return
	}
	defer cleanup()

	ctx := context.Background()
	// Ensure the schema matches the current migration state.
	// These are idempotent and safe to run on any existing DB.
	_, _ = pool.Exec(ctx, `ALTER TABLE notification_outbox ADD COLUMN IF NOT EXISTS claim_token UUID`)
	_, _ = pool.Exec(ctx, `ALTER TABLE processed_events ADD COLUMN IF NOT EXISTS notification_id UUID`)
	_, _ = pool.Exec(ctx, "DELETE FROM notification_outbox")
	_, _ = pool.Exec(ctx, "DELETE FROM notifications")
	_, _ = pool.Exec(ctx, "DELETE FROM processed_events")

	repo := postgres.NewRepository(pool)
	logger := zap.NewNop()
	svc := service.NewService(repo, logger)

	buyerID, _ := platformuuid.New()
	sellerID, _ := platformuuid.New()
	tradeID, _ := platformuuid.New()
	eventID, _ := platformuuid.New()  // domain event ID — distinct from tradeID
	buyOrderID, _ := platformuuid.New()
	sellOrderID, _ := platformuuid.New()

	// ─── Step 1: Simulate Ingestion of a Domain Event (TradeSettled) ───────────
	ev := &service.TradeSettledEvent{
		EventID:      eventID,
		TradeID:      tradeID,
		MarketID:     "BTC-USDT",
		BaseAsset:    "BTC",
		QuoteAsset:   "USDT",
		BuyerUserID:  buyerID,
		SellerUserID: sellerID,
		BuyOrderID:   buyOrderID,
		SellOrderID:  sellOrderID,
		Price:        "96450.00",
		Quantity:     "0.1500",
		ExecutedAt:   time.Now().UTC().Format(time.RFC3339),
	}

	err := svc.HandleTradeSettled(ctx, ev)
	require.NoError(t, err, "HandleTradeSettled must succeed atomically in PostgreSQL")

	// Verify persistence in notifications table
	notifs, err := repo.GetByUserID(ctx, model.PaginationFilter{
		UserID: buyerID,
		Limit:  10,
	})
	require.NoError(t, err)
	require.Len(t, notifs, 1)
	assert.Contains(t, notifs[0].Message, "BUY order of 0.1500 BTC")
	assert.False(t, strings.Contains(notifs[0].Message, sellerID), "Counterparty seller ID must not leak")

	// ─── Step 2: Simulate Redis Outage During Outbox Publication ─────────────
	rdb := newMockRedis()
	rdb.alwaysFail = true // REDIS IS DOWN

	pub := publisher.NewPublisher(repo, rdb, logger, publisher.Config{
		BatchSize:         10,
		MaxPublishRetries: 2,
	})

	// Publisher attempts to process batch while Redis is down
	processed, pubErr := pub.ProcessBatch(ctx)
	assert.Error(t, pubErr, "Publishing must fail when Redis is unreachable")
	assert.Equal(t, 0, processed)

	// Verify PostgreSQL durability guarantee: Outbox rows must remain PENDING
	var pendingCount int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM notification_outbox WHERE status = 'PENDING'`).Scan(&pendingCount)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, pendingCount, 2, "Outbox rows must remain safely persisted with status PENDING during Redis outage")

	// ─── Step 3: Simulate Redis Recovery & Catch-up ──────────────────────────
	rdb.alwaysFail = false // REDIS RECOVERS

	processedAfterRecovery, pubErrAfterRecovery := pub.ProcessBatch(ctx)
	require.NoError(t, pubErrAfterRecovery, "Outbox publisher must succeed once Redis recovers")
	assert.GreaterOrEqual(t, processedAfterRecovery, 2, "Both buyer and seller outbox events must be processed")

	// Verify outbox rows transitioned to PROCESSED in PostgreSQL
	var processedRows int
	err = pool.QueryRow(ctx, `SELECT COUNT(*) FROM notification_outbox WHERE status = 'PROCESSED' AND target_channel IN ($1, $2)`,
		"user:notifications:"+buyerID, "user:notifications:"+sellerID).Scan(&processedRows)
	require.NoError(t, err)
	assert.Equal(t, 2, processedRows, "Both outbox records must be marked PROCESSED with published_at timestamps")

	// ─── Step 4: Verify Redis Pub/Sub Frames and Envelopes ───────────────────
	rdb.mu.Lock()
	buyerMessages := rdb.published["user:notifications:"+buyerID]
	sellerMessages := rdb.published["user:notifications:"+sellerID]
	rdb.mu.Unlock()

	require.Len(t, buyerMessages, 1, "Buyer must receive exactly one publication on recovery")
	require.Len(t, sellerMessages, 1, "Seller must receive exactly one publication on recovery")

	var buyerEnv model.RedisEnvelope
	require.NoError(t, json.Unmarshal([]byte(buyerMessages[0]), &buyerEnv))
	assert.Equal(t, eventID, buyerEnv.EventID) // source domain event ID, not trade ID
	assert.NotEmpty(t, buyerEnv.NotificationID)
	assert.Equal(t, "notification.created", buyerEnv.Type)

	var sellerEnv model.RedisEnvelope
	require.NoError(t, json.Unmarshal([]byte(sellerMessages[0]), &sellerEnv))
	assert.Equal(t, eventID, sellerEnv.EventID) // same source event ID, different notification_id
	assert.NotEmpty(t, sellerEnv.NotificationID)
	assert.Equal(t, "notification.created", sellerEnv.Type)

}
