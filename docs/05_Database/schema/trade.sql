-- TradeDrift Trade History Database Schema

CREATE TABLE trades (
    id            UUID PRIMARY KEY,
    market_id     VARCHAR(20) NOT NULL,
    buyer_id      UUID NOT NULL,
    seller_id     UUID NOT NULL,
    buy_order_id  UUID NOT NULL,
    sell_order_id UUID NOT NULL,
    price         DECIMAL(30,10) NOT NULL CHECK (price > 0),
    quantity      DECIMAL(30,10) NOT NULL CHECK (quantity > 0),
    executed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for market trade history pagination
CREATE INDEX idx_trades_market_pagination ON trades (market_id, executed_at DESC, id DESC);

-- Split index paths for user trade executions queries
CREATE INDEX idx_trades_buyer_executed ON trades (buyer_id, executed_at DESC, id DESC);
CREATE INDEX idx_trades_seller_executed ON trades (seller_id, executed_at DESC, id DESC);
