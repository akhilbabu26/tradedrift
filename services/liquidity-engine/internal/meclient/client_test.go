package meclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"
)

func TestClient_CheckAllMarkets(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(StatusResponse{
				Ready:   true,
				Markets: []string{"BTC-USDT", "ETH-USDT"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := New(ts.URL, zap.NewNop())

	all, err := client.CheckAllMarkets(context.Background())
	if err != nil {
		t.Fatalf("CheckAllMarkets failed: %v", err)
	}

	if !all["BTC-USDT"] {
		t.Error("expected BTC-USDT to be true")
	}
	if !all["ETH-USDT"] {
		t.Error("expected ETH-USDT to be true")
	}
	if all["SOL-USDT"] {
		t.Error("expected SOL-USDT to be false/absent")
	}

	// Test CheckMarketHealth wrapper
	healthy, err := client.CheckMarketHealth(context.Background(), "BTC-USDT")
	if err != nil || !healthy {
		t.Errorf("expected BTC-USDT healthy, got %v, err=%v", healthy, err)
	}

	healthy, err = client.CheckMarketHealth(context.Background(), "SOL-USDT")
	if healthy || err == nil {
		t.Errorf("expected SOL-USDT unhealthy/err, got %v, err=%v", healthy, err)
	}
}

func TestClient_FetchSnapshot(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/markets/SOL-USDT/snapshot":
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(MarketSnapshot{
				MarketID:   "SOL-USDT",
				State:      "LIVE",
				Sequence:   24,
				Timestamp:  "2026-09-25T07:27:00Z",
				OrderCount: 1,
				Orders: []MMOrderSummary{
					{
						OrderID:       "ord-123",
						ClientOrderID: "MM-SOL-USDT-BID-01-G001",
						LevelID:       "MM-SOL-USDT-BID-01",
						Generation:    1,
						Side:          "BUY",
						Price:         "145.20",
						Quantity:      "50.0",
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := New(ts.URL, zap.NewNop())
	snap, err := client.FetchSnapshot(context.Background(), "SOL-USDT")
	if err != nil {
		t.Fatalf("FetchSnapshot failed: %v", err)
	}

	if snap.MarketID != "SOL-USDT" || snap.State != "LIVE" || snap.Sequence != 24 {
		t.Errorf("unexpected snapshot: %+v", snap)
	}
	if len(snap.Orders) != 1 || snap.Orders[0].ClientOrderID != "MM-SOL-USDT-BID-01-G001" {
		t.Errorf("unexpected orders: %+v", snap.Orders)
	}
}

