package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"tradedrift/services/matching-engine/internal/market"
	"tradedrift/services/matching-engine/internal/recovery"
)

func newHTTPServer(addr string, manager *market.MarketManager, isReady *atomic.Bool, isDegraded *atomic.Bool, reconService ...*recovery.ReconciliationService) *http.Server {
	var recon *recovery.ReconciliationService
	if len(reconService) > 0 {
		recon = reconService[0]
	}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "alive"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// 1. Market-specific readiness query: /readyz?market={marketID}
		marketID := strings.TrimSpace(r.URL.Query().Get("market"))
		if marketID != "" {
			eng := manager.Get(marketID)
			if eng == nil {
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status":    "not_found",
					"market_id": marketID,
				})
				return
			}

			if !isReady.Load() || eng.Mode() == market.ModeRecovery {
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status":    "not_ready",
					"market_id": marketID,
					"mode":      "RECOVERY",
				})
				return
			}

			if eng.Mode() == market.ModeReconciling {
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status":    "not_ready",
					"market_id": marketID,
					"mode":      "RECONCILING",
					"reason":    "market undergoing downstream order reconciliation",
				})
				return
			}

			if eng.Mode() == market.ModeBlocked {
				reason := "market blocked due to unrecoverable state"
				if eng.BlockedReason() != nil {
					reason = eng.BlockedReason().Error()
				}
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status":    "not_ready",
					"market_id": marketID,
					"mode":      "BLOCKED",
					"reason":    reason,
				})
				return
			}

			if eng.Mode() == market.ModeLive {
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status":    "ready",
					"market_id": marketID,
					"mode":      "LIVE",
				})
				return
			}

			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":    "not_ready",
				"market_id": marketID,
			})
			return
		}

		// 2. Global readiness query: /readyz
		if !isReady.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "recovering",
			})
			return
		}

		var liveCount, blockedCount, recoveryCount, reconcilingCount int
		marketModes := make(map[string]string, len(manager.All()))
		var blockedMarkets []string
		var reconcilingMarkets []string
		var liveMarkets []string

		for _, eng := range manager.All() {
			switch eng.Mode() {
			case market.ModeLive:
				liveCount++
				marketModes[eng.MarketID] = "LIVE"
				liveMarkets = append(liveMarkets, eng.MarketID)
			case market.ModeBlocked:
				blockedCount++
				marketModes[eng.MarketID] = "BLOCKED"
				blockedMarkets = append(blockedMarkets, eng.MarketID)
			case market.ModeReconciling:
				reconcilingCount++
				marketModes[eng.MarketID] = "RECONCILING"
				reconcilingMarkets = append(reconcilingMarkets, eng.MarketID)
			case market.ModeRecovery:
				recoveryCount++
				marketModes[eng.MarketID] = "RECOVERY"
			}
		}

		if recoveryCount > 0 || liveCount == 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":  "not_ready",
				"markets": marketModes,
			})
			return
		}

		if blockedCount > 0 || reconcilingCount > 0 {
			// DEGRADED state: some markets are blocked or reconciling. Global readiness must fail closed
			// so orchestrators and downstream callers do not route new traffic to an unhealthy engine.
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":              "degraded",
				"markets":             marketModes,
				"blocked_markets":     blockedMarkets,
				"reconciling_markets": reconcilingMarkets,
				"live_markets":        liveMarkets,
			})
			return
		}

		if isDegraded != nil && isDegraded.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "degraded",
				"reason": "initial depth seed failed",
			})
			return
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ready",
			"markets": marketModes,
		})
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
	mux.HandleFunc("/admin/markets/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/admin/markets/")
		parts := strings.Split(path, "/")
		if len(parts) == 2 && parts[1] == "approve-reconciliation" && r.Method == http.MethodPost {
			marketID := parts[0]
			if recon == nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotImplemented)
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "error", "reason": "reconciliation service not configured"})
				return
			}
			var req recovery.ReconciliationApprovalRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "error", "reason": fmt.Sprintf("invalid json body: %v", err)})
				return
			}
			req.MarketID = marketID
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				req.AuthToken = strings.TrimPrefix(authHeader, "Bearer ")
			}

			result, err := recon.ApproveReconciliation(r.Context(), req)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				if strings.Contains(strings.ToLower(err.Error()), "unauthorized") {
					w.WriteHeader(http.StatusUnauthorized)
				} else {
					w.WriteHeader(http.StatusUnprocessableEntity)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status": "error",
					"reason": err.Error(),
				})
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(result)
			return
		}
		http.NotFound(w, r)
	})

	return &http.Server{
		Addr:    addr,
		Handler: mux,
	}
}
