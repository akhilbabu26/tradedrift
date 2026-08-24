package protocol_test

import (
	"encoding/json"
	"testing"
	"time"

	"tradedrift/services/gateway/internal/ws/protocol"
)

func TestValidateStream(t *testing.T) {
	cases := []struct {
		stream     string
		wantType   string
		wantTarget string
		wantOk     bool
	}{
		{"market:orderbook:BTC-USDT", protocol.StreamTypeOrderBook, "BTC-USDT", true},
		{"market:ticker:ETH-USDT", protocol.StreamTypeTicker, "ETH-USDT", true},
		{"market:trades:SOL-USDT", protocol.StreamTypeTrades, "SOL-USDT", true},
		{"user:notifications:user-123", protocol.StreamTypeNotification, "user-123", true},
		{"market:orderbook:", protocol.StreamTypeControl, "", false},
		{"user:notifications:", protocol.StreamTypeControl, "", false},
		{"market:unknown:BTC-USDT", protocol.StreamTypeControl, "", false},
		{"invalid", protocol.StreamTypeControl, "", false},
		{"", protocol.StreamTypeControl, "", false},
	}

	for _, tc := range cases {
		st, tgt, ok := protocol.ValidateStream(tc.stream)
		if ok != tc.wantOk || st != tc.wantType || tgt != tc.wantTarget {
			t.Errorf("ValidateStream(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.stream, st, tgt, ok, tc.wantType, tc.wantTarget, tc.wantOk)
		}
	}
}

func TestPublicTradeDoesNotExposeUserIDs(t *testing.T) {
	payload := protocol.TradePayload{
		TradeID:    "trade-1",
		MarketID:   "BTC-USDT",
		Price:      "64000.00",
		Quantity:   "1.0",
		Sequence:   1,
		ExecutedAt: time.Now().UnixMilli(),
	}

	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	data := string(b)

	for _, field := range []string{"buyerUserId", "sellerUserId", "buyer_user_id", "seller_user_id"} {
		if containsString(data, field) {
			t.Fatalf("TradePayload JSON must not contain user IDs, got: %s", data)
		}
	}
}

func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
