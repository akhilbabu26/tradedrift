// Package meclient provides an HTTP client for directly probing Matching Engine health and readiness.
package meclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Client probes the Matching Engine HTTP health and snapshot endpoints.
type Client struct {
	baseURL         string
	httpClient      *http.Client
	logger          *zap.Logger
	seqMu           sync.Mutex
	lastObservedSeq map[string]uint64
}

// MMOrderSummary summarizes a single resting MM order in the Matching Engine book.
type MMOrderSummary struct {
	OrderID           string `json:"order_id"`
	ClientOrderID     string `json:"client_order_id"`
	LevelID           string `json:"level_id"`
	Generation        int    `json:"generation"`
	Side              string `json:"side"`
	Price             string `json:"price"`
	RemainingQuantity string `json:"remaining_quantity"`
	Quantity          string `json:"quantity,omitempty"`
}

// MarketSnapshot represents an atomic point-in-time view of an ME order book.
type MarketSnapshot struct {
	MarketID   string           `json:"market_id"`
	State      string           `json:"state"`
	Sequence   uint64           `json:"sequence"`
	Timestamp  string           `json:"timestamp"`
	OrderCount int              `json:"order_count"`
	Orders     []MMOrderSummary `json:"orders"`
}

// StatusResponse is the payload returned by ME /status endpoint.
type StatusResponse struct {
	Ready   bool     `json:"ready"`
	Markets []string `json:"markets"`
}

// New creates a new Matching Engine health client.
func New(baseURL string, logger *zap.Logger) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		baseURL = "http://localhost:8082"
	}
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 2 * time.Second,
		},
		logger:          logger,
		lastObservedSeq: make(map[string]uint64),
	}
}

// CheckAllMarkets queries the ME /status endpoint once and returns a map indicating
// the readiness of each registered market.
func (c *Client) CheckAllMarkets(ctx context.Context) (map[string]bool, error) {
	url := fmt.Sprintf("%s/status", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create ME health request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("probe ME health (%s): %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ME health returned HTTP %d", resp.StatusCode)
	}

	var status StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decode ME status JSON: %w", err)
	}

	result := make(map[string]bool)
	if !status.Ready {
		return result, nil
	}

	for _, m := range status.Markets {
		result[m] = true
	}

	return result, nil
}

// CheckMarketHealth probes the Matching Engine and returns true if the engine is ready
// and the specified market is live.
func (c *Client) CheckMarketHealth(ctx context.Context, marketID string) (bool, error) {
	all, err := c.CheckAllMarkets(ctx)
	if err != nil {
		return false, err
	}
	if all[marketID] {
		return true, nil
	}
	return false, fmt.Errorf("market %s not registered in ME status", marketID)
}

// FetchSnapshot queries the ME /markets/{market_id}/snapshot endpoint and decodes
// the authoritative resting state of MM orders in the live order book.
// Monotonic sequence verification: if the sequence regresses while ME claims LIVE,
// a warning is logged to alert on potential restarts or out-of-order responses.
func (c *Client) FetchSnapshot(ctx context.Context, marketID string) (*MarketSnapshot, error) {
	url := fmt.Sprintf("%s/markets/%s/snapshot", c.baseURL, marketID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create ME snapshot request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("probe ME snapshot (%s): %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ME snapshot returned HTTP %d", resp.StatusCode)
	}

	var snap MarketSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return nil, fmt.Errorf("decode ME snapshot JSON: %w", err)
	}

	c.seqMu.Lock()
	lastSeq := c.lastObservedSeq[marketID]
	if snap.Sequence < lastSeq && snap.State == "LIVE" && lastSeq > 0 {
		c.logger.Warn("ME snapshot sequence regressed",
			zap.String("market_id", marketID),
			zap.Uint64("prev_seq", lastSeq),
			zap.Uint64("new_seq", snap.Sequence))
	}
	c.lastObservedSeq[marketID] = snap.Sequence
	c.seqMu.Unlock()

	return &snap, nil
}
