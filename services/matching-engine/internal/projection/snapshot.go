package projection

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

var (
	// ErrNotFound is returned when no depth snapshot exists in Redis for the requested market.
	ErrNotFound = errors.New("orderbook projection not found")

	// ErrInvalidData is returned when the snapshot payload fails domain validation.
	ErrInvalidData = errors.New("invalid orderbook projection data")
)

// DepthLevel represents an aggregated price level with strongly typed decimals.
type DepthLevel struct {
	Price    decimal.Decimal `json:"price"`
	Quantity decimal.Decimal `json:"quantity"`
}

// OrderBookProjection is the domain representation of a market's live depth.
type OrderBookProjection struct {
	MarketID   string       `json:"market_id"`
	Sequence   uint64       `json:"sequence"`
	Bids       []DepthLevel `json:"bids"` // sorted descending (best bid first)
	Asks       []DepthLevel `json:"asks"` // sorted ascending (best ask first)
	SnapshotAt time.Time    `json:"snapshot_at"`
}

// BestBid returns the highest buying price level, or false if the bid book is empty.
// Empty-book semantics: does NOT return zero price as a valid level.
func (p *OrderBookProjection) BestBid() (DepthLevel, bool) {
	if len(p.Bids) == 0 {
		return DepthLevel{}, false
	}
	return p.Bids[0], true
}

// BestAsk returns the lowest selling price level, or false if the ask book is empty.
// Empty-book semantics: does NOT return zero price as a valid level.
func (p *OrderBookProjection) BestAsk() (DepthLevel, bool) {
	if len(p.Asks) == 0 {
		return DepthLevel{}, false
	}
	return p.Asks[0], true
}

// Spread calculates (BestAsk - BestBid).
// Returns decimal.Zero and false if either side is empty or if the book is crossed.
func (p *OrderBookProjection) Spread() (decimal.Decimal, bool) {
	bestBid, hasBid := p.BestBid()
	bestAsk, hasAsk := p.BestAsk()
	if !hasBid || !hasAsk {
		return decimal.Zero, false
	}
	spread := bestAsk.Price.Sub(bestBid.Price)
	if spread.IsNegative() {
		return decimal.Zero, false // crossed book state is invalid
	}
	return spread, true
}

// MidPrice calculates (BestBid + BestAsk) / 2.
// Returns decimal.Zero and false if either side of the book is empty.
func (p *OrderBookProjection) MidPrice() (decimal.Decimal, bool) {
	bestBid, hasBid := p.BestBid()
	bestAsk, hasAsk := p.BestAsk()
	if !hasBid || !hasAsk {
		return decimal.Zero, false
	}
	two := decimal.NewFromInt(2)
	mid := bestBid.Price.Add(bestAsk.Price).Div(two)
	return mid, true
}

// IsEmpty returns true if both bids and asks are completely empty.
func (p *OrderBookProjection) IsEmpty() bool {
	return len(p.Bids) == 0 && len(p.Asks) == 0
}
