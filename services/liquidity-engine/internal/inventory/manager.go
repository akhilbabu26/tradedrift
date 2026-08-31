// Package inventory manages the MM-001 balance state and provides
// effective available inventory calculations for the LE's skew decisions.
//
// The LE bypasses Wallet Service's ReserveFunds mechanism — MM orders are
// not reflected in wallet.reserved_balance. The Manager computes its own
// effective_available by subtracting resting order commitments.
//
// All methods must be called from the engine's single event loop goroutine.
package inventory

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"tradedrift/services/liquidity-engine/internal/kafka"
	"tradedrift/services/liquidity-engine/internal/order"
)

// AssetBalance holds the authoritative balance for one asset from the Wallet Service.
type AssetBalance struct {
	Asset            string
	AvailableBalance decimal.Decimal
}

// Manager tracks wallet balances and applies trade fills
// to maintain a fast view of effective available inventory.
type Manager struct {
	// projectedBalances = latest authoritative Wallet Service snapshot + locally observed trade deltas.
	// Rebuilt from scratch on each wallet refresh.
	projectedBalances map[string]decimal.Decimal
	lastRefresh       time.Time
	tracker           *order.Tracker
	logger            *zap.Logger
}

// NewManager creates a new inventory Manager.
func NewManager(tracker *order.Tracker, logger *zap.Logger) *Manager {
	return &Manager{
		projectedBalances: make(map[string]decimal.Decimal),
		tracker:           tracker,
		logger:            logger,
	}
}

// RefreshFromWallet updates projected balances from the authoritative Wallet Service response.
func (m *Manager) RefreshFromWallet(balances map[string]decimal.Decimal) {
	for asset, bal := range balances {
		m.projectedBalances[asset] = bal
		m.logger.Info("wallet balance refreshed",
			zap.String("asset", asset),
			zap.String("balance", bal.String()))
	}
	m.lastRefresh = time.Now()
}

// ApplyTrade updates the in-memory balance view based on a trade execution.
func (m *Manager) ApplyTrade(event kafka.TradeEvent) {
	if event.MMSide == "" {
		return
	}

	qty := event.Quantity
	quoteValue := event.Quantity.Mul(event.Price)

	switch event.MMSide {
	case "SELL":
		// MM sold base asset (ask was filled) -> base decreases, quote increases
		if bal, ok := m.projectedBalances[baseAsset(event.MarketID)]; ok {
			m.projectedBalances[baseAsset(event.MarketID)] = maxZero(bal.Sub(qty))
		}
		if bal, ok := m.projectedBalances["USDT"]; ok {
			m.projectedBalances["USDT"] = bal.Add(quoteValue)
		}
		m.logger.Info("inventory: MM SELL filled",
			zap.String("market_id", event.MarketID),
			zap.String("qty", qty.String()),
			zap.String("quote_received", quoteValue.String()))

	case "BUY":
		// MM bought base asset (bid was filled) -> quote decreases, base increases
		if bal, ok := m.projectedBalances["USDT"]; ok {
			m.projectedBalances["USDT"] = maxZero(bal.Sub(quoteValue))
		}
		if bal, ok := m.projectedBalances[baseAsset(event.MarketID)]; ok {
			m.projectedBalances[baseAsset(event.MarketID)] = bal.Add(qty)
		}
		m.logger.Info("inventory: MM BUY filled",
			zap.String("market_id", event.MarketID),
			zap.String("qty", qty.String()),
			zap.String("quote_spent", quoteValue.String()))
	}
}

// EffectiveAvailableBase returns the effective available base asset for a market.
// effective_base = projected_balance[base] - committed_base
func (m *Manager) EffectiveAvailableBase(marketID string) decimal.Decimal {
	base := baseAsset(marketID)
	walletBal, ok := m.projectedBalances[base]
	if !ok {
		return decimal.Zero
	}
	committed := m.tracker.CommittedBase(marketID)
	return maxZero(walletBal.Sub(committed))
}

// EffectiveAvailableQuote returns the effective available USDT for bid-side quoting across all markets.
func (m *Manager) EffectiveAvailableQuote(markets []string) decimal.Decimal {
	walletBal, ok := m.projectedBalances["USDT"]
	if !ok {
		return decimal.Zero
	}
	totalCommitted := decimal.Zero
	for _, marketID := range markets {
		totalCommitted = totalCommitted.Add(m.tracker.CommittedQuote(marketID))
	}
	return maxZero(walletBal.Sub(totalCommitted))
}

// LastRefresh returns when the Wallet Service balance was last successfully fetched.
func (m *Manager) LastRefresh() time.Time {
	return m.lastRefresh
}

// IsStale returns true if the wallet balance has not been refreshed within maxStaleness.
func (m *Manager) IsStale(maxStaleness time.Duration) bool {
	if m.lastRefresh.IsZero() {
		return true
	}
	return time.Since(m.lastRefresh) > maxStaleness
}

// WalletBalanceFor returns the projected balance for an asset.
func (m *Manager) WalletBalanceFor(asset string) (decimal.Decimal, bool) {
	bal, ok := m.projectedBalances[asset]
	return bal, ok
}

// ValidateMMAccount confirms MM-001 has all required asset balances.
func ValidateMMAccount(ctx context.Context, balances map[string]decimal.Decimal) error {
	required := []string{"BTC", "ETH", "SOL", "USDT"}
	for _, asset := range required {
		if _, ok := balances[asset]; !ok {
			return fmt.Errorf("MM-001 wallet missing required asset %q — run migration 00003_seed_mm001_wallet.sql", asset)
		}
	}
	return nil
}

func baseAsset(marketID string) string {
	for i := 0; i < len(marketID); i++ {
		if marketID[i] == '-' {
			return marketID[:i]
		}
	}
	return marketID
}

func maxZero(d decimal.Decimal) decimal.Decimal {
	if d.IsNegative() {
		return decimal.Zero
	}
	return d
}
