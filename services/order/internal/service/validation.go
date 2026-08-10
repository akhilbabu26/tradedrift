package service

import (
	"strings"

	"github.com/shopspring/decimal"

	"tradedrift/services/order/internal/repository"
)

func sameRequest(existing *repository.Order, p *CreateOrderParams) bool {
	if existing.UserID != p.UserID ||
		existing.MarketID != p.MarketID ||
		existing.Side != p.Side ||
		existing.OrderType != p.OrderType {
		return false
	}

	if !quantitiesEqual(existing.Quantity, p.Quantity) {
		return false
	}

	return pricesEqual(existing.Price, p.Price)
}

func pricesEqual(p1 *string, p2 string) bool {
	if p1 == nil && p2 == "" {
		return true
	}
	if p1 != nil && p2 != "" {
		d1, err1 := decimal.NewFromString(*p1)
		d2, err2 := decimal.NewFromString(p2)
		if err1 == nil && err2 == nil {
			return d1.Equal(d2)
		}
		return *p1 == p2
	}
	return false
}

func quantitiesEqual(q1, q2 string) bool {
	d1, err1 := decimal.NewFromString(q1)
	d2, err2 := decimal.NewFromString(q2)
	if err1 == nil && err2 == nil {
		return d1.Equal(d2)
	}
	return q1 == q2
}

func parseMarketID(market string) (base, quote string, err error) {
	parts := strings.Split(market, "-")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", ErrInvalidMarket
	}
	return parts[0], parts[1], nil
}
