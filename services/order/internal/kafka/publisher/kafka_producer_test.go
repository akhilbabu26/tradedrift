package publisher

import (
	"testing"

	"go.uber.org/zap"
)

func TestKafkaProducer_ResolvePartition(t *testing.T) {
	logger := zap.NewNop()
	producer, err := NewKafkaProducer([]string{"localhost:9092"}, logger)
	if err != nil {
		t.Fatalf("NewKafkaProducer failed: %v", err)
	}

	tests := []struct {
		marketID string
		expected int
	}{
		{"BTC-USDT", 0},
		{"ETH-USDT", 1},
		{"SOL-USDT", 2},
		{"UNKNOWN-PAIR", 0}, // fallback default
	}

	for _, tt := range tests {
		got := producer.ResolvePartition(tt.marketID)
		if got != tt.expected {
			t.Errorf("ResolvePartition(%q) = %d, expected %d", tt.marketID, got, tt.expected)
		}
	}
}
