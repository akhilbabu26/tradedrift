package orderservice

import (
	"testing"

	orderv1 "tradedrift/platform/api/gen/order/v1"
)

func TestParseMarketFromLevelID(t *testing.T) {
	tests := []struct {
		levelID  string
		expected string
	}{
		{"MM-BTC-USDT-ASK-01", "BTC-USDT"},
		{"MM-ETH-USDT-BID-05", "ETH-USDT"},
		{"MM-SOL-USDT-BID-02", "SOL-USDT"},
		{"INVALID", ""},
		{"OTHER-BTC-USDT", ""},
	}

	for _, tt := range tests {
		got := parseMarketFromLevelID(tt.levelID)
		if got != tt.expected {
			t.Errorf("parseMarketFromLevelID(%q) = %q, expected %q", tt.levelID, got, tt.expected)
		}
	}
}

func TestParseLevelFromClientOrderID(t *testing.T) {
	tests := []struct {
		clientOrderID string
		wantLevel     string
		wantGen       int
		wantErr       bool
	}{
		{"MM-BTC-USDT-ASK-01-G003", "MM-BTC-USDT-ASK-01", 3, false},
		{"MM-ETH-USDT-BID-02-G042", "MM-ETH-USDT-BID-02", 42, false},
		{"MM-SOL-USDT-ASK-01-G0", "MM-SOL-USDT-ASK-01", 0, false},
		{"NO-GEN-SUFFIX", "", 0, true},
		{"MM-BTC-USDT-ASK-01-Gabc", "", 0, true},
	}

	for _, tt := range tests {
		level, gen, err := parseLevelFromClientOrderID(tt.clientOrderID)
		if tt.wantErr && err == nil {
			t.Errorf("parseLevelFromClientOrderID(%q) expected error, got nil", tt.clientOrderID)
		} else if !tt.wantErr && err != nil {
			t.Errorf("parseLevelFromClientOrderID(%q) unexpected error: %v", tt.clientOrderID, err)
		} else if !tt.wantErr {
			if level != tt.wantLevel || gen != tt.wantGen {
				t.Errorf("parseLevelFromClientOrderID(%q) = (%q, %d), want (%q, %d)",
					tt.clientOrderID, level, gen, tt.wantLevel, tt.wantGen)
			}
		}
	}
}

func TestProtoStatusToString(t *testing.T) {
	tests := []struct {
		proto orderv1.OrderStatus
		want  string
	}{
		{orderv1.OrderStatus_ORDER_STATUS_OPEN, "OPEN"},
		{orderv1.OrderStatus_ORDER_STATUS_PARTIALLY_FILLED, "PARTIALLY_FILLED"},
		{orderv1.OrderStatus_ORDER_STATUS_FILLED, "FILLED"},
		{orderv1.OrderStatus_ORDER_STATUS_CANCELLING, "CANCELLING"},
		{orderv1.OrderStatus_ORDER_STATUS_CANCELLED, "CANCELLED"},
		{orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED, "UNKNOWN"},
	}

	for _, tt := range tests {
		got := protoStatusToString(tt.proto)
		if got != tt.want {
			t.Errorf("protoStatusToString(%v) = %q, want %q", tt.proto, got, tt.want)
		}
	}
}
