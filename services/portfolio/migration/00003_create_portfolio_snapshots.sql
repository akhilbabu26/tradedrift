-- +goose Up
CREATE TABLE IF NOT EXISTS portfolio_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    total_equity NUMERIC(30, 10) NOT NULL,
    cash_balance NUMERIC(30, 10) NOT NULL,
    holdings_value NUMERIC(30, 10) NOT NULL,
    unrealized_pnl NUMERIC(30, 10) NOT NULL,
    realized_pnl NUMERIC(30, 10) NOT NULL,
    trigger_type VARCHAR(20) NOT NULL, -- 'TRADE', 'PERIODIC', 'INITIAL'
    snapshot_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_portfolio_snapshots_user_time 
    ON portfolio_snapshots (user_id, snapshot_at ASC);

-- +goose Down
DROP TABLE IF EXISTS portfolio_snapshots;
