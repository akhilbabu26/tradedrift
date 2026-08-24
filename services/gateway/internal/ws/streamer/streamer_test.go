package streamer_test

import (
	"testing"
	"time"

	"tradedrift/services/gateway/internal/ws/streamer"
)

func TestTradeSequenceNumbers(t *testing.T) {
	s := streamer.NewStreamer(nil, nil, nil, "", "", nil)

	for want := uint64(1); want <= 5; want++ {
		got := s.NextTradeSeq("BTC-USDT")
		if got != want {
			t.Fatalf("BTC-USDT trade seq: want %d, got %d", want, got)
		}
	}
}

func TestMalformedTradeEvent(t *testing.T) {
	cases := []struct {
		name    string
		event   streamer.RawTradeEvent
		wantErr bool
	}{
		{"valid", streamer.RawTradeEvent{TradeID: "t1", MarketID: "BTC-USDT", Price: "100.50", Quantity: "1.25"}, false},
		{"missing trade_id", streamer.RawTradeEvent{MarketID: "BTC-USDT", Price: "100", Quantity: "1"}, true},
		{"missing market_id", streamer.RawTradeEvent{TradeID: "t1", Price: "100", Quantity: "1"}, true},
		{"missing price", streamer.RawTradeEvent{TradeID: "t1", MarketID: "BTC-USDT", Quantity: "1"}, true},
		{"missing quantity", streamer.RawTradeEvent{TradeID: "t1", MarketID: "BTC-USDT", Price: "100"}, true},
		{"non-numeric price", streamer.RawTradeEvent{TradeID: "t1", MarketID: "BTC-USDT", Price: "abc", Quantity: "1"}, true},
		{"negative price", streamer.RawTradeEvent{TradeID: "t1", MarketID: "BTC-USDT", Price: "-50.0", Quantity: "1"}, true},
		{"zero price", streamer.RawTradeEvent{TradeID: "t1", MarketID: "BTC-USDT", Price: "0", Quantity: "1"}, true},
		{"negative quantity", streamer.RawTradeEvent{TradeID: "t1", MarketID: "BTC-USDT", Price: "100", Quantity: "-5"}, true},
		{"zero quantity", streamer.RawTradeEvent{TradeID: "t1", MarketID: "BTC-USDT", Price: "100", Quantity: "0"}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := streamer.ValidateTradeEvent(tc.event)
			if tc.wantErr && err == nil {
				t.Error("expected validation error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestKafkaBackoff(t *testing.T) {
	for attempt := 0; attempt <= 6; attempt++ {
		for j := 0; j < 10; j++ {
			d := streamer.KafkaBackoff(attempt)
			if d < time.Second {
				t.Errorf("attempt %d: delay below 1s minimum: %v", attempt, d)
			}
			const cap = 30 * time.Second
			if d > cap+(cap/4) {
				t.Errorf("attempt %d: delay %v exceeds cap+jitter threshold", attempt, d)
			}
		}
	}
}

func TestAvailabilityStateMachine(t *testing.T) {
	s := streamer.NewStreamer(nil, nil, nil, "", "", nil)

	if !s.TransitionDepthUnavailable("BTC-USDT") {
		t.Fatal("first transitionDepthUnavailable must return true")
	}
	if s.TransitionDepthUnavailable("BTC-USDT") {
		t.Fatal("second transitionDepthUnavailable must return false")
	}
	if !s.ClearDepthUnavailable("BTC-USDT") {
		t.Fatal("ClearDepthUnavailable must return true when clearing an unavailable state")
	}
	if !s.TransitionDepthUnavailable("BTC-USDT") {
		t.Fatal("transitionDepthUnavailable must return true after recovery")
	}

	if !s.TransitionTickerUnavailable("ETH-USDT") {
		t.Fatal("first transitionTickerUnavailable must return true")
	}
	if s.TransitionTickerUnavailable("ETH-USDT") {
		t.Fatal("second transitionTickerUnavailable must return false")
	}
	if !s.ClearTickerUnavailable("ETH-USDT") {
		t.Fatal("ClearTickerUnavailable must return true when clearing an unavailable state")
	}
	if !s.TransitionTickerUnavailable("ETH-USDT") {
		t.Fatal("transitionTickerUnavailable must return true after recovery")
	}
}
