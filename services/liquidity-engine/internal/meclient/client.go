// Package meclient provides an HTTP client for directly probing Matching Engine health and readiness.
package meclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Client probes the Matching Engine HTTP health endpoints.
type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     *zap.Logger
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
		logger: logger,
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
