package orderbook

import (
	"container/list"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type SideType string

const (
	SideBuy  SideType = "BUY"
	SideSell SideType = "SELL"
)

type OrderType string

const (
	OrderTypeLimit  OrderType = "LIMIT"
	OrderTypeMarket OrderType = "MARKET"
)

// OrderNode represents one resting order inside the Order Book.
// Market orders are NEVER inserted — they match immediately (IOC).
//
// Rules:
//   - RemainingQty is reduced in-place on partial fill. NEVER re-insert
//     the node — it would move to the back of the queue, breaking time priority.
//   - Timestamp is set by the ME at insertion, not the Order Service time.
//   - Element is a back-pointer for O(1) removal (cancel/full-fill).
type OrderNode struct {
	OrderID      uuid.UUID
	UserID       uuid.UUID
	MarketID     string
	Side         SideType
	OrderType    OrderType
	Price        decimal.Decimal // zero for MARKET orders
	OriginalQty  decimal.Decimal // never changes
	RemainingQty decimal.Decimal // reduced on every partial fill
	Timestamp    time.Time       // ME arrival time — determines time priority
	Element      *list.Element   // back-pointer: list.Remove(node.Element) = O(1)
}
