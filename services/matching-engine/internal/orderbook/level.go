package orderbook

import (
	"container/list"

	"github.com/shopspring/decimal"
)

// PriceLevel holds all resting orders at a single price point.
// Orders are stored in a FIFO linked list — oldest order executes first.
//
// TotalQty is pre-aggregated and kept in sync on every insert/fill/cancel.
// This makes GetDepth() O(depth) instead of having to sum per-order on every snapshot.
type PriceLevel struct {
	Price    decimal.Decimal
	Orders   *list.List      // FIFO queue of *OrderNode
	TotalQty decimal.Decimal // sum of all RemainingQty at this level
}
