package refprice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"
)

// ── Mock Fetcher ──────────────────────────────────────────────────────────────

// mockFetcher is a test double for Fetcher.
type mockFetcher struct {
	mu      sync.Mutex
	results map[string]decimal.Decimal
	err     error
	calls   int
}

func (m *mockFetcher) setResults(r map[string]decimal.Decimal) {
	m.mu.Lock()
	m.results = r
	m.err = nil
	m.mu.Unlock()
}

func (m *mockFetcher) setError(err error) {
	m.mu.Lock()
	m.err = err
	m.results = nil
	m.mu.Unlock()
}

func (m *mockFetcher) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

func (m *mockFetcher) Fetch(_ context.Context, _ []string) (map[string]decimal.Decimal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	// Return a copy to prevent test races
	out := make(map[string]decimal.Decimal, len(m.results))
	for k, v := range m.results {
		out[k] = v
	}
	return out, nil
}

// ── Helper ────────────────────────────────────────────────────────────────────

func testProvider(t *testing.T, cfg Config, fetcher Fetcher, markets []string) *Provider {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	p, err := NewProvider(cfg, fetcher, markets, logger)
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}
	return p
}

func fastConfig() Config {
	return Config{
		RefreshInterval: 10 * time.Millisecond,
		StaleThreshold:  50 * time.Millisecond,
		PauseThreshold:  100 * time.Millisecond,
		FetchTimeout:    5 * time.Second,
	}
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestProvider_Seed_ReturnsPrice(t *testing.T) {
	f := &mockFetcher{}
	p := testProvider(t, fastConfig(), f, []string{"BTC-USDT"})

	btcPrice := decimal.NewFromFloat(96450.00)
	p.Seed("BTC-USDT", btcPrice)

	e, ok := p.Get("BTC-USDT")
	if !ok {
		t.Fatal("Get returned false after Seed")
	}
	if !e.Price.Equal(btcPrice) {
		t.Errorf("expected price %s, got %s", btcPrice, e.Price)
	}
	if e.State != StateFresh {
		t.Errorf("expected StateFresh immediately after Seed, got %s", e.State)
	}
	if e.Source != "seed" {
		t.Errorf("expected source='seed', got %s", e.Source)
	}
	if e.Version <= 0 {
		t.Errorf("expected Version > 0, got %d", e.Version)
	}
}

func TestProvider_Seed_IgnoresZeroPrice(t *testing.T) {
	f := &mockFetcher{}
	p := testProvider(t, fastConfig(), f, []string{"BTC-USDT"})

	p.Seed("BTC-USDT", decimal.Zero)

	_, ok := p.Get("BTC-USDT")
	if ok {
		t.Fatal("Get should return false after a zero-price Seed")
	}
}

func TestProvider_Get_ReturnsFalseBeforeSeedOrFetch(t *testing.T) {
	f := &mockFetcher{}
	p := testProvider(t, fastConfig(), f, []string{"BTC-USDT"})

	_, ok := p.Get("BTC-USDT")
	if ok {
		t.Fatal("expected false before any seed or fetch")
	}
}

func TestProvider_StateMachine_FreshToStaleToPaused(t *testing.T) {
	cfg := Config{
		RefreshInterval: 1 * time.Hour, // disable background poller during this test
		StaleThreshold:  40 * time.Millisecond,
		PauseThreshold:  80 * time.Millisecond,
		FetchTimeout:    5 * time.Second,
	}
	f := &mockFetcher{}
	p := testProvider(t, cfg, f, []string{"BTC-USDT"})

	p.Seed("BTC-USDT", decimal.NewFromFloat(96450))

	// Immediately after seed → FRESH
	e, _ := p.Get("BTC-USDT")
	if e.State != StateFresh {
		t.Errorf("expected FRESH, got %s", e.State)
	}

	// After StaleThreshold → STALE
	time.Sleep(50 * time.Millisecond)
	e, _ = p.Get("BTC-USDT")
	if e.State != StateStale {
		t.Errorf("expected STALE, got %s (age=%s)", e.State, time.Since(e.FetchedAt))
	}

	// After PauseThreshold → PAUSED
	time.Sleep(50 * time.Millisecond)
	e, _ = p.Get("BTC-USDT")
	if e.State != StatePaused {
		t.Errorf("expected PAUSED, got %s", e.State)
	}
}

func TestProvider_StateMachine_RecoversOnFetch(t *testing.T) {
	cfg := Config{
		RefreshInterval: 1 * time.Hour,
		StaleThreshold:  40 * time.Millisecond,
		PauseThreshold:  80 * time.Millisecond,
		FetchTimeout:    5 * time.Second,
	}
	f := &mockFetcher{}
	f.setResults(map[string]decimal.Decimal{
		"BTC-USDT": decimal.NewFromFloat(97000),
	})
	p := testProvider(t, cfg, f, []string{"BTC-USDT"})

	// Seed a stale value then let it age past StaleThreshold.
	p.Seed("BTC-USDT", decimal.NewFromFloat(96450))
	time.Sleep(50 * time.Millisecond) // now STALE

	// Manually trigger a fetch (simulating what Run() does on its ticker).
	p.fetchAndUpdate(context.Background())

	// State should be FRESH again with new price.
	e, ok := p.Get("BTC-USDT")
	if !ok {
		t.Fatal("Get returned false after fetch")
	}
	if e.State != StateFresh {
		t.Errorf("expected FRESH after successful fetch, got %s", e.State)
	}
	if !e.Price.Equal(decimal.NewFromFloat(97000)) {
		t.Errorf("expected price 97000 after fetch, got %s", e.Price)
	}
	if e.Source != "coingecko" {
		t.Errorf("expected source='coingecko', got %s", e.Source)
	}
}

func TestProvider_FetchFailure_RetainsLastKnownGood(t *testing.T) {
	cfg := Config{
		RefreshInterval: 1 * time.Hour,
		StaleThreshold:  50 * time.Millisecond,
		PauseThreshold:  100 * time.Millisecond,
		FetchTimeout:    5 * time.Second,
	}
	f := &mockFetcher{}
	p := testProvider(t, cfg, f, []string{"BTC-USDT"})

	original := decimal.NewFromFloat(96450)
	p.Seed("BTC-USDT", original)

	// Now make the fetcher fail.
	f.setError(fmt.Errorf("simulated API outage"))
	p.fetchAndUpdate(context.Background())

	// Price must still be the original seed.
	e, ok := p.Get("BTC-USDT")
	if !ok {
		t.Fatal("Get returned false after failed fetch")
	}
	if !e.Price.Equal(original) {
		t.Errorf("expected retained price %s, got %s", original, e.Price)
	}
}

func TestProvider_Version_MonotonicallyIncreasing(t *testing.T) {
	cfg := Config{
		RefreshInterval: 1 * time.Hour,
		StaleThreshold:  5 * time.Minute,
		PauseThreshold:  10 * time.Minute,
		FetchTimeout:    5 * time.Second,
	}
	f := &mockFetcher{}
	f.setResults(map[string]decimal.Decimal{
		"BTC-USDT": decimal.NewFromFloat(96000),
		"ETH-USDT": decimal.NewFromFloat(2780),
	})
	p := testProvider(t, cfg, f, []string{"BTC-USDT", "ETH-USDT"})

	p.Seed("BTC-USDT", decimal.NewFromFloat(96450))
	p.Seed("ETH-USDT", decimal.NewFromFloat(2780))

	e1btc, _ := p.Get("BTC-USDT")
	e1eth, _ := p.Get("ETH-USDT")

	p.fetchAndUpdate(context.Background())

	e2btc, _ := p.Get("BTC-USDT")
	e2eth, _ := p.Get("ETH-USDT")

	if e2btc.Version <= e1btc.Version {
		t.Errorf("BTC Version should increase: was %d, now %d", e1btc.Version, e2btc.Version)
	}
	if e2eth.Version <= e1eth.Version {
		t.Errorf("ETH Version should increase: was %d, now %d", e1eth.Version, e2eth.Version)
	}
}

func TestProvider_GetAll_ReturnsAllMarkets(t *testing.T) {
	cfg := DefaultConfig()
	f := &mockFetcher{}
	markets := []string{"BTC-USDT", "ETH-USDT", "SOL-USDT"}
	p := testProvider(t, cfg, f, markets)

	p.Seed("BTC-USDT", decimal.NewFromFloat(96450))
	p.Seed("ETH-USDT", decimal.NewFromFloat(2780))
	p.Seed("SOL-USDT", decimal.NewFromFloat(188))

	all := p.GetAll()
	if len(all) != 3 {
		t.Errorf("expected 3 entries from GetAll, got %d", len(all))
	}
}

func TestProvider_Run_FetchesOnStartup(t *testing.T) {
	cfg := Config{
		RefreshInterval: 1 * time.Hour, // effectively disabled after first immediate fetch
		StaleThreshold:  5 * time.Minute,
		PauseThreshold:  10 * time.Minute,
		FetchTimeout:    5 * time.Second,
	}
	f := &mockFetcher{}
	f.setResults(map[string]decimal.Decimal{
		"BTC-USDT": decimal.NewFromFloat(97000),
	})
	p := testProvider(t, cfg, f, []string{"BTC-USDT"})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go p.Run(ctx)
	<-ctx.Done()

	e, ok := p.Get("BTC-USDT")
	if !ok {
		t.Fatal("expected a price after Run completed startup fetch")
	}
	if e.Source != "coingecko" {
		t.Errorf("expected source='coingecko' after Run fetch, got %s", e.Source)
	}
	if f.callCount() < 1 {
		t.Errorf("expected at least 1 Fetcher call, got %d", f.callCount())
	}
}

func TestCoinGeckoFetcher_JSONNumberNoFloatLoss(t *testing.T) {
	// JSON with 16 digits of precision that float64 representation would alter
	precisePriceStr := "96123.456789012345"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"bitcoin":{"usd":%s}}`, precisePriceStr)
	}))
	defer server.Close()

	fetcher := NewCoinGeckoFetcher(server.URL, 2*time.Second)
	prices, err := fetcher.Fetch(context.Background(), []string{"BTC-USDT"})
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}

	btcPrice, ok := prices["BTC-USDT"]
	if !ok {
		t.Fatal("expected BTC-USDT in fetched prices")
	}

	if btcPrice.String() != precisePriceStr {
		t.Fatalf("expected exact decimal %s, got %s (float64 loss detected)", precisePriceStr, btcPrice.String())
	}
}

func TestProviderConfig_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: Config{
				RefreshInterval: 30 * time.Second,
				FetchTimeout:    5 * time.Second,
				StaleThreshold:  5 * time.Minute,
				PauseThreshold:  10 * time.Minute,
			},
			wantErr: false,
		},
		{
			name: "zero refresh interval",
			cfg: Config{
				RefreshInterval: 0,
				FetchTimeout:    5 * time.Second,
				StaleThreshold:  5 * time.Minute,
				PauseThreshold:  10 * time.Minute,
			},
			wantErr: true,
		},
		{
			name: "zero fetch timeout",
			cfg: Config{
				RefreshInterval: 30 * time.Second,
				FetchTimeout:    0,
				StaleThreshold:  5 * time.Minute,
				PauseThreshold:  10 * time.Minute,
			},
			wantErr: true,
		},
		{
			name: "zero stale threshold",
			cfg: Config{
				RefreshInterval: 30 * time.Second,
				FetchTimeout:    5 * time.Second,
				StaleThreshold:  0,
				PauseThreshold:  10 * time.Minute,
			},
			wantErr: true,
		},
		{
			name: "pause threshold equal to stale threshold",
			cfg: Config{
				RefreshInterval: 30 * time.Second,
				FetchTimeout:    5 * time.Second,
				StaleThreshold:  5 * time.Minute,
				PauseThreshold:  5 * time.Minute,
			},
			wantErr: true,
		},
		{
			name: "pause threshold less than stale threshold",
			cfg: Config{
				RefreshInterval: 30 * time.Second,
				FetchTimeout:    5 * time.Second,
				StaleThreshold:  10 * time.Minute,
				PauseThreshold:  5 * time.Minute,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestProviderSeed_CannotOverwriteLiveReference(t *testing.T) {
	f := &mockFetcher{}
	p := testProvider(t, fastConfig(), f, []string{"BTC-USDT"})

	// Live fetch sets initial price
	livePrice := decimal.RequireFromString("98000.00")
	f.setResults(map[string]decimal.Decimal{
		"BTC-USDT": livePrice,
	})
	p.fetchAndUpdate(context.Background())

	e1, ok := p.Get("BTC-USDT")
	if !ok || !e1.Price.Equal(livePrice) || e1.Source != "coingecko" {
		t.Fatalf("expected live price %s with source coingecko, got %+v", livePrice, e1)
	}

	// Attempting to Seed over a live price MUST be a no-op
	seedPrice := decimal.RequireFromString("95000.00")
	seeded := p.SeedIfAbsent("BTC-USDT", seedPrice)
	if seeded {
		t.Fatal("expected SeedIfAbsent to return false when live reference exists")
	}

	// Also verify Seed() wrapper preserves live entry
	p.Seed("BTC-USDT", seedPrice)

	e2, ok := p.Get("BTC-USDT")
	if !ok {
		t.Fatal("Get returned false")
	}
	if !e2.Price.Equal(livePrice) {
		t.Fatalf("live price was overwritten by seed: expected %s, got %s", livePrice, e2.Price)
	}
	if e2.Source != "coingecko" {
		t.Fatalf("live source was overwritten: expected coingecko, got %s", e2.Source)
	}
	if e2.Version != e1.Version {
		t.Fatalf("version changed on rejected seed: expected %d, got %d", e1.Version, e2.Version)
	}
}

func TestReferencePrice_NonPositivePriceRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Returns zero for bitcoin, negative for ethereum, valid for solana
		fmt.Fprintf(w, `{"bitcoin":{"usd":0},"ethereum":{"usd":-100.50},"solana":{"usd":195.50}}`)
	}))
	defer server.Close()

	fetcher := NewCoinGeckoFetcher(server.URL, 2*time.Second)
	prices, err := fetcher.Fetch(context.Background(), []string{"BTC-USDT", "ETH-USDT", "SOL-USDT"})
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}

	if _, ok := prices["BTC-USDT"]; ok {
		t.Fatal("expected zero price to be rejected from result map")
	}
	if _, ok := prices["ETH-USDT"]; ok {
		t.Fatal("expected negative price to be rejected from result map")
	}
	solPrice, ok := prices["SOL-USDT"]
	if !ok || !solPrice.Equal(decimal.RequireFromString("195.50")) {
		t.Fatalf("expected valid price for SOL-USDT, got %v", prices)
	}
}

func TestProvider_PerMarketMonotonicVersions(t *testing.T) {
	f := &mockFetcher{}
	p := testProvider(t, fastConfig(), f, []string{"BTC-USDT", "ETH-USDT"})

	// Seed BTC
	p.Seed("BTC-USDT", decimal.RequireFromString("96000"))
	btc1, _ := p.Get("BTC-USDT")
	if btc1.Version != 1 {
		t.Fatalf("expected initial BTC version=1, got %d", btc1.Version)
	}

	// Seed ETH
	p.Seed("ETH-USDT", decimal.RequireFromString("2800"))
	eth1, _ := p.Get("ETH-USDT")
	if eth1.Version != 1 {
		t.Fatalf("expected initial ETH version=1 (market-scoped), got %d", eth1.Version)
	}

	// Live fetch updates only BTC
	f.setResults(map[string]decimal.Decimal{
		"BTC-USDT": decimal.RequireFromString("97000"),
	})
	p.fetchAndUpdate(context.Background())

	btc2, _ := p.Get("BTC-USDT")
	eth2, _ := p.Get("ETH-USDT")

	if btc2.Version != 2 {
		t.Fatalf("expected BTC version=2 after update, got %d", btc2.Version)
	}
	if eth2.Version != 1 {
		t.Fatalf("expected ETH version=1 unchanged when not updated, got %d", eth2.Version)
	}
}

func TestCoinGeckoFetcher_HTTPStatusHandling(t *testing.T) {
	// Test 429
	server429 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server429.Close()

	fetcher429 := NewCoinGeckoFetcher(server429.URL, 2*time.Second)
	_, err429 := fetcher429.Fetch(context.Background(), []string{"BTC-USDT"})
	if err429 == nil || !strings.Contains(err429.Error(), "429") {
		t.Fatalf("expected 429 rate limit error, got: %v", err429)
	}

	// Test 500
	server500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server500.Close()

	fetcher500 := NewCoinGeckoFetcher(server500.URL, 2*time.Second)
	_, err500 := fetcher500.Fetch(context.Background(), []string{"BTC-USDT"})
	if err500 == nil || !strings.Contains(err500.Error(), "server error") {
		t.Fatalf("expected 500 server error, got: %v", err500)
	}
}

func TestCoinGeckoFetcher_PlanDemoAndProHeaders(t *testing.T) {
	var receivedDemoHeader, receivedProHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedDemoHeader = r.Header.Get("x-cg-demo-api-key")
		receivedProHeader = r.Header.Get("x-cg-pro-api-key")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"bitcoin":{"usd":"95000"}}`))
	}))
	defer server.Close()

	// 1. Test Demo plan
	demoFetcher, err := NewCoinGeckoFetcherWithConfig(CoinGeckoConfig{
		Plan:    PlanDemo,
		APIKey:  "demo-secret",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("NewCoinGeckoFetcherWithConfig demo failed: %v", err)
	}
	_, err = demoFetcher.Fetch(context.Background(), []string{"BTC-USDT"})
	if err != nil {
		t.Fatalf("demo fetch failed: %v", err)
	}
	if receivedDemoHeader != "demo-secret" {
		t.Fatalf("expected x-cg-demo-api-key 'demo-secret', got %q", receivedDemoHeader)
	}
	if receivedProHeader != "" {
		t.Fatalf("expected no x-cg-pro-api-key header on demo, got %q", receivedProHeader)
	}

	// 2. Test Pro plan
	receivedDemoHeader = ""
	receivedProHeader = ""
	proFetcher, err := NewCoinGeckoFetcherWithConfig(CoinGeckoConfig{
		Plan:    PlanPro,
		APIKey:  "pro-secret",
		BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("NewCoinGeckoFetcherWithConfig pro failed: %v", err)
	}
	_, err = proFetcher.Fetch(context.Background(), []string{"BTC-USDT"})
	if err != nil {
		t.Fatalf("pro fetch failed: %v", err)
	}
	if receivedProHeader != "pro-secret" {
		t.Fatalf("expected x-cg-pro-api-key 'pro-secret', got %q", receivedProHeader)
	}
	if receivedDemoHeader != "" {
		t.Fatalf("expected no x-cg-demo-api-key header on pro, got %q", receivedDemoHeader)
	}
}

func TestProvider_DeduplicateMarketIDs(t *testing.T) {
	cfg := fastConfig()
	fetcher := &mockFetcher{
		results: map[string]decimal.Decimal{
			"BTC-USDT": decimal.RequireFromString("95000"),
			"ETH-USDT": decimal.RequireFromString("3400"),
		},
	}
	logger, _ := zap.NewDevelopment()

	// Pass duplicate markets
	p, err := NewProvider(cfg, fetcher, []string{"BTC-USDT", "BTC-USDT", "ETH-USDT", "  "}, logger)
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}

	if len(p.marketIDs) != 2 {
		t.Fatalf("expected 2 deduplicated markets, got %d: %v", len(p.marketIDs), p.marketIDs)
	}
}

func TestProvider_OutageTransitionAndRecovery(t *testing.T) {
	cfg := Config{
		RefreshInterval: 5 * time.Millisecond,
		StaleThreshold:  30 * time.Millisecond,
		PauseThreshold:  70 * time.Millisecond,
		FetchTimeout:    1 * time.Second,
	}
	mock := &mockFetcher{
		results: map[string]decimal.Decimal{
			"BTC-USDT": decimal.RequireFromString("95000"),
		},
	}
	p := testProvider(t, cfg, mock, []string{"BTC-USDT"})

	// 1. Initial successful fetch -> FRESH
	p.fetchAndUpdate(context.Background())
	entry, ok := p.Get("BTC-USDT")
	if !ok || entry.State != StateFresh {
		t.Fatalf("expected FRESH, got ok=%v, state=%v", ok, entry.State)
	}

	// 2. Outage begins: fetcher fails
	mock.setError(fmt.Errorf("outage simulation"))

	// Wait past StaleThreshold (35ms)
	time.Sleep(35 * time.Millisecond)
	p.fetchAndUpdate(context.Background())

	entry, ok = p.Get("BTC-USDT")
	if !ok || entry.State != StateStale {
		t.Fatalf("expected STALE after %v, got state=%v", 35*time.Millisecond, entry.State)
	}

	// Wait past PauseThreshold (additional 45ms -> total 80ms)
	time.Sleep(45 * time.Millisecond)
	p.fetchAndUpdate(context.Background())

	entry, ok = p.Get("BTC-USDT")
	if !ok || entry.State != StatePaused {
		t.Fatalf("expected PAUSED after >70ms outage, got state=%v", entry.State)
	}

	// 3. Outage resolves: fetcher recovers
	mock.setResults(map[string]decimal.Decimal{
		"BTC-USDT": decimal.RequireFromString("95200"),
	})
	p.fetchAndUpdate(context.Background())

	entry, ok = p.Get("BTC-USDT")
	if !ok || entry.State != StateFresh {
		t.Fatalf("expected FRESH after recovery, got state=%v", entry.State)
	}
	if !entry.Price.Equal(decimal.RequireFromString("95200")) {
		t.Fatalf("expected recovered price 95200, got %s", entry.Price.String())
	}
}

func TestProvider_RejectUnconfiguredMarkets(t *testing.T) {
	cfg := fastConfig()
	mock := &mockFetcher{
		results: map[string]decimal.Decimal{
			"BTC-USDT":  decimal.RequireFromString("95000"),
			"DOGE-USDT": decimal.RequireFromString("0.38"),
		},
	}
	// Provider configured ONLY for BTC-USDT
	p := testProvider(t, cfg, mock, []string{"BTC-USDT"})

	// 1. RP-02: SeedIfAbsent rejects unconfigured market
	seeded := p.SeedIfAbsent("DOGE-USDT", decimal.RequireFromString("0.38"))
	if seeded {
		t.Fatalf("RP-02 violation: SeedIfAbsent accepted unconfigured market DOGE-USDT")
	}
	if _, ok := p.Get("DOGE-USDT"); ok {
		t.Fatalf("RP-02 violation: unconfigured seeded market found in provider entries")
	}

	// 2. RP-01: fetchAndUpdate rejects unconfigured market returned by fetcher
	p.fetchAndUpdate(context.Background())

	if _, ok := p.Get("BTC-USDT"); !ok {
		t.Fatalf("expected configured market BTC-USDT to be stored")
	}
	if _, ok := p.Get("DOGE-USDT"); ok {
		t.Fatalf("RP-01 violation: fetcher inserted unconfigured market DOGE-USDT into provider entries")
	}
}

func TestFetcher_PlanValidation(t *testing.T) {
	// Valid plans
	validDemo := CoinGeckoConfig{Plan: PlanDemo}
	if err := validDemo.Validate(); err != nil {
		t.Fatalf("expected valid demo plan, got: %v", err)
	}

	validPro := CoinGeckoConfig{Plan: PlanPro}
	if err := validPro.Validate(); err != nil {
		t.Fatalf("expected valid pro plan, got: %v", err)
	}

	validEmpty := CoinGeckoConfig{Plan: ""}
	if err := validEmpty.Validate(); err != nil {
		t.Fatalf("expected valid empty plan, got: %v", err)
	}

	// Invalid plan returns error (RP-06)
	invalid := CoinGeckoConfig{Plan: "enterprise"}
	if err := invalid.Validate(); err == nil {
		t.Fatalf("expected error for invalid plan 'enterprise', got nil")
	}
	_, err := NewCoinGeckoFetcherWithConfig(invalid)
	if err == nil {
		t.Fatalf("expected NewCoinGeckoFetcherWithConfig to return error for invalid plan, got nil")
	}

	// Market mapping validation (RP-03)
	validMarkets := CoinGeckoConfig{
		Plan:    PlanDemo,
		Markets: []string{"BTC-USDT", "ETH-USDT", "SOL-USDT"},
	}
	if err := validMarkets.Validate(); err != nil {
		t.Fatalf("expected supported markets to pass validation, got: %v", err)
	}

	unsupportedMarkets := CoinGeckoConfig{
		Plan:    PlanDemo,
		Markets: []string{"BTC-USDT", "DOGE-USDT"},
	}
	if err := unsupportedMarkets.Validate(); err == nil {
		t.Fatalf("expected error for unsupported market DOGE-USDT, got nil")
	}
	_, err = NewCoinGeckoFetcherWithConfig(unsupportedMarkets)
	if err == nil {
		t.Fatalf("expected NewCoinGeckoFetcherWithConfig to reject unsupported markets, got nil")
	}
}

func TestFetcher_TimeoutEnforcement(t *testing.T) {
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"bitcoin":{"usd":"95000"}}`))
	}))
	defer slowServer.Close()

	// 10ms timeout should fail before the 100ms response
	fetcher, err := NewCoinGeckoFetcherWithConfig(CoinGeckoConfig{
		Plan:    PlanDemo,
		BaseURL: slowServer.URL,
		Timeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	ctx := context.Background()
	_, err = fetcher.Fetch(ctx, []string{"BTC-USDT"})
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
}

// Compile-time check: mockFetcher implements Fetcher.
var _ Fetcher = (*mockFetcher)(nil)

func TestProvider_StateTransitionMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	cfg := Config{
		RefreshInterval: time.Hour,
		StaleThreshold:  10 * time.Millisecond,
		PauseThreshold:  20 * time.Millisecond,
		FetchTimeout:    5 * time.Second,
	}
	mock := &mockFetcher{results: make(map[string]decimal.Decimal)}
	p, err := NewProvider(cfg, mock, []string{"BTC-USDT"}, nil)
	if err != nil {
		t.Fatalf("unexpected provider error: %v", err)
	}
	p.WithMetrics(m)

	// 1. Initial seed (Fresh)
	p.SeedIfAbsent("BTC-USDT", decimal.RequireFromString("95000"))

	// Repeated Get while FRESH -> 0 increments
	p.Get("BTC-USDT")
	p.Get("BTC-USDT")
	if val := testutil.ToFloat64(m.marketStale.WithLabelValues("BTC-USDT")); val != 0 {
		t.Fatalf("expected 0 stale transitions, got %v", val)
	}

	// 2. Transition FRESH -> STALE
	time.Sleep(12 * time.Millisecond)
	entry, ok := p.Get("BTC-USDT")
	if !ok || entry.State != StateStale {
		t.Fatalf("expected StateStale, got %v", entry.State)
	}
	if val := testutil.ToFloat64(m.marketStale.WithLabelValues("BTC-USDT")); val != 1 {
		t.Fatalf("expected 1 stale transition, got %v", val)
	}

	// Repeated Get while STALE -> MUST NOT INCREMENT (+0)
	p.Get("BTC-USDT")
	p.Get("BTC-USDT")
	if val := testutil.ToFloat64(m.marketStale.WithLabelValues("BTC-USDT")); val != 1 {
		t.Fatalf("expected 1 stale transition after repeated Get(), got %v", val)
	}

	// 3. Transition STALE -> PAUSED
	time.Sleep(12 * time.Millisecond)
	entry, ok = p.Get("BTC-USDT")
	if !ok || entry.State != StatePaused {
		t.Fatalf("expected StatePaused, got %v", entry.State)
	}
	if val := testutil.ToFloat64(m.marketPaused.WithLabelValues("BTC-USDT")); val != 1 {
		t.Fatalf("expected 1 paused transition, got %v", val)
	}

	// Repeated Get while PAUSED -> MUST NOT INCREMENT (+0)
	p.Get("BTC-USDT")
	p.Get("BTC-USDT")
	if val := testutil.ToFloat64(m.marketPaused.WithLabelValues("BTC-USDT")); val != 1 {
		t.Fatalf("expected 1 paused transition after repeated Get(), got %v", val)
	}

	// 4. Recovery: fetch updates market -> PAUSED -> FRESH
	mock.setResults(map[string]decimal.Decimal{"BTC-USDT": decimal.RequireFromString("96000")})
	p.fetchAndUpdate(context.Background())

	if val := testutil.ToFloat64(m.marketRecovery.WithLabelValues("BTC-USDT")); val != 1 {
		t.Fatalf("expected 1 recovery transition, got %v", val)
	}

	// Repeated Get while FRESH -> MUST NOT INCREMENT (+0)
	p.Get("BTC-USDT")
	p.Get("BTC-USDT")
	if val := testutil.ToFloat64(m.marketRecovery.WithLabelValues("BTC-USDT")); val != 1 {
		t.Fatalf("expected 1 recovery transition after repeated Get(), got %v", val)
	}

	// 5. Unknown market rejection increments unknownMarket counter
	p.Get("UNKNOWN-MARKET")
	p.SeedIfAbsent("UNKNOWN-MARKET", decimal.RequireFromString("100"))
	if val := testutil.ToFloat64(m.unknownMarket.WithLabelValues("UNKNOWN-MARKET")); val != 2 {
		t.Fatalf("expected 2 unknown market increments, got %v", val)
	}
}

func TestProvider_ConcurrentStress(t *testing.T) {
	mock := &mockFetcher{
		results: map[string]decimal.Decimal{
			"BTC-USDT": decimal.RequireFromString("95000"),
			"ETH-USDT": decimal.RequireFromString("2800"),
		},
	}
	cfg := Config{
		RefreshInterval: 20 * time.Millisecond,
		StaleThreshold:  100 * time.Millisecond,
		PauseThreshold:  200 * time.Millisecond,
		FetchTimeout:    500 * time.Millisecond,
	}
	markets := []string{"BTC-USDT", "ETH-USDT"}
	p, err := NewProvider(cfg, mock, markets, nil)
	if err != nil {
		t.Fatalf("unexpected provider error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	var wg sync.WaitGroup

	// Background Run loop
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.Run(ctx)
	}()

	// 5 Concurrent readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					p.Get("BTC-USDT")
					p.Get("ETH-USDT")
					p.GetAll()
					p.Get("UNKNOWN-MARKET")
				}
			}
		}()
	}

	// 3 Concurrent seeders (testing SeedIfAbsent idempotency)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					p.SeedIfAbsent("BTC-USDT", decimal.RequireFromString("94000"))
					p.SeedIfAbsent("ETH-USDT", decimal.RequireFromString("2750"))
					p.SeedIfAbsent("UNKNOWN-MARKET", decimal.RequireFromString("100"))
					time.Sleep(5 * time.Millisecond)
				}
			}
		}()
	}

	// 2 Concurrent mock updaters changing prices
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			delta := 0
			for {
				select {
				case <-ctx.Done():
					return
				default:
					delta++
					mock.setResults(map[string]decimal.Decimal{
						"BTC-USDT": decimal.RequireFromString(fmt.Sprintf("%d", 95000+delta)),
						"ETH-USDT": decimal.RequireFromString(fmt.Sprintf("%d", 2800+delta)),
					})
					time.Sleep(10 * time.Millisecond)
				}
			}
		}()
	}

	wg.Wait()

	// Assert provider ended in a consistent state
	btc, ok := p.Get("BTC-USDT")
	if !ok {
		t.Fatalf("expected BTC-USDT to be present after stress test")
	}
	if !btc.Price.GreaterThan(decimal.Zero) {
		t.Fatalf("expected positive BTC-USDT price, got %v", btc.Price)
	}
	if btc.Version <= 0 {
		t.Fatalf("expected positive monotonic version, got %v", btc.Version)
	}
}

func TestProvider_AtomicSnapshotCoherence(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetrics(reg)
	mock := &mockFetcher{}

	cfg := Config{
		RefreshInterval: 10 * time.Millisecond,
		StaleThreshold:  50 * time.Millisecond,
		PauseThreshold:  100 * time.Millisecond,
		FetchTimeout:    50 * time.Millisecond,
	}

	p, err := NewProvider(cfg, mock, []string{"BTC-USDT"}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}
	p.WithMetrics(metrics)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var coherenceErrors int64
	var totalReads int64

	// Writer goroutine continuously updating prices and versions
	wg.Add(1)
	go func() {
		defer wg.Done()
		var version int64
		for {
			select {
			case <-ctx.Done():
				return
			default:
				version++
				// Price is directly tied to version: 100000 + version
				price := decimal.RequireFromString(fmt.Sprintf("%d", 100000+version))
				mock.setResults(map[string]decimal.Decimal{
					"BTC-USDT": price,
				})
				p.fetchAndUpdate(context.Background())
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	// 8 Concurrent reader goroutines testing snapshot coherence
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					entry, ok := p.Get("BTC-USDT")
					if ok {
						atomic.AddInt64(&totalReads, 1)

						// Coherence Check 1: Price and Version must strictly correspond
						expectedPrice := decimal.RequireFromString(fmt.Sprintf("%d", 100000+entry.Version))
						if !entry.Price.Equal(expectedPrice) {
							atomic.AddInt64(&coherenceErrors, 1)
							t.Errorf("INCOHERENT SNAPSHOT: Version %d has Price %s (expected %s)",
								entry.Version, entry.Price.String(), expectedPrice.String())
							return
						}

						// Coherence Check 2: FetchedAt and State must be coherent with provider's clock
						now := p.now()
						age := now.Sub(entry.FetchedAt)
						if age < 0 {
							atomic.AddInt64(&coherenceErrors, 1)
							t.Errorf("INCOHERENT SNAPSHOT: FetchedAt %v is in future relative to now %v",
								entry.FetchedAt, now)
							return
						}

						// If snapshot was just fetched (< 30ms ago), it must be FRESH
						if age < 30*time.Millisecond && entry.State != StateFresh {
							atomic.AddInt64(&coherenceErrors, 1)
							t.Errorf("INCOHERENT SNAPSHOT: age is %v but State is %v (expected FRESH)",
								age, entry.State)
							return
						}
					}
					time.Sleep(200 * time.Microsecond)
				}
			}
		}()
	}

	wg.Wait()

	if atomic.LoadInt64(&coherenceErrors) > 0 {
		t.Fatalf("FAILED: observed %d snapshot coherence violations", coherenceErrors)
	}
	reads := atomic.LoadInt64(&totalReads)
	if reads < 500 {
		t.Fatalf("expected at least 500 reads, got %d", reads)
	}
}

type mockAnchorPublisher struct {
	mu        sync.Mutex
	published []Entry
	ttls      []time.Duration
}

func (m *mockAnchorPublisher) PublishAnchor(ctx context.Context, entry Entry, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.published = append(m.published, entry)
	m.ttls = append(m.ttls, ttl)
	return nil
}

func (m *mockAnchorPublisher) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.published)
}

func (m *mockAnchorPublisher) last() (Entry, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.published) == 0 {
		return Entry{}, 0
	}
	return m.published[len(m.published)-1], m.ttls[len(m.ttls)-1]
}

func TestProvider_FetchInitial_Success(t *testing.T) {
	mock := &mockFetcher{
		results: map[string]decimal.Decimal{
			"SOL-USDT": decimal.RequireFromString("117.01"),
		},
	}
	cfg := DefaultConfig()
	cfg.FetchTimeout = 1 * time.Second
	p, err := NewProvider(cfg, mock, []string{"SOL-USDT"}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}

	pub := &mockAnchorPublisher{}
	p.WithPublisher(pub)

	ctx := context.Background()
	err = p.FetchInitial(ctx, 2*time.Second)
	if err != nil {
		t.Fatalf("FetchInitial failed: %v", err)
	}

	entry, ok := p.Get("SOL-USDT")
	if !ok {
		t.Fatal("expected entry to exist after FetchInitial")
	}
	if !entry.Price.Equal(decimal.RequireFromString("117.01")) {
		t.Fatalf("expected price 117.01, got %s", entry.Price)
	}
	if entry.Version != 1 {
		t.Fatalf("expected version 1, got %d", entry.Version)
	}
	if entry.State != StateFresh {
		t.Fatalf("expected StateFresh, got %v", entry.State)
	}

	// Verify anchor was published to Redis publisher with correct TTL
	if pub.count() != 1 {
		t.Fatalf("expected 1 published anchor, got %d", pub.count())
	}
	lastEntry, ttl := pub.last()
	if !lastEntry.Price.Equal(decimal.RequireFromString("117.01")) {
		t.Fatalf("published price mismatch: expected 117.01, got %s", lastEntry.Price)
	}
	if ttl != 60*time.Second {
		t.Fatalf("expected 60s TTL, got %v", ttl)
	}
}

func TestProvider_FetchInitial_TimeoutFallback(t *testing.T) {
	mock := &mockFetcher{
		err: errors.New("connection timeout"),
	}
	cfg := DefaultConfig()
	p, err := NewProvider(cfg, mock, []string{"SOL-USDT"}, zap.NewNop())
	if err != nil {
		t.Fatalf("NewProvider failed: %v", err)
	}

	pub := &mockAnchorPublisher{}
	p.WithPublisher(pub)

	ctx := context.Background()
	err = p.FetchInitial(ctx, 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected FetchInitial to return error on fetcher failure")
	}

	// Entry must be absent initially
	_, ok := p.Get("SOL-USDT")
	if ok {
		t.Fatal("expected no entry before seed")
	}

	// Startup seed fallback
	p.SeedIfAbsent("SOL-USDT", decimal.RequireFromString("117.00"))
	entry, ok := p.Get("SOL-USDT")
	if !ok {
		t.Fatal("expected seed entry to exist")
	}
	if !entry.Price.Equal(decimal.RequireFromString("117.00")) {
		t.Fatalf("expected seed price 117.00, got %s", entry.Price)
	}

	// Publisher must NOT publish for seed (satisfying directive 2 & 10)
	if pub.count() != 0 {
		t.Fatalf("expected 0 published anchors for seed fallback, got %d", pub.count())
	}
}



