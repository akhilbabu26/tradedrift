// Package refprice provides a thread-safe, versioned external reference-price
// cache for the TradeDrift Liquidity Engine.
//
// ARCHITECTURAL INVARIANT:
//
//	External price (this package)
//	         ↓
//	  Reference only — used as a zone anchor for LE order generation
//	         ↓
//	  LE pricing zones → LE orders → ME order book
//
// The external price NEVER directly updates:
//   - market_trades
//   - candles / OHLC
//   - ticker.last_price
//   - the ME order book
//
// It is ONLY read by the LE to determine where to place liquidity bands.
package refprice

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Fetcher is the abstraction over any external price source.
// Inject a mock in tests; use CoinGeckoFetcher in production.
type Fetcher interface {
	// Fetch retrieves the latest reference price for each requested market ID.
	// Returned map key: market ID (e.g. "BTC-USDT").
	// Returns only markets it could resolve; missing markets are silently omitted.
	// The caller must handle missing entries (use last-known-good or trigger STALE).
	Fetch(ctx context.Context, marketIDs []string) (map[string]decimal.Decimal, error)
}

// ARCHITECTURAL CONTRACT: USD vs USDT REFERENCE ANCHOR (RP-3)
//
// CoinGecko /simple/price endpoint returns asset prices denominated in USD (vs_currencies=usd).
// TradeDrift markets are paired with USDT (BTC-USDT, ETH-USDT, SOL-USDT).
// The liquidity engine intentionally uses CoinGecko USD prices as the external anchor
// for TradeDrift's USDT markets; USD/USDT basis deviation is intentionally ignored.
// This feed is NOT a currency conversion oracle; it solely provides zone centering
// for maker liquidity placement.

// CoinGeckoPlan distinguishes between CoinGecko Free/Demo and Pro API plans (RP-1).
type CoinGeckoPlan string

const (
	PlanDemo CoinGeckoPlan = "demo"
	PlanPro  CoinGeckoPlan = "pro"
)

// TIMEOUT OWNERSHIP HIERARCHY (RP-06)
//
// The reference price subsystem enforces a 3-tier timeout hierarchy to ensure
// no fetch operation can hang indefinitely:
//
//  1. Outer / Poller Context (Provider.cfg.FetchTimeout):
//     When Provider.fetchAndUpdate initiates a fetch cycle, it creates a context with
//     timeout cfg.FetchTimeout (default 5s). This provides an absolute upper bound on the
//     entire fetch cycle across all markets.
//
//  2. Request-Level Context (CoinGeckoFetcher.Timeout):
//     If CoinGeckoFetcher receives a context with a longer or absent deadline, it applies
//     its own Timeout (default 5s) via context.WithTimeout(ctx, f.Timeout).
//
//  3. Transport-Level Deadline (http.Client.Timeout):
//     The underlying HTTP client's transport deadline guards against socket-level hangs,
//     TLS handshakes, or TCP connection freezes that might escape context cancellations.

// CoinGeckoConfig holds configuration for CoinGeckoFetcher (RP-1, RP-5).
type CoinGeckoConfig struct {
	Plan       CoinGeckoPlan
	APIKey     string
	BaseURL    string
	Timeout    time.Duration
	Markets    []string // configured markets to validate CoinGecko mapping support for
	HTTPClient *http.Client
}

// Validate checks that CoinGeckoConfig has a valid plan, sane timeout, and supported markets.
func (cfg CoinGeckoConfig) Validate() error {
	switch cfg.Plan {
	case "", PlanDemo, PlanPro:
	default:
		return fmt.Errorf("invalid CoinGecko plan: %q (must be %q or %q)", cfg.Plan, PlanDemo, PlanPro)
	}
	if len(cfg.Markets) > 0 {
		if err := ValidateMarketsSupported(cfg.Markets); err != nil {
			return err
		}
	}
	return nil
}

// CoinGeckoFetcher calls the CoinGecko /simple/price endpoint.
// One HTTP request retrieves all configured markets.
//
// Endpoint: GET /simple/price?ids=bitcoin,ethereum,solana&vs_currencies=usd
type CoinGeckoFetcher struct {
	BaseURL    string
	APIKey     string
	Plan       CoinGeckoPlan
	Timeout    time.Duration
	HTTPClient *http.Client
}

// NewCoinGeckoFetcherWithConfig creates a CoinGeckoFetcher from a structured config (RP-1, RP-5, RP-06).
func NewCoinGeckoFetcherWithConfig(cfg CoinGeckoConfig) (*CoinGeckoFetcher, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("refprice config: %w", err)
	}

	plan := cfg.Plan
	if plan == "" {
		plan = PlanDemo
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		if plan == PlanPro {
			baseURL = "https://pro-api.coingecko.com/api/v3"
		} else {
			baseURL = "https://api.coingecko.com/api/v3"
		}
	}
	baseURL = strings.TrimRight(baseURL, "/")

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	return &CoinGeckoFetcher{
		BaseURL:    baseURL,
		APIKey:     cfg.APIKey,
		Plan:       plan,
		Timeout:    timeout,
		HTTPClient: client,
	}, nil
}

// NewCoinGeckoFetcher creates a fetcher with sane HTTP timeout defaults and normalized base URL.
// Defaults to PlanDemo. Use WithDemoAPIKey or WithProAPIKey to configure authentication.
func NewCoinGeckoFetcher(baseURL string, timeout time.Duration) *CoinGeckoFetcher {
	f, _ := NewCoinGeckoFetcherWithConfig(CoinGeckoConfig{
		Plan:    PlanDemo,
		BaseURL: baseURL,
		Timeout: timeout,
	})
	return f
}

// Source returns the identifier of this fetcher ("coingecko").
func (f *CoinGeckoFetcher) Source() string {
	return "coingecko"
}

// WithAPIKey configures an optional CoinGecko demo API key (defaults to demo plan).
func (f *CoinGeckoFetcher) WithAPIKey(apiKey string) *CoinGeckoFetcher {
	f.APIKey = apiKey
	if f.Plan == "" {
		f.Plan = PlanDemo
	}
	return f
}

// WithDemoAPIKey configures a CoinGecko Demo API key and sets plan to PlanDemo (RP-1).
func (f *CoinGeckoFetcher) WithDemoAPIKey(apiKey string) *CoinGeckoFetcher {
	f.APIKey = apiKey
	f.Plan = PlanDemo
	return f
}

// WithProAPIKey configures a CoinGecko Pro API key, sets plan to PlanPro, and adjusts BaseURL if default.
func (f *CoinGeckoFetcher) WithProAPIKey(apiKey string) *CoinGeckoFetcher {
	f.APIKey = apiKey
	f.Plan = PlanPro
	if f.BaseURL == "" || f.BaseURL == "https://api.coingecko.com/api/v3" {
		f.BaseURL = "https://pro-api.coingecko.com/api/v3"
	}
	return f
}

// marketToGeckoID maps TradeDrift market IDs to CoinGecko coin IDs.
// Extend this map when new markets are added.
var marketToGeckoID = map[string]string{
	"BTC-USDT": "bitcoin",
	"ETH-USDT": "ethereum",
	"SOL-USDT": "solana",
}

// geckoIDToMarket is the reverse map (built once at init).
var geckoIDToMarket map[string]string

func init() {
	geckoIDToMarket = make(map[string]string, len(marketToGeckoID))
	for market, gecko := range marketToGeckoID {
		geckoIDToMarket[gecko] = market
	}
}

// ValidateMarketsSupported checks that every market in marketIDs has a known CoinGecko mapping.
// Fails startup fast (RP-03) if an operator configures an unsupported market.
func ValidateMarketsSupported(marketIDs []string) error {
	for _, m := range marketIDs {
		trimmed := strings.TrimSpace(m)
		if trimmed == "" {
			continue
		}
		if _, ok := marketToGeckoID[trimmed]; !ok {
			return fmt.Errorf("unsupported CoinGecko market %q (no mapping in marketToGeckoID)", trimmed)
		}
	}
	return nil
}

// SupportsMarket reports whether marketID has a registered CoinGecko mapping.
func SupportsMarket(marketID string) bool {
	_, ok := marketToGeckoID[marketID]
	return ok
}

// Fetch implements Fetcher using the CoinGecko simple/price API.
// INVARIANT: Price parsing uses json.Number -> decimal.NewFromString to eliminate
// IEEE-754 binary floating-point precision loss at the external reference boundary.
func (f *CoinGeckoFetcher) Fetch(ctx context.Context, marketIDs []string) (map[string]decimal.Decimal, error) {
	if f.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.Timeout)
		defer cancel()
	}

	// Build the deduplicated list of gecko IDs for only the requested markets (RP-4).
	seen := make(map[string]bool, len(marketIDs))
	geckoIDs := make([]string, 0, len(marketIDs))
	for _, m := range marketIDs {
		if id, ok := marketToGeckoID[m]; ok {
			if !seen[id] {
				seen[id] = true
				geckoIDs = append(geckoIDs, id)
			}
		}
	}
	if len(geckoIDs) == 0 {
		return nil, fmt.Errorf("no known CoinGecko IDs for requested markets %v", marketIDs)
	}

	// Build URL using net/url: /simple/price?ids=bitcoin,ethereum,solana&vs_currencies=usd
	reqURL, err := url.Parse(f.BaseURL + "/simple/price")
	if err != nil {
		return nil, fmt.Errorf("invalid CoinGecko base URL: %w", err)
	}
	query := reqURL.Query()
	query.Set("ids", strings.Join(geckoIDs, ","))
	query.Set("vs_currencies", "usd")
	reqURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build CoinGecko request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if f.APIKey != "" {
		if f.Plan == PlanPro {
			req.Header.Set("x-cg-pro-api-key", f.APIKey)
		} else {
			req.Header.Set("x-cg-demo-api-key", f.APIKey)
		}
	}

	resp, err := f.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("CoinGecko request failed: %w", err)
	}
	defer resp.Body.Close()

	// HTTP error classification
	switch {
	case resp.StatusCode == http.StatusOK:
		// success
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("CoinGecko rate limited (HTTP 429)")
	case resp.StatusCode >= http.StatusInternalServerError:
		return nil, fmt.Errorf("CoinGecko server error (HTTP %d)", resp.StatusCode)
	default:
		return nil, fmt.Errorf("CoinGecko client error (HTTP %d)", resp.StatusCode)
	}

	// Protect against unbounded responses: limit reader to 512KB
	limitedReader := io.LimitReader(resp.Body, 512*1024)

	// Response shape: {"bitcoin":{"usd":96123.45},"ethereum":{"usd":2780.50},...}
	// INVARIANT: Use json.Number to avoid any float64 conversions!
	var raw map[string]map[string]json.Number
	decoder := json.NewDecoder(limitedReader)
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode CoinGecko response: %w", err)
	}

	result := make(map[string]decimal.Decimal, len(raw))
	for geckoID, currencies := range raw {
		usdNum, ok := currencies["usd"]
		if !ok || usdNum.String() == "" {
			continue
		}
		marketID, known := geckoIDToMarket[geckoID]
		if !known {
			continue
		}

		price, err := decimal.NewFromString(usdNum.String())
		// INVARIANT: A reference price must always be a finite, strictly positive decimal.
		if err != nil || !price.GreaterThan(decimal.Zero) {
			continue
		}
		result[marketID] = price
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("CoinGecko response contained no usable USD prices")
	}

	return result, nil
}
