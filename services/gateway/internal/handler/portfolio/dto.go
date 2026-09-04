package portfolio

import (
	portfoliov1 "tradedrift/platform/api/gen/portfolio/v1"
)

type PortfolioSummaryDTO struct {
	UserID        string `json:"userId"`
	TotalValue    string `json:"totalValue"`
	RealizedPnL   string `json:"realizedPnl"`
	UnrealizedPnL string `json:"unrealizedPnl"`
	CashBalance   string `json:"cashBalance"`
	UpdatedAt     string `json:"updatedAt"`
}

type HoldingDetailDTO struct {
	Asset             string `json:"asset"`
	TotalQuantity     string `json:"totalQuantity"`
	AverageEntryPrice string `json:"averageEntryPrice"`
	CurrentPrice      string `json:"currentPrice"`
	UnrealizedPnL     string `json:"unrealizedPnl"`
}

type PortfolioHoldingsDTO struct {
	UserID   string             `json:"userId"`
	Holdings []HoldingDetailDTO `json:"holdings"`
}

func summaryDTO(res *portfoliov1.PortfolioSummaryResponse) PortfolioSummaryDTO {
	return PortfolioSummaryDTO{
		UserID:        res.GetUserId(),
		TotalValue:    res.GetTotalValue(),
		RealizedPnL:   res.GetRealizedPnl(),
		UnrealizedPnL: res.GetUnrealizedPnl(),
		CashBalance:   res.GetCashBalance(),
		UpdatedAt:     res.GetUpdatedAt(),
	}
}

func holdingsDTO(res *portfoliov1.PortfolioHoldingsResponse) PortfolioHoldingsDTO {
	list := make([]HoldingDetailDTO, 0, len(res.GetHoldings()))
	for _, h := range res.GetHoldings() {
		list = append(list, HoldingDetailDTO{
			Asset:             h.GetAsset(),
			TotalQuantity:     h.GetTotalQuantity(),
			AverageEntryPrice: h.GetAverageEntryPrice(),
			CurrentPrice:      h.GetCurrentPrice(),
			UnrealizedPnL:     h.GetUnrealizedPnl(),
		})
	}
	return PortfolioHoldingsDTO{
		UserID:   res.GetUserId(),
		Holdings: list,
	}
}
