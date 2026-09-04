package service_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"

	marketv1 "tradedrift/platform/api/gen/market/v1"
	walletv1 "tradedrift/platform/api/gen/wallet/v1"
	"tradedrift/services/portfolio/internal/repository"
	"tradedrift/services/portfolio/internal/service"
)

// mockRepo implements repository.Repository for unit tests
type mockRepo struct {
	holdings []repository.Holding
}

func (m *mockRepo) GetHoldingsByUser(ctx context.Context, userID string) ([]repository.Holding, error) {
	return m.holdings, nil
}
func (m *mockRepo) ProcessUserTrade(ctx context.Context, in repository.UserTradeInput) (*repository.OutboxMessage, error) {
	return nil, nil
}
func (m *mockRepo) ProcessTradeSettled(ctx context.Context, in repository.TradeSettledInput) ([]repository.OutboxMessage, error) {
	return nil, nil
}
func (m *mockRepo) FetchPendingOutbox(ctx context.Context, limit int) ([]repository.OutboxMessage, error) {
	return nil, nil
}
func (m *mockRepo) MarkOutboxPublished(ctx context.Context, ids []string) error {
	return nil
}

// mockWalletClient implements walletv1.WalletServiceClient for unit tests
type mockWalletClient struct {
	walletv1.WalletServiceClient
	usdtAvailable string
	usdtReserved  string
}

func (m *mockWalletClient) GetBalances(ctx context.Context, in *walletv1.GetBalancesRequest, opts ...grpc.CallOption) (*walletv1.GetBalancesResponse, error) {
	return &walletv1.GetBalancesResponse{
		Balances: []*walletv1.AssetBalance{
			{
				Asset:            "USDT",
				AvailableBalance: m.usdtAvailable,
				ReservedBalance:  m.usdtReserved,
			},
		},
	}, nil
}

// mockMarketClient implements marketv1.MarketServiceClient for unit tests
type mockMarketClient struct {
	marketv1.MarketServiceClient
	prices map[string]string
}

func (m *mockMarketClient) GetTicker(ctx context.Context, in *marketv1.GetTickerRequest, opts ...grpc.CallOption) (*marketv1.GetTickerResponse, error) {
	price, ok := m.prices[in.GetMarketId()]
	if !ok {
		price = "100000.00"
	}
	return &marketv1.GetTickerResponse{
		Ticker: &marketv1.Ticker24H{
			MarketId:  in.GetMarketId(),
			LastPrice: price,
		},
	}, nil
}

// TestPortfolioSummary_Valuation verifies dynamic valuation blending cash + holdings * mark prices
func TestPortfolioSummary_Valuation(t *testing.T) {
	repo := &mockRepo{
		holdings: []repository.Holding{
			{
				UserID:      "018f673a-4e2b-7f11-80a2-c3bfde34aa5a",
				AssetCode:   "BTC",
				Quantity:    decimal.RequireFromString("0.02"),
				TotalCost:   decimal.RequireFromString("1900.00"), // avg 95,000
				RealizedPnL: decimal.RequireFromString("50.00"),
			},
			{
				UserID:      "018f673a-4e2b-7f11-80a2-c3bfde34aa5a",
				AssetCode:   "ETH",
				Quantity:    decimal.RequireFromString("1.0"),
				TotalCost:   decimal.RequireFromString("3000.00"), // avg 3,000
				RealizedPnL: decimal.Zero,
			},
		},
	}

	walletMock := &mockWalletClient{
		usdtAvailable: "10000.0000000000",
		usdtReserved:  "500.0000000000",
	}

	marketMock := &mockMarketClient{
		prices: map[string]string{
			"BTC-USDT": "100000.0000000000", // BTC value = 0.02 * 100,000 = 2000. Unrealized = 2000 - 1900 = +100
			"ETH-USDT": "3500.0000000000",   // ETH value = 1.0 * 3500 = 3500. Unrealized = 3500 - 3000 = +500
		},
	}

	svc := service.New(repo, walletMock, marketMock)
	summary, err := svc.GetPortfolioSummary(context.Background(), "018f673a-4e2b-7f11-80a2-c3bfde34aa5a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expected:
	// Cash = 10,000 + 500 = 10,500
	// BTC Market Value = 2,000
	// ETH Market Value = 3,500
	// Total Equity = 10,500 + 2,000 + 3,500 = 16,000
	// Total Unrealized PnL = 100 + 500 = +600
	// Realized PnL = 50

	expectedCash := "10500.0000000000"
	expectedTotal := "16000.0000000000"
	expectedUnrealized := "600.0000000000"
	expectedRealized := "50.0000000000"

	if summary.CashBalance != expectedCash {
		t.Errorf("CashBalance = %s; want %s", summary.CashBalance, expectedCash)
	}
	if summary.TotalValue != expectedTotal {
		t.Errorf("TotalValue = %s; want %s", summary.TotalValue, expectedTotal)
	}
	if summary.UnrealizedPnL != expectedUnrealized {
		t.Errorf("UnrealizedPnL = %s; want %s", summary.UnrealizedPnL, expectedUnrealized)
	}
	if summary.RealizedPnL != expectedRealized {
		t.Errorf("RealizedPnL = %s; want %s", summary.RealizedPnL, expectedRealized)
	}
}

// TestPortfolioHoldings_Details verifies detailed holdings with average entry prices
func TestPortfolioHoldings_Details(t *testing.T) {
	repo := &mockRepo{
		holdings: []repository.Holding{
			{
				UserID:      "018f673a-4e2b-7f11-80a2-c3bfde34aa5a",
				AssetCode:   "BTC",
				Quantity:    decimal.RequireFromString("0.02"),
				TotalCost:   decimal.RequireFromString("1900.00"), // avg = 95,000
				RealizedPnL: decimal.Zero,
			},
		},
	}

	walletMock := &mockWalletClient{}
	marketMock := &mockMarketClient{
		prices: map[string]string{
			"BTC-USDT": "97000.0000000000",
		},
	}

	svc := service.New(repo, walletMock, marketMock)
	holdingsRes, err := svc.GetPortfolioHoldings(context.Background(), "018f673a-4e2b-7f11-80a2-c3bfde34aa5a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(holdingsRes.Holdings) != 1 {
		t.Fatalf("len(Holdings) = %d; want 1", len(holdingsRes.Holdings))
	}

	h := holdingsRes.Holdings[0]
	if h.Asset != "BTC" {
		t.Errorf("Asset = %s; want BTC", h.Asset)
	}
	if h.AverageEntryPrice != "95000.0000000000" {
		t.Errorf("AverageEntryPrice = %s; want 95000.0000000000", h.AverageEntryPrice)
	}
	if h.CurrentPrice != "97000.0000000000" {
		t.Errorf("CurrentPrice = %s; want 97000.0000000000", h.CurrentPrice)
	}
	// Unrealized = (97000 - 95000) * 0.02 = 40.00
	if h.UnrealizedPnL != "40.0000000000" {
		t.Errorf("UnrealizedPnL = %s; want 40.0000000000", h.UnrealizedPnL)
	}
}
