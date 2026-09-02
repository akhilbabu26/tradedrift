package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/trade/internal/repository"
	"tradedrift/services/trade/internal/service"
)

// mockRepo satisfies repository.Repository for unit testing the service layer.
type mockRepo struct {
	getByIDFunc    func(ctx context.Context, id uuid.UUID) (*repository.Trade, error)
	listByUserFunc func(ctx context.Context, userID uuid.UUID, marketID string, after *repository.Cursor, limit int) ([]repository.Trade, error)
	listByMktFunc  func(ctx context.Context, marketID string, after *repository.Cursor, limit int) ([]repository.Trade, error)
}

func (m *mockRepo) Create(ctx context.Context, t *repository.Trade) error { return nil }
func (m *mockRepo) GetByID(ctx context.Context, id uuid.UUID) (*repository.Trade, error) {
	if m.getByIDFunc != nil {
		return m.getByIDFunc(ctx, id)
	}
	return nil, repository.ErrTradeNotFound
}
func (m *mockRepo) ListByUser(ctx context.Context, userID uuid.UUID, marketID string, after *repository.Cursor, limit int) ([]repository.Trade, error) {
	if m.listByUserFunc != nil {
		return m.listByUserFunc(ctx, userID, marketID, after, limit)
	}
	return nil, nil
}
func (m *mockRepo) ListByMarket(ctx context.Context, marketID string, after *repository.Cursor, limit int) ([]repository.Trade, error) {
	if m.listByMktFunc != nil {
		return m.listByMktFunc(ctx, marketID, after, limit)
	}
	return nil, nil
}

func TestGetTrade_Authorization(t *testing.T) {
	buyerID := uuid.New()
	sellerID := uuid.New()
	otherUserID := uuid.New()
	tradeID := uuid.New()

	trade := &repository.Trade{
		ID:          tradeID,
		BuyerID:     buyerID,
		SellerID:    sellerID,
		MarketID:    "BTC-USDT",
		Price:       decimal.NewFromInt(50000),
		Quantity:    decimal.NewFromFloat(1.5),
		ExecutedAt:  time.Now().UTC(),
		SettledAt:   time.Now().UTC(),
	}

	repo := &mockRepo{
		getByIDFunc: func(ctx context.Context, id uuid.UUID) (*repository.Trade, error) {
			if id == tradeID {
				return trade, nil
			}
			return nil, repository.ErrTradeNotFound
		},
	}

	svc := service.NewService(repo, zap.NewNop())
	ctx := context.Background()

	// 1. Buyer is authorized
	res, err := svc.GetTrade(ctx, tradeID, buyerID, false)
	if err != nil || res.ID != tradeID {
		t.Fatalf("expected buyer to be authorized, got err: %v", err)
	}

	// 2. Seller is authorized
	res, err = svc.GetTrade(ctx, tradeID, sellerID, false)
	if err != nil || res.ID != tradeID {
		t.Fatalf("expected seller to be authorized, got err: %v", err)
	}

	// 3. Admin is authorized even with other user ID
	res, err = svc.GetTrade(ctx, tradeID, otherUserID, true)
	if err != nil || res.ID != tradeID {
		t.Fatalf("expected admin to be authorized, got err: %v", err)
	}

	// 4. Admin is authorized even with empty user ID
	res, err = svc.GetTrade(ctx, tradeID, uuid.Nil, true)
	if err != nil || res.ID != tradeID {
		t.Fatalf("expected admin to be authorized with Nil UUID, got err: %v", err)
	}

	// 5. Unrelated user is denied (ErrNotParty)
	_, err = svc.GetTrade(ctx, tradeID, otherUserID, false)
	if !errors.Is(err, service.ErrNotParty) {
		t.Fatalf("expected ErrNotParty, got: %v", err)
	}

	// 6. Trade not found
	_, err = svc.GetTrade(ctx, uuid.New(), buyerID, false)
	if !errors.Is(err, repository.ErrTradeNotFound) {
		t.Fatalf("expected ErrTradeNotFound, got: %v", err)
	}
}

func TestListUserTrades_LimitClampingAndPagination(t *testing.T) {
	userID := uuid.New()
	t1ID := uuid.New()
	t2ID := uuid.New()
	now := time.Now().UTC()

	var passedLimit int
	repo := &mockRepo{
		listByUserFunc: func(ctx context.Context, u uuid.UUID, mkt string, after *repository.Cursor, limit int) ([]repository.Trade, error) {
			passedLimit = limit
			return []repository.Trade{
				{ID: t1ID, BuyerID: userID, ExecutedAt: now},
				{ID: t2ID, BuyerID: userID, ExecutedAt: now.Add(-time.Minute)},
			}, nil
		},
	}

	svc := service.NewService(repo, zap.NewNop())
	ctx := context.Background()

	// Default limit clamped when passed 0
	trades, cursor, err := svc.ListUserTrades(ctx, userID, "BTC-USDT", "", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if passedLimit != 20 {
		t.Errorf("expected default limit 20, got %d", passedLimit)
	}
	if len(trades) != 2 {
		t.Errorf("expected 2 trades, got %d", len(trades))
	}
	// Since 2 returned < limit (20), nextCursor should be empty
	if cursor != "" {
		t.Errorf("expected empty nextCursor, got %q", cursor)
	}

	// Test full page cursor generation
	repo.listByUserFunc = func(ctx context.Context, u uuid.UUID, mkt string, after *repository.Cursor, limit int) ([]repository.Trade, error) {
		return []repository.Trade{
			{ID: t1ID, BuyerID: userID, ExecutedAt: now},
			{ID: t2ID, BuyerID: userID, ExecutedAt: now.Add(-time.Minute)},
		}, nil
	}
	_, cursor, err = svc.ListUserTrades(ctx, userID, "BTC-USDT", "", 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cursor == "" {
		t.Errorf("expected valid base64 cursor when page is full")
	}

	// Test passing the cursor back to the next call
	_, _, err = svc.ListUserTrades(ctx, userID, "BTC-USDT", cursor, 2)
	if err != nil {
		t.Fatalf("failed to decode valid cursor on subsequent call: %v", err)
	}
}
