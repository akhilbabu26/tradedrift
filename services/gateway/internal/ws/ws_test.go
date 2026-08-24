package ws_test

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"tradedrift/services/gateway/internal/ws"
	"tradedrift/services/gateway/internal/ws/hub"
	"tradedrift/services/gateway/internal/ws/protocol"
	"tradedrift/services/gateway/internal/ws/streamer"
)

type mockProvider struct{}

func (m *mockProvider) GetImmediateOrderBook(marketID string) (*protocol.OrderBookDepthPayload, error) {
	return &protocol.OrderBookDepthPayload{
		MarketID: marketID,
		Bids:     [][2]string{{"64000.00", "1.0"}},
		Asks:     [][2]string{{"64100.00", "1.5"}},
	}, nil
}

func (m *mockProvider) GetImmediateTicker(marketID string) (*protocol.TickerPayload, error) {
	return &protocol.TickerPayload{MarketID: marketID, LastPrice: "64050.00"}, nil
}

func TestFacade_Wiring(t *testing.T) {
	logger := zap.NewNop()
	provider := &mockProvider{}

	streamerInstance := ws.NewStreamer(nil, nil, []string{"localhost:9092"}, "trades.executed", "test-grp", logger)
	hubInstance := ws.NewHub(logger, provider)
	streamerInstance.SetHub(hubInstance)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	streamerInstance.Start(ctx)

	handlerInstance := ws.NewHandler(hubInstance, "supersecretjwtkey123456789012345678", "http://localhost:5173", logger)
	if handlerInstance == nil {
		t.Fatal("expected handler to be instantiated")
	}

	st, tgt, ok := ws.ValidateStream("market:orderbook:BTC-USDT")
	if !ok || st != ws.StreamTypeOrderBook || tgt != "BTC-USDT" {
		t.Fatalf("ValidateStream failed through facade: got (%q, %q, %v)", st, tgt, ok)
	}
}

func TestFacade_TypeAliases(t *testing.T) {
	var _ *ws.Hub = &hub.Hub{}
	var _ *ws.Client = &hub.Client{}
	var _ *ws.Handler = &hub.Handler{}
	var _ *ws.Streamer = &streamer.Streamer{}
	var _ ws.SnapshotProvider = &mockProvider{}
}
