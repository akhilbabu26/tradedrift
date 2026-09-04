package service

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	marketv1 "tradedrift/platform/api/gen/market/v1"
	walletv1 "tradedrift/platform/api/gen/wallet/v1"
	"tradedrift/services/portfolio/internal/metrics"
	"tradedrift/services/portfolio/internal/repository"
)

type PortfolioSummary struct {
	UserID        string
	TotalValue    string
	RealizedPnL   string
	UnrealizedPnL string
	CashBalance   string
	UpdatedAt     string
}

type HoldingDetail struct {
	Asset             string
	TotalQuantity     string
	AverageEntryPrice string
	CurrentPrice      string
	UnrealizedPnL     string
}

type PortfolioHoldings struct {
	UserID   string
	Holdings []HoldingDetail
}

type Service struct {
	repo         repository.Repository
	walletClient walletv1.WalletServiceClient
	marketClient marketv1.MarketServiceClient
}

func New(
	repo repository.Repository,
	walletClient walletv1.WalletServiceClient,
	marketClient marketv1.MarketServiceClient,
) *Service {
	return &Service{
		repo:         repo,
		walletClient: walletClient,
		marketClient: marketClient,
	}
}

// GetPortfolioSummary computes total portfolio value, realized PnL, unrealized PnL, and cash balance on the fly.
func (s *Service) GetPortfolioSummary(ctx context.Context, userID string) (*PortfolioSummary, error) {
	timer := metrics.ValuationDurationSeconds.WithLabelValues("summary")
	start := time.Now()
	defer func() { timer.Observe(time.Since(start).Seconds()) }()

	// 1. Fetch active crypto holdings from local database
	holdings, err := s.repo.GetHoldingsByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("fetch holdings for user %s: %w", userID, err)
	}

	// 2. Query Wallet Service for cash balance (USDT)
	cashBalance := decimal.Zero
	walletRes, err := s.walletClient.GetBalances(ctx, &walletv1.GetBalancesRequest{UserId: userID})
	if err != nil {
		return nil, fmt.Errorf("fetch wallet balances for user %s: %w", userID, err)
	}

	for _, b := range walletRes.GetBalances() {
		if b.GetAsset() == "USDT" {
			avail, err1 := decimal.NewFromString(b.GetAvailableBalance())
			resrv, err2 := decimal.NewFromString(b.GetReservedBalance())
			if err1 == nil && err2 == nil {
				cashBalance = avail.Add(resrv)
			}
			break
		}
	}


	// 3. Value each holding with Market Service live prices
	totalMarketValue := decimal.Zero
	totalRealizedPnL := decimal.Zero
	totalUnrealizedPnL := decimal.Zero

	for _, h := range holdings {
		totalRealizedPnL = totalRealizedPnL.Add(h.RealizedPnL)

		marketID := fmt.Sprintf("%s-USDT", h.AssetCode)
		tickerRes, err := s.marketClient.GetTicker(ctx, &marketv1.GetTickerRequest{MarketId: marketID})
		if err != nil {
			return nil, fmt.Errorf("fetch ticker for market %s: %w", marketID, err)
		}

		currentPrice, err := decimal.NewFromString(tickerRes.GetTicker().GetLastPrice())
		if err != nil {
			return nil, fmt.Errorf("parse last price for %s: %w", marketID, err)
		}

		marketVal := h.Quantity.Mul(currentPrice)
		unrealized := marketVal.Sub(h.TotalCost)

		totalMarketValue = totalMarketValue.Add(marketVal)
		totalUnrealizedPnL = totalUnrealizedPnL.Add(unrealized)
	}

	totalValue := cashBalance.Add(totalMarketValue)
	nowRFC3339 := time.Now().UTC().Format(time.RFC3339)

	return &PortfolioSummary{
		UserID:        userID,
		TotalValue:    totalValue.StringFixed(10),
		RealizedPnL:   totalRealizedPnL.StringFixed(10),
		UnrealizedPnL: totalUnrealizedPnL.StringFixed(10),
		CashBalance:   cashBalance.StringFixed(10),
		UpdatedAt:     nowRFC3339,
	}, nil
}

// GetPortfolioHoldings returns the detailed list of holdings with average entry prices and current market valuations.
func (s *Service) GetPortfolioHoldings(ctx context.Context, userID string) (*PortfolioHoldings, error) {
	timer := metrics.ValuationDurationSeconds.WithLabelValues("holdings")
	start := time.Now()
	defer func() { timer.Observe(time.Since(start).Seconds()) }()

	holdings, err := s.repo.GetHoldingsByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("fetch holdings for user %s: %w", userID, err)
	}

	details := make([]HoldingDetail, 0, len(holdings))
	for _, h := range holdings {
		marketID := fmt.Sprintf("%s-USDT", h.AssetCode)
		tickerRes, err := s.marketClient.GetTicker(ctx, &marketv1.GetTickerRequest{MarketId: marketID})
		if err != nil {
			return nil, fmt.Errorf("fetch ticker for %s: %w", marketID, err)
		}

		currentPrice, err := decimal.NewFromString(tickerRes.GetTicker().GetLastPrice())
		if err != nil {
			return nil, fmt.Errorf("parse last price for %s: %w", marketID, err)
		}

		avgEntry := h.AverageEntryPrice()
		unrealized := currentPrice.Sub(avgEntry).Mul(h.Quantity)

		details = append(details, HoldingDetail{
			Asset:             h.AssetCode,
			TotalQuantity:     h.Quantity.StringFixed(10),
			AverageEntryPrice: avgEntry.StringFixed(10),
			CurrentPrice:      currentPrice.StringFixed(10),
			UnrealizedPnL:     unrealized.StringFixed(10),
		})
	}

	return &PortfolioHoldings{
		UserID:   userID,
		Holdings: details,
	}, nil
}
