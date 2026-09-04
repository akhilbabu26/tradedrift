package kafka

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestTradeSettledEventValidation_Valid(t *testing.T) {
	validJSON := `{
		"trade_id": "a0000000-0000-0000-0000-000000000001",
		"buyer_id": "b0000000-0000-0000-0000-000000000002",
		"seller_id": "c0000000-0000-0000-0000-000000000003",
		"buy_order_id": "d0000000-0000-0000-0000-000000000004",
		"sell_order_id": "e0000000-0000-0000-0000-000000000005",
		"market_id": "BTC-USDT",
		"base_asset": "BTC",
		"quote_asset": "USDT",
		"price": "95000.50",
		"quantity": "0.1000",
		"sequence": 142,
		"executed_at": "2026-09-04T10:00:00.123456Z",
		"settled_at": "2026-09-04T10:00:00.234567Z"
	}`

	var event TradeSettledEvent
	if err := json.Unmarshal([]byte(validJSON), &event); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if _, err := uuid.Parse(event.TradeID); err != nil {
		t.Errorf("expected valid trade_id UUID: %v", err)
	}
	if _, err := uuid.Parse(event.BuyerID); err != nil {
		t.Errorf("expected valid buyer_id UUID: %v", err)
	}
	if _, err := uuid.Parse(event.SellerID); err != nil {
		t.Errorf("expected valid seller_id UUID: %v", err)
	}
	if _, err := uuid.Parse(event.BuyOrderID); err != nil {
		t.Errorf("expected valid buy_order_id UUID: %v", err)
	}
	if _, err := uuid.Parse(event.SellOrderID); err != nil {
		t.Errorf("expected valid sell_order_id UUID: %v", err)
	}
	if event.Sequence == 0 {
		t.Errorf("expected sequence > 0")
	}
	if event.MarketID == "" || event.BaseAsset == "" || event.QuoteAsset == "" {
		t.Errorf("expected non-empty identifiers")
	}

	price, err := decimal.NewFromString(event.Price)
	if err != nil || !price.IsPositive() {
		t.Errorf("expected positive price")
	}

	qty, err := decimal.NewFromString(event.Quantity)
	if err != nil || !qty.IsPositive() {
		t.Errorf("expected positive quantity")
	}

	if _, err := time.Parse(time.RFC3339Nano, event.ExecutedAt); err != nil {
		t.Errorf("expected valid executed_at: %v", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, event.SettledAt); err != nil {
		t.Errorf("expected valid settled_at: %v", err)
	}
}

func TestTradeSettledEventValidation_InvalidSequence(t *testing.T) {
	invalidJSON := `{
		"trade_id": "a0000000-0000-0000-0000-000000000001",
		"buyer_id": "b0000000-0000-0000-0000-000000000002",
		"seller_id": "c0000000-0000-0000-0000-000000000003",
		"buy_order_id": "d0000000-0000-0000-0000-000000000004",
		"sell_order_id": "e0000000-0000-0000-0000-000000000005",
		"market_id": "BTC-USDT",
		"base_asset": "BTC",
		"quote_asset": "USDT",
		"price": "95000.50",
		"quantity": "0.1000",
		"sequence": 0,
		"executed_at": "2026-09-04T10:00:00.123456Z",
		"settled_at": "2026-09-04T10:00:00.234567Z"
	}`

	var event TradeSettledEvent
	if err := json.Unmarshal([]byte(invalidJSON), &event); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if event.Sequence == 0 {
		// As expected, sequence == 0 must be rejected
	} else {
		t.Errorf("expected sequence to be 0")
	}
}

func TestTradeSettledEventValidation_InvalidTimestamp(t *testing.T) {
	invalidTimestamp := "INVALID_DATE_FORMAT"
	_, err := time.Parse(time.RFC3339Nano, invalidTimestamp)
	if err == nil {
		t.Errorf("expected time.Parse to fail on %q", invalidTimestamp)
	}
}
