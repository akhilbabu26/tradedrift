package hub

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"tradedrift/services/gateway/internal/ws/protocol"
)

type mockSnapshotProvider struct {
	depthCalls  int
	tickerCalls int
}

func (m *mockSnapshotProvider) GetImmediateOrderBook(marketID string) (*protocol.OrderBookDepthPayload, error) {
	m.depthCalls++
	return &protocol.OrderBookDepthPayload{
		MarketID:  marketID,
		Bids:      [][2]string{{"64000.00", "1.0"}},
		Asks:      [][2]string{{"64100.00", "1.5"}},
		Timestamp: time.Now().UnixMilli(),
		Sequence:  100,
	}, nil
}

func (m *mockSnapshotProvider) GetImmediateTicker(marketID string) (*protocol.TickerPayload, error) {
	m.tickerCalls++
	return &protocol.TickerPayload{
		MarketID:  marketID,
		LastPrice: "64050.00",
	}, nil
}

type mockFailingProvider struct{}

func (m *mockFailingProvider) GetImmediateOrderBook(_ string) (*protocol.OrderBookDepthPayload, error) {
	return nil, errors.New("redis unavailable")
}
func (m *mockFailingProvider) GetImmediateTicker(_ string) (*protocol.TickerPayload, error) {
	return nil, errors.New("redis unavailable")
}

func TestHub_OnDemandMarketTracking(t *testing.T) {
	logger := zap.NewNop()
	mockProvider := &mockSnapshotProvider{}
	h := NewHub(logger, mockProvider)

	client := NewTestClient(h, "user-123", logger)
	h.Register(client)

	if len(h.GetActiveMarketIDs()) != 0 {
		t.Fatalf("expected 0 active markets, got %d", len(h.GetActiveMarketIDs()))
	}

	h.HandleClientFrame(client, protocol.InboundFrame{
		Event:   "subscribe",
		Streams: []string{"market:orderbook:BTC-USDT"},
	})

	active := h.GetActiveMarketIDs()
	if len(active) != 1 || active[0] != "BTC-USDT" {
		t.Fatalf("expected ['BTC-USDT'] active market, got %v", active)
	}

	if mockProvider.depthCalls != 1 {
		t.Fatalf("expected 1 immediate depth call, got %d", mockProvider.depthCalls)
	}

	h.HandleClientFrame(client, protocol.InboundFrame{
		Event:   "unsubscribe",
		Streams: []string{"market:orderbook:BTC-USDT"},
	})

	if len(h.GetActiveMarketIDs()) != 0 {
		t.Fatalf("expected 0 active markets after unsubscribe, got %d", len(h.GetActiveMarketIDs()))
	}
}

func TestHub_PrivateChannelAuthorization(t *testing.T) {
	logger := zap.NewNop()
	h := NewHub(logger, &mockSnapshotProvider{})

	anonClient := NewTestClient(h, "", logger)
	h.Register(anonClient)

	h.HandleClientFrame(anonClient, protocol.InboundFrame{
		Event:   "subscribe",
		Streams: []string{"user:notifications:user-123"},
	})

	if anonClient.HasSubscription("user:notifications:user-123") {
		t.Fatal("anonymous client should NOT be allowed to subscribe to private notifications")
	}

	authUserClient := NewTestClient(h, "user-456", logger)
	h.Register(authUserClient)

	h.HandleClientFrame(authUserClient, protocol.InboundFrame{
		Event:   "subscribe",
		Streams: []string{"user:notifications:user-123"},
	})

	if authUserClient.HasSubscription("user:notifications:user-123") {
		t.Fatal("user-456 should NOT be allowed to subscribe to user-123 notifications")
	}

	ownClient := NewTestClient(h, "user-123", logger)
	h.Register(ownClient)

	h.HandleClientFrame(ownClient, protocol.InboundFrame{
		Event:   "subscribe",
		Streams: []string{"user:notifications:user-123"},
	})

	if !ownClient.HasSubscription("user:notifications:user-123") {
		t.Fatal("user-123 should be allowed to subscribe to own notifications")
	}
}

func TestClient_BackpressureMatrix(t *testing.T) {
	logger := zap.NewNop()
	h := NewHub(logger, &mockSnapshotProvider{})

	client := &Client{
		hub:    h,
		send:   make(chan []byte, 1),
		userID: "user-123",
		logger: logger,
	}
	h.Register(client)

	client.Send(protocol.StreamTypeOrderBook, []byte("msg1"))

	dropped := !client.Send(protocol.StreamTypeOrderBook, []byte("msg2"))
	if !dropped {
		t.Fatal("expected orderbook update to be dropped when buffer is saturated")
	}
	if client.closed {
		t.Fatal("client should NOT be closed when orderbook snapshot is dropped")
	}
	if h.MessagesDroppedTotal() == 0 {
		t.Fatal("expected dropped messages metric to increment")
	}

	client.Send(protocol.StreamTypeTrades, []byte("trade1"))
	if !client.closed {
		t.Fatal("expected slow client to be disconnected when trade stream is saturated")
	}
	if h.SlowClientsTotal() == 0 {
		t.Fatal("expected slow clients metric to increment")
	}
}

func TestOutboundMessageSize(t *testing.T) {
	logger := zap.NewNop()
	h := NewHub(logger, nil)
	client := NewTestClient(h, "user-size", logger)
	h.Register(client)

	// Small message passes
	if !client.Send(protocol.StreamTypeOrderBook, []byte("ok")) {
		t.Fatal("small message should be accepted")
	}

	// Message exceeding 512 KB
	oversized := make([]byte, 513*1024)
	if client.Send(protocol.StreamTypeOrderBook, oversized) {
		t.Fatal("oversized outbound message should be dropped")
	}
}

func TestDuplicateSubscription(t *testing.T) {
	logger := zap.NewNop()
	mockProvider := &mockSnapshotProvider{}
	h := NewHub(logger, mockProvider)

	client := NewTestClient(h, "user-dup", logger)
	h.Register(client)

	h.HandleClientFrame(client, protocol.InboundFrame{
		Event:   "subscribe",
		Streams: []string{"market:orderbook:BTC-USDT"},
	})

	if !client.HasSubscription("market:orderbook:BTC-USDT") {
		t.Fatal("first subscription should succeed")
	}
	if mockProvider.depthCalls != 1 {
		t.Fatalf("expected 1 snapshot call on first subscribe, got %d", mockProvider.depthCalls)
	}

	h.HandleClientFrame(client, protocol.InboundFrame{
		Event:   "subscribe",
		Streams: []string{"market:orderbook:BTC-USDT"},
	})

	h.mu.RLock()
	count := h.marketSubs["BTC-USDT"]
	h.mu.RUnlock()
	if count != 1 {
		t.Fatalf("duplicate subscribe must not double-increment marketSubs: got %d, want 1", count)
	}
}

func TestAddSubscriptionLimit(t *testing.T) {
	logger := zap.NewNop()
	h := NewHub(logger, nil)
	c := NewTestClient(h, "user-limit", logger)
	h.Register(c)

	for i := 0; i < maxSubLimit; i++ {
		stream := "market:orderbook:MKT-" + string(rune('A'+i))
		h.handleSubscribe(c, stream)
	}

	if len(c.Subscriptions()) != maxSubLimit {
		t.Fatalf("expected %d subscriptions, got %d", maxSubLimit, len(c.Subscriptions()))
	}

	// 51st subscription should be rejected
	h.handleSubscribe(c, "market:orderbook:EXTRA")
	if len(c.Subscriptions()) != maxSubLimit {
		t.Fatalf("51st subscription should be rejected, got count %d", len(c.Subscriptions()))
	}
}

func TestUnsubscribeNonExistentStream(t *testing.T) {
	logger := zap.NewNop()
	mockProvider := &mockSnapshotProvider{}
	h := NewHub(logger, mockProvider)

	clientA := NewTestClient(h, "user-a", logger)
	h.Register(clientA)
	h.HandleClientFrame(clientA, protocol.InboundFrame{
		Event:   "subscribe",
		Streams: []string{"market:orderbook:BTC-USDT"},
	})

	h.mu.RLock()
	countAfterA := h.marketSubs["BTC-USDT"]
	h.mu.RUnlock()
	if countAfterA != 1 {
		t.Fatalf("after clientA subscribe: want marketSubs=1, got %d", countAfterA)
	}

	clientB := NewTestClient(h, "user-b", logger)
	h.Register(clientB)
	h.HandleClientFrame(clientB, protocol.InboundFrame{
		Event:   "unsubscribe",
		Streams: []string{"market:orderbook:BTC-USDT"},
	})

	h.mu.RLock()
	countAfterB := h.marketSubs["BTC-USDT"]
	h.mu.RUnlock()
	if countAfterB != 1 {
		t.Fatalf("after clientB phantom unsubscribe: want marketSubs=1, got %d", countAfterB)
	}

	h.HandleClientFrame(clientA, protocol.InboundFrame{
		Event:   "unsubscribe",
		Streams: []string{"market:orderbook:BTC-USDT"},
	})

	h.mu.RLock()
	countFinal := h.marketSubs["BTC-USDT"]
	h.mu.RUnlock()
	if countFinal != 0 {
		t.Fatalf("after clientA real unsubscribe: want marketSubs=0, got %d", countFinal)
	}
}

func TestConcurrentSubscribeAndClose(t *testing.T) {
	logger := zap.NewNop()

	for iteration := 0; iteration < 50; iteration++ {
		h := NewHub(logger, &mockSnapshotProvider{})
		var wg sync.WaitGroup

		numClients := 10
		clients := make([]*Client, numClients)
		for i := 0; i < numClients; i++ {
			clients[i] = NewTestClient(h, fmt.Sprintf("user-%d", i), logger)
			h.Register(clients[i])
		}

		for i := 0; i < numClients; i++ {
			c := clients[i]
			wg.Add(2)

			go func(cl *Client) {
				defer wg.Done()
				for s := 0; s < 5; s++ {
					h.HandleClientFrame(cl, protocol.InboundFrame{
						Event:   "subscribe",
						Streams: []string{"market:orderbook:BTC-USDT", "market:ticker:ETH-USDT"},
					})
				}
			}(c)

			go func(cl *Client) {
				defer wg.Done()
				time.Sleep(time.Duration(iteration%3) * time.Millisecond)
				cl.Close()
			}(c)
		}

		wg.Wait()

		h.mu.RLock()
		btcCount := h.marketSubs["BTC-USDT"]
		ethCount := h.marketSubs["ETH-USDT"]
		activeClients := len(h.clients)
		h.mu.RUnlock()

		if btcCount < 0 || ethCount < 0 {
			t.Fatalf("iteration %d: negative market count: btc=%d, eth=%d", iteration, btcCount, ethCount)
		}
		if activeClients != 0 && btcCount > activeClients {
			t.Fatalf("iteration %d: market count %d exceeds active clients %d", iteration, btcCount, activeClients)
		}
	}
}

func TestConcurrentSubscribeUnsubscribe(t *testing.T) {
	logger := zap.NewNop()
	h := NewHub(logger, &mockSnapshotProvider{})

	c := NewTestClient(h, "user-rapid", logger)
	h.Register(c)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if idx%2 == 0 {
				h.HandleClientFrame(c, protocol.InboundFrame{
					Event:   "subscribe",
					Streams: []string{"market:orderbook:BTC-USDT"},
				})
			} else {
				h.HandleClientFrame(c, protocol.InboundFrame{
					Event:   "unsubscribe",
					Streams: []string{"market:orderbook:BTC-USDT"},
				})
			}
		}(i)
	}

	wg.Wait()

	h.mu.RLock()
	marketCount := h.marketSubs["BTC-USDT"]
	hasSub := h.HasClientSubscription(c, "market:orderbook:BTC-USDT")
	h.mu.RUnlock()

	if hasSub && marketCount != 1 {
		t.Fatalf("expected marketSubs=1 when subscribed, got %d", marketCount)
	}
	if !hasSub && marketCount != 0 {
		t.Fatalf("expected marketSubs=0 when unsubscribed, got %d", marketCount)
	}
}

func TestRedisFailureEmitsErrorFrame(t *testing.T) {
	logger := zap.NewNop()
	h := NewHub(logger, &mockFailingProvider{})

	client := NewTestClient(h, "", logger)
	h.Register(client)

	h.HandleClientFrame(client, protocol.InboundFrame{
		Event:   "subscribe",
		Streams: []string{"market:orderbook:BTC-USDT"},
	})

	select {
	case msg := <-client.send:
		var evt protocol.OutboundEvent
		if err := json.Unmarshal(msg, &evt); err != nil {
			t.Fatalf("unmarshal error frame: %v", err)
		}
		if evt.Code != "MARKET_DATA_UNAVAILABLE" {
			t.Fatalf("expected MARKET_DATA_UNAVAILABLE, got %q", evt.Code)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timed out waiting for MARKET_DATA_UNAVAILABLE error frame")
	}
}
