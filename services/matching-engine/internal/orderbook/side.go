package orderbook

import "github.com/shopspring/decimal"

// Side represents one half of the order book (bids or asks).
//
// SortedPrices[0] is always the best price — O(1) lookup, no traversal.
//   - Bids: descending (highest price first)
//   - Asks: ascending  (lowest price first)
//
// PriceLevels maps price.String() → *PriceLevel for O(1) level access.
// A sorted slice is used instead of a tree because the number of active
// price levels is small in practice, and it's simpler to implement and debug.
type Side struct {
	SortedPrices []decimal.Decimal         // index 0 = best price
	PriceLevels  map[string]*PriceLevel    // price.String() → level
	IsBid        bool                      // true = bids (desc), false = asks (asc)
}
