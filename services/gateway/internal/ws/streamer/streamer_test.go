package streamer_test

import (
	"errors"
	"testing"
	"time"

	"tradedrift/services/gateway/internal/ws/protocol"
	"tradedrift/services/gateway/internal/ws/streamer"
)

func TestMalformedTradeEvent(t *testing.T) {
	cases := []struct {
		name    string
		event   streamer.RawTradeEvent
		wantErr bool
	}{
		{"valid", streamer.RawTradeEvent{TradeID: "t1", MarketID: "BTC-USDT", Price: "100.50", Quantity: "1.25", Sequence: 101}, false},
		{"missing trade_id", streamer.RawTradeEvent{MarketID: "BTC-USDT", Price: "100", Quantity: "1", Sequence: 101}, true},
		{"missing market_id", streamer.RawTradeEvent{TradeID: "t1", Price: "100", Quantity: "1", Sequence: 101}, true},
		{"missing price", streamer.RawTradeEvent{TradeID: "t1", MarketID: "BTC-USDT", Quantity: "1", Sequence: 101}, true},
		{"missing quantity", streamer.RawTradeEvent{TradeID: "t1", MarketID: "BTC-USDT", Price: "100", Sequence: 101}, true},
		{"missing sequence", streamer.RawTradeEvent{TradeID: "t1", MarketID: "BTC-USDT", Price: "100", Quantity: "1", Sequence: 0}, true},
		{"non-numeric price", streamer.RawTradeEvent{TradeID: "t1", MarketID: "BTC-USDT", Price: "abc", Quantity: "1", Sequence: 101}, true},
		{"negative price", streamer.RawTradeEvent{TradeID: "t1", MarketID: "BTC-USDT", Price: "-50.0", Quantity: "1", Sequence: 101}, true},
		{"zero price", streamer.RawTradeEvent{TradeID: "t1", MarketID: "BTC-USDT", Price: "0", Quantity: "1", Sequence: 101}, true},
		{"negative quantity", streamer.RawTradeEvent{TradeID: "t1", MarketID: "BTC-USDT", Price: "100", Quantity: "-5", Sequence: 101}, true},
		{"zero quantity", streamer.RawTradeEvent{TradeID: "t1", MarketID: "BTC-USDT", Price: "100", Quantity: "0", Sequence: 101}, true},
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

func TestProcessUserStreamPayload(t *testing.T) {
	dedup := streamer.NewDedupCache()
	userUUID1 := "00000000-0000-0000-0000-000000000001"
	userUUID2 := "00000000-0000-0000-0000-000000000002"
	notifChannel1 := "user:notifications:" + userUUID1
	portfolioChannel1 := "user:portfolio:" + userUUID1

	t.Run("valid notification processed and deduplicated", func(t *testing.T) {
		validNotifJSON := []byte(`{
			"event_id": "ev-100",
			"notification_id": "notif-200",
			"type": "TRADE_FILL",
			"channel": "` + notifChannel1 + `",
			"data": {"title": "Trade Filled", "amount": "1.5"}
		}`)

		// First delivery succeeds
		payload, streamType, err := streamer.ProcessUserStreamPayload(validNotifJSON, notifChannel1, dedup)
		if err != nil {
			t.Fatalf("unexpected error on first processing: %v", err)
		}
		if streamType != protocol.StreamTypeNotification {
			t.Fatalf("expected streamType %q, got %s", protocol.StreamTypeNotification, streamType)
		}
		if len(payload) == 0 {
			t.Fatal("expected non-empty outbound payload")
		}

		// Second delivery with identical event_id:notification_id is suppressed
		_, _, err = streamer.ProcessUserStreamPayload(validNotifJSON, notifChannel1, dedup)
		if err == nil || err != streamer.ErrDuplicateNotification {
			t.Fatalf("expected ErrDuplicateNotification on second delivery, got %v", err)
		}
	})

	t.Run("portfolio updates are duplicate-tolerant state snapshots", func(t *testing.T) {
		portfolioJSON := []byte(`{
			"event_id": "ev-portfolio-999",
			"notification_id": "",
			"type": "PORTFOLIO_SNAPSHOT",
			"channel": "` + portfolioChannel1 + `",
			"data": {"total_value": "50000.00", "cash": "12000.00"}
		}`)

		// First delivery succeeds
		_, streamType1, err1 := streamer.ProcessUserStreamPayload(portfolioJSON, portfolioChannel1, dedup)
		if err1 != nil {
			t.Fatalf("unexpected error on first portfolio processing: %v", err1)
		}
		if streamType1 != protocol.StreamTypePortfolio {
			t.Fatalf("expected streamType %q, got %s", protocol.StreamTypePortfolio, streamType1)
		}

		// Second delivery with identical event_id MUST NOT be suppressed (snapshots are duplicate-tolerant)
		_, streamType2, err2 := streamer.ProcessUserStreamPayload(portfolioJSON, portfolioChannel1, dedup)
		if err2 != nil {
			t.Fatalf("portfolio snapshot must bypass deduplication, but got error: %v", err2)
		}
		if streamType2 != protocol.StreamTypePortfolio {
			t.Fatalf("expected streamType %q, got %s", protocol.StreamTypePortfolio, streamType2)
		}
	})

	t.Run("missing required fields in notification envelope", func(t *testing.T) {
		cases := []struct {
			name string
			raw  string
		}{
			{"missing event_id", `{"notification_id":"n1","data":{"msg":"hi"}}`},
			{"missing notification_id", `{"event_id":"e1","data":{"msg":"hi"}}`},
			{"empty data", `{"event_id":"e1","notification_id":"n1","data":null}`},
			{"missing data field", `{"event_id":"e1","notification_id":"n1"}`},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, _, err := streamer.ProcessUserStreamPayload([]byte(tc.raw), notifChannel1, dedup)
				if err == nil || err != streamer.ErrInvalidNotification {
					t.Fatalf("expected ErrInvalidNotification, got %v", err)
				}
			})
		}
	})

	t.Run("channel mismatch between envelope and topic", func(t *testing.T) {
		mismatchedJSON := []byte(`{
			"event_id": "ev-1",
			"notification_id": "notif-1",
			"channel": "user:notifications:` + userUUID2 + `",
			"data": {"title": "Spoofed"}
		}`)

		_, _, err := streamer.ProcessUserStreamPayload(mismatchedJSON, notifChannel1, dedup)
		if err == nil || !errors.Is(err, streamer.ErrChannelMismatch) {
			t.Fatalf("expected ErrChannelMismatch, got %v", err)
		}
	})

	t.Run("invalid non-uuid stream channel rejected", func(t *testing.T) {
		validNotif := []byte(`{
			"event_id": "ev-1",
			"notification_id": "notif-1",
			"channel": "user:notifications:not-a-uuid",
			"data": {"title": "Test"}
		}`)

		_, _, err := streamer.ProcessUserStreamPayload(validNotif, "user:notifications:not-a-uuid", dedup)
		if err == nil || !errors.Is(err, streamer.ErrInvalidStreamChannel) {
			t.Fatalf("expected ErrInvalidStreamChannel, got %v", err)
		}
	})

	t.Run("malformed json payload", func(t *testing.T) {
		_, _, err := streamer.ProcessUserStreamPayload([]byte(`{not valid json}`), notifChannel1, dedup)
		if err == nil || !errors.Is(err, streamer.ErrMalformedJSON) {
			t.Fatalf("expected ErrMalformedJSON, got %v", err)
		}
	})
}

