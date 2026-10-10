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

func match(book *orderbook.OrderBook, incoming *orderbook.OrderNode, mode matcher.Mode) []orderbook.Fill {
	return matcher.Match(book, incoming, mode, uuid.New())
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

func marketNodeWithProtection(side orderbook.SideType, qty, protectionPrice string) *orderbook.OrderNode {
	return &orderbook.OrderNode{
		OrderID:      uuid.New(),
		UserID:       uuid.New(),
		MarketID:     "BTC-USDT",
		Side:         side,
		OrderType:    orderbook.OrderTypeMarket,
		Price:        decimal.RequireFromString(protectionPrice),
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

	result := matcher.Cancel(book, node.OrderID, node.UserID)

	if result.Status != orderbook.CancelStatusRemovedFromBook {
		t.Fatalf("expected REMOVED_FROM_BOOK, got %q", result.Status)
	}
	if result.OrderID != node.OrderID {
		t.Fatal("expected cancelled node OrderID to match")
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

func TestCancel_WrongUser_DoesNotRemoveOrder(t *testing.T) {
	book := newBook()
	node := limitNode(orderbook.SideBuy, "100", "1")
	matcher.Insert(book, node)

	wrongUser := uuid.New()
	result := matcher.Cancel(book, node.OrderID, wrongUser)

	if result.Status != orderbook.CancelStatusUserMismatch {
		t.Fatalf("expected USER_MISMATCH, got %q", result.Status)
	}
	if result.OrderID != node.OrderID {
		t.Fatalf("expected OrderID=%s, got %s", node.OrderID, result.OrderID)
	}
	if result.UserID != node.UserID {
		t.Fatalf("expected authoritative owner UserID=%s, got %s", node.UserID, result.UserID)
	}

	// Invariant: Order must remain active in the book
	if book.OrderIndex[node.OrderID] == nil {
		t.Fatal("expected node to still exist in OrderIndex")
	}
	level := book.Bids.PriceLevels["100"]
	if level == nil || level.Orders.Len() != 1 {
		t.Fatal("expected price level to still contain the order")
	}
	if !level.TotalQty.Equal(d("1")) {
		t.Fatalf("expected level TotalQty=1, got %s", level.TotalQty)
	}

	// Verify order remains cancellable by legitimate owner
	legitResult := matcher.Cancel(book, node.OrderID, node.UserID)
	if legitResult.Status != orderbook.CancelStatusRemovedFromBook {
		t.Fatalf("expected legitimate cancel to succeed with REMOVED_FROM_BOOK, got %q", legitResult.Status)
	}
	if book.OrderIndex[node.OrderID] != nil {
		t.Fatal("expected order removed after legitimate owner cancel")
	}
}

func TestCancel_NotFound_ReturnsAlreadyAbsent(t *testing.T) {
	book := newBook()
	result := matcher.Cancel(book, uuid.New(), uuid.New())
	if result.Status != orderbook.CancelStatusAlreadyAbsent {
		t.Fatalf("expected ALREADY_ABSENT for unknown order ID, got %q", result.Status)
	}
}

func TestCancel_Idempotent(t *testing.T) {
	book := newBook()
	node := limitNode(orderbook.SideBuy, "100", "1")
	matcher.Insert(book, node)

	first := matcher.Cancel(book, node.OrderID, node.UserID)
	second := matcher.Cancel(book, node.OrderID, node.UserID) // second cancel of same order

	if first.Status != orderbook.CancelStatusRemovedFromBook {
		t.Fatalf("expected first cancel to be REMOVED_FROM_BOOK, got %q", first.Status)
	}
	if second.Status != orderbook.CancelStatusAlreadyAbsent {
		t.Fatalf("expected second cancel to be ALREADY_ABSENT (idempotent), got %q", second.Status)
	}
}

func TestCancel_LevelRetainedIfOtherOrdersExist(t *testing.T) {
	book := newBook()
	a := limitNode(orderbook.SideBuy, "100", "1")
	b := limitNode(orderbook.SideBuy, "100", "2")
	matcher.Insert(book, a)
	matcher.Insert(book, b)

	matcher.Cancel(book, a.OrderID, a.UserID)

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
	fills := match(book, buy, matcher.ModeLive)

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
	fills := match(book, sell, matcher.ModeLive)

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
	fills := match(book, buy, matcher.ModeLive)

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
	fills := match(book, buy, matcher.ModeLive)

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
	fills := match(book, buy, matcher.ModeLive)

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
	fills := match(book, buy, matcher.ModeLive)

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
	fills := match(book, buy, matcher.ModeLive)

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
	fills := match(book, buy, matcher.ModeLive)

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
	fills := match(book, buy, matcher.ModeLive)

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
	fills := match(book, buy, matcher.ModeRecovery)

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
	fills := match(book, buy, matcher.ModeLive)

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
	fills := match(book, buy, matcher.ModeLive)

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
	fills := match(book, buy, matcher.ModeLive)

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

func TestMatch_DeterministicTradeIDs(t *testing.T) {
	sellOrderID := uuid.New()
	buyOrderID := uuid.New()
	eventID1 := uuid.New()

	// First match execution (eventID1)
	book1 := newBook()
	sell1 := limitNode(orderbook.SideSell, "100", "1")
	sell1.OrderID = sellOrderID
	matcher.Insert(book1, sell1)
	buy1 := limitNode(orderbook.SideBuy, "100", "1")
	buy1.OrderID = buyOrderID
	fills1 := matcher.Match(book1, buy1, matcher.ModeLive, eventID1)

	if len(fills1) != 1 {
		t.Fatalf("expected 1 fill in first match run")
	}

	// Second match execution with identical inputs and identical eventID1 (represents crash replay)
	book2 := newBook()
	sell2 := limitNode(orderbook.SideSell, "100", "1")
	sell2.OrderID = sellOrderID
	matcher.Insert(book2, sell2)
	buy2 := limitNode(orderbook.SideBuy, "100", "1")
	buy2.OrderID = buyOrderID
	fills2 := matcher.Match(book2, buy2, matcher.ModeLive, eventID1)

	if len(fills2) != 1 {
		t.Fatalf("expected 1 fill in second match run")
	}

	// Third match execution with identical orders but a different eventID2
	eventID2 := uuid.New()
	book3 := newBook()
	sell3 := limitNode(orderbook.SideSell, "100", "1")
	sell3.OrderID = sellOrderID
	matcher.Insert(book3, sell3)
	buy3 := limitNode(orderbook.SideBuy, "100", "1")
	buy3.OrderID = buyOrderID
	fills3 := matcher.Match(book3, buy3, matcher.ModeLive, eventID2)

	if len(fills3) != 1 {
		t.Fatalf("expected 1 fill in third match run")
	}

	id1 := fills1[0].TradeID
	id2 := fills2[0].TradeID
	id3 := fills3[0].TradeID

	if id1 != id2 {
		t.Errorf("expected deterministic trade IDs to match for identical eventID: %s vs %s", id1, id2)
	}

	if id1 == id3 {
		t.Errorf("expected trade IDs to differ for different eventIDs: %s vs %s", id1, id3)
	}
}

// ─── Protection Price (Slippage Boundary) Tests ───────────────────────────────

func TestMarketOrder_BuyProtectionPrice_EnforcesCeiling(t *testing.T) {
	book := newBook()
	// Insert 4 ask levels
	matcher.Insert(book, limitNode(orderbook.SideSell, "85020", "0.10"))
	matcher.Insert(book, limitNode(orderbook.SideSell, "85040", "0.05"))
	matcher.Insert(book, limitNode(orderbook.SideSell, "85060", "0.05"))
	matcher.Insert(book, limitNode(orderbook.SideSell, "85080", "0.10"))

	// Incoming BUY market order with protection price 85060 requesting 0.30
	incoming := marketNodeWithProtection(orderbook.SideBuy, "0.30", "85060")
	fills := match(book, incoming, matcher.ModeLive)

	// Must match 85020, 85040, 85060 and STOP before 85080
	if len(fills) != 3 {
		t.Fatalf("expected exactly 3 fills, got %d", len(fills))
	}

	expectedPrices := []string{"85020", "85040", "85060"}
	expectedQtys := []string{"0.10", "0.05", "0.05"}
	for i, f := range fills {
		if !f.Price.Equal(d(expectedPrices[i])) {
			t.Errorf("fill %d price mismatch: want %s got %s", i, expectedPrices[i], f.Price.String())
		}
		if !f.Quantity.Equal(d(expectedQtys[i])) {
			t.Errorf("fill %d qty mismatch: want %s got %s", i, expectedQtys[i], f.Quantity.String())
		}
	}

	// Remaining uncrossed quantity must be 0.10 (0.30 - 0.20)
	if !incoming.RemainingQty.Equal(d("0.10")) {
		t.Fatalf("expected RemainingQty=0.10, got %s", incoming.RemainingQty.String())
	}

	// Level 85080 must remain untouched on the book
	ask85080 := book.Asks.PriceLevels["85080"]
	if ask85080 == nil || !ask85080.TotalQty.Equal(d("0.10")) {
		t.Fatalf("level 85080 was modified unexpectedly: %v", ask85080)
	}
}

func TestMarketOrder_SellProtectionPrice_EnforcesFloor(t *testing.T) {
	book := newBook()
	// Insert 4 bid levels
	matcher.Insert(book, limitNode(orderbook.SideBuy, "84980", "0.10"))
	matcher.Insert(book, limitNode(orderbook.SideBuy, "84960", "0.10"))
	matcher.Insert(book, limitNode(orderbook.SideBuy, "84940", "0.05"))
	matcher.Insert(book, limitNode(orderbook.SideBuy, "84920", "0.10"))

	// Incoming SELL market order with protection price 84940 requesting 0.35
	incoming := marketNodeWithProtection(orderbook.SideSell, "0.35", "84940")
	fills := match(book, incoming, matcher.ModeLive)

	// Must match 84980, 84960, 84940 and STOP before 84920
	if len(fills) != 3 {
		t.Fatalf("expected exactly 3 fills, got %d", len(fills))
	}

	expectedPrices := []string{"84980", "84960", "84940"}
	expectedQtys := []string{"0.10", "0.10", "0.05"}
	for i, f := range fills {
		if !f.Price.Equal(d(expectedPrices[i])) {
			t.Errorf("fill %d price mismatch: want %s got %s", i, expectedPrices[i], f.Price.String())
		}
		if !f.Quantity.Equal(d(expectedQtys[i])) {
			t.Errorf("fill %d qty mismatch: want %s got %s", i, expectedQtys[i], f.Quantity.String())
		}
	}

	// Remaining uncrossed quantity must be 0.10 (0.35 - 0.25)
	if !incoming.RemainingQty.Equal(d("0.10")) {
		t.Fatalf("expected RemainingQty=0.10, got %s", incoming.RemainingQty.String())
	}

	// Level 84920 must remain untouched on the book
	bid84920 := book.Bids.PriceLevels["84920"]
	if bid84920 == nil || !bid84920.TotalQty.Equal(d("0.10")) {
		t.Fatalf("level 84920 was modified unexpectedly: %v", bid84920)
	}
}

func TestMarketOrder_Unprotected_SweepsAllLevels(t *testing.T) {
	book := newBook()
	matcher.Insert(book, limitNode(orderbook.SideSell, "85020", "0.10"))
	matcher.Insert(book, limitNode(orderbook.SideSell, "85040", "0.05"))
	matcher.Insert(book, limitNode(orderbook.SideSell, "85060", "0.05"))
	matcher.Insert(book, limitNode(orderbook.SideSell, "85080", "0.10"))

	// Unprotected incoming BUY market order (price = 0)
	incoming := marketNode(orderbook.SideBuy, "0.30")
	fills := match(book, incoming, matcher.ModeLive)

	// Must match all 4 levels including 85080
	if len(fills) != 4 {
		t.Fatalf("expected 4 fills, got %d", len(fills))
	}
	if incoming.RemainingQty.String() != "0" {
		t.Fatalf("expected RemainingQty=0, got %s", incoming.RemainingQty.String())
	}
}

// TestMicrostructureAcceptance_NaturalCandleFormedBySweptLevels validates the Phase 5 acceptance criterion:
// When LE populates a ladder and CTS sweeps across multiple price levels with a MARKET order bounded by
// a protection price, the trades emitted by ME naturally form a multi-price candle without any
// artificial candle manufacturing.
//
// Reference: 85,000
// LE Order Book:
//   Best Ask: 0.10 @ 85,020
//   L2 Ask:   0.05 @ 85,040
//   L3 Ask:   0.03 @ 85,060
// CTS:
//   Profile:    MID/SWEEP
//   Side:       BUY
//   Qty:        0.18 BTC
//   Protection: 85,070
// ME Fills:
//   0.10 @ 85,020
//   0.05 @ 85,040
//   0.03 @ 85,060
// Natural Candle:
//   Open: 85,020 | High: 85,060 | Low: 85,020 | Close: 85,060 | Volume: 0.18
func TestMicrostructureAcceptance_NaturalCandleFormedBySweptLevels(t *testing.T) {
	book := newBook()

	// 1. LE provides depth across 3 distinct ask levels
	matcher.Insert(book, limitNode(orderbook.SideSell, "85020", "0.10"))
	matcher.Insert(book, limitNode(orderbook.SideSell, "85040", "0.05"))
	matcher.Insert(book, limitNode(orderbook.SideSell, "85060", "0.03"))

	// 2. CTS submits MARKET IOC order with quantity 0.18 BTC and protection price 85,070
	incoming := marketNode(orderbook.SideBuy, "0.18")
	incoming.Price = d("85070") // Hard execution protection ceiling

	// 3. ME matches deterministically
	fills := match(book, incoming, matcher.ModeLive)

	if len(fills) != 3 {
		t.Fatalf("expected 3 fills from multi-level sweep, got %d", len(fills))
	}

	expectedFills := []struct {
		price string
		qty   string
	}{
		{"85020", "0.10"},
		{"85040", "0.05"},
		{"85060", "0.03"},
	}

	for i, exp := range expectedFills {
		if !fills[i].Price.Equal(d(exp.price)) {
			t.Errorf("fill %d price mismatch: want %s, got %s", i, exp.price, fills[i].Price.String())
		}
		if !fills[i].Quantity.Equal(d(exp.qty)) {
			t.Errorf("fill %d quantity mismatch: want %s, got %s", i, exp.qty, fills[i].Quantity.String())
		}
	}

	// 4. Invariant: remaining quantity must be 0 (all filled within protection boundary)
	if !incoming.RemainingQty.IsZero() {
		t.Fatalf("expected entire 0.18 qty filled, remaining: %s", incoming.RemainingQty.String())
	}

	// 5. Construct candle purely from the emitted trades
	var candleOpen, candleHigh, candleLow, candleClose, candleVolume decimal.Decimal
	candleVolume = decimal.Zero

	for i, trade := range fills {
		candleVolume = candleVolume.Add(trade.Quantity)
		if i == 0 {
			candleOpen = trade.Price
			candleHigh = trade.Price
			candleLow = trade.Price
			candleClose = trade.Price
			continue
		}
		if trade.Price.GreaterThan(candleHigh) {
			candleHigh = trade.Price
		}
		if trade.Price.LessThan(candleLow) {
			candleLow = trade.Price
		}
		candleClose = trade.Price
	}

	// 6. Verify candle values match expected realistic candle
	if !candleOpen.Equal(d("85020")) {
		t.Errorf("candle Open: want 85020, got %s", candleOpen)
	}
	if !candleHigh.Equal(d("85060")) {
		t.Errorf("candle High: want 85060, got %s", candleHigh)
	}
	if !candleLow.Equal(d("85020")) {
		t.Errorf("candle Low: want 85020, got %s", candleLow)
	}
	if !candleClose.Equal(d("85060")) {
		t.Errorf("candle Close: want 85060, got %s", candleClose)
	}
	if !candleVolume.Equal(d("0.18")) {
		t.Errorf("candle Volume: want 0.18, got %s", candleVolume)
	}
}

