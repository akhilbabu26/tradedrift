package orderbook

import "github.com/google/uuid"

// OrderBook holds all resting orders for a single trading pair.
// It is exclusively owned by one MarketEngine's Event Loop goroutine.
// No other goroutine ever reads or writes it — no locks needed.
//
// OrderIndex is book-level (not per-side) so Cancel can look up any order
// by ID in O(1) without knowing which side it's on.
type OrderBook struct {
	MarketID   string
	Bids       Side                     // buy side — sorted highest → lowest
	Asks       Side                     // sell side — sorted lowest → highest
	OrderIndex map[uuid.UUID]*OrderNode // O(1) cancel lookup
}

// NewOrderBook creates an empty order book for the given market.
func NewOrderBook(marketID string) *OrderBook {
	return &OrderBook{
		MarketID: marketID,
		Bids: Side{
			IsBid:       true,
			PriceLevels: make(map[string]*PriceLevel),
		},
		Asks: Side{
			IsBid:       false,
			PriceLevels: make(map[string]*PriceLevel),
		},
		OrderIndex: make(map[uuid.UUID]*OrderNode),
	}
}
