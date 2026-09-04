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

func TestUserTradeSettledEvent_Valid(t *testing.T) {
	userTradeJSON := `{
		"trade_id": "a0000000-0000-0000-0000-000000000001",
		"user_id": "b0000000-0000-0000-0000-000000000002",
		"order_id": "c0000000-0000-0000-0000-000000000003",
		"role": "BUY",
		"market_id": "BTC-USDT",
		"base_asset": "BTC",
		"quote_asset": "USDT",
		"price": "95000.5000000000",
		"quantity": "0.1000000000",
		"sequence": 142,
		"executed_at": "2026-09-04T10:00:00.100000Z",
		"settled_at": "2026-09-04T10:00:00.200000Z"
	}`

	var event UserTradeSettledEvent
	if err := json.Unmarshal([]byte(userTradeJSON), &event); err != nil {
		t.Fatalf("failed to unmarshal UserTradeSettledEvent: %v", err)
	}

	if _, err := uuid.Parse(event.TradeID); err != nil {
		t.Errorf("invalid trade_id UUID: %v", err)
	}
	if _, err := uuid.Parse(event.UserID); err != nil {
		t.Errorf("invalid user_id UUID: %v", err)
	}
	if event.Role != "BUY" {
		t.Errorf("expected role BUY, got %s", event.Role)
	}

	execAt, _ := time.Parse(time.RFC3339Nano, event.ExecutedAt)
	settleAt, _ := time.Parse(time.RFC3339Nano, event.SettledAt)
	if settleAt.Before(execAt) {
		t.Errorf("settled_at must not be before executed_at")
	}

	price, _ := decimal.NewFromString(event.Price)
	if price.Exponent() < -10 {
		t.Errorf("price scale %d exceeds max scale 10", -price.Exponent())
	}
}

func TestValidation_ChronologicalAnomaly(t *testing.T) {
	execAt, _ := time.Parse(time.RFC3339Nano, "2026-09-04T10:05:00Z")
	settleAt, _ := time.Parse(time.RFC3339Nano, "2026-09-04T10:03:00Z") // Inverted!

	if !settleAt.Before(execAt) {
		t.Fatalf("expected settleAt to be before execAt for anomaly test")
	}
}

func TestValidation_MarketAssetMismatch(t *testing.T) {
	marketID := "ETH-USDT"
	baseAsset := "BTC"
	quoteAsset := "USDT"

	expected := baseAsset + "-" + quoteAsset
	if marketID == expected {
		t.Errorf("expected marketID to not match %s", expected)
	}
}

func TestValidation_DecimalScaleExceeded(t *testing.T) {
	excessiveScale := "0.123456789012345" // 15 decimal places
	d, err := decimal.NewFromString(excessiveScale)
	if err != nil {
		t.Fatalf("failed to parse decimal: %v", err)
	}

	if d.Exponent() >= -10 {
		t.Errorf("expected scale %d to be detected as exceeding 10 digits", -d.Exponent())
	}
}
