package matcher

import (
	"container/list"
	"sort"
	"time"

	"tradedrift/services/matching-engine/internal/orderbook"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Mode controls whether Match suppresses output (RECOVERY) or emits fills (LIVE).
type Mode int

const (
	ModeRecovery Mode = iota // Replaying Kafka history — outputs suppressed
	ModeLive                 // Normal operation — outputs emitted
)

// Insert adds a resting LIMIT order to the correct side of the book.
//
// IMPORTANT: node MUST be heap-allocated by the caller.
// Do NOT pass &localVar — the pointer is stored in both the linked list
// and OrderIndex and outlives this function call.
func Insert(book *orderbook.OrderBook, node *orderbook.OrderNode) {
	// Defensive: prevent duplicate order ID
	if book.OrderIndex[node.OrderID] != nil {
		return
	}

	side := getSide(book, node.Side)
	priceKey := node.Price.String()
	level := side.PriceLevels[priceKey]

	if level == nil {
		// New price level — create it and insert into sorted slice
		level = &orderbook.PriceLevel{
			Price:  node.Price,
			Orders: list.New(),
		}
		side.PriceLevels[priceKey] = level

		idx := binarySearchInsertIndex(side, node.Price)
		side.SortedPrices = insertAt(side.SortedPrices, idx, node.Price)
	}

	// Add to back of queue — oldest order executes first (FIFO)
	node.Element = level.Orders.PushBack(node)
	level.TotalQty = level.TotalQty.Add(node.RemainingQty)
	book.OrderIndex[node.OrderID] = node
}

// Cancel removes an order from the book by its ID.
// Returns the cancelled node so the caller can build an OrderCancelled payload.
// Returns nil if the order is not in the book (already filled, or unknown ID).
// Cancel is idempotent — calling it twice is safe.
func Cancel(book *orderbook.OrderBook, orderID uuid.UUID) *orderbook.OrderNode {
	node := book.OrderIndex[orderID]
	if node == nil {
		return nil // not in book — silent no-op
	}

	side := getSide(book, node.Side)
	priceKey := node.Price.String()
	level := side.PriceLevels[priceKey]

	level.TotalQty = level.TotalQty.Sub(node.RemainingQty)
	level.Orders.Remove(node.Element) // O(1) via Element back-pointer
	delete(book.OrderIndex, orderID)

	// Remove the price level entirely if now empty
	if level.Orders.Len() == 0 {
		delete(side.PriceLevels, priceKey)
		idx := findPriceIndex(side, node.Price)
		side.SortedPrices = removeAt(side.SortedPrices, idx)
	}

	return node
}

// ExecuteBest returns the front (oldest) order from the best price level.
// This is a PEEK — it does NOT remove the order.
// Returns nil if the side is empty OR if the best level has no orders (defensive).
func ExecuteBest(side *orderbook.Side) *orderbook.OrderNode {
	if len(side.SortedPrices) == 0 {
		return nil
	}

	bestPrice := side.SortedPrices[0]
	level := side.PriceLevels[bestPrice.String()]
	if level == nil {
		return nil
	}

	// ← FIX: nil check before dereferencing Front()
	front := level.Orders.Front()
	if front == nil {
		return nil
	}

	return front.Value.(*orderbook.OrderNode)
}

// PartialFill reduces a resting order's remaining quantity in-place.
// The order stays in the book at its EXACT queue position.
// NEVER remove and re-insert — that moves it to the back, breaking Price-Time Priority.
func PartialFill(side *orderbook.Side, node *orderbook.OrderNode, filledQty decimal.Decimal) {
	node.RemainingQty = node.RemainingQty.Sub(filledQty)
	level := side.PriceLevels[node.Price.String()]
	level.TotalQty = level.TotalQty.Sub(filledQty)
	// node.Element unchanged — queue position preserved
	// node.Timestamp unchanged — time priority preserved
}

// FullFill removes a resting order that has been completely consumed.
func FullFill(book *orderbook.OrderBook, side *orderbook.Side, node *orderbook.OrderNode) {
	priceKey := node.Price.String()
	level := side.PriceLevels[priceKey]

	level.TotalQty = level.TotalQty.Sub(node.RemainingQty)
	level.Orders.Remove(node.Element)
	delete(book.OrderIndex, node.OrderID)

	if level.Orders.Len() == 0 {
		delete(side.PriceLevels, priceKey)
		idx := findPriceIndex(side, node.Price)
		side.SortedPrices = removeAt(side.SortedPrices, idx)
	}
}

// GetDepth reads the top-N price levels from each side for the Redis projection.
// TotalQty is pre-aggregated on PriceLevel — no inner loop needed. O(depth).
func GetDepth(book *orderbook.OrderBook, depth int) orderbook.DepthSnapshot {
	bids := make([]orderbook.DepthLevel, 0, depth)
	asks := make([]orderbook.DepthLevel, 0, depth)

	for i := 0; i < depth && i < len(book.Bids.SortedPrices); i++ {
		price := book.Bids.SortedPrices[i]
		level := book.Bids.PriceLevels[price.String()]
		bids = append(bids, orderbook.DepthLevel{Price: price, Quantity: level.TotalQty})
	}

	for i := 0; i < depth && i < len(book.Asks.SortedPrices); i++ {
		price := book.Asks.SortedPrices[i]
		level := book.Asks.PriceLevels[price.String()]
		asks = append(asks, orderbook.DepthLevel{Price: price, Quantity: level.TotalQty})
	}

	return orderbook.DepthSnapshot{
		MarketID:   book.MarketID,
		Sequence:   book.Sequence,
		Bids:       bids,
		Asks:       asks,
		SnapshotAt: time.Now(),
	}
}

// Match is the core matching loop.
// Processes one incoming order against the opposite side of the book.
// Returns []Fill — one Fill per individual trade.
// One incoming order can produce multiple Fills (multi-level sweep).
// In ModeRecovery, the algorithm runs identically but returns nil.
func Match(book *orderbook.OrderBook, incoming *orderbook.OrderNode, mode Mode) []orderbook.Fill {
	fills := make([]orderbook.Fill, 0)
	oppSide := getOppositeSide(book, incoming.Side)

	for incoming.RemainingQty.GreaterThan(decimal.Zero) {

		best := ExecuteBest(oppSide)
		if best == nil {
			break // opposite side is empty
		}

		if !crossable(incoming, best) {
			break // prices do not overlap
		}

		fillQty := decimal.Min(incoming.RemainingQty, best.RemainingQty)

		// Generate trade_id in-memory — no DB round-trip
		// TODO: replace uuid.New() with UUIDv7 from platform/uuid
		tradeID := uuid.New()

		// Advance authoritative monotonically increasing trade execution sequence
		book.Sequence++

		fills = append(fills, orderbook.Fill{
			TradeID:      tradeID,
			MarketID:     book.MarketID, // ← authoritative market identifier
			Sequence:     book.Sequence,
			MakerOrderID: best.OrderID,
			TakerOrderID: incoming.OrderID,
			BuyOrderID:   buyOrderOf(incoming, best),
			SellOrderID:  sellOrderOf(incoming, best),
			BuyerUserID:  buyUserOf(incoming, best),
			SellerUserID: sellUserOf(incoming, best),
			Price:        best.Price, // ALWAYS maker's price
			Quantity:     fillQty,
		})

		if fillQty.Equal(best.RemainingQty) {
			FullFill(book, oppSide, best)
		} else {
			PartialFill(oppSide, best, fillQty)
		}

		incoming.RemainingQty = incoming.RemainingQty.Sub(fillQty)
	}

	// LIMIT: insert remaining quantity as a new resting order
	if incoming.RemainingQty.GreaterThan(decimal.Zero) &&
		incoming.OrderType == orderbook.OrderTypeLimit {
		book.Sequence++
		Insert(book, incoming)
	}

	// MARKET (IOC): remainder is NOT inserted.
	// Event Loop detects incoming.RemainingQty > 0 after Match returns
	// and builds OrderCancelled{reason:"ioc_expired"} for Publisher.

	if mode == ModeRecovery {
		return nil // suppress — fills already settled before crash
	}

	return fills
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func crossable(incoming, best *orderbook.OrderNode) bool {
	if incoming.OrderType == orderbook.OrderTypeMarket {
		return true
	}
	if incoming.Side == orderbook.SideBuy {
		return incoming.Price.GreaterThanOrEqual(best.Price)
	}
	return incoming.Price.LessThanOrEqual(best.Price)
}

func getSide(book *orderbook.OrderBook, side orderbook.SideType) *orderbook.Side {
	if side == orderbook.SideBuy {
		return &book.Bids
	}
	return &book.Asks
}

func getOppositeSide(book *orderbook.OrderBook, side orderbook.SideType) *orderbook.Side {
	if side == orderbook.SideBuy {
		return &book.Asks
	}
	return &book.Bids
}

func buyOrderOf(incoming, best *orderbook.OrderNode) uuid.UUID {
	if incoming.Side == orderbook.SideBuy {
		return incoming.OrderID
	}
	return best.OrderID
}

func sellOrderOf(incoming, best *orderbook.OrderNode) uuid.UUID {
	if incoming.Side == orderbook.SideSell {
		return incoming.OrderID
	}
	return best.OrderID
}

func buyUserOf(incoming, best *orderbook.OrderNode) uuid.UUID {
	if incoming.Side == orderbook.SideBuy {
		return incoming.UserID
	}
	return best.UserID
}

func sellUserOf(incoming, best *orderbook.OrderNode) uuid.UUID {
	if incoming.Side == orderbook.SideSell {
		return incoming.UserID
	}
	return best.UserID
}

func binarySearchInsertIndex(side *orderbook.Side, price decimal.Decimal) int {
	if side.IsBid {
		return sort.Search(len(side.SortedPrices), func(i int) bool {
			return side.SortedPrices[i].LessThan(price)
		})
	}
	return sort.Search(len(side.SortedPrices), func(i int) bool {
		return side.SortedPrices[i].GreaterThan(price)
	})
}

func findPriceIndex(side *orderbook.Side, price decimal.Decimal) int {
	if side.IsBid {
		return sort.Search(len(side.SortedPrices), func(i int) bool {
			return side.SortedPrices[i].LessThanOrEqual(price)
		})
	}
	return sort.Search(len(side.SortedPrices), func(i int) bool {
		return side.SortedPrices[i].GreaterThanOrEqual(price)
	})
}

func insertAt(prices []decimal.Decimal, idx int, price decimal.Decimal) []decimal.Decimal {
	prices = append(prices, decimal.Zero)
	copy(prices[idx+1:], prices[idx:])
	prices[idx] = price
	return prices
}

func removeAt(prices []decimal.Decimal, idx int) []decimal.Decimal {
	return append(prices[:idx], prices[idx+1:]...)
}
