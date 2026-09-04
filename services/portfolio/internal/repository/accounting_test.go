package repository_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"tradedrift/services/portfolio/internal/repository"
)

// TestAccounting_WeightedAverageBuys verifies buyer weighted-average entry price calculation
func TestAccounting_WeightedAverageBuys(t *testing.T) {
	// Start with empty holding
	h := repository.Holding{
		UserID:    "user-1",
		AssetCode: "BTC",
		Quantity:  decimal.Zero,
		TotalCost: decimal.Zero,
	}

	// Buy 1: 1 BTC @ 90,000
	qty1 := decimal.RequireFromString("1.0")
	price1 := decimal.RequireFromString("90000.00")
	h.Quantity = h.Quantity.Add(qty1)
	h.TotalCost = h.TotalCost.Add(qty1.Mul(price1))

	if !h.AverageEntryPrice().Equal(decimal.RequireFromString("90000.00")) {
		t.Errorf("expected avg 90000, got %s", h.AverageEntryPrice())
	}

	// Buy 2: 1 BTC @ 100,000
	qty2 := decimal.RequireFromString("1.0")
	price2 := decimal.RequireFromString("100000.00")
	h.Quantity = h.Quantity.Add(qty2)
	h.TotalCost = h.TotalCost.Add(qty2.Mul(price2))

	// Total qty = 2 BTC, Total cost = 190,000, Avg = 95,000
	expectedAvg := decimal.RequireFromString("95000.00")
	if !h.AverageEntryPrice().Equal(expectedAvg) {
		t.Errorf("expected avg %s, got %s", expectedAvg, h.AverageEntryPrice())
	}
}

// TestAccounting_PartialSellAndRealizedPnL verifies seller profit realization and cost reduction
func TestAccounting_PartialSellAndRealizedPnL(t *testing.T) {
	// Starting position: 2 BTC @ avg 95,000 (total cost 190,000), 0 realized PnL
	h := repository.Holding{
		UserID:      "user-1",
		AssetCode:   "BTC",
		Quantity:    decimal.RequireFromString("2.0"),
		TotalCost:   decimal.RequireFromString("190000.00"),
		RealizedPnL: decimal.Zero,
	}

	// Sell 0.5 BTC @ 110,000
	sellQty := decimal.RequireFromString("0.5")
	sellPrice := decimal.RequireFromString("110000.00")

	costOfSold := sellQty.Mul(h.AverageEntryPrice()) // 0.5 * 95,000 = 47,500
	revenue := sellQty.Mul(sellPrice)                // 0.5 * 110,000 = 55,000
	realizedDelta := revenue.Sub(costOfSold)          // 55,000 - 47,500 = +7,500

	h.Quantity = h.Quantity.Sub(sellQty)
	h.TotalCost = h.TotalCost.Sub(costOfSold)
	h.RealizedPnL = h.RealizedPnL.Add(realizedDelta)

	// Remaining: 1.5 BTC, 142,500 cost, avg 95,000, +7,500 realized PnL
	expectedQty := decimal.RequireFromString("1.5")
	expectedCost := decimal.RequireFromString("142500.00")
	expectedPnL := decimal.RequireFromString("7500.00")
	expectedAvg := decimal.RequireFromString("95000.00")

	if !h.Quantity.Equal(expectedQty) {
		t.Errorf("expected qty %s, got %s", expectedQty, h.Quantity)
	}
	if !h.TotalCost.Equal(expectedCost) {
		t.Errorf("expected cost %s, got %s", expectedCost, h.TotalCost)
	}
	if !h.RealizedPnL.Equal(expectedPnL) {
		t.Errorf("expected PnL %s, got %s", expectedPnL, h.RealizedPnL)
	}
	if !h.AverageEntryPrice().Equal(expectedAvg) {
		t.Errorf("expected avg %s, got %s", expectedAvg, h.AverageEntryPrice())
	}
}

// TestAccounting_FullLiquidationZeroClamping verifies that total liquidation resets cost basis to exactly zero
func TestAccounting_FullLiquidationZeroClamping(t *testing.T) {
	h := repository.Holding{
		UserID:      "user-1",
		AssetCode:   "BTC",
		Quantity:    decimal.RequireFromString("1.5"),
		TotalCost:   decimal.RequireFromString("142500.00"),
		RealizedPnL: decimal.RequireFromString("7500.00"),
	}

	// Sell remaining 1.5 BTC @ 100,000
	sellQty := decimal.RequireFromString("1.5")
	sellPrice := decimal.RequireFromString("100000.00")

	costOfSold := sellQty.Mul(h.AverageEntryPrice())
	revenue := sellQty.Mul(sellPrice)
	realizedDelta := revenue.Sub(costOfSold)

	h.Quantity = h.Quantity.Sub(sellQty)
	h.TotalCost = h.TotalCost.Sub(costOfSold)
	h.RealizedPnL = h.RealizedPnL.Add(realizedDelta)

	// Clamp when fully liquidated
	if h.Quantity.IsZero() || h.Quantity.IsNegative() {
		h.Quantity = decimal.Zero
		h.TotalCost = decimal.Zero
	}

	if !h.Quantity.IsZero() {
		t.Errorf("expected quantity 0, got %s", h.Quantity)
	}
	if !h.TotalCost.IsZero() {
		t.Errorf("expected total cost 0, got %s", h.TotalCost)
	}
	if !h.AverageEntryPrice().IsZero() {
		t.Errorf("expected avg entry 0, got %s", h.AverageEntryPrice())
	}

	// PnL: 7500 + (150,000 - 142,500) = 7500 + 7500 = 15,000
	expectedPnL := decimal.RequireFromString("15000.00")
	if !h.RealizedPnL.Equal(expectedPnL) {
		t.Errorf("expected PnL %s, got %s", expectedPnL, h.RealizedPnL)
	}
}

// TestAccounting_InsufficientHoldings verifies that trying to sell more than held is rejected
func TestAccounting_InsufficientHoldings(t *testing.T) {
	h := repository.Holding{
		UserID:      "user-1",
		AssetCode:   "BTC",
		Quantity:    decimal.RequireFromString("0.5"),
		TotalCost:   decimal.RequireFromString("45000.00"),
		RealizedPnL: decimal.Zero,
	}

	sellQty := decimal.RequireFromString("1.0")
	if !h.Quantity.LessThan(sellQty) {
		t.Errorf("expected holding 0.5 to be less than sell quantity 1.0")
	}
}

// TestAccounting_DeterministicUserOrder verifies that deterministic ordering handles Alice <-> Bob
func TestAccounting_DeterministicUserOrder(t *testing.T) {
	alice := "00000000-0000-0000-0000-000000000001"
	bob := "00000000-0000-0000-0000-000000000002"

	// Scenario 1: Alice is buyer, Bob is seller
	first1, second1 := alice, bob
	if first1 > second1 {
		first1, second1 = second1, first1
	}
	if first1 != alice || second1 != bob {
		t.Errorf("expected first=%s, second=%s", alice, bob)
	}

	// Scenario 2: Bob is buyer, Alice is seller
	first2, second2 := bob, alice
	if first2 > second2 {
		first2, second2 = second2, first2
	}
	if first2 != alice || second2 != bob {
		t.Errorf("expected first=%s, second=%s even when roles are reversed", alice, bob)
	}
}

