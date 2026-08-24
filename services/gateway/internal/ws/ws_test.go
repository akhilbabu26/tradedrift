package ws

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

// mockSnapshotProvider implements SnapshotProvider for unit tests.
type mockSnapshotProvider struct {
	depthCalls  int
	tickerCalls int
}

func (m *mockSnapshotProvider) GetImmediateOrderBook(marketID string) (*OrderBookDepthPayload, error) {
	m.depthCalls++
	return &OrderBookDepthPayload{
		MarketID:  marketID,
		Bids:      [][2]string{{"64000.00", "1.0"}},
		Asks:      [][2]string{{"64100.00", "1.5"}},
		Timestamp: time.Now().UnixMilli(),
	}, nil
}

func (m *mockSnapshotProvider) GetImmediateTicker(marketID string) (*TickerPayload, error) {
	m.tickerCalls++
	return &TickerPayload{
		MarketID:  marketID,
		LastPrice: "64050.00",
	}, nil
}

func TestHub_OnDemandMarketTracking(t *testing.T) {
	logger := zap.NewNop()
	mockProvider := &mockSnapshotProvider{}
	hub := NewHub(logger, mockProvider)

	client := &Client{
		hub:    hub,
		send:   make(chan []byte, 10),
		userID: "user-123",
		logger: logger,
		subs:   make(map[string]bool),
	}
	hub.Register(client)

	// Initially zero active markets
	if len(hub.GetActiveMarketIDs()) != 0 {
		t.Fatalf("expected 0 active markets, got %d", len(hub.GetActiveMarketIDs()))
	}

	// Subscribe to BTC-USDT orderbook
	hub.HandleClientFrame(client, InboundFrame{
		Event:   "subscribe",
		Streams: []string{"market:orderbook:BTC-USDT"},
	})

	active := hub.GetActiveMarketIDs()
	if len(active) != 1 || active[0] != "BTC-USDT" {
		t.Fatalf("expected ['BTC-USDT'] active market, got %v", active)
	}

	// Immediate snapshot should have been invoked
	if mockProvider.depthCalls != 1 {
		t.Fatalf("expected 1 immediate depth call, got %d", mockProvider.depthCalls)
	}

	// Unsubscribe
	hub.HandleClientFrame(client, InboundFrame{
		Event:   "unsubscribe",
		Streams: []string{"market:orderbook:BTC-USDT"},
	})

	if len(hub.GetActiveMarketIDs()) != 0 {
		t.Fatalf("expected 0 active markets after unsubscribe, got %d", len(hub.GetActiveMarketIDs()))
	}
}

func TestHub_PrivateChannelAuthorization(t *testing.T) {
	logger := zap.NewNop()
	hub := NewHub(logger, &mockSnapshotProvider{})

	// Anonymous client
	anonClient := &Client{
		hub:    hub,
		send:   make(chan []byte, 10),
		userID: "",
		logger: logger,
		subs:   make(map[string]bool),
	}
	hub.Register(anonClient)

	hub.HandleClientFrame(anonClient, InboundFrame{
		Event:   "subscribe",
		Streams: []string{"user:notifications:user-123"},
	})

	if anonClient.HasSubscription("user:notifications:user-123") {
		t.Fatal("anonymous client should NOT be allowed to subscribe to private notifications")
	}

	// Authenticated client with wrong ID
	otherClient := &Client{
		hub:    hub,
		send:   make(chan []byte, 10),
		userID: "user-456",
		logger: logger,
		subs:   make(map[string]bool),
	}
	hub.Register(otherClient)

	hub.HandleClientFrame(otherClient, InboundFrame{
		Event:   "subscribe",
		Streams: []string{"user:notifications:user-123"},
	})

	if otherClient.HasSubscription("user:notifications:user-123") {
		t.Fatal("client with mismatched user_id should NOT be allowed to subscribe to other user's stream")
	}

	// Authenticated client with matching ID
	validClient := &Client{
		hub:    hub,
		send:   make(chan []byte, 10),
		userID: "user-123",
		logger: logger,
		subs:   make(map[string]bool),
	}
	hub.Register(validClient)

	hub.HandleClientFrame(validClient, InboundFrame{
		Event:   "subscribe",
		Streams: []string{"user:notifications:user-123"},
	})

	if !validClient.HasSubscription("user:notifications:user-123") {
		t.Fatal("client with matching user_id SHOULD be allowed to subscribe to private stream")
	}
}

func TestClient_BackpressureMatrix(t *testing.T) {
	logger := zap.NewNop()
	hub := NewHub(logger, &mockSnapshotProvider{})

	client := &Client{
		hub:    hub,
		send:   make(chan []byte, 1), // Buffer size 1 for easy saturation
		userID: "user-123",
		logger: logger,
		subs:   make(map[string]bool),
	}
	hub.Register(client)

	// Fill buffer
	client.Send(StreamTypeOrderBook, []byte("msg1"))

	// 1. OrderBook Backpressure -> Should drop stale snapshot without closing connection
	dropped := !client.Send(StreamTypeOrderBook, []byte("msg2"))
	if !dropped {
		t.Fatal("expected orderbook update to be dropped when buffer is saturated")
	}
	if client.closed {
		t.Fatal("client should NOT be closed when orderbook snapshot is dropped")
	}
	if hub.MessagesDroppedTotal() == 0 {
		t.Fatal("expected dropped messages metric to increment")
	}

	// 2. Trades Backpressure -> Should disconnect slow client to prevent silent trade loss
	client.Send(StreamTypeTrades, []byte("trade1"))
	if !client.closed {
		t.Fatal("expected slow client to be disconnected when trade execution stream is saturated")
	}
	if hub.SlowClientsTotal() == 0 {
		t.Fatal("expected slow clients metric to increment")
	}
}

// ─── Phase-2 Bug Fix Tests ────────────────────────────────────────────────────

// TestDuplicateSubscription verifies that subscribing to the same stream twice
// is idempotent: marketSubs counter remains 1 (not 2), and a second snapshot
// is NOT requested from the provider.
//
// Bug Fix #2: Previously AddSubscription returned true on duplicates, causing
// Hub to increment marketSubs[target] twice and call the snapshot provider twice.
func TestDuplicateSubscription(t *testing.T) {
	logger := zap.NewNop()
	mockProvider := &mockSnapshotProvider{}
	hub := NewHub(logger, mockProvider)

	client := &Client{
		hub:    hub,
		send:   make(chan []byte, 10),
		userID: "user-dup",
		logger: logger,
		subs:   make(map[string]bool),
	}
	hub.Register(client)

	// Subscribe first time
	hub.HandleClientFrame(client, InboundFrame{
		Event:   "subscribe",
		Streams: []string{"market:orderbook:BTC-USDT"},
	})

	if !client.HasSubscription("market:orderbook:BTC-USDT") {
		t.Fatal("first subscription should succeed")
	}
	if mockProvider.depthCalls != 1 {
		t.Fatalf("expected 1 snapshot call on first subscribe, got %d", mockProvider.depthCalls)
	}

	// Subscribe again to the same stream
	hub.HandleClientFrame(client, InboundFrame{
		Event:   "subscribe",
		Streams: []string{"market:orderbook:BTC-USDT"},
	})

	// marketSubs must be 1, not 2
	hub.mu.RLock()
	count := hub.marketSubs["BTC-USDT"]
	hub.mu.RUnlock()
	if count != 1 {
		t.Fatalf("duplicate subscribe must not double-increment marketSubs: got %d, want 1", count)
	}
}

// TestInvalidStream verifies that malformed stream names are rejected with
// an INVALID_STREAM error frame and never registered in the hub.
//
// Bug Fix #6: Previously unknown stream prefixes silently fell through
// ParseStreamType and were registered as StreamTypeControl.
func TestInvalidStream(t *testing.T) {
	logger := zap.NewNop()
	hub := NewHub(logger, &mockSnapshotProvider{})

	client := &Client{
		hub:    hub,
		send:   make(chan []byte, 10),
		userID: "",
		logger: logger,
		subs:   make(map[string]bool),
	}
	hub.Register(client)

	invalidStreams := []string{
		"",                          // empty
		"market",                    // only 1 part
		"market:orderbook",          // missing market ID
		"market:orderbook:",         // empty market ID
		"market:unknown:BTC-USDT",  // unrecognized sub-type
		"user:alerts:user-123",      // wrong sub (should be "notifications")
		"badprefix:orderbook:X",     // unknown prefix
		"a:b:c:d",                   // too many parts
	}

	for _, stream := range invalidStreams {
		// Drain send channel before each test
		for len(client.send) > 0 {
			<-client.send
		}

		hub.HandleClientFrame(client, InboundFrame{
			Event:   "subscribe",
			Streams: []string{stream},
		})

		// Should NOT be subscribed
		if client.HasSubscription(stream) {
			t.Errorf("invalid stream %q should NOT be subscribed", stream)
		}

		// Should have received an error frame
		if len(client.send) == 0 {
			t.Errorf("expected error frame for invalid stream %q", stream)
		}
	}
}

// TestValidateStream verifies the strict parsing logic.
func TestValidateStream(t *testing.T) {
	cases := []struct {
		stream string
		ok     bool
		typ    string
		target string
	}{
		{"market:orderbook:BTC-USDT", true, StreamTypeOrderBook, "BTC-USDT"},
		{"market:ticker:ETH-USDT", true, StreamTypeTicker, "ETH-USDT"},
		{"market:trades:SOL-USDT", true, StreamTypeTrades, "SOL-USDT"},
		{"user:notifications:uuid-123", true, StreamTypeNotification, "uuid-123"},
		{"market:orderbook:", false, StreamTypeControl, ""},
		{"market:foo:BTC-USDT", false, StreamTypeControl, ""},
		{"user:alerts:123", false, StreamTypeControl, ""},
		{"", false, StreamTypeControl, ""},
		{"only_one_part", false, StreamTypeControl, ""},
	}

	for _, tc := range cases {
		typ, target, ok := ValidateStream(tc.stream)
		if ok != tc.ok {
			t.Errorf("ValidateStream(%q) ok=%v, want %v", tc.stream, ok, tc.ok)
		}
		if ok {
			if typ != tc.typ {
				t.Errorf("ValidateStream(%q) type=%q, want %q", tc.stream, typ, tc.typ)
			}
			if target != tc.target {
				t.Errorf("ValidateStream(%q) target=%q, want %q", tc.stream, target, tc.target)
			}
		}
	}
}

// TestAddSubscriptionIdempotent verifies that calling AddSubscription for the
// same stream twice returns (false, true) on the second call and does not
// count toward the subscription limit.
func TestAddSubscriptionIdempotent(t *testing.T) {
	logger := zap.NewNop()
	hub := NewHub(logger, nil)

	c := &Client{
		hub:    hub,
		send:   make(chan []byte, 10),
		logger: logger,
		subs:   make(map[string]bool),
	}

	added, allowed := c.AddSubscription("market:orderbook:BTC-USDT")
	if !added || !allowed {
		t.Fatalf("first subscribe: want (true,true), got (%v,%v)", added, allowed)
	}

	// Second call — same stream
	added2, allowed2 := c.AddSubscription("market:orderbook:BTC-USDT")
	if added2 || !allowed2 {
		t.Fatalf("second subscribe (duplicate): want (false,true), got (%v,%v)", added2, allowed2)
	}

	// Subscription count must be 1
	if len(c.subs) != 1 {
		t.Fatalf("expected 1 subscription in subs map, got %d", len(c.subs))
	}
}

// TestAddSubscriptionLimit verifies that after 50 unique subscriptions,
// further AddSubscription calls return (false, false).
func TestAddSubscriptionLimit(t *testing.T) {
	logger := zap.NewNop()
	hub := NewHub(logger, nil)

	c := &Client{
		hub:    hub,
		send:   make(chan []byte, 10),
		logger: logger,
		subs:   make(map[string]bool),
	}

	for i := 0; i < maxSubLimit; i++ {
		stream := "market:orderbook:MKT-" + string(rune('A'+i))
		added, allowed := c.AddSubscription(stream)
		if !added || !allowed {
			t.Fatalf("sub %d should succeed, got (%v,%v)", i, added, allowed)
		}
	}

	// 51st subscription should be rejected
	added, allowed := c.AddSubscription("market:orderbook:EXTRA")
	if added || allowed {
		t.Fatalf("51st subscription: want (false,false), got (%v,%v)", added, allowed)
	}
}
