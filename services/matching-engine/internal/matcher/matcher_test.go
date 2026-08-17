package matcher_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"tradedrift/services/matching-engine/internal/matcher"
	"tradedrift/services/matching-engine/internal/orderbook"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

func newBook() *orderbook.OrderBook {
	return orderbook.NewOrderBook("BTC-USDT")
}

func limitNode(side orderbook.SideType, price, qty string) *orderbook.OrderNode {
	return &orderbook.OrderNode{
		OrderID:      uuid.New(),
		UserID:       uuid.New(),
		MarketID:     "BTC-USDT",
		Side:         side,
		OrderType:    orderbook.OrderTypeLimit,
		Price:        decimal.RequireFromString(price),
		OriginalQty:  decimal.RequireFromString(qty),
		RemainingQty: decimal.RequireFromString(qty),
		Timestamp:    time.Now(),
	}
}

func marketNode(side orderbook.SideType, qty string) *orderbook.OrderNode {
	return &orderbook.OrderNode{
		OrderID:      uuid.New(),
		UserID:       uuid.New(),
		MarketID:     "BTC-USDT",
		Side:         side,
		OrderType:    orderbook.OrderTypeMarket,
		OriginalQty:  decimal.RequireFromString(qty),
		RemainingQty: decimal.RequireFromString(qty),
		Timestamp:    time.Now(),
	}
}

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// ─── Insert Tests ─────────────────────────────────────────────────────────────

func TestInsert_AddsToOrderIndex(t *testing.T) {
	book := newBook()
	node := limitNode(orderbook.SideBuy, "100", "1")

	matcher.Insert(book, node)

	if book.OrderIndex[node.OrderID] == nil {
		t.Fatal("expected node in OrderIndex, got nil")
	}
}

func TestInsert_AddsToPriceLevels(t *testing.T) {
	book := newBook()
	node := limitNode(orderbook.SideBuy, "100", "1")

	matcher.Insert(book, node)

	level := book.Bids.PriceLevels["100"]
	if level == nil {
		t.Fatal("expected price level for 100, got nil")
	}
	if level.TotalQty.String() != "1" {
		t.Fatalf("expected TotalQty=1, got %s", level.TotalQty.String())
	}
}

func TestInsert_BidsSortedDescending(t *testing.T) {
	book := newBook()

	matcher.Insert(book, limitNode(orderbook.SideBuy, "99", "1"))
	matcher.Insert(book, limitNode(orderbook.SideBuy, "101", "1"))
	matcher.Insert(book, limitNode(orderbook.SideBuy, "100", "1"))

	prices := book.Bids.SortedPrices
	if len(prices) != 3 {
		t.Fatalf("expected 3 price levels, got %d", len(prices))
	}
	if !prices[0].Equal(d("101")) || !prices[1].Equal(d("100")) || !prices[2].Equal(d("99")) {
		t.Fatalf("bids not sorted descending: %v", prices)
	}
}

func TestInsert_AsksSortedAscending(t *testing.T) {
	book := newBook()

	matcher.Insert(book, limitNode(orderbook.SideSell, "101", "1"))
	matcher.Insert(book, limitNode(orderbook.SideSell, "99", "1"))
	matcher.Insert(book, limitNode(orderbook.SideSell, "100", "1"))

	prices := book.Asks.SortedPrices
	if len(prices) != 3 {
		t.Fatalf("expected 3 price levels, got %d", len(prices))
	}
	if !prices[0].Equal(d("99")) || !prices[1].Equal(d("100")) || !prices[2].Equal(d("101")) {
		t.Fatalf("asks not sorted ascending: %v", prices)
	}
}

func TestInsert_Duplicate_Ignored(t *testing.T) {
	book := newBook()
	node := limitNode(orderbook.SideBuy, "100", "1")

	matcher.Insert(book, node)
	matcher.Insert(book, node) // second insert — must be ignored

	level := book.Bids.PriceLevels["100"]
	if level.Orders.Len() != 1 {
		t.Fatalf("expected 1 order in level, got %d", level.Orders.Len())
	}
	if !level.TotalQty.Equal(d("1")) {
		t.Fatalf("expected TotalQty=1, got %s", level.TotalQty)
	}
}

// ─── Cancel Tests ─────────────────────────────────────────────────────────────

func TestCancel_RemovesFromBook(t *testing.T) {
	book := newBook()
	node := limitNode(orderbook.SideBuy, "100", "1")
	matcher.Insert(book, node)

	result := matcher.Cancel(book, node.OrderID)

	if result == nil {
		t.Fatal("expected cancelled node returned, got nil")
	}
	if book.OrderIndex[node.OrderID] != nil {
		t.Fatal("expected node removed from OrderIndex")
	}
	if book.Bids.PriceLevels["100"] != nil {
		t.Fatal("expected empty price level to be removed")
	}
	if len(book.Bids.SortedPrices) != 0 {
		t.Fatal("expected SortedPrices to be empty after last order cancelled")
	}
}

func TestCancel_NotFound_ReturnsNil(t *testing.T) {
	book := newBook()
	result := matcher.Cancel(book, uuid.New())
	if result != nil {
		t.Fatal("expected nil for unknown order ID, got a node")
	}
}

func TestCancel_Idempotent(t *testing.T) {
	book := newBook()
	node := limitNode(orderbook.SideBuy, "100", "1")
	matcher.Insert(book, node)

	first := matcher.Cancel(book, node.OrderID)
	second := matcher.Cancel(book, node.OrderID) // second cancel

	if first == nil {
		t.Fatal("expected first cancel to return node")
	}
	if second != nil {
		t.Fatal("expected second cancel to return nil (idempotent)")
	}
}

func TestCancel_LevelRetainedIfOtherOrdersExist(t *testing.T) {
	book := newBook()
	a := limitNode(orderbook.SideBuy, "100", "1")
	b := limitNode(orderbook.SideBuy, "100", "2")
	matcher.Insert(book, a)
	matcher.Insert(book, b)

	matcher.Cancel(book, a.OrderID)

	level := book.Bids.PriceLevels["100"]
	if level == nil {
		t.Fatal("price level should still exist with 1 order remaining")
	}
	if level.Orders.Len() != 1 {
		t.Fatalf("expected 1 order remaining, got %d", level.Orders.Len())
	}
	if !level.TotalQty.Equal(d("2")) {
		t.Fatalf("expected TotalQty=2, got %s", level.TotalQty)
	}
}

// ─── Match Tests ──────────────────────────────────────────────────────────────

func TestMatch_LimitBuyVsSellLimit_FullFill(t *testing.T) {
	book := newBook()

	// Resting SELL @ 100, qty 1
	sell := limitNode(orderbook.SideSell, "100", "1")
	matcher.Insert(book, sell)

	// Incoming BUY @ 100, qty 1
	buy := limitNode(orderbook.SideBuy, "100", "1")
	fills := matcher.Match(book, buy, matcher.ModeLive)

	if len(fills) != 1 {
		t.Fatalf("expected 1 fill, got %d", len(fills))
	}
	if !fills[0].Quantity.Equal(d("1")) {
		t.Fatalf("expected fill qty=1, got %s", fills[0].Quantity)
	}
	// Price must ALWAYS be maker's price (the resting SELL @ 100)
	if !fills[0].Price.Equal(d("100")) {
		t.Fatalf("expected fill price=100 (maker), got %s", fills[0].Price)
	}
	// Book must be empty after full fill
	if len(book.Asks.SortedPrices) != 0 {
		t.Fatal("expected ask side to be empty after full fill")
	}
	if len(book.OrderIndex) != 0 {
		t.Fatal("expected OrderIndex to be empty after full fill")
	}
}

func TestMatch_LimitSellVsBuyLimit_FullFill(t *testing.T) {
	book := newBook()

	// Resting BUY @ 100
	buy := limitNode(orderbook.SideBuy, "100", "2")
	matcher.Insert(book, buy)

	// Incoming SELL @ 100
	sell := limitNode(orderbook.SideSell, "100", "2")
	fills := matcher.Match(book, sell, matcher.ModeLive)

	if len(fills) != 1 {
		t.Fatalf("expected 1 fill, got %d", len(fills))
	}
	if !fills[0].Price.Equal(d("100")) {
		t.Fatalf("expected maker price=100, got %s", fills[0].Price)
	}
}

func TestMatch_NoMatch_PricesDoNotOverlap(t *testing.T) {
	book := newBook()

	// Resting SELL @ 101
	matcher.Insert(book, limitNode(orderbook.SideSell, "101", "1"))

	// Incoming BUY @ 100 — cannot cross 101
	buy := limitNode(orderbook.SideBuy, "100", "1")
	fills := matcher.Match(book, buy, matcher.ModeLive)

	if len(fills) != 0 {
		t.Fatalf("expected 0 fills, got %d", len(fills))
	}
	// Incoming BUY must rest in book
	if book.OrderIndex[buy.OrderID] == nil {
		t.Fatal("expected BUY order to rest in book after no match")
	}
}

func TestMatch_PartialFill_MakerPartiallyConsumed(t *testing.T) {
	book := newBook()

	// Resting SELL @ 100, qty 5
	sell := limitNode(orderbook.SideSell, "100", "5")
	matcher.Insert(book, sell)

	// Incoming BUY @ 100, qty 2 — only 2 filled, maker stays with 3 remaining
	buy := limitNode(orderbook.SideBuy, "100", "2")
	fills := matcher.Match(book, buy, matcher.ModeLive)

	if len(fills) != 1 {
		t.Fatalf("expected 1 fill, got %d", len(fills))
	}
	if !fills[0].Quantity.Equal(d("2")) {
		t.Fatalf("expected fill qty=2, got %s", fills[0].Quantity)
	}
	// Maker (SELL) should still be in book with RemainingQty=3
	remaining := book.OrderIndex[sell.OrderID]
	if remaining == nil {
		t.Fatal("expected maker still in book after partial fill")
	}
	if !remaining.RemainingQty.Equal(d("3")) {
		t.Fatalf("expected maker RemainingQty=3, got %s", remaining.RemainingQty)
	}
	// Maker must NOT have moved in queue — Element must be same
	level := book.Asks.PriceLevels["100"]
	if !level.TotalQty.Equal(d("3")) {
		t.Fatalf("expected level TotalQty=3, got %s", level.TotalQty)
	}
}

func TestMatch_PartialFill_TakerPartiallyFills_RemainsRests(t *testing.T) {
	book := newBook()

	// Resting SELL @ 100, qty 1
	matcher.Insert(book, limitNode(orderbook.SideSell, "100", "1"))

	// Incoming BUY @ 100, qty 3 — only 1 filled, 2 remains and rests
	buy := limitNode(orderbook.SideBuy, "100", "3")
	fills := matcher.Match(book, buy, matcher.ModeLive)

	if len(fills) != 1 {
		t.Fatalf("expected 1 fill, got %d", len(fills))
	}
	if !fills[0].Quantity.Equal(d("1")) {
		t.Fatalf("expected fill qty=1, got %s", fills[0].Quantity)
	}
	// Incoming BUY remainder (qty 2) must now rest in bids
	if book.OrderIndex[buy.OrderID] == nil {
		t.Fatal("expected incoming BUY remainder to rest in book")
	}
	if !buy.RemainingQty.Equal(d("2")) {
		t.Fatalf("expected buy RemainingQty=2, got %s", buy.RemainingQty)
	}
}

func TestMatch_MultiLevelSweep(t *testing.T) {
	book := newBook()

	// Two resting SELLs at different prices
	matcher.Insert(book, limitNode(orderbook.SideSell, "100", "1"))
	matcher.Insert(book, limitNode(orderbook.SideSell, "101", "1"))

	// Incoming BUY @ 102 — sweeps both levels
	buy := limitNode(orderbook.SideBuy, "102", "2")
	fills := matcher.Match(book, buy, matcher.ModeLive)

	if len(fills) != 2 {
		t.Fatalf("expected 2 fills (one per level), got %d", len(fills))
	}
	// First fill must be at the best ask (100)
	if !fills[0].Price.Equal(d("100")) {
		t.Fatalf("expected first fill at 100, got %s", fills[0].Price)
	}
	// Second fill at 101
	if !fills[1].Price.Equal(d("101")) {
		t.Fatalf("expected second fill at 101, got %s", fills[1].Price)
	}
	// Book must be empty
	if len(book.Asks.SortedPrices) != 0 {
		t.Fatal("expected ask side empty after sweep")
	}
}

func TestMatch_Market_FullFill(t *testing.T) {
	book := newBook()

	matcher.Insert(book, limitNode(orderbook.SideSell, "100", "2"))

	// MARKET BUY qty 2
	buy := marketNode(orderbook.SideBuy, "2")
	fills := matcher.Match(book, buy, matcher.ModeLive)

	if len(fills) != 1 {
		t.Fatalf("expected 1 fill, got %d", len(fills))
	}
	if !fills[0].Quantity.Equal(d("2")) {
		t.Fatalf("expected fill qty=2, got %s", fills[0].Quantity)
	}
	if !buy.RemainingQty.Equal(decimal.Zero) {
		t.Fatalf("expected buy RemainingQty=0, got %s", buy.RemainingQty)
	}
}

func TestMatch_Market_IOC_PartialFill(t *testing.T) {
	book := newBook()

	// Only 1 available
	matcher.Insert(book, limitNode(orderbook.SideSell, "100", "1"))

	// MARKET BUY qty 3 — only 1 fills, remainder is IOC (not inserted)
	buy := marketNode(orderbook.SideBuy, "3")
	fills := matcher.Match(book, buy, matcher.ModeLive)

	if len(fills) != 1 {
		t.Fatalf("expected 1 fill, got %d", len(fills))
	}
	// Remaining qty = 2 — NOT in book (IOC)
	if !buy.RemainingQty.Equal(d("2")) {
		t.Fatalf("expected buy RemainingQty=2, got %s", buy.RemainingQty)
	}
	// Market order must NOT be inserted into the book
	if book.OrderIndex[buy.OrderID] != nil {
		t.Fatal("MARKET order remainder must NOT rest in book (IOC)")
	}
}

func TestMatch_Market_NoLiquidity(t *testing.T) {
	book := newBook()

	// Empty book — nothing to match against
	buy := marketNode(orderbook.SideBuy, "1")
	fills := matcher.Match(book, buy, matcher.ModeLive)

	if len(fills) != 0 {
		t.Fatalf("expected 0 fills, got %d", len(fills))
	}
	// Must NOT be inserted
	if book.OrderIndex[buy.OrderID] != nil {
		t.Fatal("MARKET order must not rest in empty book")
	}
}

func TestMatch_RecoveryMode_ReturnsNil(t *testing.T) {
	book := newBook()
	matcher.Insert(book, limitNode(orderbook.SideSell, "100", "1"))

	buy := limitNode(orderbook.SideBuy, "100", "1")
	fills := matcher.Match(book, buy, matcher.ModeRecovery)

	// RECOVERY mode must always return nil — output suppressed
	if fills != nil {
		t.Fatalf("expected nil fills in RECOVERY mode, got %v", fills)
	}
	// But book state must still be updated (no resting orders should remain)
	if len(book.Asks.SortedPrices) != 0 {
		t.Fatal("expected book state updated even in RECOVERY mode")
	}
}

func TestMatch_TimePriority_EarlierOrderFillsFirst(t *testing.T) {
	book := newBook()

	// Two SELLs at same price — A arrives first
	sellA := limitNode(orderbook.SideSell, "100", "1")
	sellA.Timestamp = time.Now()

	time.Sleep(1 * time.Millisecond) // ensure different timestamps

	sellB := limitNode(orderbook.SideSell, "100", "1")
	sellB.Timestamp = time.Now()

	matcher.Insert(book, sellA)
	matcher.Insert(book, sellB)

	// BUY fills only 1 — must fill sellA (earlier) first
	buy := limitNode(orderbook.SideBuy, "100", "1")
	fills := matcher.Match(book, buy, matcher.ModeLive)

	if len(fills) != 1 {
		t.Fatalf("expected 1 fill, got %d", len(fills))
	}
	// MakerOrderID must be sellA (inserted first)
	if fills[0].MakerOrderID != sellA.OrderID {
		t.Fatalf("expected sellA to fill first (time priority), got sellB")
	}
	// sellB must still be in book
	if book.OrderIndex[sellB.OrderID] == nil {
		t.Fatal("expected sellB to still be in book")
	}
}

func TestMatch_MakerPrice_AlwaysUsed(t *testing.T) {
	book := newBook()

	// Resting SELL @ 100 (maker)
	matcher.Insert(book, limitNode(orderbook.SideSell, "100", "1"))

	// Incoming BUY @ 105 (taker willing to pay more)
	buy := limitNode(orderbook.SideBuy, "105", "1")
	fills := matcher.Match(book, buy, matcher.ModeLive)

	if len(fills) != 1 {
		t.Fatalf("expected 1 fill")
	}
	// Trade price MUST be 100 (maker's price), NOT 105 (taker's price)
	if !fills[0].Price.Equal(d("100")) {
		t.Fatalf("expected trade price=100 (maker), got %s", fills[0].Price)
	}
}

func TestMatch_FillIDs_BuyerSeller_Correct(t *testing.T) {
	book := newBook()

	sell := limitNode(orderbook.SideSell, "100", "1")
	matcher.Insert(book, sell)

	buy := limitNode(orderbook.SideBuy, "100", "1")
	fills := matcher.Match(book, buy, matcher.ModeLive)

	if len(fills) != 1 {
		t.Fatalf("expected 1 fill")
	}
	f := fills[0]
	if f.BuyOrderID != buy.OrderID {
		t.Fatalf("expected BuyOrderID = buy.OrderID")
	}
	if f.SellOrderID != sell.OrderID {
		t.Fatalf("expected SellOrderID = sell.OrderID")
	}
	if f.MakerOrderID != sell.OrderID {
		t.Fatalf("expected MakerOrderID = sell.OrderID (resting)")
	}
	if f.TakerOrderID != buy.OrderID {
		t.Fatalf("expected TakerOrderID = buy.OrderID (incoming)")
	}
	if f.BuyerUserID != buy.UserID {
		t.Fatalf("expected BuyerUserID = buy.UserID")
	}
	if f.SellerUserID != sell.UserID {
		t.Fatalf("expected SellerUserID = sell.UserID")
	}
}

// ─── GetDepth Tests ───────────────────────────────────────────────────────────

func TestGetDepth_EmptyBook(t *testing.T) {
	book := newBook()
	snap := matcher.GetDepth(book, 20)

	if len(snap.Bids) != 0 || len(snap.Asks) != 0 {
		t.Fatal("expected empty depth for empty book")
	}
	if snap.MarketID != "BTC-USDT" {
		t.Fatalf("expected marketID=BTC-USDT, got %s", snap.MarketID)
	}
}

func TestGetDepth_ReturnsTopN(t *testing.T) {
	book := newBook()

	// Insert 5 ask levels
	for i := 100; i <= 104; i++ {
		matcher.Insert(book, limitNode(orderbook.SideSell, decimal.NewFromInt(int64(i)).String(), "1"))
	}

	// Request only top 3
	snap := matcher.GetDepth(book, 3)

	if len(snap.Asks) != 3 {
		t.Fatalf("expected 3 ask levels, got %d", len(snap.Asks))
	}
	// First ask must be the lowest (best ask = 100)
	if !snap.Asks[0].Price.Equal(d("100")) {
		t.Fatalf("expected best ask=100, got %s", snap.Asks[0].Price)
	}
}

func TestGetDepth_TotalQtyCorrect(t *testing.T) {
	book := newBook()

	// Two orders at same price level
	matcher.Insert(book, limitNode(orderbook.SideSell, "100", "1.5"))
	matcher.Insert(book, limitNode(orderbook.SideSell, "100", "2.5"))

	snap := matcher.GetDepth(book, 5)

	if len(snap.Asks) != 1 {
		t.Fatalf("expected 1 ask level, got %d", len(snap.Asks))
	}
	// TotalQty must be the sum: 1.5 + 2.5 = 4
	if !snap.Asks[0].Quantity.Equal(d("4")) {
		t.Fatalf("expected quantity=4, got %s", snap.Asks[0].Quantity)
	}
}
