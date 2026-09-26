package publisher

type tradeExecutedMessage struct {
	TradeID      string `json:"trade_id"`
	MarketID     string `json:"market_id"`
	Sequence     uint64 `json:"sequence"`
	MakerOrderID string `json:"maker_order_id"`
	TakerOrderID string `json:"taker_order_id"`
	BuyOrderID   string `json:"buy_order_id"`
	SellOrderID  string `json:"sell_order_id"`
	BuyerUserID  string `json:"buyer_user_id"`
	SellerUserID string `json:"seller_user_id"`
	Price        string `json:"price"`
	Quantity     string `json:"quantity"`
	ExecutedAt   string `json:"executed_at"`
}

// orderCancelOutcomeMessage is published to orders.cancelled.v1 for every
// user-requested cancel attempt, regardless of ME outcome.
// Consumer routes on the status field:
//
//	"REMOVED_FROM_BOOK" — ME found and removed the order. Authoritative.
//	                       Order Service MUST transition CANCELLING → CANCELLED and release funds.
//
//	"ALREADY_ABSENT"    — ME could not find the order. Reason unknown.
//	                       Order Service MUST check its DB. DO NOT release funds on this alone.
type orderCancelOutcomeMessage struct {
	EventType string `json:"event_type"` // always "OrderCancelOutcome"
	EventID   string `json:"event_id"`
	OrderID   string `json:"order_id"`
	UserID    string `json:"user_id"` // populated for REMOVED_FROM_BOOK; zero UUID for ALREADY_ABSENT
	MarketID  string `json:"market_id"`
	Status    string `json:"status"` // "REMOVED_FROM_BOOK" | "ALREADY_ABSENT"
	Reason    string `json:"reason"` // "user_requested"
	Timestamp string `json:"timestamp"`
}

type depthSnapshotMessage struct {
	MarketID   string       `json:"market_id"`
	Sequence   uint64       `json:"sequence"`
	Bids       []depthLevel `json:"bids"`
	Asks       []depthLevel `json:"asks"`
	SnapshotAt string       `json:"snapshot_at"`
}

type depthLevel struct {
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
}
