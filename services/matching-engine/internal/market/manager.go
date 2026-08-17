package market

// MarketManager creates and owns all MarketEngine instances.
// One engine per active market (BTC-USDT, ETH-USDT, SOL-USDT).
type MarketManager struct {
	engines map[string]*MarketEngine // key: marketID
}

// NewMarketManager creates an empty manager.
func NewMarketManager() *MarketManager {
	return &MarketManager{
		engines: make(map[string]*MarketEngine),
	}
}

// Add creates and registers a new MarketEngine for the given config.
// Does NOT start the goroutine — call engine.Run() separately.
func (mm *MarketManager) Add(config MarketConfig) *MarketEngine {
	engine := NewMarketEngine(config)
	mm.engines[config.MarketID] = engine
	return engine
}

// Get returns the MarketEngine for the given market ID.
// Returns nil if no engine exists for that market.
func (mm *MarketManager) Get(marketID string) *MarketEngine {
	return mm.engines[marketID]
}

// All returns all registered engines.
func (mm *MarketManager) All() []*MarketEngine {
	result := make([]*MarketEngine, 0, len(mm.engines))
	for _, e := range mm.engines {
		result = append(result, e)
	}
	return result
}
