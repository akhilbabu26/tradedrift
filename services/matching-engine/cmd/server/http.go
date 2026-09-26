package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"tradedrift/services/matching-engine/internal/market"
)

func newHTTPServer(addr string, manager *market.MarketManager, isReady *atomic.Bool) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "alive"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !isReady.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "recovering"})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		markets := make([]string, 0, len(manager.All()))
		marketDetails := make(map[string]any, len(manager.All()))
		for _, eng := range manager.All() {
			markets = append(markets, eng.MarketID)
			snap := eng.GetSnapshot()
			if snap != nil {
				orderCount := 0
				if snap.Orders != nil {
					orderCount = len(snap.Orders)
				}
				marketDetails[eng.MarketID] = map[string]any{
					"state":          snap.State,
					"sequence":       snap.Sequence,
					"mm_order_count": orderCount,
				}
			} else {
				marketDetails[eng.MarketID] = map[string]any{
					"state":          "RECOVERING",
					"sequence":       0,
					"mm_order_count": 0,
				}
			}
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ready":          isReady.Load(),
			"markets":        markets,
			"market_details": marketDetails,
		})
	})
	mux.HandleFunc("/markets/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/markets/")
		parts := strings.Split(path, "/")
		if len(parts) == 2 && parts[1] == "snapshot" && r.Method == http.MethodGet {
			marketID := parts[0]
			eng := manager.Get(marketID)
			if eng == nil {
				http.Error(w, fmt.Sprintf("market %q not found", marketID), http.StatusNotFound)
				return
			}
			snap := eng.GetSnapshot()
			if snap == nil {
				snap = &market.MarketSnapshot{
					MarketID:   marketID,
					State:      "RECOVERING",
					Sequence:   0,
					SnapshotAt: time.Now().UTC(),
					Orders:     nil,
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(snap)
			return
		}
		http.NotFound(w, r)
	})

	return &http.Server{
		Addr:    addr,
		Handler: mux,
	}
}
