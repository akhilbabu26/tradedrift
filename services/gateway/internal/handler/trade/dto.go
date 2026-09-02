package trade

import (
	tradev1 "tradedrift/platform/api/gen/trade/v1"
)

// TradeDTO is the full trade record returned to authenticated parties (buyer/seller).
// buyer_id and seller_id are included — gateway enforces JWT auth before sending this.
type TradeDTO struct {
	ID          string `json:"id"`
	BuyerID     string `json:"buyer_id"`
	SellerID    string `json:"seller_id"`
	BuyOrderID  string `json:"buy_order_id"`
	SellOrderID string `json:"sell_order_id"`
	MarketID    string `json:"market_id"`
	BaseAsset   string `json:"base_asset"`
	QuoteAsset  string `json:"quote_asset"`
	Price       string `json:"price"`
	Quantity    string `json:"quantity"`
	ExecutedAt  string `json:"executed_at"`
	SettledAt   string `json:"settled_at"`
}

// MarketTradeDTO is the public market tape entry.
// TI-7: buyer_id and seller_id are NEVER included — anonymous callers may request this.
type MarketTradeDTO struct {
	ID         string `json:"id"`
	MarketID   string `json:"market_id"`
	BaseAsset  string `json:"base_asset"`
	QuoteAsset string `json:"quote_asset"`
	Price      string `json:"price"`
	Quantity   string `json:"quantity"`
	ExecutedAt string `json:"executed_at"`
}

func tradeDTO(t *tradev1.Trade) TradeDTO {
	return TradeDTO{
		ID:          t.GetTradeId(),
		BuyerID:     t.GetBuyerId(),
		SellerID:    t.GetSellerId(),
		BuyOrderID:  t.GetBuyOrderId(),
		SellOrderID: t.GetSellOrderId(),
		MarketID:    t.GetMarketId(),
		BaseAsset:   t.GetBaseAsset(),
		QuoteAsset:  t.GetQuoteAsset(),
		Price:       t.GetPrice(),
		Quantity:    t.GetQuantity(),
		ExecutedAt:  t.GetExecutedAt(),
		SettledAt:   t.GetSettledAt(),
	}
}

func marketTradeDTO(t *tradev1.MarketTrade) MarketTradeDTO {
	return MarketTradeDTO{
		ID:         t.GetTradeId(),
		MarketID:   t.GetMarketId(),
		BaseAsset:  t.GetBaseAsset(),
		QuoteAsset: t.GetQuoteAsset(),
		Price:      t.GetPrice(),
		Quantity:   t.GetQuantity(),
		ExecutedAt: t.GetExecutedAt(),
	}
}
