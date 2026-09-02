package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/trade/internal/repository"
)

type mockRepo struct {
	createFunc func(ctx context.Context, t *repository.Trade) error
}

func (m *mockRepo) Create(ctx context.Context, t *repository.Trade) error {
	if m.createFunc != nil {
		return m.createFunc(ctx, t)
	}
	return nil
}
func (m *mockRepo) GetByID(ctx context.Context, id uuid.UUID) (*repository.Trade, error) {
	return nil, nil
}
func (m *mockRepo) ListByUser(ctx context.Context, userID uuid.UUID, marketID string, after *repository.Cursor, limit int) ([]repository.Trade, error) {
	return nil, nil
}
func (m *mockRepo) ListByMarket(ctx context.Context, marketID string, after *repository.Cursor, limit int) ([]repository.Trade, error) {
	return nil, nil
}

func validEvent() TradeSettledEvent {
	return TradeSettledEvent{
		TradeID:     uuid.NewString(),
		BuyerID:     uuid.NewString(),
		SellerID:    uuid.NewString(),
		BuyOrderID:  uuid.NewString(),
		SellOrderID: uuid.NewString(),
		MarketID:    "BTC-USDT",
		BaseAsset:   "BTC",
		QuoteAsset:  "USDT",
		Price:       "65000.50",
		Quantity:    "0.75",
		Sequence:    101,
		ExecutedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		SettledAt:   time.Now().UTC().Format(time.RFC3339Nano),
	}
}

func TestConsumer_ProcessValidation(t *testing.T) {
	var savedTrade *repository.Trade
	repo := &mockRepo{
		createFunc: func(ctx context.Context, tr *repository.Trade) error {
			savedTrade = tr
			return nil
		},
	}
	consumer := &Consumer{repo: repo, log: zap.NewNop()}
	ctx := context.Background()

	// 1. Happy path
	evt := validEvent()
	err := consumer.process(ctx, evt)
	if err != nil {
		t.Fatalf("expected clean process, got error: %v", err)
	}
	if savedTrade == nil || savedTrade.ID.String() != evt.TradeID {
		t.Fatalf("expected trade to be persisted correctly")
	}

	// 2. Invalid trade_id -> PoisonError
	evtBadID := validEvent()
	evtBadID.TradeID = "not-a-uuid"
	err = consumer.process(ctx, evtBadID)
	var poison *PoisonError
	if !errors.As(err, &poison) {
		t.Fatalf("expected PoisonError for invalid trade ID, got: %v", err)
	}

	// 3. Self-trade (buyer == seller) -> PoisonError
	sameUserID := uuid.NewString()
	evtSelf := validEvent()
	evtSelf.BuyerID = sameUserID
	evtSelf.SellerID = sameUserID
	err = consumer.process(ctx, evtSelf)
	if !errors.As(err, &poison) {
		t.Fatalf("expected PoisonError for self-trade, got: %v", err)
	}

	// 4. Sequence == 0 -> PoisonError
	evtZeroSeq := validEvent()
	evtZeroSeq.Sequence = 0
	err = consumer.process(ctx, evtZeroSeq)
	if !errors.As(err, &poison) {
		t.Fatalf("expected PoisonError for sequence=0, got: %v", err)
	}

	// 5. Negative price -> PoisonError
	evtNegPrice := validEvent()
	evtNegPrice.Price = "-100.50"
	err = consumer.process(ctx, evtNegPrice)
	if !errors.As(err, &poison) {
		t.Fatalf("expected PoisonError for negative price, got: %v", err)
	}

	// 6. Zero quantity -> PoisonError
	evtZeroQty := validEvent()
	evtZeroQty.Quantity = "0.000"
	err = consumer.process(ctx, evtZeroQty)
	if !errors.As(err, &poison) {
		t.Fatalf("expected PoisonError for zero quantity, got: %v", err)
	}

	// 7. Sequence conflict from repo -> PoisonError
	repoConflict := &mockRepo{
		createFunc: func(ctx context.Context, tr *repository.Trade) error {
			return repository.ErrSequenceConflict
		},
	}
	consumerConflict := &Consumer{repo: repoConflict, log: zap.NewNop()}
	err = consumerConflict.process(ctx, validEvent())
	if !errors.As(err, &poison) {
		t.Fatalf("expected PoisonError for sequence conflict, got: %v", err)
	}

	// 8. Transient DB error -> Plain error (retryable, NOT poison)
	repoDBErr := &mockRepo{
		createFunc: func(ctx context.Context, tr *repository.Trade) error {
			return errors.New("connection reset by peer")
		},
	}
	consumerDBErr := &Consumer{repo: repoDBErr, log: zap.NewNop()}
	err = consumerDBErr.process(ctx, validEvent())
	if err == nil {
		t.Fatalf("expected error for DB failure")
	}
	if errors.As(err, &poison) {
		t.Fatalf("transient DB failure must NOT be classified as poison")
	}
}

func TestPriceAndQuantityPrecision(t *testing.T) {
	var savedTrade *repository.Trade
	repo := &mockRepo{
		createFunc: func(ctx context.Context, tr *repository.Trade) error {
			savedTrade = tr
			return nil
		},
	}
	consumer := &Consumer{repo: repo, log: zap.NewNop()}
	evt := validEvent()
	evt.Price = "65432.12345678"
	evt.Quantity = "0.00000001"

	err := consumer.process(context.Background(), evt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedPrice, _ := decimal.NewFromString("65432.12345678")
	expectedQty, _ := decimal.NewFromString("0.00000001")
	if !savedTrade.Price.Equal(expectedPrice) {
		t.Errorf("expected price %v, got %v", expectedPrice, savedTrade.Price)
	}
	if !savedTrade.Quantity.Equal(expectedQty) {
		t.Errorf("expected quantity %v, got %v", expectedQty, savedTrade.Quantity)
	}
}
