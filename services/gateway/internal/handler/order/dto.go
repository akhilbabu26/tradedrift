package order

import (
	"time"

	orderv1 "tradedrift/platform/api/gen/order/v1"
)

type CreateOrderRequestDTO struct {
	MarketID  string `json:"market_id"`
	Side      string `json:"side"`       // "BUY" or "SELL"
	OrderType string `json:"order_type"` // "LIMIT" or "MARKET"
	Type      string `json:"type"`       // alias for order_type
	Price     string `json:"price"`      // required for LIMIT
	Quantity  string `json:"quantity"`
}

type OrderDTO struct {
	ID             string    `json:"id"`
	UserID         string    `json:"user_id"`
	MarketID       string    `json:"market_id"`
	Side           string    `json:"side"`
	OrderType      string    `json:"order_type"`
	Status         string    `json:"status"`
	Price          string    `json:"price"`
	Quantity       string    `json:"quantity"`
	FilledQuantity string    `json:"filled_quantity"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func orderDTO(o *orderv1.Order) OrderDTO {
	return OrderDTO{
		ID:             o.GetId(),
		UserID:         o.GetUserId(),
		MarketID:       o.GetMarketId(),
		Side:           o.GetSide().String(),
		OrderType:      o.GetOrderType().String(),
		Status:         o.GetStatus().String(),
		Price:          o.GetPrice(),
		Quantity:       o.GetQuantity(),
		FilledQuantity: o.GetFilledQuantity(),
		CreatedAt:      o.GetCreatedAt().AsTime(),
		UpdatedAt:      o.GetUpdatedAt().AsTime(),
	}
}
