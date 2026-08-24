package streamer

import (
	"time"

	"tradedrift/services/gateway/internal/ws/protocol"
)

type rawRedisDepth struct {
	MarketID   string `json:"market_id"`
	Sequence   uint64 `json:"sequence"`
	Bids       []struct {
		Price    string `json:"price"`
		Quantity string `json:"quantity"`
	} `json:"bids"`
	Asks []struct {
		Price    string `json:"price"`
		Quantity string `json:"quantity"`
	} `json:"asks"`
	SnapshotAt string `json:"snapshot_at"`
}

// RawTradeEvent is the internal matching engine event format received from Kafka.
type RawTradeEvent struct {
	TradeID      string    `json:"trade_id"`
	MarketID     string    `json:"market_id"`
	Sequence     uint64    `json:"sequence"` // authoritative sequence from matching engine
	Price        string    `json:"price"`
	Quantity     string    `json:"quantity"`
	Side         string    `json:"side"`
	BuyerUserID  string    `json:"buyer_user_id"`
	SellerUserID string    `json:"seller_user_id"`
	ExecutedAt   time.Time `json:"executed_at"`
}

func convertDepthPayload(marketID string, raw rawRedisDepth) *protocol.OrderBookDepthPayload {
	bids := make([][2]string, 0, len(raw.Bids))
	for _, b := range raw.Bids {
		bids = append(bids, [2]string{b.Price, b.Quantity})
	}
	asks := make([][2]string, 0, len(raw.Asks))
	for _, a := range raw.Asks {
		asks = append(asks, [2]string{a.Price, a.Quantity})
	}

	t, _ := time.Parse(time.RFC3339Nano, raw.SnapshotAt)
	if t.IsZero() {
		t = time.Now().UTC()
	}

	return &protocol.OrderBookDepthPayload{
		MarketID:  marketID,
		Bids:      bids,
		Asks:      asks,
		Timestamp: t.UnixMilli(),
	}
}
