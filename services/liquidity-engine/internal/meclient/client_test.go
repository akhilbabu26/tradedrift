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
