package service

import (
	"context"
	"fmt"
	"strings"
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

type PortfolioHistoryPoint struct {
	Timestamp     int64
	TotalEquity   string
	CashBalance   string
	HoldingsValue string
	UnrealizedPnL string
	RealizedPnL   string
}

type PortfolioHistory struct {
	UserID string
	Points []PortfolioHistoryPoint
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
		if !currentPrice.IsPositive() {
			return nil, fmt.Errorf("invalid market price %s for %s: must be strictly positive", currentPrice.String(), marketID)
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
		if !currentPrice.IsPositive() {
			return nil, fmt.Errorf("invalid market price %s for %s: must be strictly positive", currentPrice.String(), marketID)
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

// RecordSnapshot computes the real-time valuation for a user and writes an immutable snapshot.
// Directly reuses GetPortfolioSummary to guarantee Total Equity = Cash + Holdings Value.
func (s *Service) RecordSnapshot(ctx context.Context, userID string, triggerType string) error {
	summary, err := s.GetPortfolioSummary(ctx, userID)
	if err != nil {
		return fmt.Errorf("compute summary for snapshot (user %s): %w", userID, err)
	}

	totalEquity, _ := decimal.NewFromString(summary.TotalValue)
	cashBalance, _ := decimal.NewFromString(summary.CashBalance)
	unrealizedPnL, _ := decimal.NewFromString(summary.UnrealizedPnL)
	realizedPnL, _ := decimal.NewFromString(summary.RealizedPnL)
	holdingsValue := totalEquity.Sub(cashBalance)

	snap := repository.PortfolioSnapshot{
		UserID:        userID,
		TotalEquity:   totalEquity,
		CashBalance:   cashBalance,
		HoldingsValue: holdingsValue,
		UnrealizedPnL: unrealizedPnL,
		RealizedPnL:   realizedPnL,
		TriggerType:   triggerType,
		SnapshotAt:    time.Now().UTC(),
	}

	return s.repo.RecordSnapshot(ctx, snap)
}

// GetPortfolioHistory retrieves chronological, bucket-sampled snapshots for the specified timeframe.
// Strictly returns ascending points with Unix seconds timestamps for TradingView Lightweight Charts.
func (s *Service) GetPortfolioHistory(ctx context.Context, userID string, timeframe string) (*PortfolioHistory, error) {
	now := time.Now().UTC()
	var startTime time.Time
	var bucketDuration time.Duration

	switch strings.ToUpper(strings.TrimSpace(timeframe)) {
	case "24H":
		startTime = now.Add(-24 * time.Hour)
		bucketDuration = 5 * time.Minute
	case "30D":
		startTime = now.Add(-30 * 24 * time.Hour)
		bucketDuration = 4 * time.Hour
	case "90D":
		startTime = now.Add(-90 * 24 * time.Hour)
		bucketDuration = 12 * time.Hour
	case "1Y":
		startTime = now.Add(-365 * 24 * time.Hour)
		bucketDuration = 24 * time.Hour
	case "7D":
		fallthrough
	default:
		startTime = now.Add(-7 * 24 * time.Hour)
		bucketDuration = 1 * time.Hour
	}

	snapshots, err := s.repo.GetSnapshots(ctx, userID, startTime, bucketDuration, 150)
	if err != nil {
		return nil, fmt.Errorf("fetch snapshots for user %s: %w", userID, err)
	}

	points := make([]PortfolioHistoryPoint, 0, len(snapshots))
	for _, snap := range snapshots {
		points = append(points, PortfolioHistoryPoint{
			Timestamp:     snap.SnapshotAt.Unix(),
			TotalEquity:   snap.TotalEquity.StringFixed(2),
			CashBalance:   snap.CashBalance.StringFixed(2),
			HoldingsValue: snap.HoldingsValue.StringFixed(2),
			UnrealizedPnL: snap.UnrealizedPnL.StringFixed(2),
			RealizedPnL:   snap.RealizedPnL.StringFixed(2),
		})
	}

	return &PortfolioHistory{
		UserID: userID,
		Points: points,
	}, nil
}

// StartPeriodicSnapshotWorker runs a single centralized ticker that records valuation snapshots
// for users with active holdings or recent activity. Gracefully shuts down when ctx is cancelled.
func (s *Service) StartPeriodicSnapshotWorker(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			userIDs, err := s.repo.GetActiveUserIDsWithHoldings(ctx)
			if err != nil {
				continue
			}
			for _, uid := range userIDs {
				select {
				case <-ctx.Done():
					return
				default:
					_ = s.RecordSnapshot(ctx, uid, "PERIODIC")
				}
			}
		}
	}
}
