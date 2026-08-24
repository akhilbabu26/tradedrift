package protocol

// SnapshotProvider fetches immediate depth and ticker snapshots upon client subscription.
type SnapshotProvider interface {
	GetImmediateOrderBook(marketID string) (*OrderBookDepthPayload, error)
	GetImmediateTicker(marketID string) (*TickerPayload, error)
}

// Broadcaster sends real-time market updates to connected WebSocket subscribers.
type Broadcaster interface {
	Broadcast(channel string, payload []byte, streamType string)
	HasSubscribers(channel string) bool
	GetActiveMarketIDs() []string
}
