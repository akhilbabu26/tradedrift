package market

import (
	"time"
	"github.com/shopspring/decimal"

	"tradedrift/services/matching-engine/internal/matcher"
	"tradedrift/services/matching-engine/internal/orderbook"
)

// Run is the Event Loop goroutine — the ONLY goroutine that touches book.
// It processes one InputEvent at a time and sends exactly one MatchResult
// per event to OutputQueue (one-in one-out invariant).
//
// Blocks until InputQueue is closed (graceful shutdown).
func (m *MarketEngine) Run() {
	for event := range m.InputQueue {
		m.processEvent(event)
	}
}

// processEvent processes one input event and sends exactly one MatchResult.
// one-in one-out: every input → exactly one output → exactly one checkpoint.
func (m *MarketEngine) processEvent(event InputEvent) {
	matcherMode := matcher.ModeRecovery
	if m.mode == ModeLive {
		matcherMode = matcher.ModeLive
	}

	switch event.Type {

	case EventOrderCreated:
		p := event.OrderCreated
		node := &orderbook.OrderNode{
			OrderID:      p.OrderID,
			UserID:       p.UserID,
			MarketID:     p.MarketID,
			Side:         p.Side,
			OrderType:    p.OrderType,
			Price:        p.Price,
			OriginalQty:  p.Quantity,
			RemainingQty: p.Quantity,
			Timestamp:    time.Now(), // ME arrival time — NOT Order Service time
		}

		// Pre-match validation: tick and lot size
		if !validTickAndLot(node, m.config) {
			m.OutputQueue <- orderbook.MatchResult{
				CancelResult: &orderbook.CancelledOrder{
					OrderID:           node.OrderID,
					UserID:            node.UserID,
					MarketID:          node.MarketID,
					RemainingQuantity: node.OriginalQty,
					Reason:            "invalid_order_parameters",
					CancelledAt:       time.Now(),
				},
				DepthSnapshot: matcher.GetDepth(m.book, 20),
				SourceOffset:  event.Offset,
			}
			return
		}

		fills := matcher.Match(m.book, node, matcherMode)

		var cancel *orderbook.CancelledOrder
		// MARKET IOC: if any remainder after match, signal ioc_expired
		if node.OrderType == orderbook.OrderTypeMarket &&
			node.RemainingQty.GreaterThan(decimal.Zero) {
			cancel = &orderbook.CancelledOrder{
				OrderID:           node.OrderID,
				UserID:            node.UserID,
				MarketID:          node.MarketID,
				RemainingQuantity: node.RemainingQty,
				Reason:            "ioc_expired",
				CancelledAt:       time.Now(),
			}
		}

		m.OutputQueue <- orderbook.MatchResult{
			Fills:         fills,
			CancelResult:  cancel,
			DepthSnapshot: matcher.GetDepth(m.book, 20),
			SourceOffset:  event.Offset,
		}

	case EventOrderCancel:
		p := event.OrderCancel
		node := matcher.Cancel(m.book, p.OrderID)

		var cancel *orderbook.CancelledOrder
		if node != nil {
			cancel = &orderbook.CancelledOrder{
				OrderID:           node.OrderID,
				UserID:            node.UserID,
				MarketID:          node.MarketID,
				RemainingQuantity: node.RemainingQty,
				Reason:            "user_requested",
				CancelledAt:       time.Now(),
			}
		}
		// Even if cancel is nil (order not found — idempotent no-op),
		// we still send a MatchResult so the Publisher writes one checkpoint.
		m.OutputQueue <- orderbook.MatchResult{
			CancelResult:  cancel,
			DepthSnapshot: matcher.GetDepth(m.book, 20),
			SourceOffset:  event.Offset,
		}
	}
}

// validTickAndLot checks that the order's price and quantity conform to market rules.
// Returns true if valid (order can proceed), false if the order must be rejected.
func validTickAndLot(node *orderbook.OrderNode, config MarketConfig) bool {
	// MARKET orders have no price — skip tick size check
	if node.OrderType == orderbook.OrderTypeLimit {
		if config.TickSize.GreaterThan(decimal.Zero) {
			remainder := node.Price.Mod(config.TickSize)
			if !remainder.IsZero() {
				return false // price is not a multiple of tick size
			}
		}
	}
	// Lot size check applies to both LIMIT and MARKET
	if config.LotSize.GreaterThan(decimal.Zero) {
		remainder := node.RemainingQty.Mod(config.LotSize)
		if !remainder.IsZero() {
			return false // quantity is not a multiple of lot size
		}
	}
	return true
}
